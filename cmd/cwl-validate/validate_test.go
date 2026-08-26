package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// extensionClass is a requirement class no CWL v1.2 implementation recognizes.
const extensionClass = "http://example.org/ext#Magic"

// processWithUnknownRequirement builds a process declaring a requirement class
// this implementation cannot honour.
//
// It is built rather than loaded because no document can currently reach this
// state through cwlcore.LoadFile: the loader's link check rejects a class IRI
// it cannot resolve before decoding ever produces a RawRequirement. The gate
// itself is still the specification's, and still worth holding to.
func processWithUnknownRequirement() cwlcore.Process {
	return &cwlcore.Operation{
		ProcessBase: cwlcore.ProcessBase{
			ID:           "#op",
			Requirements: []cwlcore.ProcessRequirement{&cwlcore.RawRequirement{ClassIRI: extensionClass}},
		},
	}
}

func TestCheckRequirementsWarnsByDefault(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer

	cfg := &config{documents: nil, quiet: false, strict: false, verbose: false, version: false, help: false}

	err := checkRequirements(processWithUnknownRequirement(), "doc.cwl", cfg, &stderr)
	if err != nil {
		t.Fatalf("checkRequirements = %v, want nil when permissive", err)
	}

	if !strings.Contains(stderr.String(), extensionClass) {
		t.Errorf("stderr = %q, want the unrecognized class named", stderr.String())
	}
}

func TestCheckRequirementsFailsUnderStrict(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer

	cfg := &config{documents: nil, quiet: false, strict: true, verbose: false, version: false, help: false}

	err := checkRequirements(processWithUnknownRequirement(), "doc.cwl", cfg, &stderr)
	if err == nil {
		t.Fatal("checkRequirements = nil, want a failure under -strict")
	}

	if !strings.Contains(stderr.String(), "doc.cwl: INVALID") {
		t.Errorf("stderr = %q, want the document reported invalid", stderr.String())
	}
}

func TestCheckRequirementsStaysQuiet(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer

	cfg := &config{documents: nil, quiet: true, strict: false, verbose: false, version: false, help: false}

	err := checkRequirements(processWithUnknownRequirement(), "doc.cwl", cfg, &stderr)
	if err != nil {
		t.Fatalf("checkRequirements = %v, want nil", err)
	}

	if stderr.String() != "" {
		t.Errorf("stderr = %q, want nothing under -quiet", stderr.String())
	}
}

func TestCheckRequirementsAcceptsCoreRequirements(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer

	process := &cwlcore.Operation{
		ProcessBase: cwlcore.ProcessBase{
			ID:           "#op",
			Requirements: []cwlcore.ProcessRequirement{&cwlcore.InlineJavascriptRequirement{}},
		},
	}
	cfg := &config{documents: nil, quiet: false, strict: true, verbose: false, version: false, help: false}

	err := checkRequirements(process, "doc.cwl", cfg, &stderr)
	if err != nil {
		t.Fatalf("checkRequirements = %v, want nil for a core requirement", err)
	}

	if stderr.String() != "" {
		t.Errorf("stderr = %q, want nothing", stderr.String())
	}
}
