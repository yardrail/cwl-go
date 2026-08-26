package conformance

import (
	"errors"
	"testing"
)

// TestFindCorpusReportsNoSchemaVersionToPin supplies a schema-version lookup that reports no
// version at all, exercising the branch [cwlcore.SchemaVersion] itself can never reach: it
// always reads a non-empty embedded schema/VERSION file.
func TestFindCorpusReportsNoSchemaVersionToPin(t *testing.T) {
	t.Setenv(envCorpus, "")

	_, err := findCorpusWith(func() string { return "" })
	if !errors.Is(err, errNoCorpus) {
		t.Errorf("findCorpusWith = %v, want it to wrap errNoCorpus", err)
	}
}

// TestCacheDirFallsBackToTempDir clears every variable [cacheDir] and [os.UserCacheDir]
// consult, which is what makes UserCacheDir report an error and cacheDir fall back to
// [os.TempDir].
func TestCacheDirFallsBackToTempDir(t *testing.T) {
	t.Setenv(envCache, "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "")

	got := cacheDir()
	if got == "" {
		t.Error("cacheDir returned an empty path")
	}
}
