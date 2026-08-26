package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// The Workflow step model: a step, the process it runs, and the edges wiring
// its inputs and outputs to the rest of the graph.

// WorkflowStep is one node of a Workflow's dependency graph: an embedded or
// referenced process, plus the wiring that supplies its inputs and names its
// outputs.
//
// A step is *not* a Process. It does not carry a class, inputs or outputs of
// its own — those belong to whatever Run resolves to — so it does not embed
// ProcessBase, even though it repeats four of its fields.
type WorkflowStep struct {
	// Run is the process this step executes, either embedded or referenced.
	Run StepRun

	// ID is the step's identifier, resolved to an absolute identifier.
	ID string

	// Label is a short human-readable label for the step.
	Label string

	// When is a conditional-execution expression. When it evaluates to
	// false, the step is skipped and every one of its outputs takes the
	// value null, which then propagates to the steps downstream. Left
	// unevaluated here; pkg/cwlexec evaluates it at scheduling time.
	//
	// Using it requires a CWL v1.2 document; it did not exist in v1.1.
	When Expression

	// ScatterMethod selects how multiple Scatter inputs are combined.
	// Required when Scatter names more than one input, and meaningless
	// otherwise.
	ScatterMethod ScatterMethod

	// Doc is the step documentation, normalized to a slice.
	Doc []string

	// In are the step's input wirings, in document order. Required by the
	// schema, though it may be empty.
	In []WorkflowStepInput

	// Out names the step outputs the workflow consumes, in document order.
	// The schema permits a bare identifier string as shorthand for a
	// WorkflowStepOutput record; decoding normalizes both into this slice.
	Out []WorkflowStepOutput

	// Requirements are requirements declared on the step, in document order.
	// They override those inherited from the enclosing workflow and are
	// themselves inherited by the process Run names.
	Requirements []ProcessRequirement

	// Hints are advisory requirements declared on the step, in document
	// order. The schema types these as a plain Any array rather than as
	// requirements, so an entry naming an unknown class is expected rather
	// than exceptional.
	Hints []Hint

	// Scatter names the step inputs to scatter over, normalized from the
	// schema's `string | string[]` form and resolved to absolute
	// identifiers. Non-empty requires a ScatterFeatureRequirement in scope.
	Scatter []string
}

// StepRun is the `string | Process` union of a step's run field: either an
// identifier or path referring to a process defined elsewhere, or a process
// embedded inline.
//
// It is a plain two-field struct rather than one of the opaque wrappers in
// types_value.go because there is no third possibility and no ambiguity to
// guard: exactly one of the two fields is set. Ref is checked first by
// convention, and IsRef says so in one place.
type StepRun struct {
	// Process is the inline process, when the step embedded one. It may be a
	// RawProcess if the embedded class is an extension class.
	Process Process

	// Ref is the reference to a process defined elsewhere — a "#id"
	// fragment or a path — resolved to an absolute identifier. Whether it is
	// resolvable is checked by decoding, not here.
	Ref string
}

// IsRef reports whether the step referred to a process by identifier rather
// than embedding one inline.
func (r StepRun) IsRef() bool {
	return r.Ref != ""
}

// WorkflowStepInput wires one input of the process a step runs: where its value
// comes from, and how multiple incoming values are combined.
//
// It has no doc field. The schema builds it from Identified, Sink, LoadContents
// and Labeled — but not Documented — so a label is permitted here and a doc
// string is not.
type WorkflowStepInput struct {
	// Default is the value used when Source supplies none, or when every
	// source is null. Kept as the validated salad node on the same terms as
	// CommandInputParameter.Default.
	Default salad.Node

	// ID is the input's identifier, resolved to an absolute identifier. Its
	// last path segment names the parameter of the run process this wiring
	// feeds.
	ID string

	// Label is a short human-readable label.
	Label string

	// ValueFrom computes the input's value, with self bound to the value
	// Source and Default produced. Left unevaluated here. Using it requires
	// a StepInputExpressionRequirement in scope.
	ValueFrom Expression

	// LinkMerge combines the values of multiple Source entries. Empty means
	// the document did not declare one; the schema default is
	// LinkMergeNested.
	LinkMerge LinkMergeMethod

	// PickValue filters nulls out of the merged value. Empty means no
	// filtering.
	PickValue PickValueMethod

	// LoadListing is how deeply a Directory value's listing is populated
	// before ValueFrom runs.
	LoadListing LoadListingEnum

	// Source names the workflow inputs or upstream step outputs feeding this
	// input, normalized from the schema's `string | string[]` form and
	// resolved to absolute identifiers. This is the edge set the scheduler
	// builds the dependency graph from. More than one entry requires a
	// MultipleInputFeatureRequirement in scope.
	Source []string

	// LoadContents requests that a File value's first 64 KiB be read into
	// its contents field before ValueFrom runs.
	LoadContents bool
}

// WorkflowStepOutput names one output of the process a step runs, making it
// available to the rest of the workflow as "<step-id>/<output-id>".
//
// The schema gives it nothing but an identifier — it extends Identified and
// adds no fields — which is why a step's out list may be written as bare
// strings.
type WorkflowStepOutput struct {
	// ID is the output's identifier, resolved to an absolute identifier. Its
	// last path segment names the output parameter of the run process.
	ID string
}
