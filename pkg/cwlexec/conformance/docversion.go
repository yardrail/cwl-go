package conformance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/cwlexec"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// errNoCWLVersion reports a top-level document that declares no cwlVersion.
var errNoCWLVersion = errors.New("missing cwlVersion")

// The version policy below is cmd/cwl-run's, restated.
//
// It has to be. cmd sits above pkg/cwlexec in the layering, so this driver cannot call
// into the command it stands in for, and the policy is not the engine's: whether an
// unreadable document version is a plain failure or the contract's unsupported status is a
// decision the cwl-runner contract makes, and cmd/cwl-run is where that contract lives.
// Restating it is the price of running the suite without a subprocess.
//
// It is a restatement and not a variation, which is exactly the property
// TestInProcessMatchesCwltest exists to police: cwltest drives the real binary, this drives
// the copy, and drift between them lands a test in different sets under the two harnesses.

// checkCWLVersion enforces the one version rule left for a runner to enforce: a top-level
// document has to say which version it is.
//
// Whether that version is one the engine can read is decided in [cwlcore.LoadFile], which
// routes each document to the schema for the version it declares -- so the check covers
// every document a run touches rather than only the one it was pointed at.
func checkCWLVersion(where, declared string) error {
	if declared == "" {
		return fmt.Errorf("%w: %s must declare cwlVersion, which every top-level process is required to",
			errNoCWLVersion, where)
	}

	return nil
}

// unsupportedVersion re-reports a document written against a CWL version this engine has no
// schema for as the contract's unsupported-feature failure, so that it counts as a skip
// rather than as a wrong answer. An error that is not about a version is returned untouched.
func unsupportedVersion(where string, err error) error {
	if !errors.Is(err, cwlcore.ErrUnsupportedVersion) {
		return err
	}

	return fmt.Errorf("%w: %s: %w", cwlexec.ErrUnsupportedFeature, where, err)
}

// declaredVersion reads the cwlVersion of the document at ref without validating it, and
// reports the empty string when there is none to read. A fragment is dropped: it selects
// one process inside a document, and the version is a property of the whole file.
//
// It reads the raw parse because that is the only place the answer survives: a $graph
// document declares its version once, beside the graph rather than on any process in it,
// and resolution replaces the mapping that held it with the sequence it held.
//
// Every failure is silent by design: a document that cannot be read or parsed is one the
// loader is about to reject with a far better diagnostic.
//
// cmd/cwl-run reads the same field through the salad fetcher, because a user may name an
// http URL on the command line. A conformance entry never does -- every tool: reference in
// the manifest resolves to a file in the corpus -- so this reads the file, which keeps a
// network fetch out of a path that has no context to cancel one with.
func declaredVersion(ref string) string {
	document, _, _ := strings.Cut(ref, "#")

	src, err := os.ReadFile(filepath.Clean(document))
	if err != nil {
		return ""
	}

	root, err := salad.Parse(document, src)
	if err != nil {
		return ""
	}

	return cwlcore.DeclaredVersion(root)
}
