package conformance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// The environment variables the driver honours. They are the Stage 0 sweep's, so a
// developer who has already pointed that at a checkout does not point this one at it a
// second time. See the package doc comment.
const (
	envEnable  = "CWL_CONFORMANCE"
	envCorpus  = "CWL_CONFORMANCE_CORPUS"
	envCache   = "CWL_CONFORMANCE_CACHE"
	envHarness = "CWLTEST"
)

// Layout of the pinned corpus and of the cache the Stage 0 sweep unpacks it into.
const (
	manifestName = "conformance_tests.yaml"
	corpusPrefix = "cwl-v1.2-"
	cacheVendor  = "cwl-go"
	cacheSuite   = "conformance"
)

// errNoCorpus reports that no unpacked corpus could be found.
//
// It is a reason to skip rather than to fail. This package deliberately does not fetch the
// corpus: the Stage 0 sweep already owns that code, downloading the same tarball from two
// places would race, and a driver whose tests reach the network is one that cannot run on
// a machine with none. "task test:conformance" puts it in the cache.
var errNoCorpus = errors.New("no unpacked cwl-v1.2 corpus")

// findCorpus locates an already-unpacked corpus: an explicit CWL_CONFORMANCE_CORPUS
// checkout, or the cache entry for the tag the vendored schema was cut from.
func findCorpus() (string, error) {
	explicit := strings.TrimSpace(os.Getenv(envCorpus))
	if explicit != "" {
		return checkedCorpus(explicit, envCorpus+" names it")
	}

	tag := cwlcore.SchemaVersion()
	if tag == "" {
		return "", fmt.Errorf("%w: the vendored schema declares no version to pin one to", errNoCorpus)
	}

	return checkedCorpus(filepath.Join(cacheDir(), corpusPrefix+tag),
		"run 'task test:conformance' to fetch it")
}

// checkedCorpus makes root absolute and confirms it holds a manifest, naming remedy in the
// error when it does not.
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
