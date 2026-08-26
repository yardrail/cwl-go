package main

import (
	"github.com/yardrail/cwl-go/cmd/internal/cwlcli"
	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// scopeObject dumps the requirements and hints in effect for a process.
//
// This is a different question from what the process declares, and the
// difference is the usual reason a document does not behave as written: a
// requirement declared on a workflow reaches a step's tool unless the step
// overrides it, unless the inheritance rules forbid that class reaching that
// class of process. The typed dump shows the declarations; this shows the
// answer, and where each part of it came from.
func scopeObject(p cwlcore.Process) *cwlcli.Object {
	o := cwlcli.NewObject()
	o.Set("class", p.Class())
	o.SetString("id", p.Base().ID)

	scope := cwlcore.NewScope(p)
	addScopeFields(o, scope)

	workflow, ok := p.(*cwlcore.Workflow)
	if !ok {
		return o
	}

	o.SetSlice("steps", stepScopeItems(scope, workflow.Steps))

	return o
}

// addScopeFields adds one scope's resolved requirements, hints and verdict.
func addScopeFields(o *cwlcli.Object, scope *cwlcore.RequirementScope) {
	o.Set("requirements", scopeRequirementItems(scope))
	o.SetSlice("hints", hintItems(scope.EffectiveHints()))
	o.Set("unrecognized", unrecognizedText(scope))
}

// scopeRequirementItems dumps the requirements in effect, each annotated with
// whether it was declared as a requirement or as a hint. The distinction
// decides whether the process must not run when it cannot be satisfied, so a
// dump that omitted it would be answering a different question.
func scopeRequirementItems(scope *cwlcore.RequirementScope) []any {
	effective := scope.EffectiveRequirements()
	out := make([]any, 0, len(effective))

	for _, req := range effective {
		o := requirementObject(req)

		_, _, origin := scope.GetRequirement(req.Class())
		o.SetString("origin", string(origin))

		out = append(out, o)
	}

	return out
}

// stepScopeItems dumps the scope each of a workflow's steps resolves for,
// following the chain the model prescribes: the workflow, then the step's own
// declarations, then the embedded process's.
//
// A step whose run is a reference is reported without a process scope. What it
// resolves to is not in this document, so there is nothing here to be right
// about.
func stepScopeItems(parent *cwlcore.RequirementScope, steps []cwlcore.WorkflowStep) []any {
	out := make([]any, 0, len(steps))

	for i := range steps {
		step := &steps[i]

		o := cwlcli.NewObject()
		o.SetString("id", step.ID)
		o.Set("run", runSummary(step.Run))

		scope := parent.Push(step.Requirements, step.Hints).PushProcess(step.Run.Process)
		addScopeFields(o, scope)

		out = append(out, o)
	}

	return out
}

// runSummary names a step's run target without dumping it.
//
// The scope dump answers a question about requirements, and the process a step
// runs is only relevant here for its class — which is what the inheritance
// rules key on. Dumping the process itself would bury the answer inside a copy
// of the document; the typed stage is where that belongs.
func runSummary(run cwlcore.StepRun) *cwlcli.Object {
	o := cwlcli.NewObject()
	if run.IsRef() {
		return o.Set("ref", run.Ref)
	}

	if run.Process == nil {
		return o
	}

	return o.Set("class", run.Process.Class()).SetString("id", run.Process.Base().ID)
}

// unrecognizedText reports what the specification's fail-closed gate makes of
// the scope: the first requirement class this implementation could not honour,
// or that there is none.
func unrecognizedText(scope *cwlcore.RequirementScope) string {
	err := scope.CheckKnown(nil)
	if err == nil {
		return "none"
	}

	return cwlcli.Explain(err)
}
