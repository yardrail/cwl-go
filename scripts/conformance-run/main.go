// Command conformance-run drives the CWL v1.2 conformance suite against cwl-run.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
)

// Exit statuses.
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

// settle checks the run for failures and applies the optional required-subset gate.
func settle(cfg *config, report *report) int {
	status := exitOK

	if report.overall.failed > 0 {
		fmt.Fprintf(os.Stderr, "conformance-run: %d test(s) failed\n", report.overall.failed)

		status = exitRegress
	}

	if cfg.gateRequired && report.required.failed > 0 {
		fmt.Fprintf(os.Stderr, "conformance-run: the required subset is not at 100%% (%d of %d passing)\n",
			report.required.passed, report.required.total())

		status = exitRegress
	}

	return status
}

// parseFlags builds the run configuration from the command line.
func parseFlags(args []string, stderr *os.File) (*config, error) {
	cfg := defaultConfig()

	set := flag.NewFlagSet("conformance-run", flag.ContinueOnError)
	set.SetOutput(stderr)
	set.StringVar(&cfg.corpus, "corpus", cfg.corpus, "corpus root holding conformance_tests.yaml")
	set.StringVar(&cfg.runner, "runner", cfg.runner, "cwl-runner-compatible binary under test")
	set.StringVar(&cfg.outDir, "out", cfg.outDir, "directory for the JUnit XML and badge output")
	set.IntVar(&cfg.jobs, "jobs", cfg.jobs, "how many tests cwltest runs at once")
	set.DurationVar(&cfg.timeout, "timeout", cfg.timeout, "per-test timeout")
	set.StringVar(&cfg.badges, "badges", "",
		"read a badge directory a previous run left behind instead of running the suite")
	set.BoolVar(&cfg.gateRequired, "gate-required", false, "fail when the required subset is not at 100%")

	err := set.Parse(args)
	if err != nil {
		return nil, err
	}

	return cfg, nil
}
