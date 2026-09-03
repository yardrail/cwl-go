package cwlcore

// This file and its types_*.go siblings hold the CWL v1.2 typed object model:
// one Go type per record in the vendored schema under schema/ (Process.yml,
// CommandLineTool.yml, Workflow.yml, Operation.yml). It is pure data plus
// trivial accessors — nothing here reads a file, walks a document, or
// validates. Turning a validated salad tree into these structs is decode.go's
// job; resolving requirement scope over them is requirements.go's.
//
// Three shaping decisions run through the whole model, and are worth knowing
// before reading any individual type:
//
//  1. Flat, not inherited. The schema builds its records by multiple
//     inheritance from abstract records (Labeled, Identified, FieldBase,
//     LoadContents, Parameter, Process, ...). Go has no such thing, so each
//     concrete type carries its full, flattened field set, exactly as the
//     upstream cwl-utils generated parser does. The two field sets that really
//     are identical everywhere — the seven Process fields and the ten
//     Parameter fields — are factored into the embedded ProcessBase and
//     ParameterBase.
//
//  2. Abstract records become sealed interfaces. Process, ProcessRequirement
//     and Parameter are interfaces with an unexported sealing method, so the
//     set of implementations is closed to this package and a type switch over
//     them is exhaustive by construction. This replaces the upstream Python
//     isinstance dispatch. Hint is deliberately *not* sealed; see its doc.
//
//  3. Unions become opaque wrapper values. CWL is full of "string or
//     Expression or number" style unions. Every one of them is represented by
//     a small value type with a Kind() discriminator and typed accessors — see
//     types_value.go for the pattern, types_optional.go for its three smallest
//     members, and types_schema.go for TypeRef, which applies the same shape to
//     the recursive type language.
//
// The model covers CWL *values* as well as document structure, but only the two
// the schema defines as records: File and Directory, in types_object.go. Every
// other runtime value stays a salad.Node, because the schema types it as Any
// and there is nothing to model.
//
// Expression-bearing fields are carried through this layer *unevaluated*. An
// Expression is the literal source text as written in the document;
// pkg/cwlexec evaluates it at scheduling time, when the runtime and input
// bindings a CWL expression may reference actually exist.
//
// Extension classes — a schema-valid class: this package has no concrete type
// for — are never an error here. They surface as RawProcess, RawRequirement
// and RawHint, each carrying the validated salad.Node so a downstream package
// can run its own typed decode over the fields it owns. That is the only
// extension mechanism; this package contains no vocabulary of its own beyond
// CWL v1.2.

// Process is any CWL process: one of the four core classes (CommandLineTool,
// Workflow, ExpressionTool, Operation) or a RawProcess standing in for an
// extension class.
//
// The interface is sealed by the unexported isProcess method, so callers
// distinguish kinds with an ordinary type switch and can rely on the case list
// being complete:
//
//	switch p := proc.(type) {
//	case *cwlcore.CommandLineTool: // ...
//	case *cwlcore.Workflow:        // ...
//	case *cwlcore.ExpressionTool:  // ...
//	case *cwlcore.Operation:       // ...
//	case *cwlcore.RawProcess:      // p.Class() names the extension class
//	}
type Process interface {
	// Class returns the resolved class discriminator: one of
	// ClassCommandLineTool, ClassWorkflow, ClassExpressionTool,
	// ClassOperation, or, for a RawProcess, the extension class IRI.
	Class() string

	// Base returns a pointer to the embedded ProcessBase, giving uniform
	// access to id/label/doc/requirements/hints/cwlVersion/intent across
	// every process kind. Writes through the pointer are visible on the
	// process itself.
	Base() *ProcessBase

	isProcess()
}

// StepContainer is a Process whose execution is a DAG of steps: a Workflow, or
// an extension class that extends Workflow. The execution layer dispatches on
// this interface rather than *Workflow so that extension workflow classes
// participate in step planning, subworkflow handling, and run-reference linking
// without losing their extension class identity or fields.
type StepContainer interface {
	Process
	WorkflowSteps() []WorkflowStep
	WorkflowInputs() []WorkflowInputParameter
	WorkflowOutputs() []WorkflowOutputParameter
}

// ProcessRequirement is a declared prerequisite of a process, step or workflow:
// a "requirements" entry. The seventeen core v1.2 requirement classes each have
// a concrete type in types_requirement.go; a schema-valid entry whose class this
// package does not model becomes a RawRequirement.
//
// Requirements are not validated or resolved here. Scoping and precedence —
// which requirement applies to which process, and which declaration wins — is
// requirements.go's responsibility, and it is what fails closed on a
// RawRequirement that no downstream package claims.
//
// The interface is sealed by the unexported isRequirement method.
type ProcessRequirement interface {
	// Class returns the requirement's class discriminator, for example
	// ClassDockerRequirement, or the extension class IRI for a
	// RawRequirement.
	Class() string

	isRequirement()
}

// Hint is a "hints" entry: an advisory requirement that an implementation may
// ignore rather than fail on. Every ProcessRequirement is also a Hint, because
// a hints entry may name a core requirement class; anything else becomes a
// RawHint.
//
// Unlike Process and ProcessRequirement, Hint is deliberately left unsealed. A
// hint by definition may name a class the runner has never heard of, so
// downstream packages are free to supply their own implementations.
type Hint interface {
	// Class returns the hint's class discriminator.
	Class() string
}

