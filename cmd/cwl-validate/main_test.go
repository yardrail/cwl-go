package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// result is one invocation of run, captured for assertions.
type result struct {
	err    error
	stdout string
	stderr string
}

// exercise runs the tool over args with captured output.
func exercise(t *testing.T, args ...string) result {
	t.Helper()

	var stdout, stderr bytes.Buffer

	err := run(args, &stdout, &stderr)

	return result{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func TestRunAcceptsValidDocuments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		file  string
		class string
	}{
		{name: "command line tool", file: "valid_tool.cwl", class: cwlcore.ClassCommandLineTool},
		{name: "expression tool", file: "valid_expression_tool.cwl", class: cwlcore.ClassExpressionTool},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := exercise(t, filepath.Join("testdata", tc.file))
			if got.err != nil {
				t.Fatalf("run: %v\n%s", got.err, got.stderr)
			}

			if !strings.Contains(got.stdout, "valid "+tc.class) {
				t.Errorf("stdout = %q, want it to report a valid %s", got.stdout, tc.class)
			}

			if got.stderr != "" {
				t.Errorf("stderr = %q, want nothing", got.stderr)
			}
		})
	}
}

func TestRunRejectsBadDocuments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
		// want are fragments the diagnostic must contain. Each names
		// something a user needs in order to act on the failure.
		want []string
	}{
		{
			name: "unknown type names the file, line and column",
			file: "unknown_type.cwl",
			want: []string{"unknown_type.cwl: INVALID", "unknown_type.cwl:6:11", `"strng"`},
		},
		{
			name: "valid yaml that is not cwl explains every candidate",
			file: "not_cwl.yml",
			want: []string{"not_cwl.yml: INVALID", "matches no documentRoot type", "tried CommandLineTool"},
		},
		{
			name: "unparseable yaml",
			file: "unparseable.cwl",
			want: []string{"unparseable.cwl: INVALID", "unparseable.cwl:1:8"},
		},
		{
			name: "empty file",
			file: "empty.cwl",
			want: []string{"empty.cwl: INVALID", "must be a mapping"},
		},
		{
			name: "missing file",
			file: "absent.cwl",
			want: []string{"absent.cwl: INVALID", "no such file"},
		},
		{
			name: "a directory is not a document",
			file: "",
			want: []string{"testdata: INVALID", "is a directory"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := exercise(t, filepath.Join("testdata", tc.file))
			if !errors.Is(got.err, errInvalid) {
				t.Fatalf("run error = %v, want errInvalid", got.err)
			}

			requireContains(t, got.stderr, tc.want)
		})
	}
}

func TestFragmentAddressesOneProcess(t *testing.T) {
	t.Parallel()

	got := exercise(t, filepath.Join("testdata", "graph.cwl")+"#tool")
	if got.err != nil {
		t.Fatalf("run: %v\n%s", got.err, got.stderr)
	}

	if !strings.Contains(got.stdout, "valid "+cwlcore.ClassCommandLineTool) {
		t.Errorf("stdout = %q, want the fragment's own class reported", got.stdout)
	}

	unknown := exercise(t, filepath.Join("testdata", "graph.cwl")+"#nope")
	if !errors.Is(unknown.err, errInvalid) {
		t.Fatalf("run error = %v, want errInvalid", unknown.err)
	}

	requireContains(t, unknown.stderr, []string{`declares no object with the identifier "#nope"`})
}

func TestRunReportsEveryFailureNotJustTheFirst(t *testing.T) {
	t.Parallel()

	got := exercise(t,
		filepath.Join("testdata", "unknown_type.cwl"),
		filepath.Join("testdata", "valid_tool.cwl"),
		filepath.Join("testdata", "not_cwl.yml"))

	if !errors.Is(got.err, errInvalid) {
		t.Fatalf("run error = %v, want errInvalid", got.err)
	}

	requireContains(t, got.stderr, []string{
		"unknown_type.cwl: INVALID",
		"not_cwl.yml: INVALID",
		"2 of 3 documents are not valid",
	})

	if !strings.Contains(got.stdout, "valid_tool.cwl: valid") {
		t.Errorf("stdout = %q, want the valid document still reported", got.stdout)
	}
}

func TestRunQuietPrintsNothing(t *testing.T) {
	t.Parallel()

	got := exercise(t, "-quiet", filepath.Join("testdata", "unknown_type.cwl"))
	if !errors.Is(got.err, errInvalid) {
		t.Fatalf("run error = %v, want errInvalid", got.err)
	}

	if got.stdout != "" || got.stderr != "" {
		t.Errorf("quiet run printed stdout %q stderr %q", got.stdout, got.stderr)
	}

	valid := exercise(t, "-q", filepath.Join("testdata", "valid_tool.cwl"))
	if valid.err != nil {
		t.Fatalf("run: %v", valid.err)
	}

	if valid.stdout != "" || valid.stderr != "" {
		t.Errorf("quiet run printed stdout %q stderr %q", valid.stdout, valid.stderr)
	}
}

