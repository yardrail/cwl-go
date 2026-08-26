package cwlexec

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// gateToken is the opaque correlation identifier the suspending fixtures hand back.
const gateToken = "await-approval"

// suspendingHandler suspends every call to the diamond's left branch and answers every other call
// with the join rule.
func suspendingHandler() func(context.Context, *StepCall) (Result, error) {
	rest := joinHandler()

	return func(ctx context.Context, call *StepCall) (Result, error) {
		if call.StepID != stepLeft {
			return rest(ctx, call)
		}

		return call.Suspend(gateToken, []byte(`{"deadline":"tomorrow"}`))
	}
}

// roundTrip persists a snapshot the way a caller would and reads it back.
func roundTrip(t *testing.T, state RunState) RunState {
	t.Helper()

	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("json.Marshal(RunState): unexpected error: %v", err)
	}

	var restored RunState

	err = json.Unmarshal(encoded, &restored)
	if err != nil {
		t.Fatalf("json.Unmarshal(RunState): unexpected error: %v", err)
	}

	return restored
}

func TestSuspendPausesOnlyItsOwnBranch(t *testing.T) {
	t.Parallel()

	spec := diamondWorkflow()
	runner := mustRunner(t, spec, testRegistry(suspendingHandler()), nil)

	result := mustRun(t, runner, object("x", "seed"))

	if result.Status != StatusSuspended {
		t.Fatalf("status = %q, want %q", result.Status, StatusSuspended)
	}

	assertInt(t, "suspensions", len(result.Suspensions), 1)
	assertDeepEqual(t, "suspension", result.Suspensions[0], Suspension{
		StepID: stepLeft, Token: gateToken, Payload: []byte(`{"deadline":"tomorrow"}`),
	})

	if right := result.State.steps[stepRight]; right.Status != StatusSuccess {
		t.Fatalf("sibling branch status = %q, want %q", right.Status, StatusSuccess)
	}
}

func TestResumeRoundTripsThroughJSON(t *testing.T) {
	t.Parallel()

	runner := mustRunner(t, diamondWorkflow(), testRegistry(suspendingHandler()), nil)

	suspended := mustRun(t, runner, object("x", "seed"))
	restored := roundTrip(t, suspended.State)

	resumed := []ResumedStep{{StepID: stepLeft, Status: StatusSuccess, Outputs: object(portA, "approved")}}

	result, err := runner.Resume(t.Context(), restored, resumed)
	if err != nil {
		t.Fatalf("Resume: unexpected error: %v", err)
	}

	if result.Status != StatusSuccess {
		t.Fatalf("status = %q, want %q", result.Status, StatusSuccess)
	}

	assertDeepEqual(t, "outputs", result.Outputs, object(portFinal, "approved+seed/fork/right"))
}

func TestResumeRejectsAVersionMismatch(t *testing.T) {
	t.Parallel()

	runner := mustRunner(t, diamondWorkflow(), testRegistry(suspendingHandler()), nil)
	suspended := mustRun(t, runner, object("x", "seed"))

	stale := suspended.State
	stale.version = RunStateVersion + 1

	_, err := runner.Resume(t.Context(), stale, nil)
	assertErrorIs(t, "Resume", err, ErrStateVersion)
}

func TestResumeRejectsAStateThatWasNeverVersioned(t *testing.T) {
	t.Parallel()

	runner := mustRunner(t, diamondWorkflow(), testRegistry(suspendingHandler()), nil)

	_, err := runner.Resume(t.Context(), RunState{}, nil)
	assertErrorIs(t, "Resume", err, ErrStateVersion)
}

