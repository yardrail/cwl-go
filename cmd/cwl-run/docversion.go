package main

import (
	"errors"
	"fmt"

	"github.com/yardrail/cwl-go/cmd/internal/cwlcli"
	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/cwlexec"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// keyCWLVersion is the document field naming the CWL version a process is
// written against.
const keyCWLVersion = "cwlVersion"

// errNoCWLVersion reports a top-level document that declares no cwlVersion.
//
// The specification requires one on a top-level process, and the vendored
// schema makes the field optional because an embedded process inherits it from
// the document around it. That leaves nothing enforcing the rule for the
// top-level case, so this does — as an ordinary failure rather than as
// "unsupported", because a document that says nothing about its version is
// malformed, not written against a version we lack.
var errNoCWLVersion = errors.New("missing cwlVersion")

// checkCWLVersion reports whether a document declaring cwlVersion is one this
// engine will run.
//
// The policy is deliberately narrow: only v1.2 runs, and anything else exits
// 33. This implementation has no document-upgrade machinery — supporting older
// versions is out of scope by design — so "unsupported" is the honest answer
// for a v1.0 or v1.1 document. Reporting it as one also keeps the conformance
// numbers truthful in both directions: a mixed-version test that ought to pass
// is recorded as skipped rather than as a false pass, and a should_fail test
// that fails precisely because it claims an old version while using v1.2-only
// syntax is not credited to us either.
//
// where names the document or process for the message.
func checkCWLVersion(where, declared string) error {
	if declared == "" {
		return fmt.Errorf("%w: %s must declare cwlVersion: %s, which every top-level process is required to",
			errNoCWLVersion, where, cwlcore.CWLVersionV12)
	}

	if declared != cwlcore.CWLVersionV12 {
		return unsupportedVersion(where, declared)
	}

	return nil
}

// unsupportedVersion builds the exit-33 error for a document written against a
// CWL version this engine does not implement.
func unsupportedVersion(where, declared string) error {
	return fmt.Errorf(
		"%w: %s declares cwlVersion %q, and this engine implements %s only; "+
			"it does not upgrade documents written against an earlier version",
		cwlexec.ErrUnsupportedFeature, where, declared, cwlcore.CWLVersionV12,
	)
}

// declaredVersion reads the cwlVersion of the document at ref without
// validating it, and reports the empty string when there is none to read.
//
// It runs *before* the document is loaded, which is the whole point of it. A
// v1.0 document is not required to satisfy the v1.2 schema, so validating one
// first would report it as invalid — exit 1, a hard failure — when the truthful
// answer is that this engine does not implement its version. Reading the field
// off an unvalidated parse is what lets the version answer come first.
//
// Every failure is silent by design: a document that cannot be fetched or
// parsed is one the loader is about to reject with a far better diagnostic than
// anything that could be produced here, so this gets out of its way.
func declaredVersion(ref string) string {
	src, url, err := cwlcli.Fetch(ref)
	if err != nil {
		return ""
	}

	root, err := salad.Parse(url, src)
	if err != nil {
		return ""
	}

	document, ok := salad.AsMap(root)
	if !ok {
		return ""
	}

	node, ok := document.Get(keyCWLVersion)
	if !ok {
		return ""
	}

	version, ok := salad.AsString(node)
	if !ok {
		return ""
	}

	return version
}

// checkRunVersions applies the same policy to every process a workflow's steps
// run, recursively.
//
// An embedded run: target inherits the enclosing document's version and so
// declares none, which passes. An external one is a document in its own right
// and may declare an older version — which is exactly the shape the suite's
// mixed-version tests take, and exactly the case a check on the entry document
// alone would miss.
func checkRunVersions(process cwlcore.Process) error {
	workflow, ok := process.(*cwlcore.Workflow)
	if !ok {
		return nil
	}

	for index := range workflow.Steps {
		step := &workflow.Steps[index]

		target := step.Run.Process
		if target == nil {
			continue
		}

		declared := target.Base().CWLVersion
		if declared != "" && declared != cwlcore.CWLVersionV12 {
			return unsupportedVersion(fmt.Sprintf("the %s run by step %q",
				target.Class(), cwlexec.ShortName(step.ID)), declared)
		}

		err := checkRunVersions(target)
		if err != nil {
			return err
		}
	}

	return nil
}
