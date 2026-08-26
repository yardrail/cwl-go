package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// The five Process implementations. Each embeds ProcessBase for the fields the
// schema's abstract Process record supplies, and declares its own inputs and
// outputs, because the schema specializes the parameter type per class.

// Compile-time proof that every concrete process satisfies Process. If one of
// these breaks, a type switch over Process somewhere downstream has silently
// stopped being exhaustive.
var (
	_ Process = (*CommandLineTool)(nil)
	_ Process = (*Workflow)(nil)
	_ Process = (*ExpressionTool)(nil)
	_ Process = (*Operation)(nil)
	_ Process = (*RawProcess)(nil)
)

// ProcessBase holds the fields the schema's abstract Process record contributes
// to every concrete process, flattened out of the Identified, Labeled and
// Documented records it extends.
//
// Inputs and outputs are deliberately *not* here. The schema specializes them
// per class — a CommandLineTool takes CommandInputParameter, a Workflow takes
// WorkflowInputParameter — and Go has no generic embedding that would express
// that without erasing the element type at every use site.
type ProcessBase struct {
	// ID is the process's unique identifier, resolved to an absolute
	// identifier during decoding.
	//
	// The schema makes id optional, but a process embedded as a workflow
	// step's run target still needs one to be referred to. Decoding
	// therefore assigns an absent-but-required identifier a blank node of
	// the form "_:<uuid>", as the Schema Salad identifier rules provide for.
	// Generating it is decode.go's job; this field just carries the result.
	ID string

	// Label is a short human-readable label for the process.
	Label string

	// CWLVersion is the CWL version the document declares. Required on a
	// top-level process and absent on an embedded one, where it is inherited
	// from the enclosing document. Only CWLVersionV12 is accepted.
	CWLVersion string

	// Doc is the process documentation, normalized from the schema's
	// `string | string[]` form to a slice.
	Doc []string

	// Requirements are the process's declared requirements, in document
	// order. The schema also permits the map form keyed by class, which
	// decoding normalizes into this slice; order is preserved either way
	// because requirement precedence depends on it.
	Requirements []ProcessRequirement

	// Hints are the process's advisory requirements, in document order. An
	// unrecognized hint becomes a RawHint rather than an error.
	Hints []Hint

	// Intent lists ontology IRIs describing what the process is intended to
	// do, for discovery. Advisory: an implementation must not fail on an
	// intent it does not recognize.
	Intent []string
}

// Base returns a pointer to the ProcessBase itself, so that every type
// embedding it satisfies the Base method of Process without restating it.
// Writes through the returned pointer are visible on the enclosing process.
func (p *ProcessBase) Base() *ProcessBase {
	return p
}

// isProcess seals the Process interface. Declaring it on ProcessBase means
// every type that embeds ProcessBase is a Process, which is exactly the set of
// process types in this package.
func (p *ProcessBase) isProcess() {}

// CommandLineTool describes the invocation of a command-line program: how its
// inputs become arguments, how it is run, and how its outputs are collected
// from the files it leaves behind.
type CommandLineTool struct {
	ProcessBase

	// Stdin names a file to connect to the tool's standard input. May be an
	// expression; left unevaluated here.
	Stdin Expression

	// Stdout names the file the tool's standard output is captured to. May
	// be an expression. When a parameter uses the `stdout` type shortcut and
	// this field is empty, the implementation invents a name.
	Stdout Expression

	// Stderr names the file the tool's standard error is captured to, on the
	// same terms as Stdout.
	Stderr Expression

	// Inputs are the tool's input parameters, in document order.
	Inputs []CommandInputParameter

	// Outputs are the tool's output parameters, in document order.
	Outputs []CommandOutputParameter

	// BaseCommand is the program to run, normalized from the schema's
	// `string | string[]` form. Its elements are prepended to the arguments
	// the bindings produce.
	BaseCommand []string

	// Arguments are command-line bindings not associated with any input
	// parameter, in document order.
	Arguments []CommandLineArgument

	// SuccessCodes lists exit codes that mean success. Empty means only 0
	// succeeds.
	SuccessCodes []int

	// TemporaryFailCodes lists exit codes that mean a retryable failure.
	TemporaryFailCodes []int

	// PermanentFailCodes lists exit codes that mean a permanent failure.
	PermanentFailCodes []int
}

