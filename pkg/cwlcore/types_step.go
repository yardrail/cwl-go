package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// WorkflowStep is one node in a Workflow's dependency graph.
type WorkflowStep struct {
	Run           StepRun
	ID            string
	Label         string
	When          Expression
	ScatterMethod ScatterMethod
	Doc           []string
	In            []WorkflowStepInput
	Out           []WorkflowStepOutput
	Requirements  []ProcessRequirement
	Hints         []Hint
	Scatter       []string
}

// StepRun is the `string | Process` union for a step's run field.
type StepRun struct {
	Process Process
	Ref     string
}

// IsRef reports whether the step references a process rather than embedding one.
func (r StepRun) IsRef() bool {
	return r.Ref != ""
}

// WorkflowStepInput wires one input of a step's process.
type WorkflowStepInput struct {
	Default      salad.Node
	ID           string
	Label        string
	ValueFrom    Expression
	LinkMerge    LinkMergeMethod
	PickValue    PickValueMethod
	LoadListing  LoadListingEnum
	Source       []string
	LoadContents bool
}

// WorkflowStepOutput names one output of a step's process.
type WorkflowStepOutput struct {
	ID string
}
