package conformance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

func TestCheckCWLVersionRejectsAMissingVersion(t *testing.T) {
	t.Parallel()

	err := checkCWLVersion("somewhere", "")
	if !errors.Is(err, errNoCWLVersion) {
		t.Errorf("checkCWLVersion = %v, want it to wrap errNoCWLVersion", err)
	}

	err = checkCWLVersion("somewhere", "v1.2")
	if err != nil {
		t.Errorf("a declared version was rejected: %v", err)
	}
}

func TestUnsupportedVersionRewrapsAVersionError(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("wrap: %w", cwlcore.ErrUnsupportedVersion)

	got := unsupportedVersion("where", wrapped)
	if !errors.Is(got, cwlcore.ErrUnsupportedVersion) {
		t.Errorf("unsupportedVersion = %v, want it to still wrap cwlcore.ErrUnsupportedVersion", got)
	}
}

func TestUnsupportedVersionLeavesAnUnrelatedErrorAlone(t *testing.T) {
	t.Parallel()

	got := unsupportedVersion("where", errPlain)
	if !errors.Is(got, errPlain) {
		t.Errorf("unsupportedVersion(errPlain) = %v, want errPlain unchanged", got)
	}
}

func TestDeclaredVersionIsSilentOnFailure(t *testing.T) {
	t.Parallel()

	t.Run("a file that does not exist", func(t *testing.T) {
		t.Parallel()

		got := declaredVersion(filepath.Join(t.TempDir(), "does-not-exist.cwl"))
		if got != "" {
			t.Errorf("declaredVersion = %q, want the empty string", got)
		}
	})

	t.Run("a file that does not parse", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "malformed.cwl")

		err := os.WriteFile(path, []byte("{not: valid: yaml: ["), 0o600)
		if err != nil {
			t.Fatalf("writing the fixture: %v", err)
		}

		got := declaredVersion(path)
		if got != "" {
			t.Errorf("declaredVersion = %q, want the empty string", got)
		}
	})
}
