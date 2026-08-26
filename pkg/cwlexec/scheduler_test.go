package cwlexec

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// portIn is the input port name the fixture steps read.
const portIn = "n"

// linearWorkflow is two steps in a line: s2 consumes what s1 produced.
func linearWorkflow() *wfSpec {
	return &wfSpec{
		inputs: []string{"x"},
		steps: []stepSpec{
			{name: "s1", in: []inSpec{{name: portIn, sources: []string{"x"}}}, out: []string{portA}},
			{name: "s2", in: []inSpec{{name: portIn, sources: []string{"s1/" + portA}}}, out: []string{portA}},
		},
		outputs: []outSpec{{name: portFinal, sources: []string{"s2/" + portA}}},
	}
}

// suffixHandler publishes port a holding the step's input with the step identifier appended, so a
// result records the path a value took through the graph.
func suffixHandler() func(context.Context, *StepCall) (Result, error) {
	return func(_ context.Context, call *StepCall) (Result, error) {
		return Success(object(portA, fmt.Sprintf("%v/%s", call.Inputs[portIn], call.StepID)))
	}
}

func TestRunLinearWorkflow(t *testing.T) {
	t.Parallel()

	runner := mustRunner(t, linearWorkflow(), testRegistry(suffixHandler()), nil)
	result := mustRun(t, runner, object("x", "seed"))

	if result.Status != StatusSuccess {
		t.Fatalf("status = %q, want %q", result.Status, StatusSuccess)
	}

	assertDeepEqual(t, "outputs", result.Outputs, object(portFinal, "seed/s1/s2"))
}

// diamondWorkflow forks into two independent branches and joins them again.
func diamondWorkflow() *wfSpec {
	return &wfSpec{
		inputs: []string{"x"},
		steps: []stepSpec{
			{name: "fork", in: []inSpec{{name: portIn, sources: []string{"x"}}}, out: []string{portA}},
			{name: stepLeft, in: []inSpec{{name: portIn, sources: []string{"fork/" + portA}}}, out: []string{portA}},
			{name: stepRight, in: []inSpec{{name: portIn, sources: []string{"fork/" + portA}}}, out: []string{portA}},
			{name: "join", out: []string{portA}, in: []inSpec{
				{name: "l", sources: []string{"left/" + portA}},
				{name: "r", sources: []string{"right/" + portA}},
			}},
		},
		outputs: []outSpec{{name: portFinal, sources: []string{"join/" + portA}}},
	}
}

// joinHandler answers the join step by concatenating its two inputs and every other step by the
// suffix rule.
func joinHandler() func(context.Context, *StepCall) (Result, error) {
	suffix := suffixHandler()

	return func(ctx context.Context, call *StepCall) (Result, error) {
		if call.StepID != "join" {
			return suffix(ctx, call)
		}

		return Success(object(portA, fmt.Sprintf("%v+%v", call.Inputs["l"], call.Inputs["r"])))
	}
}

func TestRunDiamondWorkflow(t *testing.T) {
	t.Parallel()

	runner := mustRunner(t, diamondWorkflow(), testRegistry(joinHandler()), nil)
	result := mustRun(t, runner, object("x", "seed"))

	assertDeepEqual(t, "outputs", result.Outputs, object(portFinal, "seed/fork/left+seed/fork/right"))
}

func TestRunDiamondIsDeterministic(t *testing.T) {
	t.Parallel()

	spec := diamondWorkflow()
	spec.inputs = append(spec.inputs, "xs")
	spec.steps = append(spec.steps, stepSpec{
		name:    "spread",
		in:      []inSpec{{name: portIn, sources: []string{"xs"}}},
		out:     []string{portA},
		scatter: []string{portIn},
	})
	spec.outputs = append(spec.outputs, outSpec{name: "spread", sources: []string{"spread/" + portA}})

	runner := mustRunner(t, spec, testRegistry(joinHandler()), nil)
	inputs := object("x", "seed", "xs", list("p", "q", "r"))

	first := mustRun(t, runner, inputs)
	assertDeepEqual(t, "gathered", first.Outputs["spread"], list("p/spread", "q/spread", "r/spread"))

	for range 5 {
		assertDeepEqual(t, "outputs", mustRun(t, runner, inputs).Outputs, first.Outputs)
	}
}

