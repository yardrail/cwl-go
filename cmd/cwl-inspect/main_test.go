package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/cmd/internal/cwlcli"
	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// The fixtures every test works from, and the flags they are dumped with.
const (
	toolFixture     = "testdata/tool.cwl"
	workflowFixture = "testdata/workflow.cwl"
	requirements    = "testdata/requirements.cwl"
	operation       = "testdata/operation.cwl"
	graph           = "testdata/graph.cwl"
	externalRun     = "testdata/external_run.cwl"
	stageFlag       = "-stage"
	formatFlag      = "-format"

	// Fragments that recur across the tables below.
	classTool     = `"class": "CommandLineTool"`
	classWorkflow = `"class": "Workflow"`
	refKey        = `"ref": "file://`
	graphToolID   = `graph.cwl#tool`
	locKey        = `"loc": "file://`
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

// dump runs the tool and fails the test if it did not produce a dump.
func dump(t *testing.T, args ...string) string {
	t.Helper()

	got := exercise(t, args...)
	if got.err != nil {
		t.Fatalf("run(%v): %v\n%s", args, got.err, got.stderr)
	}

	return got.stdout
}

// stageCase is one stage/document pair and the fragments its dump must show.
type stageCase struct {
	name     string
	stage    string
	document string
	// want are fragments the dump must contain, chosen so that a stage
	// rendering another stage's view would fail rather than pass.
	want []string
}

// parsedCases cover the two stages that dump a document rather than a process.
func parsedCases() []stageCase {
	return []stageCase{
		{
			name:     "parsed keeps kinds and source lines",
			stage:    string(StageParsed),
			document: toolFixture,
			want:     []string{`"kind": "mapping"`, locKey, `tool.cwl:2:8`, `"key": "cwlVersion"`},
		},
		{
			name:     "resolved shows the tree the loader produced",
			stage:    string(StageResolved),
			document: workflowFixture,
			want: []string{
				`"baseURI": "file://`,
				`"root"`,
				`"kind": "mapping"`,
				// Identifier resolution has run: a bare step-output
				// name is an absolute identifier by this point.
				`workflow.cwl#main/step_one/out`,
			},
		},
		{
			name:     "resolved keeps the document metadata",
			stage:    string(StageResolved),
			document: requirements,
			want:     []string{`"metadata"`, `"key": "$namespaces"`, `"http://example.org/ext#"`},
		},
	}
}

// typedCases cover the decoded model across every process class.
func typedCases() []stageCase {
	return []stageCase{
		{
			name:     "typed shows the decoded model",
			stage:    string(StageTyped),
			document: toolFixture,
			want:     []string{classTool, `"baseCommand"`, `"outputEval": "$(self[0])"`, `"default": "hello"`},
		},
		{
			name:     "typed dumps a workflow's steps",
			stage:    string(StageTyped),
			document: workflowFixture,
			want:     []string{classWorkflow, `"steps"`, `"embedded"`, `"outputSource"`},
		},
		{
			name:     "typed dumps an operation",
			stage:    string(StageTyped),
			document: operation,
			want:     []string{`"class": "Operation"`, `"default": "hi"`, `"label": "an abstract operation"`},
		},
		{
			name:     "typed dumps every requirement class that carries fields",
			stage:    string(StageTyped),
			document: requirements,
			want: []string{
				`"package": "samtools"`,                  // SoftwareRequirement
				`"envName": "PATH"`,                      // EnvVarRequirement
				`"entryname": "script.sh"`,               // InitialWorkDirRequirement
				`"networkAccess": "true"`,                // NetworkAccess
				`"inplaceUpdate": true`,                  // InplaceUpdateRequirement
				`"loadListing": "no_listing"`,            // LoadListingRequirement
				`"timelimit": "30"`,                      // ToolTimeLimit
				`"class": "ShellCommandRequirement"`,     // a marker with no fields
				`"types"`,                                // SchemaDefRequirement
				`"class": "http://example.org/ext#Note"`, // an extension hint
				`"level": "info"`,                        // read from the hint's kept node
			},
		},
	}
}

// referenceCases cover the two stages against documents whose steps name a
// process defined elsewhere, in a $graph and in another file.
func referenceCases() []stageCase {
	return []stageCase{
		{
			name:     "typed resolves a graph's main process and its run reference",
			stage:    string(StageTyped),
			document: graph,
			want:     []string{classWorkflow, refKey, graphToolID},
		},
		{
			name: "typed follows an external run reference",
			// The reference names the document, not the process inside
			// it: tool.cwl declares its own id, and the step asked for
			// the file.
			stage:    string(StageTyped),
			document: externalRun,
			want:     []string{classWorkflow, refKey, `testdata/tool.cwl"`},
		},
		{
			name:     "graph shows every process, not only the entry point",
			stage:    string(StageGraph),
			document: graph,
			// The tool the entry-point rules hide behind #main.
			want: []string{`"processes"`, classWorkflow, classTool, graphToolID},
		},
		{
			name:     "graph of a document with no $graph is its one process",
			stage:    string(StageGraph),
			document: toolFixture,
			want:     []string{`"processes"`, classTool, `tool.cwl#echo`},
		},
		{
			name:     "scope resolves inherited requirements",
			stage:    string(StageScope),
			document: workflowFixture,
			want: []string{
				`"unrecognized": "none"`,
				`"origin": "requirements"`,
				`"ToolTimeLimit"`,
				`"ResourceRequirement"`,
			},
		},
		{
			name:     "scope reports a referenced run without inventing a process",
			stage:    string(StageScope),
			document: graph,
			want:     []string{refKey, `"unrecognized": "none"`},
		},
	}
}

func TestEveryStageAndFormatProducesADump(t *testing.T) {
	t.Parallel()

	tests := slices.Concat(parsedCases(), typedCases(), referenceCases())

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := dump(t, stageFlag, tc.stage, tc.document)
			requireJSON(t, got)
			requireContains(t, got, tc.want)

			text := dump(t, stageFlag, tc.stage, formatFlag, "text", tc.document)
			if text == "" {
				t.Error("text dump is empty")
			}
		})
	}
}

