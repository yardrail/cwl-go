package cwlexec

import (
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// The literals the execution tests repeat, named once so that goconst stays quiet and so that a
// reader can tell which occurrences are meant to be the same string.
const (
	// execToolID is the resolved identifier the fixture tools carry, and execOutID that of their
	// single output parameter. ShortName(execOutID) is outPort.
	execToolID = "file:///run.cwl#runtool"
	execOutID  = "file:///run.cwl#runtool/out"

	// execShell and execDashC spawn a shell script, which is the one program every POSIX system
	// is guaranteed to have.
	execShell = "/bin/sh"
	execDashC = "-c"

	// execCat, execEcho, execTrue, execFalse and execSleep are the other portable programs the
	// tests reach for.
	execCat   = "cat"
	execEcho  = "echo"
	execTrue  = "true"
	execFalse = "false"
	execSleep = "sleep"

	// execOutName is the file most fixtures write, and execGreeting what they write into it.
	execOutName  = "out.txt"
	execGreeting = "hello\n"

	// execInPort is the input parameter the staging fixtures feed a File to.
	execInPort = "f"

	// execSourceName is the file on the host that those fixtures stage.
	execSourceName = "source.txt"
)

// execSkipUnless skips the test when a program the fixture needs is not on this machine.
func execSkipUnless(t *testing.T, program string) {
	t.Helper()

	_, err := exec.LookPath(program)
	if err != nil {
		t.Skipf("%s is not available: %v", program, err)
	}
}

// execToolOf returns the CommandLineTool a call runs, or an empty one when it runs something else,
// so that a table row can reach into the tool without an unchecked assertion.
func execToolOf(call *StepCall) *cwlcore.CommandLineTool {
	tool, ok := call.Process.(*cwlcore.CommandLineTool)
	if !ok {
		return &cwlcore.CommandLineTool{}
	}

	return tool
}

// execTool builds a CommandLineTool with the given baseCommand and output parameters.
func execTool(base []string, outputs ...cwlcore.CommandOutputParameter) *cwlcore.CommandLineTool {
	tool := &cwlcore.CommandLineTool{BaseCommand: base, Outputs: outputs}
	tool.ID = execToolID

	return tool
}

// execScript builds a CommandLineTool that runs a shell script.
func execScript(script string, outputs ...cwlcore.CommandOutputParameter) *cwlcore.CommandLineTool {
	return execTool([]string{execShell, execDashC, script}, outputs...)
}

// execFileOut is an output parameter of type File globbing name.
func execFileOut(name string) cwlcore.CommandOutputParameter {
	return outTestParam(execOutID, outTypeFile, outGlobBinding(name))
}

// execCall builds a StepCall for tool, with its two directories allocated but not created under a
// fresh temporary directory — which is the shape the scheduler hands a handler.
func execCall(t *testing.T, tool *cwlcore.CommandLineTool) *StepCall {
	t.Helper()

	base := t.TempDir()

	return &StepCall{
		StepID:  stepID,
		Process: tool,
		Class:   Class(cwlcore.ClassCommandLineTool),
		Inputs:  make(map[string]any),
		OutDir:  filepath.Join(base, "out"),
		TmpDir:  filepath.Join(base, "tmp"),
		Eval:    cwlcore.NewEvaluator(cwlcore.WithJS(nil)),
		Logger:  slog.New(slog.DiscardHandler),
	}
}

// execRun runs a call through the built-in CommandLineTool handler, exactly as the scheduler would.
func execRun(t *testing.T, call *StepCall) (Result, error) {
	t.Helper()

	handler, found := NewRegistry().Handler(Class(cwlcore.ClassCommandLineTool))
	if !found {
		t.Fatal("NewRegistry has no CommandLineTool handler")
	}

	return Outcome(handler.Execute(t.Context(), call))
}

// execSucceed runs a call and requires it to succeed, returning its outputs.
func execSucceed(t *testing.T, call *StepCall) map[string]any {
	t.Helper()

	result, err := execRun(t, call)
	if err != nil {
		t.Fatalf("Execute: unexpected error: %v", err)
	}

	if result.Status != StatusSuccess {
		t.Fatalf("status = %q, want %q", result.Status, StatusSuccess)
	}

	return result.Outputs
}

// execFail runs a call and requires it to fail with the given sentinel, returning the status.
func execFail(t *testing.T, call *StepCall, want error) Status {
	t.Helper()

	result, err := execRun(t, call)
	if err == nil {
		t.Fatalf("Execute: want an error wrapping %v, got outputs %v", want, result.Outputs)
	}

	if want != nil && !errors.Is(err, want) {
		t.Fatalf("Execute: error %v does not wrap %v", err, want)
	}

	return result.Status
}

// execScope builds a requirement scope declaring reqs on the tool itself.
func execScope(reqs ...cwlcore.ProcessRequirement) *cwlcore.RequirementScope {
	tool := &cwlcore.CommandLineTool{}
	tool.Requirements = reqs

	return cwlcore.NewScope(tool)
}

// execInheritedScope builds the scope a tool under a workflow step sees: the workflow declares the
// requirements, and the tool itself declares nothing.
//
// It is the shape that matters for anything CollectOutputs resolves for itself, because the scope it
// builds internally is cwlcore.NewScope(tool) — which sees the inner frame and nothing above it.
func execInheritedScope(t *testing.T, reqs ...cwlcore.ProcessRequirement) *cwlcore.RequirementScope {
	t.Helper()

	workflow := &cwlcore.Workflow{}
	workflow.Requirements = reqs

	return cwlcore.NewScope(workflow).PushProcess(&cwlcore.CommandLineTool{})
}

// execSchemaDef builds a SchemaDefRequirement from a YAML fixture declaring its types.
func execSchemaDef(t *testing.T, src string) *cwlcore.SchemaDefRequirement {
	t.Helper()

	node, err := salad.Parse("schemadef.yml", []byte(src))
	if err != nil {
		t.Fatalf("salad.Parse: %v", err)
	}

	declarations, ok := salad.AsSeq(node)
	if !ok {
		t.Fatalf("the schemadef fixture parsed as %s, want a sequence", salad.NodeKind(node))
	}

	return &cwlcore.SchemaDefRequirement{Types: declarations.Items()}
}

// execHintScope builds a requirement scope declaring hints on the tool itself.
func execHintScope(hints ...cwlcore.Hint) *cwlcore.RequirementScope {
	tool := &cwlcore.CommandLineTool{}
	tool.Hints = hints

	return cwlcore.NewScope(tool)
}

// execOutFile requires the single declared output to be a File and returns it.
func execOutFile(t *testing.T, outputs map[string]any) *cwlcore.File {
	t.Helper()

	file, ok := outputs[outPort].(*cwlcore.File)
	if !ok {
		t.Fatalf("output %q = %#v, want a *cwlcore.File", outPort, outputs[outPort])
	}

	return file
}

// execRead reads a file a tool produced.
func execRead(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	return string(data)
}

// execWantContent requires the single declared File output to hold the given text.
func execWantContent(t *testing.T, outputs map[string]any, want string) {
	t.Helper()

	file := execOutFile(t, outputs)

	if got := execRead(t, file.Path); got != want {
		t.Errorf("%s holds %q, want %q", file.Basename, got, want)
	}
}

// execSourceFile writes a file on the host and returns it as a fully-populated input value.
func execSourceFile(t *testing.T, content string) *cwlcore.File {
	t.Helper()

	local := outWriteFile(t, t.TempDir(), execSourceName, content)

	return &cwlcore.File{
		Location: outFileURI(local),
		Path:     local,
		Basename: execSourceName,
		Dirname:  filepath.Dir(local),
		Nameroot: "source",
		Nameext:  ".txt",
	}
}

// execLiteralFile returns a File literal: contents and no path at all.
func execLiteralFile(basename, contents string) *cwlcore.File {
	return &cwlcore.File{Basename: basename, Contents: cwlcore.NewOptString(contents)}
}
