// Command cwl-validate validates a CWL document against the embedded schema for the CWL
// version it declares, and reports whether a document written against an earlier version
// also upgrades cleanly into the v1.2 form this implementation runs. For use in local
// development and CI.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// toolName is how the tool names itself in its usage and version output.
const toolName = "cwl-validate"

// The exit statuses, which are this tool's primary output: CI reads the status
// and only a human reads the text.
const (
	exitInvalid = 1
	exitUsage   = 2
)

// The two failure modes run reports, kept apart because they mean different
// things to the caller: a document that does not validate is the tool working,
// and a command line that does not parse is not.
var (
	errInvalid = errors.New("one or more documents are not valid CWL")
	errUsage   = errors.New("invalid command line")
)

func main() {
	err := run(os.Args[1:], os.Stdout, os.Stderr)
	if err == nil {
		return
	}

	if errors.Is(err, errUsage) {
		os.Exit(exitUsage)
	}

	os.Exit(exitInvalid)
}

// run validates every document named on the command line and reports whether
// they were all valid. It writes its own diagnostics, so main has nothing left
// to do but turn the returned error into an exit status.
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

	if len(cfg.documents) == 0 {
		fmt.Fprint(stderr, usageText())

		return fmt.Errorf("%w: no document to validate", errUsage)
	}

	return validateAll(cfg, stdout, stderr)
}

// config is the parsed command line.
type config struct {
	// documents are the paths or URLs to validate, in the order given.
	documents []string
	// quiet suppresses all output, leaving the exit status as the result.
	quiet bool
	// strict promotes advisory diagnostics to errors.
	strict bool
	// verbose prints every line of an error tree instead of its head.
	verbose bool
	// version asks for the version banner instead of a validation run.
	version bool
	// help records that the flag set already printed the usage message in
	// response to -h, so there is nothing left to do and nothing has failed.
	help bool
}

// validateOptions turns the strict flag into the options the loader takes.
//
// Under -strict, the conditions the specification lets an implementation
// tolerate — an unrecognized field, most of all — become errors rather than
// advisories. That is the check a project wants in CI: permissive validation
// discards advisories entirely, so a typo'd field name is otherwise reported
// nowhere at all, and a document that does nothing passes.
func (c *config) validateOptions() []salad.ValidateOption {
	if !c.strict {
		return nil
	}

	return []salad.ValidateOption{salad.Strict(true)}
}

// parseFlags reads args into a config. Flag errors are written to stderr by
// the flag set itself, so the returned error only has to carry the exit status.
func parseFlags(args []string, stderr io.Writer) (*config, error) {
	cfg := &config{documents: make([]string, 0, len(args))}

	fs := flag.NewFlagSet(toolName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usageText()) }

	fs.BoolVar(&cfg.quiet, "quiet", false, "print nothing; report the result through the exit status alone")
	fs.BoolVar(&cfg.quiet, "q", false, "alias for -quiet")
	fs.BoolVar(&cfg.strict, "strict", false, "treat advisory diagnostics as errors")
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

	cfg.documents = append(cfg.documents, fs.Args()...)

	return cfg, nil
}
