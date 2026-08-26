package conformance

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// identity is a no-op trim function for tests that do not care about corpus-path
// stripping.
func identity(s string) string { return s }

// errBoom stands in for an arbitrary load failure in tests that only care that a
// docResult carries some error, not which one.
var errBoom = errors.New("boom")

// testCorpusTag and docA are named so goconst does not see their literals repeated
// across this file's tests.
const (
	testCorpusTag = "v1.2.1"
	docA          = "a.cwl"
)

func TestReportWithNoClustersMatchesSummary(t *testing.T) {
	t.Parallel()

	s := &sweep{
		tag:     testCorpusTag,
		root:    "/corpus",
		results: []docResult{{path: docA}, {path: "b.cwl"}},
		passed:  2,
	}

	got := s.report(nil)
	want := s.summary()

	if got != want {
		t.Errorf("report(nil) = %q, want the summary alone %q", got, want)
	}
}

func TestReportRendersEachCluster(t *testing.T) {
	t.Parallel()

	failing := docResult{path: docA, err: errBoom}
	s := &sweep{
		tag:     testCorpusTag,
		results: []docResult{{path: "ok.cwl"}, failing},
		passed:  1,
		failed:  1,
	}

	clusters := clusterFailures([]docResult{failing})

	got := s.report(clusters)
	if !strings.Contains(got, "failure cluster(s)") {
		t.Errorf("report(clusters) is missing the cluster section:\n%s", got)
	}

	if !strings.Contains(got, docA) {
		t.Errorf("report(clusters) does not mention the failing document:\n%s", got)
	}
}

func TestTagBreakdownCountsByFrequencyThenName(t *testing.T) {
	t.Parallel()

	failures := []docResult{
		{entry: &manifestEntry{tags: []string{tagScatter, tagRequired}}},
		{entry: &manifestEntry{tags: []string{tagScatter}}},
		{entry: nil},
	}

	got := tagBreakdown(failures)

	want := []string{"scatter(2)", "required(1)"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tagBreakdown = %v, want %v", got, want)
	}
}

func TestTrimWithNoRootLeavesTextUnchanged(t *testing.T) {
	t.Parallel()

	s := &sweep{}

	if got := s.trim("some text"); got != "some text" {
		t.Errorf("trim = %q, want the text unchanged", got)
	}
}

func TestWriteClusterOmitsTagsAndPeersLinesForAOneMemberUntaggedCluster(t *testing.T) {
	t.Parallel()

	c := &cluster{
		headline: "boom",
		members:  []docResult{{path: docA, err: errBoom}},
		tags:     make(map[string]int),
	}

	var b strings.Builder

	writeCluster(&b, 1, c, identity)

	got := b.String()
	if strings.Contains(got, "tags:") {
		t.Errorf("writeCluster included a tags line for an empty tag set:\n%s", got)
	}

	if strings.Contains(got, "also:") {
		t.Errorf("writeCluster included a peers line for a single-member cluster:\n%s", got)
	}
}

func TestWriteClusterListsTagsAndPeersForAMultiMemberCluster(t *testing.T) {
	t.Parallel()

	c := &cluster{
		headline: "boom",
		members: []docResult{
			{path: docA, err: errBoom},
			{path: "b.cwl", err: errBoom},
		},
		tags: map[string]int{tagScatter: 2},
	}

	var b strings.Builder

	writeCluster(&b, 1, c, identity)

	got := b.String()
	if !strings.Contains(got, "tags: scatter(2)") {
		t.Errorf("writeCluster did not render the tags line:\n%s", got)
	}

	if !strings.Contains(got, "also: b.cwl") {
		t.Errorf("writeCluster did not list the peer:\n%s", got)
	}
}

func TestExpectedNote(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		r    docResult
		want string
	}{
		{name: "no manifest entry", r: docResult{}, want: ""},
		{name: "entry does not always fail", r: docResult{entry: &manifestEntry{alwaysFails: false}}, want: ""},
		{
			name: "entry always fails",
			r:    docResult{entry: &manifestEntry{alwaysFails: true}},
			want: "  [should_fail only]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := expectedNote(tt.r); got != tt.want {
				t.Errorf("expectedNote = %q, want %q", got, tt.want)
			}
		})
	}
}

// namedResults builds n docResult values with distinct paths, for memberNames.
func namedResults(n int) []docResult {
	out := make([]docResult, n)
	for i := range out {
		out[i] = docResult{path: "doc" + strconv.Itoa(i) + ".cwl"}
	}

	return out
}

func TestMemberNames(t *testing.T) {
	t.Parallel()

	if got := memberNames(nil); got != nil {
		t.Errorf("memberNames(nil) = %v, want nil", got)
	}

	tests := []struct {
		name string
		n    int
		want int // number of names memberNames returns
		tail string
	}{
		{name: "below the cap", n: 3, want: 3},
		{name: "exactly the cap", n: maxNamedPeers, want: maxNamedPeers},
		{name: "over the cap", n: maxNamedPeers + 4, want: maxNamedPeers + 1, tail: "... and 4 more"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := memberNames(namedResults(tt.n))
			if len(got) != tt.want {
				t.Fatalf("memberNames(%d) returned %d names, want %d: %v", tt.n, len(got), tt.want, got)
			}

			if tt.tail != "" && got[len(got)-1] != tt.tail {
				t.Errorf("memberNames(%d) tail = %q, want %q", tt.n, got[len(got)-1], tt.tail)
			}
		})
	}
}

func TestPrettyOf(t *testing.T) {
	t.Parallel()

	if got := prettyOf(errPlain); got != errPlain.Error() {
		t.Errorf("prettyOf(plain error) = %q, want %q", got, errPlain.Error())
	}

	se := salad.Errorf(salad.SourceLine{}, "boom")
	if got := prettyOf(se); got != se.Pretty() {
		t.Errorf("prettyOf(salad error) = %q, want %q", got, se.Pretty())
	}
}

func TestPercent(t *testing.T) {
	t.Parallel()

	if got := percent(0, 0); got != "n/a" {
		t.Errorf("percent(0, 0) = %q, want n/a", got)
	}

	if got := percent(1, 4); got != "25.0%" {
		t.Errorf("percent(1, 4) = %q, want 25.0%%", got)
	}
}
