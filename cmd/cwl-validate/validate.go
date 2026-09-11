package main

import (
	"context"
	"fmt"
	"io"

	"github.com/yardrail/cwl-go/cmd/internal/cwlcli"
	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// reportIndent indents error tree lines under the document heading.
const reportIndent = "  "

// maxErrorLines caps the error tree output without -verbose.
const maxErrorLines = 40

// validateAll validates every configured document and summarizes the run.
func validateAll(cfg *config, stdout, stderr io.Writer) error {
	ctx := context.Background()

	failed := 0

	for _, ref := range cfg.documents {
		err := validateOne(ctx, ref, cfg, stdout, stderr)
		if err != nil {
			failed++
		}
	}

	if failed == 0 {
		return nil
	}

	if !cfg.quiet && len(cfg.documents) > 1 {
		fmt.Fprintf(stderr, "\n%s: %d of %d documents are not valid CWL\n", toolName, failed, len(cfg.documents))
	}

	return errInvalid
}

// validateOne loads, validates and decodes a single document.
func validateOne(ctx context.Context, ref string, cfg *config, stdout, stderr io.Writer) error {
	process, err := cwlcore.LoadFile(ctx, ref, cfg.loadOptions()...)
	if err != nil {
		reportFailure(stderr, ref, err, cfg)

		return err
	}

	err = checkRequirements(process, ref, cfg, stderr)
	if err != nil {
		return err
	}

	if !cfg.quiet {
		reportValid(stdout, ref, process)
	}

	return nil
}

// reportValid writes the verdict for a valid document.
func reportValid(stdout io.Writer, ref string, process cwlcore.Process) {
	fmt.Fprintf(stdout, "%s: valid %s\n", ref, process.Class())

	declared := declaredVersion(ref)
	if declared == "" || declared == cwlcore.CWLVersionV12 {
		return
	}

	fmt.Fprintf(stdout, "%sas declared : %s  OK\n", reportIndent, declared)
	fmt.Fprintf(stdout, "%supgraded to : %s  OK\n", reportIndent, cwlcore.CWLVersionV12)
}

// declaredVersion reads the cwlVersion from the raw parse, or "" if absent.
func declaredVersion(ref string) string {
	src, url, err := cwlcli.Fetch(ref)
	if err != nil {
		return ""
	}

	root, err := salad.Parse(url, src)
	if err != nil {
		return ""
	}

	return cwlcore.DeclaredVersion(root)
}

// checkRequirements checks for unrecognized requirement classes. Under -strict they fail; otherwise they warn.
func checkRequirements(process cwlcore.Process, ref string, cfg *config, stderr io.Writer) error {
	scope := cwlcore.NewScope(process)

	if cfg.strict {
		err := scope.CheckKnown(nil)
		if err != nil {
			reportFailure(stderr, ref, err, cfg)

			return err
		}

		return nil
	}

	warn := func(e *salad.Error) {
		if cfg.quiet {
			return
		}

		fmt.Fprintf(stderr, "%s: %s\n", ref, e.Pretty())
	}

	return scope.CheckKnown(nil, cwlcore.WithLenient(), cwlcore.WithWarnFunc(warn))
}

// reportFailure writes the error tree to stderr, trimmed unless -verbose.
func reportFailure(stderr io.Writer, ref string, err error, cfg *config) {
	if cfg.quiet {
		return
	}

	limit := maxErrorLines
	if cfg.verbose {
		limit = 0
	}

	shown, omitted := cwlcli.LimitLines(cwlcli.Explain(err), limit)

	fmt.Fprintf(stderr, "%s: INVALID\n%s\n", ref, cwlcli.Indent(shown, reportIndent))

	if omitted > 0 {
		fmt.Fprintf(stderr, "%s... %d more lines; re-run with -verbose for the whole report\n", reportIndent, omitted)
	}
}