// Parameter is any input or output parameter of a process. The seven concrete
// parameter types in types_param.go all embed ParameterBase, which supplies the
// ID accessor and the fields every parameter shares.
//
// The interface is sealed by the unexported isParameter method.
type Parameter interface {
	// ID returns the parameter's identifier. After decoding this is the
	// absolute, resolved identifier, not the short name as written.
	ID() string

	isParameter()
}

// Process class discriminators. These are the short names the vendored schema
// gives the four concrete Process records; a document's class field resolves to
// one of them, or to an extension IRI carried by RawProcess.
const (
	// ClassCommandLineTool is the class of a CommandLineTool process.
	ClassCommandLineTool = "CommandLineTool"

	// ClassWorkflow is the class of a Workflow process.
	ClassWorkflow = "Workflow"

	// ClassExpressionTool is the class of an ExpressionTool process.
	ClassExpressionTool = "ExpressionTool"

	// ClassOperation is the class of an Operation process — a process with
	// declared inputs and outputs but no implementation, whose execution is
	// deferred to the engine.
	ClassOperation = "Operation"
)

// CWLVersionV12 is the only cwlVersion this implementation accepts. Documents
// written against earlier CWL versions are deliberately out of scope: there is
// no document-upgrade machinery.
const CWLVersionV12 = "v1.2"

// Expression is a CWL expression, or a string that may embed one, carried as
// written and left unevaluated by this package.
//
// The vendored schema models expressions as an enum with the single placeholder
// symbol cwl:ExpressionPlaceholder, which is the Schema Salad idiom for "any
// string here may be an expression". Consequently every union member spelled
// "string or Expression" collapses to this one Go type; where a distinction
// between a plain literal and an expression matters, the surrounding union
// wrapper records it as a ValueKind.
//
// Two syntaxes exist: parameter references, $(...), and — only when an
// InlineJavascriptRequirement is in scope — JavaScript function bodies,
// ${...}. Both are evaluated by expression.go, not here.
type Expression string

// ScatterMethod selects how a scattered WorkflowStep combines its scatter
// inputs into the set of jobs to run. The zero value is the empty string,
// meaning the step declared no scatterMethod; it is required when a step
// scatters over more than one input.
type ScatterMethod string

// The scatterMethod symbols defined by Workflow.yml.
const (
	// ScatterDotProduct pairs the scatter inputs elementwise. Every input
	// must have the same length, and that length is the number of jobs.
	ScatterDotProduct ScatterMethod = "dotproduct"

	// ScatterNestedCrossProduct takes the cartesian product of the scatter
	// inputs, producing output arrays nested one level per scatter input.
	ScatterNestedCrossProduct ScatterMethod = "nested_crossproduct"

	// ScatterFlatCrossProduct takes the cartesian product of the scatter
	// inputs, producing a single flat output array.
	ScatterFlatCrossProduct ScatterMethod = "flat_crossproduct"
)

// LinkMergeMethod selects how multiple sources feeding one sink are combined.
// The zero value is the empty string, meaning the field was absent; the schema
// default is LinkMergeNested, and applying that default is the consumer's job
// so that "absent" stays distinguishable from an explicit declaration.
type LinkMergeMethod string

// The linkMerge symbols defined by Workflow.yml.
const (
	// LinkMergeNested collects each source's value as one element of the
	// sink array, preserving source structure. This is the schema default.
	LinkMergeNested LinkMergeMethod = "merge_nested"

	// LinkMergeFlattened concatenates the sources, which must all be arrays
	// of compatible item type, into a single flat array.
	LinkMergeFlattened LinkMergeMethod = "merge_flattened"
)

// PickValueMethod selects how null values are filtered out of a sink's incoming
// values, after linkMerge has run. The zero value is the empty string, meaning
// no pickValue was declared and no filtering happens.
type PickValueMethod string

// The pickValue symbols defined by Workflow.yml.
const (
	// PickFirstNonNull takes the first non-null value, and is an error if
	// every value is null.
	PickFirstNonNull PickValueMethod = "first_non_null"

	// PickTheOnlyNonNull takes the single non-null value, and is an error
	// unless exactly one value is non-null.
	PickTheOnlyNonNull PickValueMethod = "the_only_non_null"

	// PickAllNonNull keeps every non-null value, in order.
	PickAllNonNull PickValueMethod = "all_non_null"
)

// LoadListingEnum selects how deeply a Directory value's listing field is
// populated before expressions run against it. The zero value is the empty
// string, meaning the field was absent, which matters because the effective
// value is resolved by a three-level precedence — the parameter, then any
// LoadListingRequirement in scope, then LoadListingNone.
type LoadListingEnum string

// The loadListing symbols defined by Process.yml.
const (
	// LoadListingNone leaves the directory listing unpopulated. This is the
	// final fallback in the loadListing precedence chain.
	LoadListingNone LoadListingEnum = "no_listing"

	// LoadListingShallow populates only the top-level listing, without
	// recursing into subdirectories.
	LoadListingShallow LoadListingEnum = "shallow_listing"

	// LoadListingDeep populates the listing recursively, through every
	// subdirectory.
	LoadListingDeep LoadListingEnum = "deep_listing"
)