func TestDumpsAreDeterministic(t *testing.T) {
	t.Parallel()

	for _, stage := range stageOrder {
		for _, format := range []cwlcli.Format{cwlcli.FormatJSON, cwlcli.FormatText} {
			t.Run(string(stage)+"/"+string(format), func(t *testing.T) {
				t.Parallel()

				args := []string{stageFlag, string(stage), formatFlag, string(format), workflowFixture}

				first := dump(t, args...)
				second := dump(t, args...)

				if first != second {
					t.Errorf("two runs differ:\n%s\n---\n%s", first, second)
				}
			})
		}
	}
}

func TestTypedDumpRendersOpaqueValuesReadably(t *testing.T) {
	t.Parallel()

	got := dump(t, formatFlag, "text", toolFixture)

	// Every one of these comes from a model type carrying unexported
	// fields, which a reflective dump would render empty or as a Go
	// struct literal.
	requireContains(t, got, []string{
		"type: string",                 // TypeRef
		"outdirMin: $(inputs.threads)", // ResourceValue holding an expression
		"position: 3",                  // ExprLong inside an argument binding
		"separate: false",              // OptBool read through Or
		"required: true",               // ExprBool on a secondary file
	})
}

func TestFragmentSelectsOneProcessOfAGraph(t *testing.T) {
	t.Parallel()

	got := dump(t, graph+"#tool")
	requireContains(t, got, []string{classTool, graphToolID})

	if strings.Contains(got, classWorkflow) {
		t.Errorf("dump names the workflow, want only the process the fragment selected:\n%s", got)
	}

	// The stages that dump a whole document ignore the fragment rather
	// than failing on it, because a fragment names one object inside the
	// file and those stages are about the file.
	whole := dump(t, stageFlag, string(StageParsed), graph+"#tool")
	requireContains(t, whole, []string{`"key": "$graph"`})

	unknown := exercise(t, graph+"#nope")
	requireDocumentFailure(t, unknown, `declares no object with the identifier "#nope"`)
	requireContains(t, unknown.stderr, []string{"graph.cwl#tool", "graph.cwl#main"})
}

