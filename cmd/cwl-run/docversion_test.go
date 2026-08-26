package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/cwlexec"
)

func TestDeclaredVersionReadsTheDocumentWithoutValidatingIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
		want string
	}{
		{name: "a v1.2 document", file: expressionTool, want: cwlcore.CWLVersionV12},
		// The point of reading it unvalidated: a v1.0 document need
		// not satisfy the v1.2 schema, so its version has to be
		// legible before validation gets a chance to reject it.
		{name: "a v1.0 document", file: oldVersion, want: "v1.0"},
		{name: "a document declaring none", file: noVersion, want: ""},
		{name: "a document that is not a mapping", file: "sequence.yml", want: ""},
		{name: "a document that does not exist", file: missingFile, want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := declaredVersion(fixture(tc.file)); got != tc.want {
				t.Errorf("declaredVersion(%s) = %q, want %q", tc.file, got, tc.want)
			}
		})
	}
}

func TestDeclaredVersionIgnoresAFragment(t *testing.T) {
	t.Parallel()

	if got := declaredVersion(fixture(expressionTool) + "#main"); got != cwlcore.CWLVersionV12 {
		t.Errorf("declaredVersion with a fragment = %q, want %q", got, cwlcore.CWLVersionV12)
	}
}

func TestCheckCWLVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want     error
		name     string
		declared string
	}{
		{name: "the version this engine implements", declared: cwlcore.CWLVersionV12, want: nil},
		// An earlier version is no longer this check's business: the
		// loader routes it to its own schema and upgrades it, so by the
		// time the version is checked it is a version we ran.
		{name: "an earlier version", declared: cwlcore.CWLVersionV10, want: nil},
		{name: "none at all", declared: "", want: errNoCWLVersion},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := checkCWLVersion("the document", tc.declared)
			if !errors.Is(err, tc.want) {
				t.Fatalf("checkCWLVersion(%q) = %v, want %v", tc.declared, err, tc.want)
			}

			if err != nil && !strings.Contains(err.Error(), "the document") {
				t.Errorf("checkCWLVersion = %q, want it to name what it is talking about", err)
			}
		})
	}
}

// TestUnsupportedVersionMapsOnlyTheVersionFailure pins the translation that decides
// between exit 33 and exit 1: a version this engine has no schema for is an unsupported
// feature, and every other failure passes through untouched so that an invalid document
// is not quietly recorded as a skip.
// errMalformed stands in for any failure that is not about a version.
var errMalformed = errors.New("the document is malformed")

func TestUnsupportedVersionMapsOnlyTheVersionFailure(t *testing.T) {
	t.Parallel()

	got := unsupportedVersion("the document", errMalformed)
	if !errors.Is(got, errMalformed) {
		t.Errorf("unsupportedVersion on an unrelated error = %v, want it returned unchanged", got)
	}

	if errors.Is(errMalformed, cwlexec.ErrUnsupportedFeature) {
		t.Fatal("the control error must not already be an unsupported-feature failure")
	}

	version := fmt.Errorf("%w: %q", cwlcore.ErrUnsupportedVersion, "draft-3")

	got = unsupportedVersion("the document", version)
	if !errors.Is(got, cwlexec.ErrUnsupportedFeature) || !errors.Is(got, cwlcore.ErrUnsupportedVersion) {
		t.Fatalf("unsupportedVersion = %v, want both an unsupported-feature and a version failure", got)
	}

	if !strings.Contains(got.Error(), "the document") {
		t.Errorf("unsupportedVersion = %q, want it to name the document", got)
	}
}
