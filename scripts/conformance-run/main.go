// Command conformance-run drives the official CWL v1.2 conformance suite
// (Stage 1+) against the cwl-run binary and ratchets the result.
//
// The authoritative harness is cwltest, the engine-agnostic runner the
// specification repository ships. This command locates it, points it at the
// pinned corpus the Stage 0 sweep already fetches, turns its per-tag badge
// output into a machine-readable report, and compares that report against the
// checked-in record in stage1-ratchet.json.
//
// It is deliberately hard to fail for the wrong reason. A missing cwltest, a
// missing corpus or a missing runner is reported and exits 0, because none of
// them is evidence that the engine regressed; only a test that used to pass and
// no longer does fails the build.
//
// cwltest itself is taken from PATH by name, so a virtualenv install is
// selected by activating it rather than by a flag.
//
// Usage:
//
//	go run ./scripts/conformance-run [flags]
//
// Flags:
//
//	-corpus DIR    corpus root (default: $CWL_CONFORMANCE_CORPUS, else the Stage 0 cache)
//	-runner PATH   the cwl-runner-compatible binary under test (default: bin/cwl-run)
//	-out DIR       where the JUnit XML and badge files are written
//	-jobs N        how many tests cwltest runs at once
//	-timeout D     per-test timeout
//	-update        rewrite stage1-ratchet.json from this run instead of comparing
//	-gate-required fail when the required subset is not at 100%
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
)

// exit statuses. Anything other than a genuine regression exits 0, so that a
// machine without cwltest installed does not turn the build red.
const (
	exitOK      = 0
	exitRegress = 1
)

func main() {
	cfg, err := parseFlags(os.Args[1:], os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitRegress)
	}

	os.Exit(dispatch(context.Background(), cfg))
}

// dispatch runs the suite and reports the process status.
func dispatch(ctx context.Context, cfg *config) int {
	report, err := gather(ctx, cfg)

	// The whole error is printed rather than its cause: errSkipped is wrapped
	// alongside the underlying failure with two %w verbs, so Unwrap returns nil
	// and only the rendered text carries the reason.
	if errors.Is(err, errSkipped) {
		fmt.Fprintf(os.Stderr, "conformance-run: %v\n", err)

		return exitOK
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "conformance-run: %v\n", err)

		return exitRegress
	}

	report.write(os.Stdout)

	return settle(cfg, report)
}

// settle applies the ratchet and the optional required-subset gate.
func settle(cfg *config, report *report) int {
	if cfg.update {
		err := writeRatchet(report.asRatchet())
		if err != nil {
			fmt.Fprintf(os.Stderr, "conformance-run: %v\n", err)

			return exitRegress
		}

		fmt.Fprintf(os.Stderr, "conformance-run: rewrote %s\n", ratchetName)

		return exitOK
	}

	status := exitOK

	problems := compare(ratchetName, report)
	for _, problem := range problems {
		fmt.Fprintf(os.Stderr, "conformance-run: %s\n", problem)

		status = exitRegress
	}

	if cfg.gateRequired && report.required.failed > 0 {
		fmt.Fprintf(os.Stderr, "conformance-run: the required subset is not at 100%% (%d of %d passing)\n",
			report.required.passed, report.required.total())

		status = exitRegress
	}

	return status
}

// parseFlags builds the run configuration from the command line and the
// environment.
func parseFlags(args []string, stderr *os.File) (*config, error) {
	cfg := defaultConfig()

	set := flag.NewFlagSet("conformance-run", flag.ContinueOnError)
	set.SetOutput(stderr)
	set.StringVar(&cfg.corpus, "corpus", cfg.corpus, "corpus root holding conformance_tests.yaml")
	set.StringVar(&cfg.runner, "runner", cfg.runner, "cwl-runner-compatible binary under test")
	set.StringVar(&cfg.outDir, "out", cfg.outDir, "directory for the JUnit XML and badge output")
	set.IntVar(&cfg.jobs, "jobs", cfg.jobs, "how many tests cwltest runs at once")
	set.DurationVar(&cfg.timeout, "timeout", cfg.timeout, "per-test timeout")
	set.BoolVar(&cfg.update, "update", false, "rewrite the ratchet record from this run")
	set.BoolVar(&cfg.gateRequired, "gate-required", false, "fail when the required subset is not at 100%")

	err := set.Parse(args)
	if err != nil {
		return nil, err
	}

	return cfg, nil
}
