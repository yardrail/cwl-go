package cwlcore

// CWL v1.2 typed object model. Pure data and accessors; decoding is in decode.go,
// requirement scoping in requirements.go. Expressions are carried unevaluated.

// Process is a CWL process: CommandLineTool, Workflow, ExpressionTool, Operation, or RawProcess.
// Sealed interface; exhaustive type switch.
type Process interface {
	// Class returns the process class discriminator.
	Class() string

	// Base returns a pointer to the embedded [ProcessBase].
	Base() *ProcessBase

	isProcess()
}

// StepContainer is a Process with a step DAG: Workflow or an extension of it.
type StepContainer interface {
	Process
	WorkflowSteps() []WorkflowStep
	WorkflowInputs() []WorkflowInputParameter
	WorkflowOutputs() []WorkflowOutputParameter
}

// ProcessRequirement is a "requirements" entry. Sealed interface.
// Unknown classes become [RawRequirement].
type ProcessRequirement interface {
	// Class returns the requirement's class discriminator.
	Class() string

	isRequirement()
}

// Hint is a "hints" entry: an advisory requirement that may be ignored.
// Unsealed — downstream packages may implement it.
type Hint interface {
	// Class returns the hint's class discriminator.
	Class() string
}

// Parameter is an input or output parameter. Sealed interface.
type Parameter interface {
	// ID returns the parameter's resolved absolute identifier.
	ID() string

	isParameter()
}

// Process class discriminators.
const (
	ClassCommandLineTool = "CommandLineTool"
	ClassWorkflow        = "Workflow"
	ClassExpressionTool  = "ExpressionTool"
	ClassOperation       = "Operation"
)

// CWLVersionV12 is the only cwlVersion this implementation accepts.
const CWLVersionV12 = "v1.2"

// Expression is a CWL expression carried unevaluated. Evaluated by expression.go.
type Expression string

// ScatterMethod selects how scatter inputs are combined into jobs.
type ScatterMethod string

const (
	// ScatterDotProduct pairs scattered arrays element-wise.
	ScatterDotProduct ScatterMethod = "dotproduct"
	// ScatterNestedCrossProduct produces a nested array over the full grid.
	ScatterNestedCrossProduct ScatterMethod = "nested_crossproduct"
	// ScatterFlatCrossProduct produces a flat array over the full grid.
	ScatterFlatCrossProduct ScatterMethod = "flat_crossproduct"
)

// LinkMergeMethod selects how multiple sources feeding one sink are combined.
type LinkMergeMethod string

const (
	// LinkMergeNested collects each source as one element of the sink array (schema default).
	LinkMergeNested LinkMergeMethod = "merge_nested"

	// LinkMergeFlattened concatenates source arrays into a single flat array.
	LinkMergeFlattened LinkMergeMethod = "merge_flattened"
)

// PickValueMethod selects how null values are filtered after linkMerge.
type PickValueMethod string

const (
	// PickFirstNonNull selects the first non-null value.
	PickFirstNonNull PickValueMethod = "first_non_null"
	// PickTheOnlyNonNull requires exactly one non-null value.
	PickTheOnlyNonNull PickValueMethod = "the_only_non_null"
	// PickAllNonNull keeps all non-null values.
	PickAllNonNull PickValueMethod = "all_non_null"
)

// LoadListingEnum selects how deeply a Directory's listing is populated.
type LoadListingEnum string

const (
	// LoadListingNone omits the directory listing.
	LoadListingNone LoadListingEnum = "no_listing"
	// LoadListingShallow populates one level of entries.
	LoadListingShallow LoadListingEnum = "shallow_listing"
	// LoadListingDeep recursively populates the full tree.
	LoadListingDeep LoadListingEnum = "deep_listing"
)
