package main

import (
	"context"
	"fmt"
	"io"

	"github.com/yardrail/cwl-go/cmd/internal/cwlcli"
	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// reportIndent nests a rendered error tree under the heading naming the
// document it came from, so a run over several documents reads as blocks.
const reportIndent = "  "

// maxErrorLines is how much of one document's error tree is printed without
// -verbose. Validating a mapping against an abstract type records why every
// concrete subtype was rejected, which for CWL's twenty-odd requirement
// classes runs to hundreds of lines; the head of the tree holds the answer.
const maxErrorLines = 40

// validateAll validates every configured document and summarizes the run.
//
// Every document is validated even after one has failed. A validator that
// stopped at the first failure would cost a CI cycle per error, which is the
// one thing a CI tool must not do.
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

// validateOne loads, validates and decodes a single document, then checks that
// every requirement it declares is one this implementation could honour.
//
// Decoding is part of validation on purpose. A document can satisfy the schema
// and still not be a CWL process — a $graph with nothing to run, a cwlVersion
// this implementation does not accept — and a validator that reported those as
// valid would be telling its user something untrue.
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

// reportValid writes the verdict for a document that validated.
//
// A document written against an earlier CWL version gets two extra lines,
// because it passed two checks and a reader is entitled to see both. "As
// declared" is schema validation against the version the document itself names
// — the check that catches a document using syntax its declared version did not
// have. "Upgraded to" is the rewrite into v1.2 form and the decode of the
// result, which is what this implementation actually runs. Reporting only the
// second would claim a v1.0 document is valid v1.2, which is not what was
// tested; reporting only the first would leave unsaid whether it can be run.
//
// A v1.2 document keeps the single line it has always had: for it the two checks
// are the same check.
func reportValid(stdout io.Writer, ref string, process cwlcore.Process) {
	fmt.Fprintf(stdout, "%s: valid %s\n", ref, process.Class())

	declared := declaredVersion(ref)
	if declared == "" || declared == cwlcore.CWLVersionV12 {
		return
	}

	fmt.Fprintf(stdout, "%sas declared : %s  OK\n", reportIndent, declared)
	fmt.Fprintf(stdout, "%supgraded to : %s  OK\n", reportIndent, cwlcore.CWLVersionV12)
}

// declaredVersion reads the cwlVersion of the document at ref off an unvalidated
// parse, and reports the empty string when there is none to read.
//
// It is read from the raw document rather than from the loaded process because
// by then it is gone: loading upgrades an older document into v1.2 form, and
// stamping the new version over the old is the point of that step. The declared
// version survives only in the file.
//
// Every failure is silent by design. This runs after the document has already
// loaded cleanly, so there is nothing left for it to diagnose; the only way it
// can fail is a file that changed underneath the run, and the answer to that is
// to say nothing extra rather than to contradict the verdict just printed.
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

// checkRequirements applies the specification's rule that a requirement class
// the implementation does not recognize is fatal — "unless overridden at user
// option", which is what the permissive default is.
//
// The default reports an unrecognized class as a warning and still calls the
// document valid, because a document that uses an extension another engine
// implements is not malformed. Under -strict the same finding fails the run,
// which is the check a project wants in CI once it has settled on an engine.
//
// It is the second half of what -strict means; the first is the schema-level
// strictness config.validateOptions passes to the loader. They are separate
// gates because they catch different things: the loader rejects a document the
// schema does not describe, and this rejects a document the schema describes
// perfectly well but that this implementation could not honour.
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

// reportFailure writes one document's failure to stderr as a heading naming
// the document followed by the indented error tree, trimmed to a readable
// length unless -verbose asked for all of it.
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
