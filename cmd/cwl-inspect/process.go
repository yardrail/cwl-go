package main

import (
	"github.com/yardrail/cwl-go/cmd/internal/cwlcli"
	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// processObject dumps a decoded process.
//
// The dump is a deliberate projection, not a reflection of the Go struct.
// Reflecting the struct would be both less and more than what a debugger
// wants: less, because the model's opaque value types (TypeRef, ExprLong,
// OptBool) carry unexported fields and would come out empty; and more, because
// every parameter carries the salad node it was decoded from, which would
// repeat the whole document inside its own decoded form. Everything here is
// rendered through the accessors the model provides for exactly this purpose.
func processObject(p cwlcore.Process) *cwlcli.Object {
	base := p.Base()

	o := cwlcli.NewObject()
	o.Set("class", p.Class())
	o.SetString("id", base.ID)
	o.SetString("label", base.Label)
	o.SetString("cwlVersion", base.CWLVersion)
	o.SetSlice("doc", stringItems(base.Doc))
	o.SetSlice("intent", stringItems(base.Intent))

	addBody(o, p)

	o.SetSlice("requirements", requirementItems(base.Requirements))
	o.SetSlice("hints", hintItems(base.Hints))

	return o
}

// addBody adds the fields that belong to one process class.
//
// The type switch is exhaustive over a sealed interface, so the compiler's
// case list is the specification's class list; a process kind added later
// shows up here as a missing case rather than as a silently thinner dump.
func addBody(o *cwlcli.Object, p cwlcore.Process) {
	switch t := p.(type) {
	case *cwlcore.CommandLineTool:
		addCommandLineToolBody(o, t)
	case *cwlcore.Workflow:
		addWorkflowBody(o, t)
	case *cwlcore.ExpressionTool:
		o.SetString("expression", string(t.Expression))
		o.Set("inputs", workflowInputItems(t.Inputs))
		o.Set("outputs", expressionToolOutputItems(t.Outputs))
	case *cwlcore.Operation:
		o.Set("inputs", operationInputItems(t.Inputs))
		o.Set("outputs", operationOutputItems(t.Outputs))
	case *cwlcore.RawProcess:
		o.SetString("classIRI", t.ClassIRI)
		o.Set("inputs", operationInputItems(t.Inputs))
		o.Set("outputs", operationOutputItems(t.Outputs))
	default:
		// Unreachable: Process is sealed by an unexported method, so
		// the cases above are the whole of it. A process kind added
		// later lands here, and the shared fields are still dumped.
	}
}

// addCommandLineToolBody adds the fields specific to a CommandLineTool.
func addCommandLineToolBody(o *cwlcli.Object, t *cwlcore.CommandLineTool) {
	o.SetSlice("baseCommand", stringItems(t.BaseCommand))
	o.SetSlice("arguments", argumentItems(t.Arguments))
	o.SetString("stdin", string(t.Stdin))
	o.SetString("stdout", string(t.Stdout))
	o.SetString("stderr", string(t.Stderr))
	o.SetSlice("successCodes", intItems(t.SuccessCodes))
	o.SetSlice("temporaryFailCodes", intItems(t.TemporaryFailCodes))
	o.SetSlice("permanentFailCodes", intItems(t.PermanentFailCodes))
	o.Set("inputs", commandInputItems(t.Inputs))
	o.Set("outputs", commandOutputItems(t.Outputs))
}

// addWorkflowBody adds the fields specific to a Workflow.
func addWorkflowBody(o *cwlcli.Object, w *cwlcore.Workflow) {
	o.Set("inputs", workflowInputItems(w.Inputs))
	o.Set("outputs", workflowOutputItems(w.Outputs))
	o.Set("steps", stepItems(w.Steps))
}

// stepItems dumps a workflow's steps in document order, which is also the
// order the scheduler reads them in.
func stepItems(steps []cwlcore.WorkflowStep) []any {
	out := make([]any, 0, len(steps))

	for i := range steps {
		step := &steps[i]

		o := cwlcli.NewObject()
		o.SetString("id", step.ID)
		o.SetString("label", step.Label)
		o.SetSlice("doc", stringItems(step.Doc))
		o.Set("run", runObject(step.Run))
		o.Set("in", stepInputItems(step.In))
		o.Set("out", stepOutputItems(step.Out))
		o.SetSlice("scatter", stringItems(step.Scatter))
		o.SetString("scatterMethod", string(step.ScatterMethod))
		o.SetString("when", string(step.When))
		o.SetSlice("requirements", requirementItems(step.Requirements))
		o.SetSlice("hints", hintItems(step.Hints))

		out = append(out, o)
	}

	return out
}

// runObject dumps a step's run target, distinguishing a reference to a process
// defined elsewhere from a process embedded inline. Which of the two it is
// decides whether the step's requirements are inherited by anything visible in
// this document, so the dump says so rather than making the reader infer it.
func runObject(run cwlcore.StepRun) *cwlcli.Object {
	o := cwlcli.NewObject()
	if run.IsRef() {
		return o.Set("ref", run.Ref)
	}

	if run.Process == nil {
		return o
	}

	return o.Set("embedded", processObject(run.Process))
}

// stepInputItems dumps a step's input wirings in document order.
func stepInputItems(inputs []cwlcore.WorkflowStepInput) []any {
	out := make([]any, 0, len(inputs))

	for i := range inputs {
		in := &inputs[i]

		o := cwlcli.NewObject()
		o.SetString("id", in.ID)
		o.SetString("label", in.Label)
		o.SetSlice("source", stringItems(in.Source))
		o.SetString("linkMerge", string(in.LinkMerge))
		o.SetString("pickValue", string(in.PickValue))
		o.SetString("valueFrom", string(in.ValueFrom))

		if in.LoadContents {
			o.Set("loadContents", true)
		}

		o.SetString("loadListing", string(in.LoadListing))
		addDefault(o, in.Default)

		out = append(out, o)
	}

	return out
}

// stepOutputItems dumps the step outputs the workflow consumes.
func stepOutputItems(outputs []cwlcore.WorkflowStepOutput) []any {
	out := make([]any, 0, len(outputs))
	for _, o := range outputs {
		out = append(out, o.ID)
	}

	return out
}
