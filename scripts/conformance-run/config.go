package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Environment variables honoured, mirroring the Stage 0 sweep's so that a
// developer who has already pointed that at a checkout does not have to point
// this one at it a second time.
const (
	envCorpus = "CWL_CONFORMANCE_CORPUS"
	envCache  = "CWL_CONFORMANCE_CACHE"
)

// Layout of the pinned corpus and of this repository.
const (
	manifestName = "conformance_tests.yaml"
	versionPath  = "pkg/cwlcore/schema/VERSION"
	corpusPrefix = "cwl-v1.2-"
	runnerPath   = "bin/cwl-run"
	defaultOut   = ".task/conformance"
	harnessName  = "cwltest"
)

// Defaults for the cwltest invocation. The per-test timeout is far below
// cwltest's own ten-minute default because a conformance test that takes two
// minutes on this engine is hung, not slow.
const (
	defaultJobs    = 4
	defaultTimeout = 2 * time.Minute
	dirPerm        = 0o750
)

// errSkipped wraps the reason a run could not happen for a reason that is not
// the engine's fault. It is reported and exits 0.
var errSkipped = errors.New("conformance run skipped")

// config is one resolved invocation of the suite.
type config struct {
	// badges names a badge directory a previous run left behind, read instead
	// of producing one. See [recorded].
	badges string

	corpus       string
	runner       string
	outDir       string
	timeout      time.Duration
	jobs         int
	gateRequired bool
}

// defaultConfig fills every field from the environment and the repository
// layout, before flags get a chance to override them.
func defaultConfig() *config {
	return &config{
		badges:       "",
		corpus:       defaultCorpus(),
		runner:       runnerPath,
		outDir:       defaultOut,
		timeout:      defaultTimeout,
		jobs:         defaultJobs,
		gateRequired: false,
	}
}

// defaultCorpus is the corpus the Stage 0 sweep would have unpacked: an
// explicit checkout if one is configured, otherwise the cache entry for the tag
// the vendored schema was cut from.
func defaultCorpus() string {
	explicit := strings.TrimSpace(os.Getenv(envCorpus))
	if explicit != "" {
		return explicit
	}

	return filepath.Join(cacheDir(), corpusPrefix+schemaVersion())
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

	return filepath.Join(base, "cwl-go", "conformance")
}

// schemaVersion reads the tag the CWL schema was vendored from. The corpus is
// pinned to it rather than floating, because sweeping a different tag than the
// schema was cut from would measure two things at once.
func schemaVersion() string {
	raw, err := os.ReadFile(versionPath)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(raw))
}

// resolve turns the configuration into a runnable one, or into the reason it is
// not runnable.
func (c *config) resolve() error {
	_, err := exec.LookPath(harnessName)
	if err != nil {
		return fmt.Errorf("%w: %s is not on PATH (pip install cwltest): %w", errSkipped, harnessName, err)
	}

	runner, err := filepath.Abs(c.runner)
	if err != nil {
		return err
	}

	info, err := os.Stat(runner)
	if err != nil || info.IsDir() {
		return fmt.Errorf("%w: %s is not built (run 'task build')", errSkipped, c.runner)
	}

	c.runner = runner

	return c.resolveCorpus()
}

// resolveCorpus checks that the corpus is present and makes its path absolute.
func (c *config) resolveCorpus() error {
	if c.corpus == "" {
		return fmt.Errorf("%w: no corpus configured and %s is unreadable", errSkipped, versionPath)
	}

	root, err := filepath.Abs(c.corpus)
	if err != nil {
		return err
	}

	info, err := os.Stat(filepath.Join(root, manifestName))
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s holds no %s (run 'task test:conformance' to fetch it)",
			errSkipped, c.corpus, manifestName)
	}

	c.corpus = root

	return nil
}
