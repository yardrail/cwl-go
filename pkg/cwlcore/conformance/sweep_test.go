package conformance

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// The testdata/loadone fixture documents, named so goconst does not see their literals
// repeated across this file's tests.
const (
	fixtureValidDoc   = "valid.cwl"
	fixtureInvalidDoc = "invalid.cwl"
	fixtureGraphDoc   = "graph.cwl"
)

// loadOneFixtureCorpus is the small local corpus loadOne and run are exercised against,
// rather than the real cwl-v1.2 corpus, so these tests run without CWL_CONFORMANCE=1 and
// without any network access.
var loadOneFixtureCorpus = &corpus{root: "testdata/loadone", tag: testFixtureTag}

func TestLoadOneValidToolLoadsCleanly(t *testing.T) {
	t.Parallel()

	got := loadOne(t.Context(), loadOneFixtureCorpus, fixtureValidDoc, manifest{})
	if !got.ok() {
		t.Errorf("loadOne(%s) = %+v, want ok", fixtureValidDoc, got)
	}

	if got.graphOnly {
		t.Errorf("loadOne(%s) reported graphOnly, want false", fixtureValidDoc)
	}
}

func TestLoadOneDocumentMissingClassFailsToLoad(t *testing.T) {
	t.Parallel()

	got := loadOne(t.Context(), loadOneFixtureCorpus, fixtureInvalidDoc, manifest{})
	if got.ok() {
		t.Errorf("loadOne(%s) reported ok, want a load error", fixtureInvalidDoc)
	}

	if got.graphOnly {
		t.Errorf("loadOne(%s) reported graphOnly, want false", fixtureInvalidDoc)
	}
}

func TestLoadOneGraphWithNoMainRetriesAsWholeGraphDecode(t *testing.T) {
	t.Parallel()

	got := loadOne(t.Context(), loadOneFixtureCorpus, fixtureGraphDoc, manifest{})
	if !got.ok() {
		t.Errorf("loadOne(%s) = %+v, want ok (recovered via decodeWholeGraph)", fixtureGraphDoc, got)
	}

	if !got.graphOnly {
		t.Errorf("loadOne(%s) did not report graphOnly", fixtureGraphDoc)
	}
}

// TestRunTalliesAndReportsFailures runs the same local fixture corpus through run(), which
// exercises the worker pool and the tally alongside sweep.failures/failingPaths.
func TestRunTalliesAndReportsFailures(t *testing.T) {
	t.Parallel()

	docs := []string{fixtureValidDoc, fixtureInvalidDoc, fixtureGraphDoc}

	s := run(t.Context(), loadOneFixtureCorpus, docs, manifest{})

	if s.tag != testFixtureTag || s.root != loadOneFixtureCorpus.root {
		t.Errorf("run() sweep = %+v, want tag/root taken from the corpus", s)
	}

	if s.passed != 2 || s.failed != 1 {
		t.Errorf("run() passed=%d failed=%d, want passed=2 failed=1", s.passed, s.failed)
	}

	failures := s.failures()
	if len(failures) != 1 || failures[0].path != fixtureInvalidDoc {
		t.Errorf("failures() = %+v, want just %s", failures, fixtureInvalidDoc)
	}

	paths := s.failingPaths()
	if len(paths) != 1 || paths[0] != fixtureInvalidDoc {
		t.Errorf("failingPaths() = %v, want [%s]", paths, fixtureInvalidDoc)
	}
}

