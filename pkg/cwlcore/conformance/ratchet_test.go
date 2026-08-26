package conformance

import (
	"strings"
	"testing"
)

// record is a small constructor so the table below reads as data.
func record(tag string, docs, passing int, failures ...string) *ratchet {
	return &ratchet{CorpusTag: tag, Documents: docs, Passing: passing, KnownFailures: failures}
}

func TestCompareRatchet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want *ratchet
		got  *ratchet
		// contains is a substring every reported problem set must mention; an empty
		// string means the comparison must report nothing at all.
		contains string
	}{
		{
			name: "identical is clean",
			want: record("v1.2.1", 10, 8, "a.cwl", "b.cwl"),
			got:  record("v1.2.1", 10, 8, "a.cwl", "b.cwl"),
		},
		{
			name:     "a drop is a regression",
			want:     record("v1.2.1", 10, 9, "a.cwl"),
			got:      record("v1.2.1", 10, 8, "a.cwl", "b.cwl"),
			contains: "REGRESSION",
		},
		{
			name:     "a rise asks to be re-recorded",
			want:     record("v1.2.1", 10, 8, "a.cwl", "b.cwl"),
			got:      record("v1.2.1", 10, 9, "a.cwl"),
			contains: envUpdate,
		},
		{
			name:     "a swap at the same count is still a failure",
			want:     record("v1.2.1", 10, 9, "a.cwl"),
			got:      record("v1.2.1", 10, 9, "b.cwl"),
			contains: "newly failing",
		},
		{
			name:     "a corpus tag change short-circuits",
			want:     record("v1.2.0", 10, 9, "a.cwl"),
			got:      record("v1.2.1", 12, 11, "z.cwl"),
			contains: "corpus tag changed",
		},
		{
			name:     "a corpus size change is reported",
			want:     record("v1.2.1", 10, 10),
			got:      record("v1.2.1", 11, 11),
			contains: "corpus size changed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assertProblems(t, compare(tt.want, tt.got), tt.contains)
		})
	}
}

// assertProblems checks a compare result against an expectation: an empty want means the
// comparison must report nothing at all.
func assertProblems(t *testing.T, problems []string, want string) {
	t.Helper()

	if want == "" {
		if len(problems) != 0 {
			t.Fatalf("compare reported %v, want nothing", problems)
		}

		return
	}

	if !strings.Contains(strings.Join(problems, "\n"), want) {
		t.Errorf("compare reported %v, want a problem mentioning %q", problems, want)
	}
}

func TestCompareTagChangeSuppressesEverythingElse(t *testing.T) {
	t.Parallel()

	// Comparing counts across two different corpora is meaningless, so the tag mismatch
	// must be the only thing reported.
	problems := compare(record("v1.2.0", 10, 9, "a.cwl"), record("v1.2.1", 400, 1, "z.cwl"))
	if len(problems) != 1 {
		t.Errorf("compare reported %d problems, want only the tag mismatch: %v", len(problems), problems)
	}
}

func TestCommittedRatchetIsReadable(t *testing.T) {
	t.Parallel()

	// The committed record is what the gate reads. If it goes missing or stops parsing,
	// the sweep degrades into an unconditional failure that nobody can act on -- and this
	// runs without the corpus, so it catches that in a plain "go test ./..." run.
	r, err := readRatchet()
	if err != nil {
		t.Fatalf("readRatchet: %v", err)
	}

	if r.CorpusTag == "" || r.Documents <= 0 {
		t.Errorf("committed record looks empty: %+v", r)
	}

	if r.Passing+len(r.KnownFailures) != r.Documents {
		t.Errorf("committed record does not add up: %d passing + %d known failures != %d documents",
			r.Passing, len(r.KnownFailures), r.Documents)
	}
}

func TestObservedTrimsTheCorpusPathOutOfHeadlines(t *testing.T) {
	t.Parallel()

	s := &sweep{
		tag:     "v1.2.1",
		root:    "/var/cache/cwl-v1.2-v1.2.1",
		results: []docResult{{path: testDoc}},
	}
	clusters := []*cluster{{headline: "undefined reference to file:///var/cache/cwl-v1.2-v1.2.1/tests/b"}}

	got := observed(s, clusters)

	if strings.Contains(got.Clusters[0].Headline, "/var/cache") {
		t.Errorf("committed headline still carries the cache path: %q", got.Clusters[0].Headline)
	}
}

func TestMissingReportsSetDifference(t *testing.T) {
	t.Parallel()

	got := strings.Join(missing([]string{"a", "b", "c"}, []string{"b"}), ",")
	if got != "a,c" {
		t.Errorf("missing = %q, want %q", got, "a,c")
	}
}
