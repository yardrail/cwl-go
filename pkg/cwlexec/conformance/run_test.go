package conformance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/cwlexec"
)

// runFixture resolves a document under testdata/run.
func runFixture(t *testing.T, name string) string {
	t.Helper()

	path, err := filepath.Abs(filepath.Join("testdata", "run", name))
	if err != nil {
		t.Fatalf("resolving %s: %v", name, err)
	}

	return path
}

// TestProduceRejectsADocumentWithNoCWLVersion exercises checkCWLVersion's error branch by
// way of a real document: a schema-valid top-level CommandLineTool that declares no
// cwlVersion at all loads successfully (the field is optional in the schema) but leaves
// both declaredVersion and process.Base().CWLVersion empty.
func TestProduceRejectsADocumentWithNoCWLVersion(t *testing.T) {
	t.Parallel()

	path := runFixture(t, "no-cwlversion.cwl")

	run := &invocation{process: path, job: "", outDir: t.TempDir(), baseDir: filepath.Dir(path)}

	_, err := produce(t.Context(), run)
	if !errors.Is(err, errNoCWLVersion) {
		t.Errorf("produce = %v, want it to wrap errNoCWLVersion", err)
	}
}

// TestProduceRejectsAMissingRequiredInput exercises produce's jobOrder error branch: the
// fixture corpus's echo.cwl declares a required "message" input with no default, and a run
// that names no job file runs against an empty input object, so resolving it fails.
func TestProduceRejectsAMissingRequiredInput(t *testing.T) {
	t.Parallel()

	path, err := filepath.Abs(filepath.Join("testdata", "corpus", "tests", "echo.cwl"))
	if err != nil {
		t.Fatalf("resolving the fixture: %v", err)
	}

	run := &invocation{process: path, job: "", outDir: t.TempDir(), baseDir: filepath.Dir(path)}

	_, err = produce(t.Context(), run)
	if err == nil {
		t.Error("a run missing a required input was accepted")
	}
}

// TestExecutePropagatesARunError exercises execute's runner.Run() error branch -- distinct
// from the NewRunner (planning) branch below -- with an already-cancelled context, which
// the loop reports as ctx.Err() before any step starts.
func TestExecutePropagatesARunError(t *testing.T) {
	t.Parallel()

	path, err := filepath.Abs(filepath.Join("testdata", "corpus", "tests", "echo.cwl"))
	if err != nil {
		t.Fatalf("resolving the fixture: %v", err)
	}

	process, err := cwlcore.LoadFile(t.Context(), path, cwlcore.Strict(true))
	if err != nil {
		t.Fatalf("loading the fixture: %v", err)
	}

	// Use os.MkdirTemp instead of t.TempDir: the cancelled context makes
	// runner.Run return immediately, but the step goroutine may still be
	// touching the directory — t.TempDir's automatic cleanup races with it.
	//nolint:usetesting // t.TempDir cleanup races with the step goroutine
	outDir, mkErr := os.MkdirTemp("", "cwl-run-error-*")
	if mkErr != nil {
		t.Fatalf("create temp dir: %v", mkErr)
	}

	t.Cleanup(func() {
		// The step goroutine exits via <-l.finished shortly after run()
		// returns, but there's a small window. Give it time to drain.
		time.Sleep(50 * time.Millisecond)
		os.RemoveAll(outDir)
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = execute(ctx, process, map[string]any{"message": "hi"}, outDir)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("execute = %v, want it to wrap context.Canceled", err)
	}
}

// TestExecuteRejectsAnUnplannableWorkflow exercises the NewRunner error branch: a workflow
// output whose outputSource names a step's own identifier -- a valid link as far as schema
// resolution is concerned, since a step carries an identifier of its own, but not a source
// [newPlan] ever records, since only a step's declared "out" ports and the workflow's own
// inputs are recorded as sources.
func TestExecuteRejectsAnUnplannableWorkflow(t *testing.T) {
	t.Parallel()

	path := runFixture(t, "unknown-output-source.cwl")

	process, err := cwlcore.LoadFile(t.Context(), path, cwlcore.Strict(true))
	if err != nil {
		t.Fatalf("loading the fixture: %v", err)
	}

	_, err = execute(t.Context(), process, make(map[string]any), t.TempDir())
	if !errors.Is(err, cwlexec.ErrUnknownSource) {
		t.Errorf("execute = %v, want it to wrap cwlexec.ErrUnknownSource", err)
	}
}

// TestOutputsFromResultMapsStatus unit-tests the status->error mapping [execute] delegates
// to, including the StatusSuspended branch: no built-in handler ever leaves a top-level run
// suspended, so it can only be reached with a hand-built [cwlexec.RunResult].
func TestOutputsFromResultMapsStatus(t *testing.T) {
	t.Parallel()

	t.Run("a successful run", func(t *testing.T) {
		t.Parallel()

		want := map[string]any{outputName: testValue}

		outputs, err := outputsFromResult(
			cwlexec.RunResult{
				Outputs:     want,
				Status:      cwlexec.StatusSuccess,
				Suspensions: nil,
				State:       cwlexec.RunState{},
			},
		)
		if err != nil {
			t.Fatalf("outputsFromResult: %v", err)
		}

		if len(outputs) != 1 || outputs[outputName] != testValue {
			t.Errorf("outputs = %v, want %v", outputs, want)
		}
	})

	t.Run("a suspended run", func(t *testing.T) {
		t.Parallel()

		_, err := outputsFromResult(
			cwlexec.RunResult{
				Outputs:     nil,
				Status:      cwlexec.StatusSuspended,
				Suspensions: nil,
				State:       cwlexec.RunState{},
			},
		)
		if !errors.Is(err, errRun) {
			t.Errorf("outputsFromResult = %v, want it to wrap errRun", err)
		}
	})
}

// TestOutputObjectRendersNullsExplicitly asserts a nil output is emitted as an explicit
// null rather than dropped, and that a non-nil value is routed through
// [cwlcore.ToExpressionValue].
func TestOutputObjectRendersNullsExplicitly(t *testing.T) {
	t.Parallel()

	object := outputObject(map[string]any{"missing": nil, "present": testValue})

	present, ok := object["present"]
	if !ok || present != testValue {
		t.Errorf(`object["present"] = %v, %v, want %q, true`, present, ok, testValue)
	}

	missing, ok := object["missing"]
	if !ok || missing != nil {
		t.Errorf(`object["missing"] = %v, %v, want nil, true`, missing, ok)
	}
}
