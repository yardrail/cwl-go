package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// writeFixtureRatchet drops a record where a test can point compare at it.
// writeRatchet itself always writes the checked-in file, which a test must not
// touch, so the fixture is encoded here instead.
func writeFixtureRatchet(t *testing.T, r *ratchet) string {
	t.Helper()

	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshalling the fixture record: %v", err)
	}

	path := filepath.Join(t.TempDir(), "ratchet.json")

	err = os.WriteFile(path, raw, 0o600)
	if err != nil {
		t.Fatalf("writing the fixture record: %v", err)
	}

	return path
}

// Test ids used across the fixtures, named so goconst does not see a literal
// repeated in every assertion.
const (
	idAlpha = "alpha"
	idBeta  = "beta"
	idGamma = "gamma"
)

// badgeFixture is one tag listing in the shape cwltest writes.
const badgeFixture = `# ` + "`all`" + ` tests
## List of passed tests
- [alpha](file://x#L1) ([tool](file://t))
- [beta](file://x#L2) ([tool](file://t))
## List of failed tests
- [gamma](file://x#L3) ([tool](file://t))
## List of unsupported tests
- [delta](file://x#L4) ([tool](file://t))
`

func writeBadges(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()

	for name, body := range files {
		err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600)
		if err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	return dir
}

func TestReadBadgesCountsEverySection(t *testing.T) {
	t.Parallel()

	dir := writeBadges(t, map[string]string{"all.md": badgeFixture, "all.json": "{}"})

	results, err := readBadges(dir)
	if err != nil {
		t.Fatalf("readBadges: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("readBadges returned %d tags, want 1", len(results))
	}

	got := results["all"]

	if got.passed != 2 || got.failed != 1 || got.skipped != 1 {
		t.Fatalf("counts = %d/%d/%d, want 2/1/1", got.passed, got.failed, got.skipped)
	}

	if got.total() != 4 {
		t.Fatalf("total = %d, want 4", got.total())
	}

	if !slices.Equal(got.passing, []string{idAlpha, idBeta}) {
		t.Fatalf("passing = %v, want [alpha beta]", got.passing)
	}
}

func TestReadBadgesRejectsAnEmptyDirectory(t *testing.T) {
	t.Parallel()

	_, err := readBadges(t.TempDir())
	if err == nil {
		t.Fatal("readBadges accepted a directory cwltest never wrote to")
	}
}

func TestTagResultRateIsZeroForAnEmptyTag(t *testing.T) {
	t.Parallel()

	empty := &tagResult{}
	if empty.rate() != 0 {
		t.Fatalf("rate = %v, want 0", empty.rate())
	}
}

func TestCompareReportsEveryKindOfRegression(t *testing.T) {
	t.Parallel()

	path := writeFixtureRatchet(t, &ratchet{
		Overall:  tagRecord{Total: 4, Passed: 2},
		Required: tagRecord{Total: 2, Passed: 2},
		Passing:  []string{idAlpha, idBeta},
	})

	observed := &report{
		overall:  &tagResult{passed: 1, failed: 3, passing: []string{idAlpha}},
		required: &tagResult{passed: 1, failed: 1},
		tags:     make(map[string]*tagResult),
	}

	problems := compare(path, observed)
	if len(problems) != 3 {
		t.Fatalf("compare reported %d problems (%v), want 3", len(problems), problems)
	}
}

func TestCompareIsSilentWhenNothingRegressed(t *testing.T) {
	t.Parallel()

	path := writeFixtureRatchet(t, &ratchet{
		Overall:  tagRecord{Total: 4, Passed: 2},
		Required: tagRecord{Total: 2, Passed: 1},
		Passing:  []string{idAlpha, idBeta},
	})

	observed := &report{
		overall:  &tagResult{passed: 3, passing: []string{idAlpha, idBeta, idGamma}},
		required: &tagResult{passed: 2},
		tags:     make(map[string]*tagResult),
	}

	problems := compare(path, observed)
	if len(problems) != 0 {
		t.Fatalf("compare reported %v, want none", problems)
	}
}

func TestResolveSkipsWhenTheRunnerIsNotBuilt(t *testing.T) {
	t.Parallel()

	cfg := &config{runner: filepath.Join(t.TempDir(), "no-such-runner")}

	err := cfg.resolve()
	if err == nil {
		t.Fatal("resolve accepted a runner that is not built")
	}
}
