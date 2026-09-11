package main

import (
	"github.com/yardrail/cwl-go/cmd/internal/cwlcli"
	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// scopeObject dumps the resolved requirements and hints in effect for a process.
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

// scopeRequirementItems dumps effective requirements with their origin.
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

// stepScopeItems dumps the resolved scope for each workflow step.
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

// runSummary names a step's run target without dumping the full process.
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

// unrecognizedText reports the first unrecognized requirement class, or "none".
func unrecognizedText(scope *cwlcore.RequirementScope) string {
	err := scope.CheckKnown(nil)
	if err == nil {
		return "none"
	}

	return cwlcli.Explain(err)
}
