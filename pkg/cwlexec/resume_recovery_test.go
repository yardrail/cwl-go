package cwlexec

import (
	"context"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// gateAndFail suspends the left branch of the diamond and fails the right one, so a single run
// finishes carrying both a recorded failure and an outstanding suspension.
func gateAndFail() func(context.Context, *StepCall) (Result, error) {
	rest := joinHandler()

	return func(ctx context.Context, call *StepCall) (Result, error) {
		switch call.StepID {
		case stepLeft:
			return call.Suspend(gateToken, nil)
		case stepRight:
			return PermanentFail(errBoom)
		default:
			return rest(ctx, call)
		}
	}
}

func TestResumeSurfacesAStepFailureRecordedInAnEarlierSegment(t *testing.T) {
	t.Parallel()

	cfg := &Config{OnError: OnErrorContinue}
	runner := mustRunner(t, diamondWorkflow(), testRegistry(gateAndFail()), cfg)

	first, err := runner.Run(t.Context(), object("x", "seed"))
	assertErrorIs(t, "Run", err, errBoom)

	resumed := []ResumedStep{{StepID: stepLeft, Status: StatusSuccess, Outputs: object(portA, "approved")}}

	second, resumeErr := runner.Resume(t.Context(), roundTrip(t, first.State), resumed)
	if resumeErr == nil {
		t.Fatalf("Resume: want the earlier failure to surface, got %+v", second)
	}

	if !containsAll(resumeErr.Error(), stepRight, errBoom.Error()) {
		t.Fatalf("error = %q, want it to name the failed step and preserve its explanation", resumeErr.Error())
	}
}

// pickedScatter routes two producers into one scatter through a the_only_non_null pick, so that
// tampering with a recorded output makes the resumed run's input resolution fail.
func pickedScatter() *wfSpec {
	return &wfSpec{
		inputs: []string{"x"},
		steps: []stepSpec{
			{name: "p1", in: []inSpec{{name: portIn, sources: []string{"x"}}}, out: []string{portA}},
			{name: "p2", in: []inSpec{{name: portIn, sources: []string{"x"}}}, out: []string{portA}},
			{
				name:    stepFan,
				out:     []string{portA},
				scatter: []string{portIn},
				in: []inSpec{{
					name:      portIn,
					sources:   []string{"p1/" + portA, "p2/" + portA},
					pickValue: cwlcore.PickTheOnlyNonNull,
				}},
			},
		},
		outputs: []outSpec{{name: portFinal, sources: []string{"fan/" + portA}}},
	}
}

// producerOrSuspend answers the producers with fixed values and suspends one scatter slot.
func producerOrSuspend() func(context.Context, *StepCall) (Result, error) {
	gate := suspendOneIndex()

	return func(ctx context.Context, call *StepCall) (Result, error) {
		switch call.StepID {
		case "p1":
			return Success(object(portA, list("a", "b")))
		case "p2":
			return Success(object(portA, nil))
		default:
			return gate(ctx, call)
		}
	}
}

// suspendedPickedScatter runs pickedScatter to its suspension and returns the runner and snapshot.
func suspendedPickedScatter(t *testing.T) (*Runner, RunState) {
	t.Helper()

	runner := mustRunner(t, pickedScatter(), testRegistry(producerOrSuspend()), nil)
	suspended := mustRun(t, runner, object("x", 1))

	if suspended.Status != StatusSuspended {
		t.Fatalf("status = %q, want %q", suspended.Status, StatusSuspended)
	}

	return runner, suspended.State
}

func TestResumeReportsInputResolutionThatNoLongerWorks(t *testing.T) {
	t.Parallel()

	runner, state := suspendedPickedScatter(t)
	state.steps["p2"].Outputs[portA] = list("c")

	_, err := runner.Resume(t.Context(), state, nil)
	assertErrorIs(t, "Resume", err, ErrPickValue)
}

func TestResumeReportsAScatterThatNoLongerExpands(t *testing.T) {
	t.Parallel()

	runner, state := suspendedPickedScatter(t)
	state.steps["p1"].Outputs[portA] = "no longer an array"

	_, err := runner.Resume(t.Context(), state, nil)
	assertErrorIs(t, "Resume", err, ErrScatterInputNotArray)
}

func TestRunReportsAWorkflowOutputThatCannotBePicked(t *testing.T) {
	t.Parallel()

	spec := wfSpec{
		inputs: []string{"x"},
		steps: []stepSpec{
			{name: "p1", in: []inSpec{{name: portIn, sources: []string{"x"}}}, out: []string{portA}},
			{name: "p2", in: []inSpec{{name: portIn, sources: []string{"x"}}}, out: []string{portA}},
		},
		outputs: []outSpec{{
			name:      portFinal,
			pickValue: cwlcore.PickFirstNonNull,
			sources:   []string{"p1/" + portA, "p2/" + portA},
		}},
	}

	runner := mustRunner(t, &spec, testRegistry(constOutputs(object(portA, nil))), nil)

	result, err := runner.Run(t.Context(), object("x", 1))
	assertErrorIs(t, "Run", err, ErrPickValue)

	if result.Status != StatusPermanentFail {
		t.Fatalf("status = %q, want %q", result.Status, StatusPermanentFail)
	}
}

func TestPickValueAppliesToASingleUnwrappedSource(t *testing.T) {
	t.Parallel()

	wiring := wiringSpec{sources: []string{"p1/" + portA}, pickValue: cwlcore.PickAllNonNull}
	runner := mustRunner(t, wiringWorkflow(&wiring), producerRegistry(object("p1", "one")), nil)

	assertDeepEqual(t, "resolved", mustRun(t, runner, object("x", 0)).Outputs["got"], list("one"))
}

func TestSourceValueRejectsAnIdentifierThePlanDoesNotKnow(t *testing.T) {
	t.Parallel()

	runner := mustRunner(t, linearWorkflow(), testRegistry(suffixHandler()), nil)

	if _, known := runner.newLoop(newRunState(nil)).sourceValue("file:///elsewhere#ghost"); known {
		t.Fatal("sourceValue reported a value for an identifier the plan does not know")
	}
}

func TestRunLeavesAnUnwiredWorkflowOutputNull(t *testing.T) {
	t.Parallel()

	spec := wfSpec{
		steps:   []stepSpec{{name: "s1", out: []string{portA}}},
		outputs: []outSpec{{name: portFinal}, {name: portSeen, sources: []string{"s1/" + portA}}},
	}

	runner := mustRunner(t, &spec, testRegistry(constOutputs(object(portA, "ok"))), nil)

	assertDeepEqual(t, "outputs", mustRun(t, runner, nil).Outputs, object(portFinal, nil, portSeen, "ok"))
}