// TestStage0Sweep loads every *.cwl document in the pinned cwl-v1.2 corpus through
// pkg/salad and pkg/cwlcore and holds the pass count to the committed ratchet.
//
// It is opt-in (CWL_CONFORMANCE=1) and skips rather than fails when the corpus cannot be
// obtained, so "go test ./..." stays fast on a developer machine and green on one with no
// network. See the package doc comment for the full environment.
func TestStage0Sweep(t *testing.T) {
	t.Parallel()

	requireEnabled(t)

	c := requireCorpus(t)

	docs, err := c.documents()
	if err != nil {
		t.Fatalf("walking %s/%s: %v", c.root, testsDirName, err)
	}

	if len(docs) == 0 {
		t.Fatalf("no %s documents under %s/%s", cwlExt, c.root, testsDirName)
	}

	m, err := loadManifest(c)
	if err != nil {
		t.Fatalf("loading %s through pkg/salad: %v", manifestName, err)
	}

	result := run(t.Context(), c, docs, m)
	failures := result.failures()
	clusters := clusterFailures(failures)

	t.Log("\n" + result.report(clusters))

	if len(failures) > 0 {
		t.Logf("failing documents by feature tag: %s", strings.Join(tagBreakdown(failures), " "))
	}

	checkRatchet(t, result, clusters)
}

// TestStage0ManifestIndexesTheCorpus is a sanity check on the manifest reader itself: if
// $import resolution or the relative-path handling silently broke, every sub-suite
// document would look untagged and the tag breakdown in the sweep report would quietly
// become useless.
func TestStage0ManifestIndexesTheCorpus(t *testing.T) {
	t.Parallel()

	requireEnabled(t)

	c := requireCorpus(t)

	m, err := loadManifest(c)
	if err != nil {
		t.Fatalf("loading %s through pkg/salad: %v", manifestName, err)
	}

	// A path that only exists inside an $import-ed sub-suite. Resolving it proves the
	// sub-suite was inlined and that its relative "tool" values were resolved against the
	// sub-suite's own directory rather than the corpus root.
	const nested = "tests/scatter/simple-simple-scatter.cwl"

	entry, ok := m[nested]
	if !ok {
		t.Fatalf("manifest has no entry for %s; $import resolution or path rebasing is broken", nested)
	}

	if len(entry.tags) == 0 || len(entry.ids) == 0 {
		t.Errorf("entry for %s carries no ids/tags: %+v", nested, entry)
	}
}

// checkRatchet compares the observed sweep against the committed record, rewriting it
// instead when CWL_CONFORMANCE_UPDATE=1.
func checkRatchet(t *testing.T, result *sweep, clusters []*cluster) {
	t.Helper()

	got := observed(result, clusters)

	if os.Getenv(envUpdate) == "1" {
		err := writeRatchet(got)
		if err != nil {
			t.Fatalf("rewriting %s: %v", ratchetPath, err)
		}

		t.Logf("rewrote %s: %d/%d documents loaded", ratchetPath, got.Passing, got.Documents)

		return
	}

	want, err := readRatchet()
	if err != nil {
		t.Fatalf("%v -- create it by running with %s=1", err, envUpdate)
	}

	for _, problem := range compare(want, got) {
		t.Errorf("Stage 0 ratchet: %s", problem)
	}
}

// requireEnabled skips unless the sweep was asked for.
func requireEnabled(t *testing.T) {
	t.Helper()

	if os.Getenv(envEnable) != "1" {
		t.Skipf("set %s=1 to run the Stage 0 conformance sweep", envEnable)
	}
}

// requireCorpus locates the pinned corpus, skipping when it is neither cached nor
// reachable. A developer with no network gets a skip with instructions, never a failure.
func requireCorpus(t *testing.T) *corpus {
	t.Helper()

	tag := cwlcore.SchemaVersion()

	c, err := sharedCorpus(context.WithoutCancel(t.Context()), tag, defaultCacheDir())
	if err != nil {
		t.Skipf(
			"cwl-v1.2 %s corpus unavailable (%v)\n"+
				"populate the cache with:\n"+
				"  mkdir -p %[4]s && curl -sSL %[5]s%[1]s | tar -xz -C %[4]s &&\\\n"+
				"  mv %[4]s/cwl-v1.2-* %[4]s/cwl-v1.2-%[1]s\n"+
				"or point %[3]s at an existing checkout",
			tag, err, envCorpus, defaultCacheDir(), codeloadBase)
	}

	if c.fetched {
		t.Logf("fetched cwl-v1.2 %s into %s", tag, c.root)
	}

	return c
}
