package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// Process implementations.

// Compile-time interface assertions.
var (
	_ Process = (*CommandLineTool)(nil)
	_ Process = (*Workflow)(nil)
	_ Process = (*ExpressionTool)(nil)
	_ Process = (*Operation)(nil)
	_ Process = (*RawProcess)(nil)
	_ Process = (*ExtensionWorkflow)(nil)
)

var (
	_ StepContainer = (*Workflow)(nil)
	_ StepContainer = (*ExtensionWorkflow)(nil)
)

// ProcessBase holds fields shared by all process types.
type ProcessBase struct {
	ID           string
	Label        string
	CWLVersion   string
	Doc          []string
	Requirements []ProcessRequirement
	Hints        []Hint
	Intent       []string
}

// Base returns a pointer to the ProcessBase.
func (p *ProcessBase) Base() *ProcessBase {
	return p
}

// isProcess seals the Process interface.
func (p *ProcessBase) isProcess() {}

// CommandLineTool describes a command-line program invocation.
type CommandLineTool struct {
	ProcessBase

	Stdin              Expression
	Stdout             Expression
	Stderr             Expression
	Inputs             []CommandInputParameter
	Outputs            []CommandOutputParameter
	BaseCommand        []string
	Arguments          []CommandLineArgument
	SuccessCodes       []int
	TemporaryFailCodes []int
	PermanentFailCodes []int
}

// Class returns ClassCommandLineTool.
func (*CommandLineTool) Class() string {
	return ClassCommandLineTool
}

// Workflow describes a DAG of steps wired by input sources.
type Workflow struct {
	ProcessBase

	Inputs  []WorkflowInputParameter
	Outputs []WorkflowOutputParameter
	Steps   []WorkflowStep
}

// Class returns ClassWorkflow.
func (*Workflow) Class() string {
	return ClassWorkflow
}

// WorkflowSteps returns the workflow's steps.
func (w *Workflow) WorkflowSteps() []WorkflowStep { return w.Steps }

// WorkflowInputs returns the workflow's input parameters.
func (w *Workflow) WorkflowInputs() []WorkflowInputParameter { return w.Inputs }

// WorkflowOutputs returns the workflow's output parameters.
func (w *Workflow) WorkflowOutputs() []WorkflowOutputParameter { return w.Outputs }

// ExpressionTool computes outputs from a single CWL expression.
type ExpressionTool struct {
	ProcessBase

	Expression Expression
	Inputs     []WorkflowInputParameter
	Outputs    []ExpressionToolOutputParameter
}

// Class returns ClassExpressionTool.
func (*ExpressionTool) Class() string {
	return ClassExpressionTool
}

// Operation is a process with declared I/O but no implementation.
type Operation struct {
	ProcessBase

	Inputs  []OperationInputParameter
	Outputs []OperationOutputParameter
}

// Class returns ClassOperation.
func (*Operation) Class() string {
	return ClassOperation
}

// RawProcess is a schema-valid process whose class this package does not model.
// Extension point for downstream process classes.
type RawProcess struct {
	ProcessBase

	Node     salad.Node
	ClassIRI string
	Inputs   []OperationInputParameter
	Outputs  []OperationOutputParameter
}

// Class returns the extension class IRI.
func (r *RawProcess) Class() string {
	return r.ClassIRI
}

// ExtensionWorkflow is an extension class that extends Workflow.
// Carries full decoded workflow structure alongside raw node and extension class.
type ExtensionWorkflow struct {
	ProcessBase

	Node     *salad.MapNode
	ClassIRI string
	Steps    []WorkflowStep
	Inputs   []WorkflowInputParameter
	Outputs  []WorkflowOutputParameter
}

// Class returns the extension class IRI.
func (ew *ExtensionWorkflow) Class() string { return ew.ClassIRI }

// WorkflowSteps returns the extension workflow's steps.
func (ew *ExtensionWorkflow) WorkflowSteps() []WorkflowStep { return ew.Steps }

// WorkflowInputs returns the extension workflow's input parameters.
func (ew *ExtensionWorkflow) WorkflowInputs() []WorkflowInputParameter { return ew.Inputs }

// WorkflowOutputs returns the extension workflow's output parameters.
func (ew *ExtensionWorkflow) WorkflowOutputs() []WorkflowOutputParameter { return ew.Outputs }
