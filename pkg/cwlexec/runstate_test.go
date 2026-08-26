package cwlexec

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRunStateUnmarshalRejectsMalformedJSON(t *testing.T) {
	t.Parallel()

	var state RunState

	// Called directly: encoding/json rejects a syntactically invalid document before it ever
	// reaches a custom unmarshaler, so going through json.Unmarshal would not exercise this.
	err := state.UnmarshalJSON([]byte(`{"version":`))
	if err == nil {
		t.Fatal("UnmarshalJSON: want an error for malformed JSON, got none")
	}
}

func TestRunStateUnmarshalTolerAtesAnEmptyDocument(t *testing.T) {
	t.Parallel()

	var state RunState

	err := json.Unmarshal([]byte(`{}`), &state)
	if err != nil {
		t.Fatalf("json.Unmarshal: unexpected error: %v", err)
	}

	if state.steps == nil {
		t.Fatal("steps map is nil, want an empty map so the loop can record into it")
	}

	assertInt(t, "version", state.version, 0)
}

func TestRunStateSnapshotDoesNotAliasTheRun(t *testing.T) {
	t.Parallel()

	runner := mustRunner(t, linearWorkflow(), testRegistry(suffixHandler()), nil)
	result := mustRun(t, runner, object("x", "seed"))

	snapshot := result.State
	live := runner.newLoop(newRunState(nil))
	live.state.step("s1").Status = StatusPermanentFail

	if snapshot.steps["s1"].Status != StatusSuccess {
		t.Fatalf("snapshot step status = %q, want the run's own outcome", snapshot.steps["s1"].Status)
	}
}

func TestRunOutputsReportsAnUnproducedSource(t *testing.T) {
	t.Parallel()

	runner := mustRunner(t, linearWorkflow(), testRegistry(suffixHandler()), nil)

	_, err := runner.newLoop(newRunState(nil)).runOutputs()
	assertErrorIs(t, "runOutputs", err, ErrIncomplete)
}

// suspendAndFail suspends index 1 of the scatter and fails index 0, leaving the step waiting on a
// slot it can only be resumed out of while already carrying a failure.
func suspendAndFail() func(context.Context, *StepCall) (Result, error) {
	return func(_ context.Context, call *StepCall) (Result, error) {
		switch call.ScatterIndex[0] {
		case 0:
			return PermanentFail(errBoom)
		case 1:
			return call.Suspend(gateToken, nil)
		default:
			return Success(object(portA, call.Inputs[portIn]))
		}
	}
}

func TestResumeCarriesAFailureRecordedInTheSnapshot(t *testing.T) {
	t.Parallel()

	cfg := &Config{OnError: OnErrorContinue}
	runner := mustRunner(t, scatterOverInput(), testRegistry(suspendAndFail()), cfg)

	suspended := mustRun(t, runner, object("xs", list("a", "b", "c")))

	resumed := []ResumedStep{
		{StepID: stepFan, ScatterIndex: []int{1}, Status: StatusSuccess, Outputs: object(portA, "B")},
	}

	result, err := runner.Resume(t.Context(), roundTrip(t, suspended.State), resumed)
	if err == nil {
		t.Fatalf("Resume: want the recorded failure to surface, got %+v", result)
	}

	if result.Status != StatusPermanentFail {
		t.Fatalf("status = %q, want %q", result.Status, StatusPermanentFail)
	}

	if !containsAll(err.Error(), errBoom.Error()) {
		t.Fatalf("error = %q, want it to preserve the recorded explanation", err.Error())
	}
}

func TestResumeWithNothingToInjectSuspendsAgain(t *testing.T) {
	t.Parallel()

	runner := mustRunner(t, scatterOverInput(), testRegistry(suspendOneIndex()), nil)
	suspended := mustRun(t, runner, object("xs", list("a", "b", "c")))

	again, err := runner.Resume(t.Context(), roundTrip(t, suspended.State), nil)
	if err != nil {
		t.Fatalf("Resume: unexpected error: %v", err)
	}

	if again.Status != StatusSuspended {
		t.Fatalf("status = %q, want %q", again.Status, StatusSuspended)
	}

	assertDeepEqual(t, "suspension coordinates", again.Suspensions[0].ScatterIndex, []int{1})
}

func TestResumeRejectsASnapshotThatNoLongerMatches(t *testing.T) {
	t.Parallel()

	runner := mustRunner(t, scatterOverInput(), testRegistry(suspendOneIndex()), nil)
	suspended := mustRun(t, runner, object("xs", list("a", "b", "c")))

	shrunk := suspended.State
	shrunk.inputs["xs"] = list("a", "b")

	_, err := runner.Resume(t.Context(), shrunk, nil)
	assertErrorIs(t, "Resume", err, ErrStateMismatch)
}

func TestGatherFailsAStepWhoseSnapshotShapeIsImpossible(t *testing.T) {
	t.Parallel()

	runner := mustRunner(t, scatterOverInput(), testRegistry(constOutputs(object(portA, 1))), nil)

	state := newRunState(object("xs", list("a", "b")))
	recorded := state.step(stepFan)
	recorded.Started = true
	recorded.Shape = []int{1}
	recorded.Jobs = []jobState{
		{Status: StatusSuccess, Index: []int{0}, Outputs: object(portA, "a")},
		{Status: StatusSuccess, Index: []int{1}, Outputs: object(portA, "b")},
	}

	_, err := runner.Resume(t.Context(), *state, nil)
	assertErrorIs(t, "Resume", err, ErrScatterShape)
}

func TestConfigEvalTimeoutReachesTheEvaluator(t *testing.T) {
	t.Parallel()

	spec := gatedWorkflow()
	cfg := &Config{EvalTimeout: 250 * 1e6}

	runner := mustRunner(t, spec, testRegistry(passThroughHandler()), cfg)

	result := mustRun(t, runner, object("flag", true))
	assertDeepEqual(t, "outputs", result.Outputs["gatedA"], true)
}
