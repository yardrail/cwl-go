package conformance

import (
	"errors"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// tagScatter is a stand-in feature tag, named so goconst does not see the literal
// repeated across this package's tests.
const tagScatter = "scatter"

func TestNormalizeErasesDocumentSpecificDetail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msg  string
		want string
	}{
		{
			name: "file URLs collapse",
			msg:  `duplicate identifier file:///tmp/corpus/tests/a.cwl#filelist`,
			want: "duplicate identifier <path>",
		},
		{
			name: "quoted names collapse",
			msg:  `field "location" contains an undefined reference to "x"`,
			want: "field <name> contains an undefined reference to <name>",
		},
		{
			name: "bare numbers collapse",
			msg:  "expected 3 items, got 17",
			want: "expected <n> items, got <n>",
		},
		{
			name: "whitespace is folded",
			msg:  "a\n  b\tc",
			want: "a b c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := normalize(tt.msg)
			if got != tt.want {
				t.Errorf("normalize(%q) = %q, want %q", tt.msg, got, tt.want)
			}
		})
	}
}

func TestNormalizeGroupsTheSameCauseAcrossDocuments(t *testing.T) {
	t.Parallel()

	a := normalize(`file:///a/one.cwl:3:1: duplicate identifier file:///a/one.cwl#out`)
	b := normalize(`file:///b/two.cwl:99:7: duplicate identifier file:///b/two.cwl#result`)

	if a != b {
		t.Errorf("two documents with the same cause produced different keys:\n  %q\n  %q", a, b)
	}
}

func TestSignatureUsesLeavesNotMemberOrder(t *testing.T) {
	t.Parallel()

	// A union rejection records one child per member. Two documents that failed the same
	// way must cluster together even though the member messages arrive in a different
	// order and name different values.
	first := salad.Group(salad.SourceLine{}, "value is not valid",
		salad.Errorf(salad.SourceLine{}, "expected a %s", "File"),
		salad.Errorf(salad.SourceLine{}, "expected a %s", "Directory"),
	)
	second := salad.Group(salad.SourceLine{}, "value is not valid",
		salad.Errorf(salad.SourceLine{}, "expected a %s", "Directory"),
		salad.Errorf(salad.SourceLine{}, "expected a %s", "File"),
	)

	firstKey := signature(first).key

	secondKey := signature(second).key
	if firstKey != secondKey {
		t.Errorf("signatures differ by member order:\n  %q\n  %q", firstKey, secondKey)
	}
}

func TestSignatureHeadlineJoinsRootAndTip(t *testing.T) {
	t.Parallel()

	err := salad.Group(salad.SourceLine{}, "document has unresolved links",
		salad.Errorf(salad.SourceLine{}, "field %q is undefined", "location"),
	)

	headline := signature(err).headline

	if !strings.Contains(headline, "unresolved links") || !strings.Contains(headline, "location") {
		t.Errorf("headline = %q, want it to mention both the root and the tip", headline)
	}
}

// errPlain stands in for an error that did not come from pkg/salad.
var errPlain = errors.New("no such file or directory")

func TestSignatureHandlesAPlainError(t *testing.T) {
	t.Parallel()

	sig := signature(errPlain)

	if sig.key == "" || sig.headline == "" {
		t.Errorf("signature(plain error) = %+v, want both fields non-empty", sig)
	}
}

func TestClusterFailuresRanksByDocumentCount(t *testing.T) {
	t.Parallel()

	rare := salad.Errorf(salad.SourceLine{}, "a rare problem")
	common := func(n string) error { return salad.Errorf(salad.SourceLine{}, "a common problem with %q", n) }

	failures := []docResult{
		{path: "one.cwl", err: rare},
		{path: "two.cwl", err: common("x"), entry: &manifestEntry{tags: []string{tagScatter}}},
		{path: "three.cwl", err: common("y"), entry: &manifestEntry{tags: []string{tagScatter}}},
		{path: "four.cwl", err: common("z")},
	}

	clusters := clusterFailures(failures)

	if len(clusters) != 2 {
		t.Fatalf("got %d clusters, want 2: %+v", len(clusters), clusters)
	}

	if clusters[0].size() != 3 {
		t.Errorf("largest cluster has %d members, want 3", clusters[0].size())
	}

	if clusters[0].tags[tagScatter] != 2 {
		t.Errorf("scatter tag counted %d times, want 2", clusters[0].tags[tagScatter])
	}

	if clusters[0].representative().path != "two.cwl" {
		t.Errorf("representative = %q, want the first member in corpus order", clusters[0].representative().path)
	}
}

func TestSignatureFallsBackForAnErrorTreeWithNoLeaves(t *testing.T) {
	t.Parallel()

	// A zero-value *salad.Error has no Msg and no Children, so se.Leaves() is empty and
	// signature must fall back to se.Error() rather than indexing into an empty slice.
	se := &salad.Error{}

	sig := signature(se)

	want := se.Error()
	if sig.headline != want {
		t.Errorf("headline = %q, want the se.Error() fallback %q", sig.headline, want)
	}

	if sig.key != normalize(want) {
		t.Errorf("key = %q, want normalize(%q)", sig.key, want)
	}
}

func TestTopTagsOrdersByFrequency(t *testing.T) {
	t.Parallel()

	c := &cluster{tags: map[string]int{"a": 1, "b": 9, "c": 5}}

	got := strings.Join(c.topTags(2), ",")
	if got != "b,c" {
		t.Errorf("topTags(2) = %q, want %q", got, "b,c")
	}
}
