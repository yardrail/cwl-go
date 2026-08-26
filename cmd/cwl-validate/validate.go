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
	process, err := cwlcore.LoadFile(ctx, ref, cfg.validateOptions()...)
	if err != nil {
		reportFailure(stderr, ref, err, cfg)

		return err
	}

	err = checkRequirements(process, ref, cfg, stderr)
	if err != nil {
		return err
	}

	if !cfg.quiet {
		fmt.Fprintf(stdout, "%s: valid %s\n", ref, process.Class())
	}

	return nil
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
