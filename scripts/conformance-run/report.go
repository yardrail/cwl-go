package main

import (
	"fmt"
	"io"
	"maps"
	"slices"
)

// requiredTag marks tests using no optional feature.
const requiredTag = "required"

// minTagSize filters small tags from the printed table.
const minTagSize = 3

// report is one conformance run, ready to print and evaluate.
type report struct {
	tags     map[string]*tagResult
	overall  *tagResult
	required *tagResult
	junit    string
	corpus   string
}

// newReport assembles a run's numbers.
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
		return &tagResult{passing: make([]string, 0), passed: 0, failed: 0, skipped: 0}
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

// writeRow prints one line of the table. Nil-safe.
func (r *report) writeRow(out io.Writer, name string, result *tagResult) {
	row := result
	if row == nil {
		row = &tagResult{passing: nil, passed: 0, failed: 0, skipped: 0}
	}

	fmt.Fprintf(out, "%-26s %5d %6d %6d %6d %7.1f%%\n",
		name, row.total(), row.passed, row.failed, row.skipped, row.rate())
}

// tagNames lists feature tags worth printing, largest first.
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
