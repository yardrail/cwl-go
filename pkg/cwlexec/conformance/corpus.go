package conformance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Environment variables (shared with the Stage 0 sweep).
const (
	envEnable  = "CWL_CONFORMANCE"
	envCorpus  = "CWL_CONFORMANCE_CORPUS"
	envCache   = "CWL_CONFORMANCE_CACHE"
	envHarness = "CWLTEST"
)

// Corpus layout constants.
const (
	manifestName = "conformance_tests.yaml"
	corpusPrefix = "cwl-v1.2-"
	cacheVendor  = "cwl-go"
	cacheSuite   = "conformance"
)

// errNoCorpus reports that no unpacked corpus was found. Reason to skip, not fail.
var errNoCorpus = errors.New("no unpacked cwl-v1.2 corpus")

// findCorpus locates an already-unpacked corpus.
func findCorpus() (string, error) {
	return findCorpusWith(cwlcore.SchemaVersion)
}

// findCorpusWith is [findCorpus] with a pluggable schema-version lookup.
func findCorpusWith(schemaVersion func() string) (string, error) {
	explicit := strings.TrimSpace(os.Getenv(envCorpus))
	if explicit != "" {
		return checkedCorpus(explicit, envCorpus+" names it")
	}

	tag := schemaVersion()
	if tag == "" {
		return "", fmt.Errorf("%w: the vendored schema declares no version to pin one to", errNoCorpus)
	}

	return checkedCorpus(filepath.Join(cacheDir(), corpusPrefix+tag),
		"run 'task test:conformance' to fetch it")
}

// checkedCorpus validates root holds a manifest and returns its absolute path.
func checkedCorpus(root, remedy string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}

	info, statErr := os.Stat(filepath.Join(abs, manifestName))
	if statErr != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %s holds no %s (%s)", errNoCorpus, abs, manifestName, remedy)
	}

	return abs, nil
}

// cacheDir mirrors the Stage 0 sweep's download cache location.
func cacheDir() string {
	override := strings.TrimSpace(os.Getenv(envCache))
	if override != "" {
		return override
	}

	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}

	return filepath.Join(base, cacheVendor, cacheSuite)
}
