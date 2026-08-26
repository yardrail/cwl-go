package main

import (
	"errors"
	"fmt"

	"github.com/yardrail/cwl-go/cmd/internal/cwlcli"
	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/cwlexec"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// errNoCWLVersion reports a top-level document that declares no cwlVersion.
//
// The specification requires one on a top-level process, and the vendored
// schemas make the field optional because an embedded process inherits it from
// the document around it. That leaves nothing enforcing the rule for the
// top-level case, so this does — as an ordinary failure rather than as
// "unsupported", because a document that says nothing about its version is
// malformed, not written against a version we lack.
var errNoCWLVersion = errors.New("missing cwlVersion")

// checkCWLVersion enforces the one version rule left for this command to
// enforce: a top-level document has to say which version it is.
//
// Whether that version is one this engine can read is no longer decided here.
// [cwlcore.LoadFile] routes each document to the schema for the version it
// declares and reports [cwlcore.ErrUnsupportedVersion] for a version it has no
// schema for — which is a strictly better place for the check, because it
// applies to every document a run touches rather than only to the one named on
// the command line. See unsupportedVersion for how that answer is reported.
//
// where names the document or process for the message.
func checkCWLVersion(where, declared string) error {
	if declared == "" {
		return fmt.Errorf("%w: %s must declare cwlVersion, which every top-level process is required to",
			errNoCWLVersion, where)
	}

	return nil
}

// unsupportedVersion re-reports a document written against a CWL version this
// engine has no schema for as the cwl-runner contract's unsupported-feature
// failure, so that it exits 33 rather than 1.
//
// The distinction is the contract's, and it is worth keeping precisely: exit 1
// says "that document is wrong", and this case is not that. The document may be
// perfectly good CWL of a version — a draft, a development release — that this
// implementation does not vendor a schema for, and cwltest is entitled to record
// the difference.
//
// An error that is not about a version is returned untouched.
func unsupportedVersion(where string, err error) error {
	if !errors.Is(err, cwlcore.ErrUnsupportedVersion) {
		return err
	}

	return fmt.Errorf("%w: %s: %w", cwlexec.ErrUnsupportedFeature, where, err)
}

// declaredVersion reads the cwlVersion of the document at ref without validating
// it, and reports the empty string when there is none to read.
//
// It reads the raw parse because that is the only place the answer survives: a
// $graph document declares its version once, beside the graph rather than on any
// process in it, and resolution replaces the mapping that held it with the
// sequence it held. So the loaded process reports no version at all for exactly
// the documents where checkCWLVersion most needs one.
//
// Every failure is silent by design: a document that cannot be fetched or parsed
// is one the loader is about to reject with a far better diagnostic than anything
// that could be produced here, so this gets out of its way.
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
