package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
)

// maxProblems is how many distinct regressions compare can report, used only
// to size its result slice.
const maxProblems = 3

// jsonIndent keeps the committed record readable in a diff.
const jsonIndent = "  "

// filePerm is the mode the record is written with.
const filePerm = 0o600

// tagRecord is one tag's committed counts. Only the counts are compared; the
// per-tag pass lists would triple the file for no extra signal, because every
// test in them also appears in the overall list.
type tagRecord struct {
	Total   int `json:"total"`
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

// ratchet is the checked-in record of where the execution suite stands.
//
// It records the passing test ids rather than only a count, because a count
// alone lets one test start passing while another stops — which is exactly the
// regression a ratchet exists to catch.
type ratchet struct {
	Tags     map[string]tagRecord `json:"tags"`
	Required tagRecord            `json:"required"`
	Overall  tagRecord            `json:"overall"`
	Passing  []string             `json:"passing"`
}

// asRatchet renders the run as the record it would commit.
func (r *report) asRatchet() *ratchet {
	tags := make(map[string]tagRecord, len(r.tags))
	for name, result := range maps.All(r.tags) {
		tags[name] = asTagRecord(result)
	}

	return &ratchet{
		Overall:  asTagRecord(r.overall),
		Required: asTagRecord(r.required),
		Tags:     tags,
		Passing:  slices.Clone(r.overall.passing),
	}
}

// asTagRecord reduces a tag result to its committed counts.
func asTagRecord(t *tagResult) tagRecord {
	return tagRecord{Total: t.total(), Passed: t.passed, Failed: t.failed, Skipped: t.skipped}
}

// writeRatchet rewrites the committed record.
func writeRatchet(r *ratchet) error {
	raw, err := json.MarshalIndent(r, "", jsonIndent)
	if err != nil {
		return err
	}

	file, err := os.OpenFile(ratchetName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, filePerm)
	if err != nil {
		return err
	}

	_, err = file.Write(append(raw, '\n'))

	return errors.Join(err, file.Close())
}

// readRatchet loads the committed record.
func readRatchet(path string) (*ratchet, error) {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}

	r := new(ratchet)

	err = json.Unmarshal(raw, r)
	if err != nil {
		return nil, err
	}

	return r, nil
}

// compare reports every way the run is worse than the committed record.
//
// A missing record is not a regression: it is the state before the first
// -update, and failing there would make the tool impossible to adopt.
func compare(path string, r *report) []string {
	recorded, err := readRatchet(path)
	if err != nil {
		return []string{fmt.Sprintf("no ratchet record at %s (run with -update to write one): %v", path, err)}
	}

	problems := make([]string, 0, maxProblems)

	if r.overall.passed < recorded.Overall.Passed {
		problems = append(problems, fmt.Sprintf("total passing fell from %d to %d",
			recorded.Overall.Passed, r.overall.passed))
	}

	if r.required.passed < recorded.Required.Passed {
		problems = append(problems, fmt.Sprintf("required passing fell from %d to %d",
			recorded.Required.Passed, r.required.passed))
	}

	lost := regressed(recorded.Passing, r.overall.passing)
	if len(lost) > 0 {
		problems = append(problems, fmt.Sprintf("%d test(s) that used to pass no longer do: %v", len(lost), lost))
	}

	return problems
}

// regressed lists the recorded ids that are no longer passing.
func regressed(recorded, observed []string) []string {
	passing := make(map[string]bool, len(observed))
	for _, id := range observed {
		passing[id] = true
	}

	lost := make([]string, 0)

	for _, id := range recorded {
		if !passing[id] {
			lost = append(lost, id)
		}
	}

	return lost
}
