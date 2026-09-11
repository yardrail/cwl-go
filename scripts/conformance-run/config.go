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

// Environment variables (shared with the Stage 0 sweep).
const (
	envCorpus = "CWL_CONFORMANCE_CORPUS"
	envCache  = "CWL_CONFORMANCE_CACHE"
)

// Corpus and repository layout constants.
const (
	manifestName = "conformance_tests.yaml"
	versionPath  = "pkg/cwlcore/schema/VERSION"
	corpusPrefix = "cwl-v1.2-"
	runnerPath   = "bin/cwl-run"
	defaultOut   = ".task/conformance"
	harnessName  = "cwltest"
)

// Defaults for the cwltest invocation.
const (
	defaultJobs    = 4
	defaultTimeout = 2 * time.Minute
	dirPerm        = 0o750
)

// errSkipped wraps the reason a run was skipped (exits 0).
var errSkipped = errors.New("conformance run skipped")

// config is one resolved invocation of the suite.
type config struct {
	// badges reads a previous run's badge directory instead of running.
	badges string

	corpus       string
	runner       string
	outDir       string
	timeout      time.Duration
	jobs         int
	gateRequired bool
}

// defaultConfig fills every field from the environment and repository layout.
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

// defaultCorpus returns the corpus path from the environment or cache.
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

// schemaVersion reads the tag the CWL schema was vendored from.
func schemaVersion() string {
	raw, err := os.ReadFile(versionPath)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(raw))
}

// resolve validates and completes the configuration.
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