func TestResumeRejectsAnUnmatchedStep(t *testing.T) {
	t.Parallel()

	runner := mustRunner(t, diamondWorkflow(), testRegistry(suspendingHandler()), nil)
	suspended := mustRun(t, runner, object("x", "seed"))

	cases := []struct {
		name   string
		step   ResumedStep
		reason string
	}{
		{name: "unknown step", step: ResumedStep{StepID: "nowhere", Status: StatusSuccess}},
		{name: "not suspended", step: ResumedStep{StepID: stepRight, Status: StatusSuccess}},
		{name: "wrong coordinates", step: ResumedStep{StepID: stepLeft, Status: StatusSuccess, ScatterIndex: []int{3}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := runner.Resume(t.Context(), suspended.State, []ResumedStep{tc.step})
			assertErrorIs(t, "Resume", err, ErrNoSuspension)
		})
	}
}

func TestResumeRejectsAnUnusableStatus(t *testing.T) {
	t.Parallel()

	runner := mustRunner(t, diamondWorkflow(), testRegistry(suspendingHandler()), nil)
	suspended := mustRun(t, runner, object("x", "seed"))

	resumed := []ResumedStep{{StepID: stepLeft, Status: StatusSuspended}}

	_, err := runner.Resume(t.Context(), suspended.State, resumed)
	assertErrorIs(t, "Resume", err, ErrResumedStatus)
}

func TestResumeWithAFailureShortCircuitsTheBranch(t *testing.T) {
	t.Parallel()

	runner := mustRunner(t, diamondWorkflow(), testRegistry(suspendingHandler()), nil)
	suspended := mustRun(t, runner, object("x", "seed"))

	resumed := []ResumedStep{{StepID: stepLeft, Status: StatusPermanentFail}}

	result, err := runner.Resume(t.Context(), suspended.State, resumed)
	if err == nil {
		t.Fatalf("Resume: want an error for a failed branch, got result %+v", result)
	}

	if result.Status != StatusPermanentFail {
		t.Fatalf("status = %q, want %q", result.Status, StatusPermanentFail)
	}
}

func TestResumeWithASkipFeedsNullsDownstream(t *testing.T) {
	t.Parallel()

	runner := mustRunner(t, diamondWorkflow(), testRegistry(suspendingHandler()), nil)
	suspended := mustRun(t, runner, object("x", "seed"))

	resumed := []ResumedStep{{StepID: stepLeft, Status: StatusSkipped}}

	result, err := runner.Resume(t.Context(), suspended.State, resumed)
	if err != nil {
		t.Fatalf("Resume: unexpected error: %v", err)
	}

	assertDeepEqual(t, "outputs", result.Outputs, object(portFinal, "<nil>+seed/fork/right"))
}

// chainedGates suspends whichever step is asked for, one gate after the other.
func chainedGates(gates map[string]bool) func(context.Context, *StepCall) (Result, error) {
	suffix := suffixHandler()

	return func(ctx context.Context, call *StepCall) (Result, error) {
		if gates[call.StepID] {
			return call.Suspend(call.StepID, nil)
		}

		return suffix(ctx, call)
	}
}

func TestResumeMayItselfSuspendAgain(t *testing.T) {
	t.Parallel()

	gates := map[string]bool{"s1": true, "s2": true}
	runner := mustRunner(t, linearWorkflow(), testRegistry(chainedGates(gates)), nil)

	first := mustRun(t, runner, object("x", "seed"))
	assertDeepEqual(t, "first gate", first.Suspensions[0].StepID, "s1")

	second, err := runner.Resume(t.Context(), roundTrip(t, first.State),
		[]ResumedStep{{StepID: "s1", Status: StatusSuccess, Outputs: object(portA, "one")}})
	if err != nil {
		t.Fatalf("Resume: unexpected error: %v", err)
	}

	if second.Status != StatusSuspended {
		t.Fatalf("status = %q, want %q", second.Status, StatusSuspended)
	}

	assertDeepEqual(t, "second gate", second.Suspensions[0].StepID, "s2")

	final, err := runner.Resume(t.Context(), roundTrip(t, second.State),
		[]ResumedStep{{StepID: "s2", Status: StatusSuccess, Outputs: object(portA, "two")}})
	if err != nil {
		t.Fatalf("Resume: unexpected error: %v", err)
	}

	assertDeepEqual(t, "outputs", final.Outputs, object(portFinal, "two"))
}

