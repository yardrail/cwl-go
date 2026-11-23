package cwlexec

import (
	"context"
	"errors"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// These tests pin the Result.Process contract: every CommandLineTool outcome
// after the tool ran — success, non-zero exit, or executor error — carries the
// ProcessResult so observers can surface execution detail.

var errProcBoom = errors.New("process boom")

func TestCommandLineToolResultCarriesProcessOnSuccess(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execShell)

	call := execCall(t, execScript(execWriteScript, execFileOut(execOutName)))

	result, err := execRun(t, call)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Process == nil {
		t.Fatal("Process = nil, want the tool's ProcessResult on success")
	}

	if result.Process.ExitCode != 0 {
		t.Errorf("Process.ExitCode = %d, want 0", result.Process.ExitCode)
	}

	if result.Process.CaptureLimit != 0 {
		t.Errorf("Process.CaptureLimit = %d, want 0 (local exec does not capture)", result.Process.CaptureLimit)
	}
}

func TestCommandLineToolResultCarriesProcessOnToolExit(t *testing.T) {
	t.Parallel()
	execSkipUnless(t, execShell)

	call := execCall(t, execScript("exit 3"))

	result, err := execRun(t, call)
	if !errors.Is(err, ErrToolExit) {
		t.Fatalf("Execute: err = %v, want ErrToolExit", err)
	}

	if result.Process == nil {
		t.Fatal("Process = nil, want the ProcessResult preserved through the failure outcome")
	}

	if result.Process.ExitCode != 3 {
		t.Errorf("Process.ExitCode = %d, want 3", result.Process.ExitCode)
	}
}

// fakeProcExecutor hands out a scripted invocation, standing in for a real
// container runtime.
type fakeProcExecutor struct {
	inv Invocation
}

func (f *fakeProcExecutor) EnsureImage(context.Context, *cwlcore.DockerRequirement) error {
	return nil
}

func (f *fakeProcExecutor) NewInvocation(context.Context, *ContainerSpec) (Invocation, error) {
	return f.inv, nil
}

// fakeProcInvocation returns a scripted ProcessResult and error from Run.
type fakeProcInvocation struct {
	fs  WriteFS
	res *ProcessResult
	err error
}

func (f *fakeProcInvocation) StageFS() WriteFS { return f.fs }
func (f *fakeProcInvocation) OutFS() WriteFS   { return f.fs }
func (f *fakeProcInvocation) TmpFS() WriteFS   { return f.fs }
func (f *fakeProcInvocation) Close() error     { return nil }

func (f *fakeProcInvocation) Run(context.Context, *ProcessSpec) (*ProcessResult, error) {
	return f.res, f.err
}

// TestCommandLineToolResultCarriesPartialProcessOnRunError pins the
// partial-result contract: an executor that fails mid-run may still return
// captures, and they must survive to the failure Result.
func TestCommandLineToolResultCarriesPartialProcessOnRunError(t *testing.T) {
	t.Parallel()

	partial := &ProcessResult{
		ExitCode:     1,
		Stdout:       []byte("partial stdout"),
		CaptureLimit: 64,
	}

	call := execCall(t, execTool([]string{execTrue}))
	call.ContainerExecutor = &fakeProcExecutor{
		inv: &fakeProcInvocation{fs: NewLocalDirFS(t.TempDir()), res: partial, err: errProcBoom},
	}
	call.Requirements = execHintScope(&cwlcore.DockerRequirement{DockerPull: ctrImage})

	result, err := execRun(t, call)
	if !errors.Is(err, errProcBoom) {
		t.Fatalf("Execute: err = %v, want errProcBoom", err)
	}

	if result.Status != StatusPermanentFail {
		t.Errorf("status = %q, want %q", result.Status, StatusPermanentFail)
	}

	if result.Process != partial {
		t.Fatalf("Process = %+v, want the partial ProcessResult from the failed run", result.Process)
	}

	if string(result.Process.Stdout) != "partial stdout" {
		t.Errorf("Process.Stdout = %q, want the partial capture", result.Process.Stdout)
	}
}

func TestOutcomePreservesProcessOnError(t *testing.T) {
	t.Parallel()

	proc := &ProcessResult{ExitCode: 7}

	// A failure status keeps its status and its Process.
	result, err := Outcome(Result{Status: StatusTemporaryFail, Process: proc}, errProcBoom)
	if !errors.Is(err, errProcBoom) || result.Status != StatusTemporaryFail {
		t.Fatalf("Outcome = (%+v, %v), want temporaryFail with the error", result, err)
	}

	if result.Process != proc {
		t.Error("Process dropped from a failure-status Result")
	}

	// A non-failure status with an error normalizes to permanentFail, Process intact.
	result, err = Outcome(Result{Status: StatusSuccess, Process: proc}, errProcBoom)
	if !errors.Is(err, errProcBoom) || result.Status != StatusPermanentFail || result.Process != proc {
		t.Errorf("Outcome = %+v, want permanentFail with Process preserved", result)
	}
}
