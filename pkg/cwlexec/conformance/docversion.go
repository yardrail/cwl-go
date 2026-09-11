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

// Version policy restated from cmd/cwl-run (layering prevents importing it).

// checkCWLVersion ensures a top-level document declares a cwlVersion.
func checkCWLVersion(where, declared string) error {
	if declared == "" {
		return fmt.Errorf("%w: %s must declare cwlVersion, which every top-level process is required to",
			errNoCWLVersion, where)
	}

	return nil
}

// unsupportedVersion wraps [cwlcore.ErrUnsupportedVersion] as [cwlexec.ErrUnsupportedFeature].
func unsupportedVersion(where string, err error) error {
	if !errors.Is(err, cwlcore.ErrUnsupportedVersion) {
		return err
	}

	return fmt.Errorf("%w: %s: %w", cwlexec.ErrUnsupportedFeature, where, err)
}

// declaredVersion reads the cwlVersion from the raw parse, or "" if absent.
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