// Class returns ClassCommandLineTool.
func (*CommandLineTool) Class() string {
	return ClassCommandLineTool
}

// Workflow describes a directed acyclic graph of steps, each running another
// process, wired together by their input sources.
type Workflow struct {
	ProcessBase

	// Inputs are the workflow's input parameters, in document order.
	Inputs []WorkflowInputParameter

	// Outputs are the workflow's output parameters, in document order. Each
	// draws its value from one or more step outputs through OutputSource.
	Outputs []WorkflowOutputParameter

	// Steps are the workflow's steps, in document order. Order is
	// documentation only: execution order is determined by the dependency
	// graph the step sources describe, not by this slice.
	Steps []WorkflowStep
}

// Class returns ClassWorkflow.
func (*Workflow) Class() string {
	return ClassWorkflow
}

// ExpressionTool computes its outputs from a single CWL expression over its
// inputs, without running any program.
//
// Two consequences of that, both from the specification: the outputs of an
// ExpressionTool are always considered valid, and no software container is
// required or allowed.
type ExpressionTool struct {
	ProcessBase

	// Expression computes the output object. Left unevaluated here.
	Expression Expression

	// Inputs are the tool's input parameters, in document order. The schema
	// specializes these to WorkflowInputParameter, not to a type of their
	// own.
	Inputs []WorkflowInputParameter

	// Outputs are the tool's output parameters, in document order.
	Outputs []ExpressionToolOutputParameter
}

// Class returns ClassExpressionTool.
func (*ExpressionTool) Class() string {
	return ClassExpressionTool
}

// Operation is a process with declared inputs and outputs but no
// implementation. Executing it is deferred to the engine, which is expected to
// know what the operation means from its identifier and requirements.
//
// It is also the template the RawProcess extension point generalizes: an
// extension class that declares inputs and outputs and leaves execution to a
// downstream handler has exactly Operation's shape.
type Operation struct {
	ProcessBase

	// Inputs are the operation's input parameters, in document order.
	Inputs []OperationInputParameter

	// Outputs are the operation's output parameters, in document order.
	Outputs []OperationOutputParameter
}

// Class returns ClassOperation.
func (*Operation) Class() string {
	return ClassOperation
}

// RawProcess is the fallback for a schema-valid process whose class this
// package does not model: the extension point through which a downstream
// package adds process classes of its own.
//
// It is not a degraded result. The shared ProcessBase is fully decoded, and so
// are the inputs and outputs, using the generic Operation parameter shape —
// which is sound because an extension class that adds a process to CWL
// specializes the abstract parameter records the same way Operation does. That
// is enough for a scheduler to wire the process's in/out edges without knowing
// anything about the class. Class-specific fields are read from Node; extra
// fields on an individual parameter are reachable through that parameter's own
// ParameterBase.Node, so there is never a need to re-walk this node to find
// them.
type RawProcess struct {
	ProcessBase

	// Node is the complete validated salad node for the process, ready for a
	// downstream typed decode of the class-specific fields.
	Node salad.Node

	// ClassIRI is the resolved class discriminator — an extension IRI in a
	// namespace the document declared.
	ClassIRI string

	// Inputs are the process's input parameters, decoded with the generic
	// Operation parameter shape.
	Inputs []OperationInputParameter

	// Outputs are the process's output parameters, decoded with the generic
	// Operation parameter shape.
	Outputs []OperationOutputParameter
}

// Class returns the extension class IRI this process declared.
func (r *RawProcess) Class() string {
	return r.ClassIRI
}
