// Command cwl-run is a cwl-runner-compatible CLI entrypoint: it drives execution of a
// CWL document, following the cwl-runner invocation and exit-code contract
// (https://www.commonwl.org/v1.2/CommandLineTool.html#Executing_CWL_documents_and_tools)
// so it can be exercised by the cwltest conformance harness.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/yardrail/cwl-go/pkg/cwlexec"
)

// toolName is how the tool names itself in its usage and version output.
const toolName = "cwl-run"

// The exit statuses. They are the tool's primary interface: the cwltest harness
// reads the status, and only a human reads the text on stderr.
const (
	// exitFailure reports a run that did not produce an output object: an
	// invalid document, a job order that does not fit it, or a step that
	// failed. A should_fail conformance test passes on this.
	exitFailure = 1

	// exitUsage reports a command line that could not be understood. It is
	// kept apart from exitFailure because a mistyped flag is the caller's
	// mistake rather than the document's; to cwltest both are failures.
	exitUsage = 2

	// exitUnsupported is the cwl-runner contract's status for a document
	// that "could not be run because a feature is unsupported". cwltest
	// counts it as a skip rather than a failure — unless the test is tagged
	// required, where a skip is still a failure. It is what keeps
	// incremental bring-up honest: a feature this engine has not implemented
	// reports as missing instead of as a wrong answer.
	exitUnsupported = 33
)

// maxPositional is how many positional arguments the cwl-runner contract
// defines: the process document, and optionally the job order.
const maxPositional = 2

// errUsage marks a command line that could not be understood.
var errUsage = errors.New("invalid command line")

func main() {
	err := run(os.Args[1:], os.Stdout, os.Stderr)
	if err == nil {
		return
	}

	os.Exit(exitStatus(err))
}

// exitStatus maps a failed run onto the cwl-runner contract's exit statuses.
//
// Unsupported outranks usage because it is the more specific answer, and the
// two cannot both apply: a command line that did not parse never reached a
// document.
func exitStatus(err error) int {
	switch {
	case errors.Is(err, cwlexec.ErrUnsupportedFeature):
		return exitUnsupported
	case errors.Is(err, errUsage):
		return exitUsage
	default:
		return exitFailure
	}
}

// run executes one CWL document and writes its output object to stdout.
//
// Everything that is not the output object — every log line, warning and
// diagnostic — goes to stderr. That separation is the contract, not a
// courtesy: cwltest parses the whole of stdout as the run's output object, so
// one stray byte on it fails a test that otherwise passed.
//
// run writes its own diagnostics, so main has nothing left to do but turn the
// returned error into an exit status.
func run(args []string, stdout, stderr io.Writer) error {
	cfg, err := parseFlags(args, stderr)
	if err != nil {
		return err
	}

	if cfg.help {
		return nil
	}

	if cfg.version {
		fmt.Fprintln(stdout, versionText())

		return nil
	}

	if cfg.process == "" {
		fmt.Fprint(stderr, usageText())

		return fmt.Errorf("%w: no process to run", errUsage)
	}

	return execute(context.Background(), cfg, stdout, stderr)
}

// config is the parsed command line.
type config struct {
	// process is the path or URL of the CWL document to run, optionally
	// with a #fragment selecting one process of a $graph.
	process string
	// job is the path of the job order supplying the input object, empty
	// when the process is to run against an empty one.
	job string
	// outdir is the directory the run's output files are written under.
	// Empty means the process working directory.
	outdir string
	// quiet suppresses the non-essential half of stderr, leaving failures.
	quiet bool
	// verbose prints every line of an error tree instead of its head.
	verbose bool
	// version asks for the version banner instead of a run.
	version bool
	// help records that the flag set already printed the usage message in
	// response to -h, so there is nothing left to do and nothing failed.
	help bool
}

// parseFlags reads args into a config. Flag errors are written to stderr by the
// flag set itself, so the returned error only has to carry the exit status.
//
// The positional arguments are the cwl-runner contract's: a process document,
// and optionally a job order. A third is rejected rather than ignored, because
// silently dropping it would make a mistyped flag look like a working command.
func parseFlags(args []string, stderr io.Writer) (*config, error) {
	cfg := &config{process: "", job: "", outdir: "", quiet: false, verbose: false, version: false, help: false}

	fs := flag.NewFlagSet(toolName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usageText()) }

	fs.StringVar(&cfg.outdir, "outdir", "", "write the run's output files under this directory")
	fs.BoolVar(&cfg.quiet, "quiet", false, "suppress progress and advisory messages on stderr")
	fs.BoolVar(&cfg.quiet, "q", false, "alias for -quiet")
	fs.BoolVar(&cfg.verbose, "verbose", false, "print every line of an error report instead of its head")
	fs.BoolVar(&cfg.verbose, "v", false, "alias for -verbose")
	fs.BoolVar(&cfg.version, "version", false, "print version information and exit")

	err := fs.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		cfg.help = true

		return cfg, nil
	}

	if err != nil {
		return nil, fmt.Errorf("%w: %w", errUsage, err)
	}

	if fs.NArg() > maxPositional {
		fmt.Fprint(stderr, usageText())

		return nil, fmt.Errorf("%w: %s takes a process and an optional job order, got %d arguments",
			errUsage, toolName, fs.NArg())
	}

	cfg.process, cfg.job = fs.Arg(0), fs.Arg(1)

	return cfg, nil
}
