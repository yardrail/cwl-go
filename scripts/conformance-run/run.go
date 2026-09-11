package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// Names of the outputs cwltest writes under the -out directory.
const (
	junitName  = "conformance.xml"
	badgesName = "badges"
)

// outputs is where cwltest writes its two machine-readable artefacts.
type outputs struct {
	// junit is the JUnit XML path, kept for CI to publish.
	junit string
	// badges is the per-tag listing directory this command reads its numbers from.
	badges string
}

// gather resolves the configuration, runs cwltest and reads its result.
func gather(ctx context.Context, cfg *config) (*report, error) {
	if cfg.badges != "" {
		return recorded(cfg)
	}

	err := cfg.resolve()
	if err != nil {
		return nil, err
	}

	paths, err := prepareOutputs(cfg.outDir)
	if err != nil {
		return nil, err
	}

	err = runCWLTest(ctx, cfg, paths)
	if err != nil {
		return nil, err
	}

	statuses, err := readBadges(paths.badges)
	if err != nil {
		return nil, err
	}

	return newReport(cfg, statuses, paths.junit), nil
}

// recorded reads a previous run's badge directory.
func recorded(cfg *config) (*report, error) {
	dir, err := filepath.Abs(cfg.badges)
	if err != nil {
		return nil, err
	}

	statuses, err := readBadges(dir)
	if err != nil {
		return nil, err
	}

	return newReport(cfg, statuses, ""), nil
}

// prepareOutputs creates the output directory and returns the paths inside it.
func prepareOutputs(outDir string) (*outputs, error) {
	root, err := filepath.Abs(outDir)
	if err != nil {
		return nil, err
	}

	err = os.MkdirAll(root, dirPerm)
	if err != nil {
		return nil, err
	}

	paths := &outputs{
		junit:  filepath.Join(root, junitName),
		badges: filepath.Join(root, badgesName),
	}

	return paths, os.RemoveAll(paths.badges)
}

// runCWLTest execs cwltest from the corpus root. A non-zero exit means tests failed, not an error.
func runCWLTest(ctx context.Context, cfg *config, paths *outputs) error {
	harness, err := exec.LookPath(harnessName)
	if err != nil {
		return fmt.Errorf("%w: %s is not on PATH: %w", errSkipped, harnessName, err)
	}

	cmd := exec.CommandContext(ctx, harness, cwltestArgs(cfg, paths)...)
	cmd.Dir = cfg.corpus
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	err = cmd.Run()

	exit := new(exec.ExitError)
	if err != nil && !errors.As(err, &exit) {
		return fmt.Errorf("running %s: %w", harness, err)
	}

	return nil
}

// cwltestArgs renders the harness command line.
func cwltestArgs(cfg *config, paths *outputs) []string {
	return []string{
		"--test", manifestName,
		"--tool", filepath.Clean(cfg.runner),
		"--timeout", strconv.Itoa(int(cfg.timeout.Seconds())),
		"-j", strconv.Itoa(cfg.jobs),
		"--junit-xml", filepath.Clean(paths.junit),
		"--badgedir", filepath.Clean(paths.badges),
	}
}
