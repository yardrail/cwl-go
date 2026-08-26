package cwlexec

import (
	"context"
	"maps"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// The identifiers the workflow fixtures resolve against, spelled the way decoding resolves them.
const (
	wfURI = "file:///wf.cwl#wf"

	// fakeClass is the process class every fixture step runs. Operation is used because it is the
	// simplest process the model has — declared inputs and outputs and nothing else — and because
	// registering over its built-in handler exercises the same override path a downstream engine
	// uses for its own classes.
	fakeClass = Class(cwlcore.ClassOperation)

	// portA and portB are the output ports fixture steps publish.
	portA = "a"
	portB = "b"

	// stepFan, stepLeft and stepRight are step names several fixtures share.
	stepFan   = "fan"
	stepLeft  = "left"
	stepRight = "right"

	// portFinal is the workflow output port the fixtures publish their result on.
	portFinal = "final"

	// stepBad is the step name the failure fixtures give the step that fails.
	stepBad = "bad"

	// valueTwo and valueFallback are values the wiring fixtures thread through the graph.
	valueTwo      = "two"
	valueFallback = "fallback"

	// flagsInput is the array-of-booleans workflow input the per-element gate fixtures scatter over.
	flagsInput = "flags"

	// portSeen is the workflow output port that reports what a downstream step observed.
	portSeen = "seen"

	// notANumber is a value the resource fixtures feed to an expression that must yield a number.
	notANumber = "not a number"
)

// wfID renders a workflow-local name as the absolute identifier decoding would have produced.
func wfID(name string) string {
	return wfURI + "/" + name
}

// inSpec describes one step input wiring.
type inSpec struct {
	def       any
	name      string
	valueFrom string
	linkMerge cwlcore.LinkMergeMethod
	pickValue cwlcore.PickValueMethod
	sources   []string
}

// outSpec describes one workflow output wiring.
type outSpec struct {
	name      string
	linkMerge cwlcore.LinkMergeMethod
	pickValue cwlcore.PickValueMethod
	sources   []string
}

// stepSpec describes one workflow step and the process under its run.
type stepSpec struct {
	outTypes map[string]cwlcore.TypeRef
	name     string
	when     string
	method   cwlcore.ScatterMethod
	run      cwlcore.Process
	in       []inSpec
	out      []string
	runOut   []string
	scatter  []string
	reqs     []cwlcore.ProcessRequirement
}

// wfSpec describes a whole workflow fixture.
type wfSpec struct {
	inputs     []string
	steps      []stepSpec
	outputs    []outSpec
	noFeatures bool
}

// featureRequirements is the set of feature requirements the fixtures declare by default, so that a
// test exercising scatter or valueFrom does not have to restate the gate it is not testing.
func featureRequirements() []cwlcore.ProcessRequirement {
	return []cwlcore.ProcessRequirement{
		&cwlcore.InlineJavascriptRequirement{},
		&cwlcore.ScatterFeatureRequirement{},
		&cwlcore.MultipleInputFeatureRequirement{},
		&cwlcore.StepInputExpressionRequirement{},
		&cwlcore.SubworkflowFeatureRequirement{},
	}
}

// buildWorkflow assembles a Workflow from its specification.
func buildWorkflow(spec *wfSpec) *cwlcore.Workflow {
	workflow := &cwlcore.Workflow{}
	workflow.ID = wfURI

	if !spec.noFeatures {
		workflow.Requirements = featureRequirements()
	}

	workflow.Inputs = make([]cwlcore.WorkflowInputParameter, 0, len(spec.inputs))

	for _, name := range spec.inputs {
		param := cwlcore.WorkflowInputParameter{}
		param.IDField = wfID(name)
		workflow.Inputs = append(workflow.Inputs, param)
	}

	workflow.Steps = make([]cwlcore.WorkflowStep, 0, len(spec.steps))
	for index := range spec.steps {
		workflow.Steps = append(workflow.Steps, buildStep(&spec.steps[index]))
	}

	workflow.Outputs = buildWorkflowOutputs(spec.outputs)

	return workflow
}

// buildWorkflowOutputs assembles the workflow's own output parameters.
func buildWorkflowOutputs(specs []outSpec) []cwlcore.WorkflowOutputParameter {
	outputs := make([]cwlcore.WorkflowOutputParameter, 0, len(specs))

	for _, spec := range specs {
		param := cwlcore.WorkflowOutputParameter{
			OutputSource: absoluteIDs(spec.sources),
			LinkMerge:    spec.linkMerge,
			PickValue:    spec.pickValue,
		}
		param.IDField = wfID(spec.name)
		outputs = append(outputs, param)
	}

	return outputs
}

// buildStep assembles one workflow step and the process under its run.
func buildStep(spec *stepSpec) cwlcore.WorkflowStep {
	scatter := make([]string, 0, len(spec.scatter))
	for _, key := range spec.scatter {
		scatter = append(scatter, wfID(spec.name+"/"+key))
	}

	return cwlcore.WorkflowStep{
		Run:           cwlcore.StepRun{Process: buildRunProcess(spec)},
		ID:            wfID(spec.name),
		When:          cwlcore.Expression(spec.when),
		ScatterMethod: spec.method,
		In:            buildStepInputs(spec),
		Out:           buildStepOutputs(spec),
		Requirements:  spec.reqs,
		Scatter:       scatter,
	}
}

// buildStepInputs assembles a step's input wirings.
func buildStepInputs(spec *stepSpec) []cwlcore.WorkflowStepInput {
	ins := make([]cwlcore.WorkflowStepInput, 0, len(spec.in))

	for _, in := range spec.in {
		ins = append(ins, cwlcore.WorkflowStepInput{
			Default:   mustNode(in.def),
			ID:        wfID(spec.name + "/" + in.name),
			ValueFrom: cwlcore.Expression(in.valueFrom),
			LinkMerge: in.linkMerge,
			PickValue: in.pickValue,
			Source:    absoluteIDs(in.sources),
		})
	}

	return ins
}

// buildStepOutputs assembles a step's out list.
func buildStepOutputs(spec *stepSpec) []cwlcore.WorkflowStepOutput {
	outs := make([]cwlcore.WorkflowStepOutput, 0, len(spec.out))
	for _, name := range spec.out {
		outs = append(outs, cwlcore.WorkflowStepOutput{ID: wfID(spec.name + "/" + name)})
	}

	return outs
}

// buildRunProcess assembles the process under a step's run, whose declared ports are the step's
// unless the specification names a different set — which is how a step that consumes a subset of
// what its tool produces is expressed.
func buildRunProcess(spec *stepSpec) cwlcore.Process {
	if spec.run != nil {
		return spec.run
	}

	ports := spec.runOut
	if ports == nil {
		ports = spec.out
	}

	names := make([]string, 0, len(spec.in))
	for _, in := range spec.in {
		names = append(names, in.name)
	}

	return newOperation(wfID(spec.name)+"/run", names, ports, spec.outTypes)
}

// newOperation builds an Operation with the named input and output ports.
func newOperation(id string, ins, outs []string, types map[string]cwlcore.TypeRef) *cwlcore.Operation {
	operation := &cwlcore.Operation{}
	operation.ID = id
	operation.Inputs = make([]cwlcore.OperationInputParameter, 0, len(ins))
	operation.Outputs = make([]cwlcore.OperationOutputParameter, 0, len(outs))

	for _, name := range ins {
		param := cwlcore.OperationInputParameter{}
		param.IDField = id + "/" + name
		operation.Inputs = append(operation.Inputs, param)
	}

	for _, name := range outs {
		param := cwlcore.OperationOutputParameter{}
		param.IDField = id + "/" + name
		param.Type = types[name]
		operation.Outputs = append(operation.Outputs, param)
	}

	return operation
}

// absoluteIDs renders workflow-local source names as the absolute identifiers decoding resolves
// them to.
func absoluteIDs(names []string) []string {
	ids := make([]string, 0, len(names))
	for _, name := range names {
		ids = append(ids, wfID(name))
	}

	return ids
}

// mustNode renders a plain Go value as the validated salad node a decoded default is kept as.
func mustNode(value any) salad.Node {
	if value == nil {
		return nil
	}

	node, err := salad.FromAny(value, salad.SourceLine{})
	if err != nil {
		panic(err)
	}

	return node
}

// testRegistry returns a registry whose Operation handler is fn, replacing the built-in that
// refuses to execute one.
func testRegistry(fn func(context.Context, *StepCall) (Result, error)) *Registry {
	registry := NewRegistry()
	registry.Register(fakeClass, HandlerFunc(fn))

	return registry
}

// constOutputs answers every call with a copy of the same output object.
func constOutputs(outputs map[string]any) func(context.Context, *StepCall) (Result, error) {
	return func(_ context.Context, _ *StepCall) (Result, error) {
		return Success(maps.Clone(outputs))
	}
}

// mustRunner builds a runner over spec, failing the test if the plan is rejected.
func mustRunner(t *testing.T, spec *wfSpec, registry *Registry, cfg *Config) *Runner {
	t.Helper()

	runner, err := NewRunner(t.Context(), buildWorkflow(spec), registry, cfg)
	if err != nil {
		t.Fatalf("NewRunner: unexpected error: %v", err)
	}

	return runner
}

// mustRun executes a runner to a terminal or suspended state, failing the test on an error.
func mustRun(t *testing.T, runner *Runner, inputs map[string]any) RunResult {
	t.Helper()

	result, err := runner.Run(t.Context(), inputs)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}

	return result
}

// object is a shorthand for an input or output object literal.
func object(pairs ...any) map[string]any {
	built := make(map[string]any, len(pairs)/2)
	for index := 0; index+1 < len(pairs); index += 2 {
		name, ok := pairs[index].(string)
		if !ok {
			panic("object: key is not a string")
		}

		built[name] = pairs[index+1]
	}

	return built
}