func TestStrictPromotesAdvisoriesToErrors(t *testing.T) {
	t.Parallel()

	document := filepath.Join("testdata", "undeclared_field.cwl")

	// Permissive validation is the specification's default and discards
	// advisories entirely, so the typo is reported nowhere at all.
	permissive := exercise(t, document)
	if permissive.err != nil {
		t.Fatalf("run: %v\n%s", permissive.err, permissive.stderr)
	}

	if permissive.stderr != "" {
		t.Errorf("stderr = %q, want the advisory dropped by default", permissive.stderr)
	}

	strict := exercise(t, "-strict", document)
	if !errors.Is(strict.err, errInvalid) {
		t.Fatalf("run(-strict) = %v, want errInvalid", strict.err)
	}

	requireContains(t, strict.stderr, []string{
		"undeclared_field.cwl: INVALID",
		"undeclared_field.cwl:4:13",
		`the field "bogusField" is not declared by CommandLineTool`,
	})
}

func TestExtensionClasses(t *testing.T) {
	t.Parallel()

	// A hints entry is typed Any, so an extension class under a declared
	// $namespaces is a perfectly ordinary CWL document.
	hint := exercise(t, filepath.Join("testdata", "extension_hint.cwl"))
	if hint.err != nil {
		t.Fatalf("an extension hint should validate: %v\n%s", hint.err, hint.stderr)
	}

	// A requirements entry is not. ProcessRequirement is a closed union of
	// the seventeen core classes, so an extension class there is a document
	// this schema does not describe, and rejecting it is correct: reaching
	// RawRequirement takes a downstream package supplying an extended
	// schema, not a permissive reading of this one.
	req := exercise(t, filepath.Join("testdata", "extension_requirement.cwl"))
	if !errors.Is(req.err, errInvalid) {
		t.Fatalf("an extension requirement should not validate against the core schema, got %v", req.err)
	}

	requireContains(t, req.stderr, []string{"no concrete subtype of ProcessRequirement matches"})
}

func TestRunTrimsLongReportsUnlessVerbose(t *testing.T) {
	t.Parallel()

	// A mapping rejected against an abstract type explains every concrete
	// subtype it was tried as, which for a requirement runs to hundreds of
	// lines: the shape the trimming exists for.
	document := filepath.Join("testdata", "extension_requirement.cwl")
	brief := exercise(t, document)
	verbose := exercise(t, "-verbose", document)

	// The heading and the "N more lines" note sit outside the trimmed tree,
	// so allow a few lines of slack over the limit itself.
	const slack = 4

	if lines := strings.Count(brief.stderr, "\n"); lines > maxErrorLines+slack {
		t.Errorf("the default report is %d lines, want it trimmed to about %d", lines, maxErrorLines)
	}

	if !strings.Contains(brief.stderr, "re-run with -verbose") {
		t.Errorf("stderr = %q, want it to say how to see the rest", brief.stderr)
	}

	if len(verbose.stderr) < len(brief.stderr) {
		t.Error("the verbose report is shorter than the trimmed one")
	}
}

func TestRunVersion(t *testing.T) {
	t.Parallel()

	got := exercise(t, "-version")
	if got.err != nil {
		t.Fatalf("run: %v", got.err)
	}

	for _, want := range []string{toolName, cwlcore.SchemaVersion()} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("stdout = %q, want it to name %q", got.stdout, want)
		}
	}
}

func TestRunUsageErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "no documents", args: make([]string, 0)},
		{name: "unknown flag", args: []string{"-nope"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := exercise(t, tc.args...)
			if !errors.Is(got.err, errUsage) {
				t.Fatalf("run error = %v, want errUsage", got.err)
			}

			if got.stdout != "" {
				t.Errorf("stdout = %q, want nothing", got.stdout)
			}
		})
	}
}

func TestRunHelpIsNotAFailure(t *testing.T) {
	t.Parallel()

	got := exercise(t, "-h")
	if got.err != nil {
		t.Fatalf("run(-h) = %v, want no error", got.err)
	}

	if !strings.Contains(got.stderr, "Usage:") {
		t.Errorf("stderr = %q, want the usage message", got.stderr)
	}
}

// requireContains fails the test for every fragment missing from got.
func requireContains(t *testing.T, got string, want []string) {
	t.Helper()

	for _, fragment := range want {
		if !strings.Contains(got, fragment) {
			t.Errorf("output does not contain %q:\n%s", fragment, got)
		}
	}
}
