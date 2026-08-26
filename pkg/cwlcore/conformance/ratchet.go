package conformance

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

// ratchetPath is the checked-in record of where the sweep stands, relative to this
// package directory. It is committed on purpose: it is what turns the pass count from a
// number somebody once quoted into a gate that a regression trips.
const ratchetPath = "testdata/stage0-ratchet.json"

// jsonIndent keeps the committed record readable in a diff.
const jsonIndent = "  "

// maxProblems is the number of distinct problems compare can report, used only to size
// its result slice.
const maxProblems = 4

// errRatchetMissing reports that the checked-in record could not be read.
var errRatchetMissing = errors.New("the Stage 0 ratchet record is missing or unreadable")

// clusterNote is a human-readable record of one failure cluster. It is written for the
// reader of the committed file and is never compared, so a change in wording cannot fail
// the build on its own.
type clusterNote struct {
	// Headline is the representative leaf message for the cluster.
	Headline string `json:"headline"`
	// Documents is how many documents failed this way.
	Documents int `json:"documents"`
}

// ratchet is the checked-in Stage 0 record.
type ratchet struct {
	// CorpusTag is the cwl-v1.2 release the record was taken against.
	CorpusTag string `json:"corpusTag"`
	// Documents is how many *.cwl files the corpus held.
	Documents int `json:"documents"`
	// Passing is how many of them loaded. This is the ratcheted number.
	Passing int `json:"passing"`
	// KnownFailures lists every document that did not load, sorted.
	KnownFailures []string `json:"knownFailures"`
	// Clusters summarises why, for the reader. Informational only.
	Clusters []clusterNote `json:"clusters"`
}

// readRatchet loads the committed record.
func readRatchet() (*ratchet, error) {
	return readRatchetFrom(ratchetPath)
}

// readRatchetFrom loads the record at path. It is the body of readRatchet, taken as a
// parameter so tests can point it at a fixture instead of the committed record.
func readRatchetFrom(path string) (*ratchet, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", errRatchetMissing, path)
	}

	var r ratchet

	err = json.Unmarshal(raw, &r)
	if err != nil {
		return nil, err
	}

	return &r, nil
}

// writeRatchet rewrites the committed record from an observed sweep.
func writeRatchet(r *ratchet) error {
	return writeRatchetTo(ratchetPath, r)
}

// writeRatchetTo rewrites the record at path from an observed sweep. It is the body of
// writeRatchet, taken as a parameter so tests can point it at a temp file instead of the
// committed record.
func writeRatchetTo(path string, r *ratchet) error {
	raw, err := json.MarshalIndent(r, "", jsonIndent)
	if err != nil {
		return err
	}

	return os.WriteFile(path, append(raw, '\n'), filePerm)
}

// observed builds the record this sweep would commit.
func observed(s *sweep, clusters []*cluster) *ratchet {
	failing := s.failingPaths()
	slices.Sort(failing)

	// The corpus lives in a per-machine cache directory, so its path must not reach the
	// committed file: it would differ for every developer and churn the diff.
	notes := make([]clusterNote, 0, len(clusters))
	for _, c := range clusters {
		notes = append(notes, clusterNote{Headline: s.trim(c.headline), Documents: c.size()})
	}

	return &ratchet{
		CorpusTag:     s.tag,
		Documents:     len(s.results),
		Passing:       s.passed,
		KnownFailures: failing,
		Clusters:      notes,
	}
}

// compare checks an observed sweep against the committed record and returns one message
// per problem found. An empty result means the sweep is exactly where it was recorded.
//
// Both directions are failures. A drop is a regression. A rise is a stale record: leaving
// it unchanged would let the next regression hide inside the slack, which is precisely
// what a ratchet exists to prevent.
func compare(want, got *ratchet) []string {
	problems := make([]string, 0, maxProblems)

	if want.CorpusTag != got.CorpusTag {
		problems = append(problems, fmt.Sprintf(
			"corpus tag changed: recorded %s, swept %s -- re-record with %s=1",
			want.CorpusTag, got.CorpusTag, envUpdate))

		return problems
	}

	problems = appendCountProblems(problems, want, got)

	return appendSetProblems(problems, want, got)
}

// appendCountProblems reports a change in the ratcheted pass count or the corpus size.
func appendCountProblems(problems []string, want, got *ratchet) []string {
	if want.Documents != got.Documents {
		problems = append(problems, fmt.Sprintf(
			"corpus size changed: recorded %d documents, swept %d", want.Documents, got.Documents))
	}

	switch {
	case got.Passing < want.Passing:
		problems = append(problems, fmt.Sprintf(
			"REGRESSION: %d documents loaded, down from the recorded %d", got.Passing, want.Passing))
	case got.Passing > want.Passing:
		problems = append(problems, fmt.Sprintf(
			"progress: %d documents loaded, up from the recorded %d -- re-record with %s=1",
			got.Passing, want.Passing, envUpdate))
	default:
	}

	return problems
}

// appendSetProblems reports documents that changed side without changing the total, which
// a count comparison alone would miss.
func appendSetProblems(problems []string, want, got *ratchet) []string {
	broke := missing(got.KnownFailures, want.KnownFailures)
	if len(broke) > 0 {
		problems = append(problems, "newly failing documents:\n  "+strings.Join(broke, "\n  "))
	}

	fixed := missing(want.KnownFailures, got.KnownFailures)
	if len(fixed) > 0 {
		problems = append(problems, fmt.Sprintf(
			"documents that now load and should leave the record (%s=1):\n  %s",
			envUpdate, strings.Join(fixed, "\n  ")))
	}

	return problems
}

// missing returns the members of a that are not in b.
func missing(a, b []string) []string {
	index := make(map[string]struct{}, len(b))
	for _, v := range b {
		index[v] = struct{}{}
	}

	out := make([]string, 0, len(a))

	for _, v := range a {
		_, found := index[v]
		if !found {
			out = append(out, v)
		}
	}

	return out
}