func TestParsedStageNeedsNoSchema(t *testing.T) {
	t.Parallel()

	// The parse tree is what you look at when a document does not load, so
	// it must be reachable for a document that does not.
	got := dump(t, stageFlag, "parsed", "testdata/not_cwl.yml")
	if !strings.Contains(got, `"key": "foo"`) {
		t.Errorf("dump does not contain the document's keys:\n%s", got)
	}
}

func TestRunReportsFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing file", args: []string{"testdata/absent.cwl"}, want: "no such file"},
		{name: "directory", args: []string{"testdata"}, want: "is a directory"},
		{
			name: "unparseable",
			args: []string{stageFlag, "parsed", "testdata/unparseable.cwl"},
			want: "unparseable.cwl:1:8",
		},
		{name: "not cwl", args: []string{"testdata/not_cwl.yml"}, want: "matches no documentRoot type"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			requireDocumentFailure(t, exercise(t, tc.args...), tc.want)
		})
	}
}

func TestRunUsageErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "no document", args: make([]string, 0)},
		{name: "two documents", args: []string{toolFixture, workflowFixture}},
		{name: "unknown stage", args: []string{stageFlag, "nonesuch", toolFixture}},
		{name: "unknown format", args: []string{formatFlag, "yaml", toolFixture}},
		{name: "unknown flag", args: []string{"-nope", toolFixture}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := exercise(t, tc.args...)
			if !errors.Is(got.err, errUsage) {
				t.Fatalf("run(%v) = %v, want errUsage", tc.args, got.err)
			}

			if got.stdout != "" {
				t.Errorf("stdout = %q, want nothing", got.stdout)
			}
		})
	}
}

func TestRunVersionAndHelp(t *testing.T) {
	t.Parallel()

	version := exercise(t, "-version")
	if version.err != nil {
		t.Fatalf("run(-version): %v", version.err)
	}

	if !strings.Contains(version.stdout, cwlcore.SchemaVersion()) {
		t.Errorf("stdout = %q, want the schema version", version.stdout)
	}

	help := exercise(t, "-h")
	if help.err != nil {
		t.Fatalf("run(-h) = %v, want no error", help.err)
	}

	if !strings.Contains(help.stderr, "Usage:") {
		t.Errorf("stderr = %q, want the usage message", help.stderr)
	}
}

func TestStageDefaultsToTyped(t *testing.T) {
	t.Parallel()

	var stage Stage

	if stage.String() != string(StageTyped) {
		t.Errorf("zero Stage = %q, want %q", stage.String(), StageTyped)
	}

	if got := Stages(); got != "parsed|resolved|typed|graph|scope" {
		t.Errorf("Stages() = %q, want parsed|resolved|typed|graph|scope", got)
	}

	err := stage.Set("scope")
	if err != nil || stage != StageScope {
		t.Errorf("Set(scope) = (%v, %q)", err, stage)
	}

	err = stage.Set("nope")
	if !errors.Is(err, ErrStage) {
		t.Errorf("Set(nope) = %v, want ErrStage", err)
	}
}

// requireJSON fails the test unless got parses as JSON.
func requireJSON(t *testing.T, got string) {
	t.Helper()

	var decoded any

	err := json.Unmarshal([]byte(got), &decoded)
	if err != nil {
		t.Fatalf("dump is not valid JSON: %v\n%s", err, got)
	}
}

// requireContains fails the test for every fragment missing from got.
func requireContains(t *testing.T, got string, want []string) {
	t.Helper()

	for _, fragment := range want {
		if !strings.Contains(got, fragment) {
			t.Errorf("dump does not contain %q:\n%s", fragment, got)
		}
	}
}

// requireDocumentFailure fails the test unless got is a failure about the
// document rather than about the command line, reported only on stderr.
func requireDocumentFailure(t *testing.T, got result, want string) {
	t.Helper()

	if got.err == nil {
		t.Fatal("run returned nil, want a failure")
	}

	if errors.Is(got.err, errUsage) {
		t.Fatalf("run = %v, want a document failure not a usage error", got.err)
	}

	if !strings.Contains(got.stderr, want) {
		t.Errorf("stderr does not contain %q:\n%s", want, got.stderr)
	}

	if got.stdout != "" {
		t.Errorf("stdout = %q, want nothing so a failed dump does not pollute a pipe", got.stdout)
	}
}
