package cwlexec

import (
	"errors"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// valueFromConst is a valueFrom expression with no references in it, for the rows that only care
// that a valueFrom is present at all.
const valueFromConst = "a constant"

// planError builds a runner over spec and reports the error the plan was rejected with.
func planError(t *testing.T, spec *wfSpec) error {
	t.Helper()

	_, err := NewRunner(t.Context(), buildWorkflow(spec), testRegistry(constOutputs(nil)), nil)
	if err == nil {
		t.Fatal("NewRunner: want an error, got none")
	}

	return err
}

func TestNewRunnerRejectsAnUnresolvedRunReference(t *testing.T) {
	t.Parallel()

	workflow := buildWorkflow(&wfSpec{steps: []stepSpec{{name: "s1", out: []string{portA}}}})
	workflow.Steps[0].Run = cwlcore.StepRun{Ref: "file:///elsewhere.cwl#tool"}

	_, err := NewRunner(t.Context(), workflow, testRegistry(constOutputs(nil)), nil)
	assertErrorIs(t, "NewRunner", err, ErrUnresolvedRun)
}

func TestNewRunnerRejectsUnknownSources(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		spec wfSpec
	}{
		{
			name: "step input",
			spec: wfSpec{steps: []stepSpec{
				{name: "s1", in: []inSpec{{name: portIn, sources: []string{"ghost"}}}, out: []string{portA}},
			}},
		},
		{
			name: "workflow output",
			spec: wfSpec{
				steps:   []stepSpec{{name: "s1", out: []string{portA}}},
				outputs: []outSpec{{name: portFinal, sources: []string{"s1/ghost"}}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assertErrorIs(t, "NewRunner", planError(t, &tc.spec), ErrUnknownSource)
		})
	}
}

func TestNewRunnerRejectsDuplicateStepIdentifiers(t *testing.T) {
	t.Parallel()

	workflow := buildWorkflow(&wfSpec{steps: []stepSpec{
		{name: "s1", out: []string{portA}},
		{name: "s2", out: []string{portA}},
	}})
	workflow.Steps[1].ID = "file:///other.cwl#other/s1"

	_, err := NewRunner(t.Context(), workflow, testRegistry(constOutputs(nil)), nil)
	assertErrorIs(t, "NewRunner", err, ErrDuplicateStep)
}

func TestNewRunnerRejectsACycle(t *testing.T) {
	t.Parallel()

	spec := wfSpec{steps: []stepSpec{
		{name: "s1", in: []inSpec{{name: portIn, sources: []string{"s2/" + portA}}}, out: []string{portA}},
		{name: "s2", in: []inSpec{{name: portIn, sources: []string{"s1/" + portA}}}, out: []string{portA}},
	}}

	assertErrorIs(t, "NewRunner", planError(t, &spec), ErrCycle)
}

func TestNewRunnerRejectsAFeatureWithoutItsRequirement(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		spec wfSpec
	}{
		{
			name: "scatter",
			spec: wfSpec{noFeatures: true, inputs: []string{"xs"}, steps: []stepSpec{{
				name:    stepFan,
				in:      []inSpec{{name: portIn, sources: []string{"xs"}}},
				out:     []string{portA},
				scatter: []string{portIn},
			}}},
		},
		{
			name: "several sources on a step input",
			spec: wfSpec{noFeatures: true, inputs: []string{"x", "y"}, steps: []stepSpec{{
				name: "s1",
				in:   []inSpec{{name: portIn, sources: []string{"x", "y"}}},
				out:  []string{portA},
			}}},
		},
		{
			name: "valueFrom",
			spec: wfSpec{noFeatures: true, inputs: []string{"x"}, steps: []stepSpec{{
				name: "s1",
				in:   []inSpec{{name: portIn, sources: []string{"x"}, valueFrom: valueFromConst}},
				out:  []string{portA},
			}}},
		},
		{
			name: "several sources on a workflow output",
			spec: wfSpec{
				noFeatures: true,
				steps: []stepSpec{
					{name: "s1", out: []string{portA}},
					{name: "s2", out: []string{portA}},
				},
				outputs: []outSpec{{name: portFinal, sources: []string{"s1/" + portA, "s2/" + portA}}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assertErrorIs(t, "NewRunner", planError(t, &tc.spec), ErrRequirementNotInScope)
		})
	}
}

// stepTool builds a CommandLineTool for a workflow step to run, with the named input and output
// ports. It matters that it is a CommandLineTool and not the Operation the other fixtures use: a
// CommandLineTool is the one process class the specification restricts inheritance for, and so the
// only one that can expose a scope built the wrong way round.
func stepTool(step string, ins, outs []string) *cwlcore.CommandLineTool {
	tool := &cwlcore.CommandLineTool{}
	tool.ID = wfID(step) + "/run"

	tool.Inputs = make([]cwlcore.CommandInputParameter, 0, len(ins))
	for _, name := range ins {
		param := cwlcore.CommandInputParameter{}
		param.IDField = tool.ID + "/" + name
		tool.Inputs = append(tool.Inputs, param)
	}

	tool.Outputs = make([]cwlcore.CommandOutputParameter, 0, len(outs))
	for _, name := range outs {
		param := cwlcore.CommandOutputParameter{}
		param.IDField = tool.ID + "/" + name
		tool.Outputs = append(tool.Outputs, param)
	}

	return tool
}

// TestNewRunnerAcceptsAWorkflowFeatureAroundACommandLineToolStep pins the two-scope split in
// planStep.
//
// A CommandLineTool inherits none of the four workflow-feature requirements, so a scope with the
// tool pushed onto it has had every one of them filtered out of the enclosing workflow's frame.
// Checking the step's own features against that scope would report the workflow as never having
// declared them — which is every scattered tool step in the conformance suite, and most conditional
// ones. The step's features are therefore checked against the step's scope instead.
func TestNewRunnerAcceptsAWorkflowFeatureAroundACommandLineToolStep(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		spec wfSpec
	}{
		{
			name: "scatter",
			spec: wfSpec{inputs: []string{"xs"}, steps: []stepSpec{{
				name:    stepFan,
				run:     stepTool(stepFan, []string{portIn}, []string{portA}),
				in:      []inSpec{{name: portIn, sources: []string{"xs"}}},
				out:     []string{portA},
				scatter: []string{portIn},
			}}},
		},
		{
			name: "several sources on a step input",
			spec: wfSpec{inputs: []string{"x", "y"}, steps: []stepSpec{{
				name: "s1",
				run:  stepTool("s1", []string{portIn}, []string{portA}),
				in:   []inSpec{{name: portIn, sources: []string{"x", "y"}}},
				out:  []string{portA},
			}}},
		},
		{
			name: "valueFrom on a step input",
			spec: wfSpec{inputs: []string{"x"}, steps: []stepSpec{{
				name: "s1",
				run:  stepTool("s1", []string{portIn}, []string{portA}),
				in:   []inSpec{{name: portIn, sources: []string{"x"}, valueFrom: valueFromConst}},
				out:  []string{portA},
			}}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewRunner(t.Context(), buildWorkflow(&tc.spec), NewRegistry(), nil)
			if err != nil {
				t.Fatalf("NewRunner: unexpected error: %v", err)
			}
		})
	}
}