// suspendedIndex is the scatter slot the suspending fixtures pause.
const suspendedIndex = 1

// suspendOneIndex suspends exactly one sub-job of a scattered step and lets its siblings finish.
func suspendOneIndex() func(context.Context, *StepCall) (Result, error) {
	return func(_ context.Context, call *StepCall) (Result, error) {
		if len(call.ScatterIndex) == 1 && call.ScatterIndex[0] == suspendedIndex {
			return call.Suspend(gateToken, nil)
		}

		return Success(object(portA, call.Inputs[portIn]))
	}
}

func TestSuspendedScatterSlotWaitsWhileSiblingsFinish(t *testing.T) {
	t.Parallel()

	runner := mustRunner(t, scatterOverInput(), testRegistry(suspendOneIndex()), nil)

	suspended := mustRun(t, runner, object("xs", list("a", "b", "c")))

	if suspended.Status != StatusSuspended {
		t.Fatalf("status = %q, want %q", suspended.Status, StatusSuspended)
	}

	assertDeepEqual(t, "suspension coordinates", suspended.Suspensions[0].ScatterIndex, []int{1})

	fan := suspended.State.steps[stepFan]
	assertDeepEqual(t, "sibling statuses", []Status{fan.Jobs[0].Status, fan.Jobs[1].Status, fan.Jobs[2].Status},
		[]Status{StatusSuccess, StatusSuspended, StatusSuccess})

	if fan.Status != "" {
		t.Fatalf("step status = %q, want the step to still be waiting", fan.Status)
	}

	resumed := []ResumedStep{
		{StepID: stepFan, ScatterIndex: []int{1}, Status: StatusSuccess, Outputs: object(portA, "B")},
	}

	result, err := runner.Resume(t.Context(), roundTrip(t, suspended.State), resumed)
	if err != nil {
		t.Fatalf("Resume: unexpected error: %v", err)
	}

	assertDeepEqual(t, "gathered", result.Outputs[portFinal], list("a", "B", "c"))
}

func TestResumeValidatesOutputsAgainstDeclaredTypes(t *testing.T) {
	t.Parallel()

	spec := diamondWorkflow()
	spec.steps[1].outTypes = map[string]cwlcore.TypeRef{portA: cwlcore.NewPrimitiveType(cwlcore.PrimitiveString)}

	runner := mustRunner(t, spec, testRegistry(suspendingHandler()), nil)
	suspended := mustRun(t, runner, object("x", "seed"))

	cases := []struct {
		outputs map[string]any
		name    string
		want    error
	}{
		{name: "wrong type", outputs: object(portA, 42), want: ErrOutputType},
		{name: "undeclared port", outputs: object(portA, "ok", "ghost", 1), want: ErrUndeclaredResumedOutput},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resumed := []ResumedStep{{StepID: stepLeft, Status: StatusSuccess, Outputs: tc.outputs}}

			_, err := runner.Resume(t.Context(), suspended.State, resumed)
			assertErrorIs(t, "Resume", err, tc.want)
		})
	}
}

func TestResumeAcceptsOutputsThatMatchTheDeclaredType(t *testing.T) {
	t.Parallel()

	spec := diamondWorkflow()
	spec.steps[1].outTypes = map[string]cwlcore.TypeRef{portA: cwlcore.NewPrimitiveType(cwlcore.PrimitiveString)}

	runner := mustRunner(t, spec, testRegistry(suspendingHandler()), nil)
	suspended := mustRun(t, runner, object("x", "seed"))

	resumed := []ResumedStep{{StepID: stepLeft, Status: StatusSuccess, Outputs: object(portA, "ok")}}

	result, err := runner.Resume(t.Context(), suspended.State, resumed)
	if err != nil {
		t.Fatalf("Resume: unexpected error: %v", err)
	}

	assertDeepEqual(t, "outputs", result.Outputs, object(portFinal, "ok+seed/fork/right"))
}
