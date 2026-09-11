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

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// toolName is how the tool names itself in its usage and version output.
const toolName = "cwl-validate"

// Exit statuses.
const (
	exitInvalid = 1
	exitUsage   = 2
)

// Failure sentinels.
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

// run validates every document named on the command line.
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
	documents []string // paths or URLs to validate
	quiet     bool
	strict    bool
	verbose   bool
	version   bool
	help      bool
}

// loadOptions maps the strict flag to loader options.
func (c *config) loadOptions() []cwlcore.LoadOption {
	if !c.strict {
		return nil
	}

	return []cwlcore.LoadOption{cwlcore.Strict(true)}
}

// parseFlags reads args into a config.
func parseFlags(args []string, stderr io.Writer) (*config, error) {
	cfg := &config{
		documents: make([]string, 0, len(args)),
		quiet:     false,
		strict:    false,
		verbose:   false,
		version:   false,
		help:      false,
	}

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
