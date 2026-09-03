package main

import (
	"fmt"
	"io"
	"maps"
	"slices"
)

// requiredTag is the manifest tag marking a test that uses no optional feature.
// It is the project's stated gate, so it is broken out of the tag table.
const requiredTag = "required"

// minTagSize keeps the printed table to the tags worth watching; the long tail
// of one- and two-test tags stays in the badge directory.
const minTagSize = 3

// report is one conformance run, ready to print and evaluate.
type report struct {
	tags     map[string]*tagResult
	overall  *tagResult
	required *tagResult
	junit    string
	corpus   string
}

// newReport assembles a run's numbers, defaulting a tag cwltest did not write
// to an empty result so that neither printing nor comparison has to test for
// its absence.
func newReport(cfg *config, tags map[string]*tagResult, junit string) *report {
	return &report{
		tags:     tags,
		overall:  orEmpty(tags[allTag]),
		required: orEmpty(tags[requiredTag]),
		junit:    junit,
		corpus:   cfg.corpus,
	}
}

// orEmpty substitutes an empty result for a missing tag.
func orEmpty(t *tagResult) *tagResult {
	if t == nil {
		return &tagResult{passing: make([]string, 0)}
	}

	return t
}

// write prints the headline numbers and the per-tag table.
func (r *report) write(out io.Writer) {
	fmt.Fprintf(out, "corpus:  %s\n", r.corpus)
	fmt.Fprintf(out, "junit:   %s\n\n", r.junit)

	fmt.Fprintf(out, "%-26s %5s %6s %6s %6s %8s\n", "tag", "total", "pass", "fail", "skip", "rate")
	r.writeRow(out, allTag, r.overall)
	r.writeRow(out, requiredTag, r.required)

	for _, name := range r.tagNames() {
		r.writeRow(out, name, r.tags[name])
	}
}

// writeRow prints one line of the table.
//
// An absent tag prints as a row of zeroes rather than panicking. The names come from the tag map
// itself so a nil is not reachable today, but the table is also fed the two headline rows, and a
// row that silently disappears from a conformance report is worse than one reading zero.
func (r *report) writeRow(out io.Writer, name string, result *tagResult) {
	row := result
	if row == nil {
		row = &tagResult{}
	}

	fmt.Fprintf(out, "%-26s %5d %6d %6d %6d %7.1f%%\n",
		name, row.total(), row.passed, row.failed, row.skipped, row.rate())
}

// tagNames lists the feature tags worth printing, largest first, with the two
// headline rows removed because they are printed above the table.
func (r *report) tagNames() []string {
	names := make([]string, 0, len(r.tags))

	for name, result := range maps.All(r.tags) {
		if name == allTag || name == requiredTag || result.total() < minTagSize {
			continue
		}

		names = append(names, name)
	}

	slices.SortFunc(names, func(a, b string) int {
		if diff := r.tags[b].total() - r.tags[a].total(); diff != 0 {
			return diff
		}

		return compareStrings(a, b)
	})

	return names
}

// compareStrings orders two tag names, breaking a size tie deterministically.
func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