// bareTool is a CommandLineTool run as the top-level process, with no workflow around it.
func bareTool() *cwlcore.CommandLineTool {
	tool := &cwlcore.CommandLineTool{}
	tool.ID = "file:///tool.cwl#echo"

	input := cwlcore.CommandInputParameter{Default: mustNode("fallback")}
	input.IDField = tool.ID + "/" + portIn
	tool.Inputs = []cwlcore.CommandInputParameter{input}

	output := cwlcore.CommandOutputParameter{}
	output.IDField = tool.ID + "/" + portA
	tool.Outputs = []cwlcore.CommandOutputParameter{output}

	return tool
}

func TestRunBareCommandLineTool(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	registry.Register(Class(cwlcore.ClassCommandLineTool), HandlerFunc(suffixHandler()))

	runner, err := NewRunner(t.Context(), bareTool(), registry, nil)
	if err != nil {
		t.Fatalf("NewRunner: unexpected error: %v", err)
	}

	assertDeepEqual(t, "supplied", mustRun(t, runner, object(portIn, "given")).Outputs, object(portA, "given/echo"))
	assertDeepEqual(t, "defaulted", mustRun(t, runner, nil).Outputs, object(portA, "fallback/echo"))
}

func TestNewRunnerRejectsNoProcess(t *testing.T) {
	t.Parallel()

	_, err := NewRunner(t.Context(), nil, NewRegistry(), nil)
	assertErrorIs(t, "NewRunner(nil)", err, ErrNoProcess)
}

func TestNewRunnerFailsClosedOnUnregisteredClass(t *testing.T) {
	t.Parallel()

	extension := &cwlcore.RawProcess{ClassIRI: "ex:Custom"}
	extension.ID = "file:///w.cwl#custom"

	_, err := NewRunner(t.Context(), extension, NewRegistry(), nil)
	assertErrorIs(t, "NewRunner", err, ErrNoHandler)
}

func TestRunSurfacesTemporaryFailWithoutRetrying(t *testing.T) {
	t.Parallel()

	calls := 0
	registry := testRegistry(func(_ context.Context, _ *StepCall) (Result, error) {
		calls++

		return TemporaryFail(errBoom)
	})

	runner := mustRunner(t, linearWorkflow(), registry, nil)

	result, err := runner.Run(t.Context(), object("x", "seed"))
	assertErrorIs(t, "Run", err, errBoom)
	assertInt(t, "handler calls", calls, 1)

	if result.Status != StatusTemporaryFail {
		t.Fatalf("status = %q, want %q", result.Status, StatusTemporaryFail)
	}

	if result.Outputs != nil {
		t.Fatalf("outputs = %v, want nil for a failed run", result.Outputs)
	}
}

func TestRunPrefersPermanentFailOverTemporary(t *testing.T) {
	t.Parallel()

	spec := wfSpec{
		inputs: []string{"x"},
		steps: []stepSpec{
			{name: "temp", in: []inSpec{{name: portIn, sources: []string{"x"}}}, out: []string{portA}},
			{name: "perm", in: []inSpec{{name: portIn, sources: []string{"x"}}}, out: []string{portA}},
		},
	}

	registry := testRegistry(func(_ context.Context, call *StepCall) (Result, error) {
		if call.StepID == "temp" {
			return TemporaryFail(errBoom)
		}

		return PermanentFail(errPermanent)
	})

	cfg := &Config{OnError: OnErrorContinue}

	result, err := mustRunner(t, &spec, registry, cfg).Run(t.Context(), object("x", 1))
	assertErrorIs(t, "Run", err, errPermanent)

	if result.Status != StatusPermanentFail {
		t.Fatalf("status = %q, want %q", result.Status, StatusPermanentFail)
	}
}