// TestNewRunnerAcceptsAStepLevelFeatureRequirementOnACommandLineToolStep covers the other frame the
// step's scope carries: a requirement the step declares on itself rather than inheriting.
func TestNewRunnerAcceptsAStepLevelFeatureRequirementOnACommandLineToolStep(t *testing.T) {
	t.Parallel()

	spec := wfSpec{
		noFeatures: true,
		inputs:     []string{"xs"},
		steps: []stepSpec{{
			name:    stepFan,
			run:     stepTool(stepFan, []string{portIn}, []string{portA}),
			in:      []inSpec{{name: portIn, sources: []string{"xs"}}},
			out:     []string{portA},
			scatter: []string{portIn},
			reqs:    []cwlcore.ProcessRequirement{&cwlcore.ScatterFeatureRequirement{}},
		}},
	}

	_, err := NewRunner(t.Context(), buildWorkflow(&spec), NewRegistry(), nil)
	if err != nil {
		t.Fatalf("NewRunner: unexpected error: %v", err)
	}
}

// TestNewRunnerStillRejectsAnUndeclaredFeatureOnACommandLineToolStep keeps the gate fail-closed:
// widening the scope the step is checked against must not turn the check off.
func TestNewRunnerStillRejectsAnUndeclaredFeatureOnACommandLineToolStep(t *testing.T) {
	t.Parallel()

	spec := wfSpec{
		noFeatures: true,
		inputs:     []string{"xs"},
		steps: []stepSpec{{
			name:    stepFan,
			run:     stepTool(stepFan, []string{portIn}, []string{portA}),
			in:      []inSpec{{name: portIn, sources: []string{"xs"}}},
			out:     []string{portA},
			scatter: []string{portIn},
		}},
	}

	_, err := NewRunner(t.Context(), buildWorkflow(&spec), NewRegistry(), nil)
	assertErrorIs(t, "NewRunner", err, ErrRequirementNotInScope)
}

