package main

import (
	"bytes"
	"path/filepath"
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

// TestValidateReportsBothVersions pins the two-line report a document written
// against an earlier CWL version gets. It passed two checks -- validation
// against the version it declares, and the upgrade into the v1.2 form this
// implementation runs -- and a reader is entitled to see both said out loud.
func TestValidateReportsBothVersions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
		want []string
	}{
		{
			name: "a v1.0 document",
			file: "tool_v1_0.cwl",
			want: []string{"valid CommandLineTool", "as declared : v1.0  OK", "upgraded to : v1.2  OK"},
		},
		{
			name: "a v1.2 document keeps its single line",
			file: "valid_tool.cwl",
			want: []string{"valid CommandLineTool"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := exercise(t, filepath.Join("testdata", tc.file))
			if got.err != nil {
				t.Fatalf("run: %v\n%s", got.err, got.stderr)
			}

			assertLines(t, got.stdout, tc.want)
		})
	}
}

// TestValidateRejectsNewSyntaxUnderAnOldVersion is the negative half: the
// document below is perfectly good v1.2 and is refused anyway, because the
// record spelling of secondaryFiles did not exist in the version it declares.
func TestValidateRejectsNewSyntaxUnderAnOldVersion(t *testing.T) {
	t.Parallel()

	got := exercise(t, "-verbose", filepath.Join("testdata", "tool_v1_0_invalid.cwl"))
	if got.err == nil {
		t.Fatalf("run succeeded on a v1.0 document using v1.1 syntax\n%s", got.stdout)
	}

	if !strings.Contains(got.stderr, "secondaryFiles") {
		t.Errorf("stderr = %q, want it to name the offending field", got.stderr)
	}
}

// assertLines checks that stdout holds exactly the wanted lines, in the sense
// that each appears and nothing else does.
func assertLines(t *testing.T, stdout string, want []string) {
	t.Helper()

	for _, line := range want {
		if !strings.Contains(stdout, line) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, line)
		}
	}

	if lines := strings.Count(stdout, "\n"); lines != len(want) {
		t.Errorf("stdout = %q, want exactly %d line(s)", stdout, len(want))
	}
}
