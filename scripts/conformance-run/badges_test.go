package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Test ids used across the fixtures, named so goconst does not see a literal
// repeated in every assertion.
const (
	idAlpha = "alpha"
	idBeta  = "beta"
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

func TestResolveSkipsWhenTheRunnerIsNotBuilt(t *testing.T) {
	t.Parallel()

	cfg := &config{runner: filepath.Join(t.TempDir(), "no-such-runner")}

	err := cfg.resolve()
	if err == nil {
		t.Fatal("resolve accepted a runner that is not built")
	}
}
