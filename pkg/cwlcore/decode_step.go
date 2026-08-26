package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// Workflow steps: the node, the process it runs, and the edges wiring it into
// the rest of the graph.

// Keys a WorkflowStep and its sinks add to the shared field set.
const (
	keyIn            = "in"
	keyOut           = "out"
	keyRun           = "run"
	keyWhen          = "when"
	keyScatter       = "scatter"
	keyScatterMethod = "scatterMethod"
	keySource        = "source"
)

// workflowStep decodes one step of a Workflow.
func (d *decoder) workflowStep(node salad.Node) WorkflowStep {
	m := d.mapping(node, "a workflow step")

	return WorkflowStep{
		Run:           d.stepRun(m),
		ID:            d.text(m, keyID),
		Label:         d.text(m, keyLabel),
		When:          d.expression(m, keyWhen),
		ScatterMethod: ScatterMethod(shortName(d.text(m, keyScatterMethod))),
		Doc:           d.textList(m, keyDoc),
		In:            decodeEach(d.listItems(m, keyIn, keyID, keySource), d.workflowStepInput),
		Out:           decodeEach(d.listItems(m, keyOut, "", ""), d.workflowStepOutput),
		Requirements:  d.requirements(m, keyRequirements),
		Hints:         d.hints(m, keyHints),
		Scatter:       d.textList(m, keyScatter),
	}
}

// stepRun decodes a step's run field, whose schema type is `string | Process`: a
// reference to a process defined elsewhere, or one embedded inline.
func (d *decoder) stepRun(m *salad.MapNode) StepRun {
	value := fieldNode(m, keyRun)
	if value == nil {
		d.missingField(m, keyRun, "a workflow step")

		return StepRun{}
	}

	if ref, ok := salad.AsString(value); ok {
		return StepRun{Ref: ref}
	}

	return StepRun{Process: d.process(value)}
}

// workflowStepInput decodes one input wiring of a step.
func (d *decoder) workflowStepInput(node salad.Node) WorkflowStepInput {
	m := d.mapping(node, "a workflow step input")

	return WorkflowStepInput{
		Default:      fieldNode(m, keyDefault),
		ID:           d.text(m, keyID),
		Label:        d.text(m, keyLabel),
		ValueFrom:    d.expression(m, keyValueFrom),
		LinkMerge:    LinkMergeMethod(shortName(d.text(m, keyLinkMerge))),
		PickValue:    PickValueMethod(shortName(d.text(m, keyPickValue))),
		LoadListing:  LoadListingEnum(d.text(m, keyLoadListing)),
		Source:       d.textList(m, keySource),
		LoadContents: d.flag(m, keyLoadContents),
	}
}

// workflowStepOutput decodes one entry of a step's out list. The schema permits
// a bare identifier string as shorthand for the record, and both forms normalize
// into the same struct.
func (d *decoder) workflowStepOutput(node salad.Node) WorkflowStepOutput {
	if id, ok := salad.AsString(node); ok {
		return WorkflowStepOutput{ID: id}
	}

	m := d.mapping(node, "a workflow step output")

	return WorkflowStepOutput{ID: d.text(m, keyID)}
}
