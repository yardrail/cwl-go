package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// The seven concrete parameter types. The schema builds them by specializing
// the abstract InputParameter and OutputParameter records per process class, so
// they differ only in the handful of fields they add to the shared
// ParameterBase.

// Compile-time proof that every concrete parameter satisfies Parameter.
var (
	_ Parameter = (*CommandInputParameter)(nil)
	_ Parameter = (*CommandOutputParameter)(nil)
	_ Parameter = (*WorkflowInputParameter)(nil)
	_ Parameter = (*WorkflowOutputParameter)(nil)
	_ Parameter = (*OperationInputParameter)(nil)
	_ Parameter = (*OperationOutputParameter)(nil)
	_ Parameter = (*ExpressionToolOutputParameter)(nil)
)

// ParameterBase holds the fields every input and output parameter shares,
// flattened out of the abstract Parameter record and the FieldBase, Documented,
// Identified, InputFormat and OutputFormat records it extends. Embedding it is
// what makes a struct a Parameter.
//
// The identifier is stored in IDField rather than ID because ID is the accessor
// that satisfies the Parameter interface, and a Go field and method cannot
// share a name. This is the one place in the model where a field is not named
// after its document key.
type ParameterBase struct {
	// Node is the validated salad node the parameter was decoded from.
	//
	// It is the extension hook for parameters: when a downstream package
	// specializes a parameter record and adds fields of its own, it reads
	// them from here rather than re-walking the enclosing process node.
	Node salad.Node

	// IDField is the parameter's identifier, resolved to an absolute
	// identifier during decoding. Read it through the ID method.
	IDField string

	// Label is a short human-readable label for the parameter.
	Label string

	// Doc is the parameter documentation, normalized from the schema's
	// `string | string[]` form to a slice.
	Doc []string

	// Type is the parameter's declared type. Required by the schema on every
	// concrete parameter, so a decoded parameter always has one.
	Type TypeRef

	// SecondaryFiles declares the files that must accompany a File value of
	// this parameter, as patterns over the primary file's name. These are
	// patterns, not values; the values themselves are File.SecondaryFiles.
	SecondaryFiles []SecondaryFileSchema

	// Format constrains, on an input, the media types the parameter accepts,
	// and declares, on an output, the media type it produces.
	//
	// The two sides have different schema types — an input's format is
	// `string | string[] | Expression`, an output's is `string | Expression`
	// — and both normalize into this slice. An output parameter therefore
	// carries at most one entry. Values may be expressions and are left
	// unevaluated.
	Format []Expression

	// LoadContents requests that a File value's first 64 KiB be read into
	// its contents field before expressions run.
	//
	// The schema only gives LoadContents and LoadListing to input
	// parameters. They are on the shared base anyway, because keeping the
	// base identical across all seven parameter types is worth more than
	// forbidding a field that decoding simply never sets on an output. On an
	// output parameter the equivalent settings live on CommandOutputBinding.
	LoadContents bool

	// LoadListing is how deeply a Directory value's listing is populated
	// before expressions run. Carried here for the same reason as
	// LoadContents.
	LoadListing LoadListingEnum

	// Streamable declares that a File value may be a named pipe rather than
	// a seekable file, and so may only be read once.
	Streamable bool
}

// ID returns the parameter's resolved identifier, satisfying Parameter.
func (p *ParameterBase) ID() string {
	return p.IDField
}

// isParameter seals the Parameter interface. Declaring it on ParameterBase
// means every type embedding ParameterBase is a Parameter.
func (p *ParameterBase) isParameter() {}

// CommandInputParameter is an input parameter of a CommandLineTool. It is the
// only parameter type that may use the `stdin` type shortcut.
type CommandInputParameter struct {
	ParameterBase

	// InputBinding describes how this parameter's value becomes command-line
	// arguments. Nil when the parameter is bound only by an expression, or
	// not bound to the command line at all.
	InputBinding *CommandLineBinding

	// Default is the value used when the input object supplies none. It is
	// kept as the validated salad node rather than a decoded Go value: the
	// schema type is `File | Directory | Any`, so it may be any CWL value at
	// all, including ones this package does not model. Use salad.ToAny to
	// materialize it, or decode it as a File or Directory when the
	// parameter's type says so.
	Default salad.Node
}

// CommandOutputParameter is an output parameter of a CommandLineTool. It is the
// only parameter type that may use the `stdout` and `stderr` type shortcuts.
type CommandOutputParameter struct {
	ParameterBase

	// OutputBinding describes how this parameter's value is collected from
	// the output directory. Nil when the parameter's value comes from the
	// stdout/stderr shortcuts or from an expression instead.
	OutputBinding *CommandOutputBinding
}

// WorkflowInputParameter is an input parameter of a Workflow, and also of an
// ExpressionTool, which the schema specializes to this same type rather than
// giving it one of its own.
type WorkflowInputParameter struct {
	ParameterBase

	// InputBinding is declared by the schema but, per the specification, is
	// not used or allowed as a tool binding at the workflow level. It
	// carries only loadContents.
	InputBinding *InputBinding

	// Default is the value used when no source supplies one, kept as a salad
	// node on the same terms as CommandInputParameter.Default.
	Default salad.Node
}

// WorkflowOutputParameter is an output parameter of a Workflow. Its value is
// drawn from one or more step outputs rather than collected from a filesystem.
type WorkflowOutputParameter struct {
	ParameterBase

	// OutputSource names the step outputs this parameter draws from,
	// normalized from the schema's `string | string[]` form and resolved to
	// absolute identifiers. More than one entry requires a
	// MultipleInputFeatureRequirement to be in scope.
	OutputSource []string

	// LinkMerge combines the values of multiple OutputSource entries. Empty
	// means the document did not declare one; the schema default is
	// LinkMergeNested.
	LinkMerge LinkMergeMethod

	// PickValue filters nulls out of the merged value. Empty means no
	// filtering.
	PickValue PickValueMethod
}

// OperationInputParameter is an input parameter of an Operation, and the
// generic input shape RawProcess uses for an extension class.
type OperationInputParameter struct {
	ParameterBase

	// Default is the value used when no source supplies one, kept as a salad
	// node on the same terms as CommandInputParameter.Default.
	Default salad.Node
}

// OperationOutputParameter is an output parameter of an Operation, and the
// generic output shape RawProcess uses for an extension class. It adds nothing
// to ParameterBase.
type OperationOutputParameter struct {
	ParameterBase
}

// ExpressionToolOutputParameter is an output parameter of an ExpressionTool. It
// adds nothing to ParameterBase: an ExpressionTool's outputs come from its
// expression's result object, so there is nothing to bind them to.
type ExpressionToolOutputParameter struct {
	ParameterBase
}
