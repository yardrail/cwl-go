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

// prepareOutputs creates the output directory and returns the paths inside it.
//
// The badge directory is removed rather than created: cwltest makes it itself
// and refuses to start if it already exists, and removing it also guarantees a
// tag that has disappeared upstream is not read again from a stale file.
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

// runCWLTest execs the harness from the corpus root, which is what the relative
// tool and job paths in the manifest are written against.
//
// A non-zero status means tests failed, which is this command's subject rather
// than its error: only a harness that could not start is an error here, and an
// [exec.ExitError] is exactly what distinguishes the two.
func runCWLTest(ctx context.Context, cfg *config, paths *outputs) error {
	// The harness is looked up here, from a constant name, rather than carried
	// on the config: a program path that has travelled through a struct field
	// is one gosec cannot prove safe to execute, and there is no need for it to
	// travel. Put the cwltest you want first on PATH.
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

// cwltestArgs renders the harness command line. Every path is cleaned on the
// way in, which is both what the harness wants to see in its own output and
// what keeps a caller-supplied path from carrying traversal segments into a
// subprocess.
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
