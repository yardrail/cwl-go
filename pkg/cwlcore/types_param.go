package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// Concrete parameter types.

// Compile-time interface assertions.
var (
	_ Parameter = (*CommandInputParameter)(nil)
	_ Parameter = (*CommandOutputParameter)(nil)
	_ Parameter = (*WorkflowInputParameter)(nil)
	_ Parameter = (*WorkflowOutputParameter)(nil)
	_ Parameter = (*OperationInputParameter)(nil)
	_ Parameter = (*OperationOutputParameter)(nil)
	_ Parameter = (*ExpressionToolOutputParameter)(nil)
)

// ParameterBase holds fields shared by all parameter types.
// IDField is used instead of ID because the ID() method satisfies [Parameter].
type ParameterBase struct {
	Node           salad.Node
	IDField        string
	Label          string
	Doc            []string
	Type           TypeRef
	SecondaryFiles []SecondaryFileSchema
	Format         []Expression
	LoadContents   bool
	LoadListing    LoadListingEnum
	Streamable     bool
}

// ID returns the parameter's resolved identifier.
func (p *ParameterBase) ID() string {
	return p.IDField
}

// isParameter seals the Parameter interface.
func (p *ParameterBase) isParameter() {}

// CommandInputParameter is an input parameter of a CommandLineTool.
type CommandInputParameter struct {
	ParameterBase

	InputBinding *CommandLineBinding
	Default      salad.Node
}

// CommandOutputParameter is an output parameter of a CommandLineTool.
type CommandOutputParameter struct {
	ParameterBase

	OutputBinding *CommandOutputBinding
}

// WorkflowInputParameter is an input parameter of a Workflow or ExpressionTool.
type WorkflowInputParameter struct {
	ParameterBase

	InputBinding *InputBinding
	Default      salad.Node
}

// WorkflowOutputParameter is an output parameter of a Workflow.
type WorkflowOutputParameter struct {
	ParameterBase

	OutputSource []string
	LinkMerge    LinkMergeMethod
	PickValue    PickValueMethod
}

// OperationInputParameter is an input parameter of an Operation or RawProcess.
type OperationInputParameter struct {
	ParameterBase

	Default salad.Node
}

// OperationOutputParameter is an output parameter of an Operation or RawProcess.
type OperationOutputParameter struct {
	ParameterBase
}

// ExpressionToolOutputParameter is an output parameter of an ExpressionTool.
type ExpressionToolOutputParameter struct {
	ParameterBase
}
