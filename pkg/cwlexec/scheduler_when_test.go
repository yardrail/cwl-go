package cwlexec

import (
	"context"
	"fmt"
	"testing"
)

// gatedWorkflow gates its first step on a boolean workflow input and feeds whatever that step
// produced into a second, ungated step.
func gatedWorkflow() *wfSpec {
	return &wfSpec{
		inputs: []string{"flag"},
		steps: []stepSpec{
			{
				name: "gated",
				when: "$(inputs." + portIn + ")",
				in:   []inSpec{{name: portIn, sources: []string{"flag"}}},
				out:  []string{portA, portB},
			},
			{
				name: "after",
				in:   []inSpec{{name: portIn, sources: []string{"gated/" + portA}}},
				out:  []string{portA},
			},
		},
		outputs: []outSpec{
			{name: "gatedA", sources: []string{"gated/" + portA}},
			{name: "gatedB", sources: []string{"gated/" + portB}},
			{name: portSeen, sources: []string{"after/" + portA}},
		},
	}
}

// passThroughHandler publishes port a holding whatever arrived on port n, so a null flowing out of
// a skipped step is visible downstream.
func passThroughHandler() func(context.Context, *StepCall) (Result, error) {
	return func(_ context.Context, call *StepCall) (Result, error) {
		return Success(object(portA, call.Inputs[portIn], portB, "ran"))
	}
}

func TestWhenFalseSkipsStepAndFeedsNullsDownstream(t *testing.T) {
	t.Parallel()

	runner := mustRunner(t, gatedWorkflow(), testRegistry(passThroughHandler()), nil)

	result := mustRun(t, runner, object("flag", false))
	assertDeepEqual(t, "outputs", result.Outputs, object("gatedA", nil, "gatedB", nil, portSeen, nil))
}

func TestWhenTrueRunsStepNormally(t *testing.T) {
	t.Parallel()

	runner := mustRunner(t, gatedWorkflow(), testRegistry(passThroughHandler()), nil)

	result := mustRun(t, runner, object("flag", true))
	assertDeepEqual(t, "outputs", result.Outputs, object("gatedA", true, "gatedB", "ran", portSeen, true))
}

func TestWhenSkipUsesTheStepsOutPortsNotTheProcessOutputs(t *testing.T) {
	t.Parallel()

	spec := gatedWorkflow()
	spec.steps[0].runOut = []string{portA, portB, extraPort}

	runner := mustRunner(t, spec, testRegistry(passThroughHandler()), nil)

	result := mustRun(t, runner, object("flag", false))
	if _, phantom := result.Outputs[extraPort]; phantom {
		t.Fatalf("outputs = %v, want no port for an output the step does not declare", result.Outputs)
	}
}

func TestWhenErrorFailsTheStep(t *testing.T) {
	t.Parallel()

	runner := mustRunner(t, gatedWorkflow(), testRegistry(passThroughHandler()), nil)

	_, err := runner.Run(t.Context(), object("flag", "yes"))
	assertErrorIs(t, "Run", err, ErrWhenNotBoolean)
}

// perIndexScatter gates each element of a scatter on the element itself.
func perIndexScatter() *wfSpec {
	return &wfSpec{
		inputs: []string{flagsInput},
		steps: []stepSpec{{
			name:    stepFan,
			when:    "$(inputs." + portIn + ")",
			in:      []inSpec{{name: portIn, sources: []string{flagsInput}}},
			out:     []string{portA},
			scatter: []string{portIn},
		}},
		outputs: []outSpec{{name: portFinal, sources: []string{"fan/" + portA}}},
	}
}

func TestWhenIsEvaluatedOncePerScatterIndex(t *testing.T) {
	t.Parallel()

	runner := mustRunner(t, perIndexScatter(), testRegistry(constOutputs(object(portA, "ran"))), nil)

	result := mustRun(t, runner, object(flagsInput, list(true, false, true)))
	assertDeepEqual(t, "gathered", result.Outputs[portFinal], list("ran", nil, "ran"))
}

func TestScatterOverEmptyArrayProducesEmptyArrays(t *testing.T) {
	t.Parallel()

	registry := testRegistry(func(_ context.Context, _ *StepCall) (Result, error) {
		t.Error("handler must not be called for a zero-cardinality scatter")

		return Success(nil)
	})

	runner := mustRunner(t, scatterOverInput(), registry, nil)

	result := mustRun(t, runner, object("xs", make([]any, 0)))
	assertDeepEqual(t, "gathered", result.Outputs[portFinal], make([]any, 0))
}

func TestScatterOverANonArrayFailsTheStep(t *testing.T) {
	t.Parallel()

	runner := mustRunner(t, scatterOverInput(), testRegistry(constOutputs(object(portA, 1))), nil)

	_, err := runner.Run(t.Context(), object("xs", "not an array"))
	assertErrorIs(t, "Run", err, ErrScatterInputNotArray)
}

func TestScatterNestedCrossproductGathersNested(t *testing.T) {
	t.Parallel()

	spec := wfSpec{
		inputs: []string{"xs", "ys"},
		steps: []stepSpec{{
			name:    "grid",
			method:  "nested_crossproduct",
			out:     []string{portA},
			scatter: []string{portIn, "m"},
			in: []inSpec{
				{name: portIn, sources: []string{"xs"}},
				{name: "m", sources: []string{"ys"}},
			},
		}},
		outputs: []outSpec{{name: portFinal, sources: []string{"grid/" + portA}}},
	}

	registry := testRegistry(func(_ context.Context, call *StepCall) (Result, error) {
		return Success(object(portA, fmt.Sprintf("%v%v", call.Inputs[portIn], call.Inputs["m"])))
	})

	result := mustRun(t, mustRunner(t, &spec, registry, nil), object("xs", list("a", "b"), "ys", list("1", "2")))
	assertDeepEqual(t, "gathered", result.Outputs[portFinal], list(list("a1", "a2"), list("b1", "b2")))
}