func TestNewRunnerRejectsASubworkflowWithoutItsRequirement(t *testing.T) {
	t.Parallel()

	inner := &cwlcore.Workflow{}
	inner.ID = "file:///inner.cwl#inner"

	spec := wfSpec{noFeatures: true, steps: []stepSpec{{name: "s1", run: inner, out: make([]string, 0)}}}

	assertErrorIs(t, "NewRunner", planError(t, &spec), ErrRequirementNotInScope)
}

// unknownRequirement is an extension requirement class this engine cannot honour.
func unknownRequirement() []cwlcore.ProcessRequirement {
	return []cwlcore.ProcessRequirement{&cwlcore.RawRequirement{ClassIRI: "ex:Telepathy"}}
}

func TestNewRunnerFailsClosedOnAnUnknownRequirement(t *testing.T) {
	t.Parallel()

	spec := wfSpec{steps: []stepSpec{{name: "s1", out: []string{portA}, reqs: unknownRequirement()}}}

	err := planError(t, &spec)
	if err == nil {
		t.Fatal("NewRunner: want an error naming the unrecognized requirement")
	}

	var reported *salad.Error
	if !errors.As(err, &reported) {
		t.Fatalf("NewRunner error = %v, want a salad.Error naming the class", err)
	}

	if !strings.Contains(reported.Error(), "ex:Telepathy") {
		t.Fatalf("NewRunner error = %q, want it to name the offending class", reported.Error())
	}
}

func TestNewRunnerHonoursTheLenientOverride(t *testing.T) {
	t.Parallel()

	spec := wfSpec{
		steps:   []stepSpec{{name: "s1", out: []string{portA}, reqs: unknownRequirement()}},
		outputs: []outSpec{{name: portFinal, sources: []string{"s1/" + portA}}},
	}

	cfg := &Config{LenientRequirements: true}
	runner := mustRunner(t, &spec, testRegistry(constOutputs(object(portA, "ok"))), cfg)

	assertDeepEqual(t, "outputs", mustRun(t, runner, nil).Outputs, object(portFinal, "ok"))
}

func TestNewRunnerAcceptsAVouchedForExtensionRequirement(t *testing.T) {
	t.Parallel()

	spec := wfSpec{
		steps:   []stepSpec{{name: "s1", out: []string{portA}, reqs: unknownRequirement()}},
		outputs: []outSpec{{name: portFinal, sources: []string{"s1/" + portA}}},
	}

	cfg := &Config{AllowRequirements: map[string]bool{"ex:Telepathy": true}}
	runner := mustRunner(t, &spec, testRegistry(constOutputs(object(portA, "ok"))), cfg)

	assertDeepEqual(t, "outputs", mustRun(t, runner, nil).Outputs, object(portFinal, "ok"))
}

func TestBareProcessPlanFailsClosedOnAnUnknownRequirement(t *testing.T) {
	t.Parallel()

	tool := bareTool()
	tool.Requirements = unknownRequirement()

	_, err := NewRunner(t.Context(), tool, NewRegistry(), nil)
	if err == nil {
		t.Fatal("NewRunner: want an error for a bare process with an unrecognized requirement")
	}
}

func TestProcessStepID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		id   string
		want string
	}{
		{name: "named process", id: "file:///t.cwl#echo", want: "echo"},
		{name: "no identifier", id: "", want: implicitStepID},
		{name: "blank node", id: "_:6f1c", want: implicitStepID},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			process := &cwlcore.Operation{}
			process.ID = tc.id

			assertDeepEqual(t, "step id", processStepID(process), tc.want)
		})
	}
}

func TestPortDeclsOfANilProcessAreEmpty(t *testing.T) {
	t.Parallel()

	assertInt(t, "inputs", len(inputDecls(nil)), 0)
	assertInt(t, "outputs", len(outputDecls(nil)), 0)
}

func TestPortDeclsCoverEveryProcessKind(t *testing.T) {
	t.Parallel()

	tool := bareTool()
	expression := newExpressionTool("$({})", outID)
	expression.Inputs = []cwlcore.WorkflowInputParameter{{}}
	raw := &cwlcore.RawProcess{ClassIRI: "ex:Custom"}
	raw.Inputs = []cwlcore.OperationInputParameter{{}}
	raw.Outputs = []cwlcore.OperationOutputParameter{{}}

	processes := []cwlcore.Process{
		tool, buildWorkflow(&wfSpec{
			inputs:  []string{"x"},
			outputs: []outSpec{{name: "y"}},
		}), expression, raw,
		newOperation("file:///o.cwl#op", []string{portIn}, []string{portA}, nil),
	}

	for _, process := range processes {
		if len(inputDecls(process)) == 0 || len(outputDecls(process)) == 0 {
			t.Fatalf("%T: inputs %v, outputs %v, want both non-empty",
				process, inputDecls(process), outputDecls(process))
		}
	}
}
