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

// Exit statuses per the cwl-runner contract.
const (
	exitFailure     = 1
	exitUsage       = 2
	exitUnsupported = 33
)

// maxPositional is the cwl-runner contract's positional argument count.
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

// exitStatus maps an error onto the cwl-runner contract's exit statuses.
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
	process        string // CWL document path or URL
	job            string // job order path, or empty
	outdir         string // output directory, or empty for cwd
	noContainer    bool
	noMatchUser    bool
	noReadOnly     bool
	leaveContainer bool
	quiet          bool
	verbose        bool
	version        bool
	help           bool
}

// parseFlags reads args into a config.
func parseFlags(args []string, stderr io.Writer) (*config, error) {
	cfg := &config{
		process: "", job: "", outdir: "",
		noContainer: false, noMatchUser: false, noReadOnly: false, leaveContainer: false,
		quiet: false, verbose: false, version: false, help: false,
	}

	fs := flag.NewFlagSet(toolName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usageText()) }

	fs.StringVar(&cfg.outdir, "outdir", "", "write the run's output files under this directory")
	fs.BoolVar(&cfg.noContainer, "no-container", false,
		"do not run tools in a software container, even where DockerRequirement is a hint")
	fs.BoolVar(&cfg.noMatchUser, "no-match-user", false,
		"do not pass this process's uid and gid to a container")
	fs.BoolVar(&cfg.noReadOnly, "no-read-only", false,
		"do not hold a container's root filesystem read-only")
	fs.BoolVar(&cfg.leaveContainer, "leave-container", false,
		"do not remove a container once its tool has exited")
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
