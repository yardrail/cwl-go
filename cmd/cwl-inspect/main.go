// Command cwl-inspect parses a CWL document and dumps its resolved intermediate
// representation, for debugging pkg/salad and pkg/cwlcore.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/yardrail/cwl-go/cmd/internal/cwlcli"
)

// toolName is how the tool names itself in its usage and version output.
const toolName = "cwl-inspect"

// The exit statuses. Anything that stops a dump being produced is a failure;
// only the command line is distinguished, because that is the user's mistake
// rather than the document's.
const (
	exitFailure = 1
	exitUsage   = 2
)

// errUsage marks a command line that could not be understood.
var errUsage = errors.New("invalid command line")

func main() {
	err := run(os.Args[1:], os.Stdout, os.Stderr)
	if err == nil {
		return
	}

	if errors.Is(err, errUsage) {
		os.Exit(exitUsage)
	}

	os.Exit(exitFailure)
}

// run dumps one document's intermediate representation. It writes the dump to
// stdout and any diagnostic to stderr, so a dump can be piped without the
// diagnostics contaminating it.
func run(args []string, stdout, stderr io.Writer) error {
	cfg, err := parseFlags(args, stderr)
	if err != nil {
		return err
	}

	if cfg.help {
		return nil
	}

	if cfg.version {
		fmt.Fprintln(stdout, cwlcli.VersionText(toolName))

		return nil
	}

	if cfg.document == "" {
		fmt.Fprint(stderr, usageText())

		return fmt.Errorf("%w: no document to inspect", errUsage)
	}

	value, err := cfg.stage.Inspect(cfg.document)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %s\n", cfg.document, cwlcli.Explain(err))

		return err
	}

	return cfg.format.Render(stdout, value)
}

// config is the parsed command line.
type config struct {
	// document is the path or URL to inspect.
	document string
	// stage selects which intermediate representation to dump.
	stage Stage
	// format selects the output encoding.
	format cwlcli.Format
	// version asks for the version banner instead of a dump.
	version bool
	// help records that the flag set already printed the usage message.
	help bool
}

// parseFlags reads args into a config, rejecting a second positional argument
// rather than silently ignoring it: a dump is of one document, and quietly
// dropping the rest would make a mistyped flag look like a working command.
func parseFlags(args []string, stderr io.Writer) (*config, error) {
	cfg := &config{stage: StageTyped, format: cwlcli.FormatJSON, document: "", version: false, help: false}

	fs := flag.NewFlagSet(toolName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usageText()) }

	fs.Var(&cfg.stage, "stage", "which representation to dump: "+Stages())
	fs.Var(&cfg.format, "format", "output encoding: "+cwlcli.Formats())
	fs.BoolVar(&cfg.version, "version", false, "print version information and exit")

	err := fs.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		cfg.help = true

		return cfg, nil
	}

	if err != nil {
		return nil, fmt.Errorf("%w: %w", errUsage, err)
	}

	if fs.NArg() > 1 {
		fmt.Fprint(stderr, usageText())

		return nil, fmt.Errorf("%w: %s inspects one document at a time, got %d", errUsage, toolName, fs.NArg())
	}

	cfg.document = fs.Arg(0)

	return cfg, nil
}
