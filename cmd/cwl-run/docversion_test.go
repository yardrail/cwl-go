package main

import (
	"errors"
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
		{name: "an earlier version", declared: "v1.0", want: cwlexec.ErrUnsupportedFeature},
		{name: "a draft version", declared: "draft-3", want: cwlexec.ErrUnsupportedFeature},
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

func TestCheckRunVersionsIgnoresAProcessWithNoSteps(t *testing.T) {
	t.Parallel()

	err := checkRunVersions(&cwlcore.ExpressionTool{})
	if err != nil {
		t.Errorf("checkRunVersions on a non-Workflow = %v, want nil", err)
	}
}

func TestCheckRunVersionsWalksNestedWorkflows(t *testing.T) {
	t.Parallel()

	legacy := &cwlcore.ExpressionTool{}
	legacy.CWLVersion = "v1.1"

	inner := &cwlcore.Workflow{Steps: []cwlcore.WorkflowStep{
		{ID: "outer/deep", Run: cwlcore.StepRun{Process: legacy}},
	}}

	outer := &cwlcore.Workflow{Steps: []cwlcore.WorkflowStep{
		// A step that embeds nothing at all is skipped rather than
		// blamed: an unresolved run: reference is the scheduler's to
		// report, not this check's.
		{ID: "unresolved", Run: cwlcore.StepRun{Ref: "elsewhere.cwl"}},
		{ID: "outer", Run: cwlcore.StepRun{Process: inner}},
	}}

	err := checkRunVersions(outer)
	if !errors.Is(err, cwlexec.ErrUnsupportedFeature) {
		t.Fatalf("checkRunVersions = %v, want an unsupported-feature failure", err)
	}

	if !strings.Contains(err.Error(), `step "deep"`) {
		t.Errorf("checkRunVersions = %q, want it to name the offending step", err)
	}
}