// errPermanent is the permanent failure the outcome-ranking fixture reports.
var errPermanent = errors.New("permanent failure")

// failThenChain is a workflow whose first step fails and whose second, independent chain does not
// depend on it at all.
func failThenChain() *wfSpec {
	return &wfSpec{
		inputs: []string{"x"},
		steps: []stepSpec{
			{name: stepBad, in: []inSpec{{name: portIn, sources: []string{"x"}}}, out: []string{portA}},
			{name: "t1", in: []inSpec{{name: portIn, sources: []string{"x"}}}, out: []string{portA}},
			{name: "t2", in: []inSpec{{name: portIn, sources: []string{"t1/" + portA}}}, out: []string{portA}},
		},
	}
}

// recordingRegistry answers with the suffix rule but fails the named step, recording every step it
// was asked to run.
func recordingRegistry(fail string, seen *[]string) *Registry {
	suffix := suffixHandler()

	return testRegistry(func(ctx context.Context, call *StepCall) (Result, error) {
		*seen = append(*seen, call.StepID)

		if call.StepID == fail {
			return PermanentFail(errBoom)
		}

		return suffix(ctx, call)
	})
}

func TestRunOnErrorStopStartsNothingFurther(t *testing.T) {
	t.Parallel()

	seen := make([]string, 0)
	cfg := &Config{MaxParallel: 1}

	runner := mustRunner(t, failThenChain(), recordingRegistry(stepBad, &seen), cfg)

	_, err := runner.Run(t.Context(), object("x", "seed"))
	assertErrorIs(t, "Run", err, errBoom)
	assertDeepEqual(t, "steps executed", seen, []string{stepBad})
}

func TestRunOnErrorContinueRunsIndependentBranches(t *testing.T) {
	t.Parallel()

	seen := make([]string, 0)
	cfg := &Config{MaxParallel: 1, OnError: OnErrorContinue}

	runner := mustRunner(t, failThenChain(), recordingRegistry(stepBad, &seen), cfg)

	result, err := runner.Run(t.Context(), object("x", "seed"))
	assertErrorIs(t, "Run", err, errBoom)
	assertDeepEqual(t, "steps executed", seen, []string{stepBad, "t1", "t2"})

	if result.Status != StatusPermanentFail {
		t.Fatalf("status = %q, want %q", result.Status, StatusPermanentFail)
	}
}

func TestRunProjectsStepOutputsOntoDeclaredPorts(t *testing.T) {
	t.Parallel()

	spec := wfSpec{
		inputs: []string{"x"},
		steps: []stepSpec{{
			name:   "s1",
			in:     []inSpec{{name: portIn, sources: []string{"x"}}},
			out:    []string{portA},
			runOut: []string{portA, portB},
		}},
		outputs: []outSpec{{name: portFinal, sources: []string{"s1/" + portA}}},
	}

	registry := testRegistry(constOutputs(object(portA, 1, portB, 2)))
	result := mustRun(t, mustRunner(t, &spec, registry, nil), object("x", 1))

	assertDeepEqual(t, "outputs", result.Outputs, object(portFinal, 1))
}

func TestRunFillsDeclaredPortTheHandlerOmitted(t *testing.T) {
	t.Parallel()

	spec := wfSpec{
		inputs: []string{"x"},
		steps: []stepSpec{
			{name: "s1", in: []inSpec{{name: portIn, sources: []string{"x"}}}, out: []string{portA, portB}},
		},
		outputs: []outSpec{
			{name: "one", sources: []string{"s1/" + portA}},
			{name: valueTwo, sources: []string{"s1/" + portB}},
		},
	}

	registry := testRegistry(constOutputs(object(portA, "only")))
	result := mustRun(t, mustRunner(t, &spec, registry, nil), object("x", 1))

	assertDeepEqual(t, "outputs", result.Outputs, object("one", "only", valueTwo, nil))
}
