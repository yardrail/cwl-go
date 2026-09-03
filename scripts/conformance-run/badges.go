package main

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// status is one test's outcome, as cwltest counted it.
type status string

// The three outcomes the cwl-runner contract produces: a matching output
// object, a mismatch or unexpected exit, and exit 33 for a feature the engine
// does not implement.
const (
	statusPass status = "pass"
	statusFail status = "fail"
	statusSkip status = "skip"
)

// allTag is the synthetic tag cwltest computes over every test.
const allTag = "all"

// badgeExt is the per-tag listing this command reads.
const badgeExt = ".md"

// errNoBadges reports a badge directory cwltest left empty, which means the
// harness never got as far as running a test.
var errNoBadges = errors.New("cwltest wrote no badge listings")

// Section headings and entry shape of a cwltest badge listing.
var (
	sectionPattern = regexp.MustCompile(`(?m)^## List of (passed|failed|unsupported) tests\s*$`)
	entryPattern   = regexp.MustCompile(`(?m)^- \[([^\]]+)\]`)
)

// sectionStatus maps a badge listing's section heading onto an outcome.
var sectionStatus = map[string]status{
	"passed":      statusPass,
	"failed":      statusFail,
	"unsupported": statusSkip,
}

// tagResult is one feature tag's outcome, and the test ids behind it.
type tagResult struct {
	passing []string
	passed  int
	failed  int
	skipped int
}

// total is how many tests carry the tag.
//
// A nil receiver counts zero, so a tag the run never observed reports as empty rather than
// panicking. The check is written out rather than left implicit because a guard inside another
// method is not visible to static analysis across the call.
func (t *tagResult) total() int {
	if t == nil {
		return 0
	}

	return t.passed + t.failed + t.skipped
}

// rate is the tag's pass rate as a percentage, or 0 for an empty or absent tag.
func (t *tagResult) rate() float64 {
	if t == nil || t.total() == 0 {
		return 0
	}

	return 100 * float64(t.passed) / float64(t.total())
}

// readBadges turns cwltest's badge directory into one tagResult per tag.
//
// The badge listings are read rather than the JUnit XML because cwltest does
// not record a failure element for a should_fail test that wrongly succeeded,
// so the XML undercounts failures. The listings and cwltest's own printed
// tally agree; the XML does not.
func readBadges(dir string) (map[string]*tagResult, error) {
	entries, err := filepath.Glob(filepath.Join(dir, "*"+badgeExt))
	if err != nil {
		return nil, err
	}

	if len(entries) == 0 {
		return nil, errNoBadges
	}

	results := make(map[string]*tagResult, len(entries))

	for _, path := range entries {
		result, readErr := readBadge(path)
		if readErr != nil {
			return nil, readErr
		}

		results[strings.TrimSuffix(filepath.Base(path), badgeExt)] = result
	}

	return results, nil
}

// readBadge parses one tag's listing.
func readBadge(path string) (*tagResult, error) {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}

	result := &tagResult{passing: make([]string, 0)}

	for heading, body := range sections(string(raw)) {
		ids := testIDs(body)

		switch sectionStatus[heading] {
		case statusPass:
			result.passed += len(ids)
			result.passing = append(result.passing, ids...)
		case statusFail:
			result.failed += len(ids)
		case statusSkip:
			result.skipped += len(ids)
		default:
			// A heading the pattern matched but sectionStatus does not know.
			// Unreachable while the two agree; ignored rather than fatal so a
			// new cwltest section cannot break the build.
		}
	}

	slices.Sort(result.passing)

	return result, nil
}

// sections yields each "## List of ... tests" heading keyword and the body
// beneath it.
func sections(doc string) map[string]string {
	found := make(map[string]string)

	matches := sectionPattern.FindAllStringSubmatchIndex(doc, -1)
	for i, match := range matches {
		end := len(doc)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}

		found[doc[match[2]:match[3]]] = doc[match[1]:end]
	}

	return found
}

// testIDs lists the conformance test ids linked in a listing section.
func testIDs(body string) []string {
	matches := entryPattern.FindAllStringSubmatch(body, -1)

	ids := make([]string, 0, len(matches))
	for _, match := range matches {
		ids = append(ids, match[1])
	}

	return ids
}
