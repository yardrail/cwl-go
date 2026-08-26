package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// The seventeen core CWL v1.2 requirement classes, plus the RawRequirement
// fallback. Every one is a plain struct implementing ProcessRequirement through
// a Class method backed by a constant.
//
// Nothing here is evaluated or resolved. Several requirements carry
// Expression-or-literal unions — ResourceRequirement's eight fields,
// WorkReuse's enableReuse, ToolTimeLimit's timelimit — and they are held
// unevaluated, because a requirement's expressions are evaluated against the
// job it applies to, which does not exist until pkg/cwlexec schedules one.
// Which declaration of a requirement is in scope for a given process is
// likewise not decided here; that is requirements.go.

// Requirement class discriminators, spelled exactly as the vendored schema
// spells them. A misspelling here would surface at runtime as a requirement
// that silently never matches, so types_class_test.go checks every one of them
// against the schema.
const (
	// ClassInlineJavascriptRequirement enables JavaScript expressions.
	ClassInlineJavascriptRequirement = "InlineJavascriptRequirement"

	// ClassSchemaDefRequirement declares reusable named types.
	ClassSchemaDefRequirement = "SchemaDefRequirement"

	// ClassLoadListingRequirement sets the default Directory listing depth.
	ClassLoadListingRequirement = "LoadListingRequirement"

	// ClassDockerRequirement selects a software container to run in.
	ClassDockerRequirement = "DockerRequirement"

	// ClassSoftwareRequirement declares required software packages.
	ClassSoftwareRequirement = "SoftwareRequirement"

	// ClassInitialWorkDirRequirement stages files into the working directory.
	ClassInitialWorkDirRequirement = "InitialWorkDirRequirement"

	// ClassEnvVarRequirement sets environment variables.
	ClassEnvVarRequirement = "EnvVarRequirement"

	// ClassShellCommandRequirement runs the command line through a shell.
	ClassShellCommandRequirement = "ShellCommandRequirement"

	// ClassResourceRequirement declares CPU, memory and disk needs.
	ClassResourceRequirement = "ResourceRequirement"

	// ClassWorkReuse controls reuse of a previous run's results.
	ClassWorkReuse = "WorkReuse"

	// ClassNetworkAccess declares whether the tool needs network access.
	ClassNetworkAccess = "NetworkAccess"

	// ClassInplaceUpdateRequirement permits in-place modification of inputs.
	ClassInplaceUpdateRequirement = "InplaceUpdateRequirement"

	// ClassToolTimeLimit caps how long the tool may run.
	ClassToolTimeLimit = "ToolTimeLimit"

	// ClassSubworkflowFeatureRequirement permits a step to run a Workflow.
	ClassSubworkflowFeatureRequirement = "SubworkflowFeatureRequirement"

	// ClassScatterFeatureRequirement permits a step to scatter.
	ClassScatterFeatureRequirement = "ScatterFeatureRequirement"

	// ClassMultipleInputFeatureRequirement permits a sink to have multiple
	// sources.
	ClassMultipleInputFeatureRequirement = "MultipleInputFeatureRequirement"

	// ClassStepInputExpressionRequirement permits valueFrom on a step input.
	ClassStepInputExpressionRequirement = "StepInputExpressionRequirement"
)

// Compile-time proof that every requirement satisfies ProcessRequirement — and
// therefore also Hint, since a hints entry may name any requirement class.
var (
	_ ProcessRequirement = (*InlineJavascriptRequirement)(nil)
	_ ProcessRequirement = (*SchemaDefRequirement)(nil)
	_ ProcessRequirement = (*LoadListingRequirement)(nil)
	_ ProcessRequirement = (*DockerRequirement)(nil)
	_ ProcessRequirement = (*SoftwareRequirement)(nil)
	_ ProcessRequirement = (*InitialWorkDirRequirement)(nil)
	_ ProcessRequirement = (*EnvVarRequirement)(nil)
	_ ProcessRequirement = (*ShellCommandRequirement)(nil)
	_ ProcessRequirement = (*ResourceRequirement)(nil)
	_ ProcessRequirement = (*WorkReuse)(nil)
	_ ProcessRequirement = (*NetworkAccess)(nil)
	_ ProcessRequirement = (*InplaceUpdateRequirement)(nil)
	_ ProcessRequirement = (*ToolTimeLimit)(nil)
	_ ProcessRequirement = (*SubworkflowFeatureRequirement)(nil)
	_ ProcessRequirement = (*ScatterFeatureRequirement)(nil)
	_ ProcessRequirement = (*MultipleInputFeatureRequirement)(nil)
	_ ProcessRequirement = (*StepInputExpressionRequirement)(nil)
	_ ProcessRequirement = (*RawRequirement)(nil)

	_ Hint = (*DockerRequirement)(nil)
	_ Hint = (*RawHint)(nil)
)

// requirementBase seals ProcessRequirement for every requirement in this
// package. Embedding it is what makes a struct a ProcessRequirement; it holds
// no state and adds nothing to the struct's size.
type requirementBase struct{}

// isRequirement seals the ProcessRequirement interface.
func (requirementBase) isRequirement() {}

// InlineJavascriptRequirement enables the JavaScript expression syntax,
// ${...}, in the scope it applies to. Without it in scope, a JavaScript
// expression is an error and only parameter references, $(...), may be used.
type InlineJavascriptRequirement struct {
	requirementBase

	// ExpressionLib holds JavaScript fragments evaluated, in order, in the
	// global scope before each expression runs. Typically function
	// definitions the expressions then call.
	ExpressionLib []string
}

// Class returns ClassInlineJavascriptRequirement.
func (*InlineJavascriptRequirement) Class() string {
	return ClassInlineJavascriptRequirement
}

// SchemaDefRequirement declares named record, enum and array types that
// parameters in scope may then refer to by name.
type SchemaDefRequirement struct {
	requirementBase

	// Types are the declared type schemas, in document order.
	//
	// They are kept as validated salad nodes rather than decoded TypeRefs
	// because resolving one may require the others: a schema in this list
	// may refer by name to another declared alongside it, and untangling
	// that needs the whole requirement scope rather than a single node.
	// requirements.go resolves them.
	Types []salad.Node
}

// Class returns ClassSchemaDefRequirement.
func (*SchemaDefRequirement) Class() string {
	return ClassSchemaDefRequirement
}

// LoadListingRequirement sets the default listing depth for Directory values in
// scope. It sits in the middle of the three-level precedence chain: an explicit
// loadListing on a parameter wins, this requirement comes next, and
// LoadListingNone is the fallback.
type LoadListingRequirement struct {
	requirementBase

	// LoadListing is the default listing depth. Empty means the requirement
	// declared none.
	LoadListing LoadListingEnum
}

// Class returns ClassLoadListingRequirement.
func (*LoadListingRequirement) Class() string {
	return ClassLoadListingRequirement
}

// DockerRequirement declares that the tool must run inside a software
// container, and names the image to use.
//
// Its five image-source fields are alternatives, and the specification's
// resolution order is: DockerPull, DockerLoad, DockerFile, DockerImport,
// DockerImageID. Choosing between them is the runner's job.
type DockerRequirement struct {
	requirementBase

	// DockerPull is an image name to pull from a registry.
	DockerPull string

	// DockerLoad is an HTTP URL of a saved image archive to load.
	DockerLoad string

	// DockerFile is the literal contents of a Dockerfile to build.
	DockerFile string

	// DockerImport is an HTTP URL of a filesystem tarball to import.
	DockerImport string

	// DockerImageID is the image identifier to run, and also the tag applied
	// to an image built or loaded by the fields above. Spelled dockerImageId
	// in the document.
	DockerImageID string

	// DockerOutputDirectory is the path inside the container that the tool's
	// designated output directory is mounted at. Setting it is discouraged;
	// the specification notes it may be deprecated in a future version.
	DockerOutputDirectory string
}

// Class returns ClassDockerRequirement.
func (*DockerRequirement) Class() string {
	return ClassDockerRequirement
}

// SoftwareRequirement declares software packages that must be available in the
// tool's execution environment. It is advisory in the sense that an
// implementation may satisfy it however it likes — a package manager, a module
// system, or by assuming the software is already installed.
type SoftwareRequirement struct {
	requirementBase

	// Packages are the required packages, in document order. Required by the
	// schema.
	Packages []SoftwarePackage
}

// Class returns ClassSoftwareRequirement.
func (*SoftwareRequirement) Class() string {
	return ClassSoftwareRequirement
}

// InitialWorkDirRequirement stages files and directories into the tool's
// working directory before it runs.
type InitialWorkDirRequirement struct {
	requirementBase

	// Listing is what to stage: either a list of entries or a single
	// expression producing that list. Required by the schema, so a decoded
	// requirement's Listing is never unset.
	Listing InitialWorkDirListing
}

// Class returns ClassInitialWorkDirRequirement.
func (*InitialWorkDirRequirement) Class() string {
	return ClassInitialWorkDirRequirement
}

// EnvVarRequirement sets environment variables in the tool's execution
// environment.
type EnvVarRequirement struct {
	requirementBase

	// EnvDef are the variables to set, in document order. Required by the
	// schema. The schema also permits the map form keyed by envName, which
	// decoding normalizes into this slice.
	EnvDef []EnvironmentDef
}

// Class returns ClassEnvVarRequirement.
func (*EnvVarRequirement) Class() string {
	return ClassEnvVarRequirement
}

// ShellCommandRequirement is a marker: it declares that the command line is
// assembled into a shell command string and run through a shell, so that
// arguments with shellQuote false may contain shell metacharacters, pipes and
// redirects. It has no fields beyond its class.
type ShellCommandRequirement struct {
	requirementBase
}

// Class returns ClassShellCommandRequirement.
func (*ShellCommandRequirement) Class() string {
	return ClassShellCommandRequirement
}

// ResourceRequirement declares the compute resources a tool needs, as
// soft minimum and maximum bounds.
//
// Every field is a ResourceValue and every field may legitimately be unset.
// That is deliberate on the schema's part: it declines to declare defaults for
// the minima so that an implementation can distinguish a value that was not
// provided from one that happens to equal the documented default, and apply the
// specification's resolution rules itself. Do not read an unset field as zero.
//
// The documented defaults, for reference, are: coresMin 1, ramMin 256 (MiB),
// tmpdirMin 1024 (MiB), outdirMin 1024 (MiB), and each maximum defaults to its
// corresponding minimum.
type ResourceRequirement struct {
	requirementBase

	// CoresMin is the minimum reserved number of CPU cores. May be
	// fractional.
	CoresMin ResourceValue

	// CoresMax is the maximum reserved number of CPU cores.
	CoresMax ResourceValue

	// RAMMin is the minimum reserved RAM in mebibytes. Spelled ramMin in the
	// document.
	RAMMin ResourceValue

	// RAMMax is the maximum reserved RAM in mebibytes. Spelled ramMax in the
	// document.
	RAMMax ResourceValue

	// TmpdirMin is the minimum reserved filesystem space, in mebibytes, for
	// the designated temporary directory.
	TmpdirMin ResourceValue

	// TmpdirMax is the maximum reserved filesystem space, in mebibytes, for
	// the designated temporary directory.
	TmpdirMax ResourceValue

	// OutdirMin is the minimum reserved filesystem space, in mebibytes, for
	// the designated output directory.
	OutdirMin ResourceValue

	// OutdirMax is the maximum reserved filesystem space, in mebibytes, for
	// the designated output directory.
	OutdirMax ResourceValue
}

// Class returns ClassResourceRequirement.
func (*ResourceRequirement) Class() string {
	return ClassResourceRequirement
}

// WorkReuse controls whether the implementation may reuse the results of a
// previous identical invocation instead of running the process again.
type WorkReuse struct {
	requirementBase

	// EnableReuse permits reuse. The schema default is true, so an unset
	// value means true, not false. Required by the schema, so a decoded
	// requirement's EnableReuse is set.
	EnableReuse ExprBool
}

// Class returns ClassWorkReuse.
func (*WorkReuse) Class() string {
	return ClassWorkReuse
}

// NetworkAccess declares whether the tool requires outgoing network access. A
// tool that is not granted it must still be able to run; the specification
// makes no promise about which network the access reaches.
type NetworkAccess struct {
	requirementBase

	// NetworkAccess grants network access. Required by the schema and has no
	// default, so an unset value means the requirement was not declared at
	// all rather than that access was denied.
	NetworkAccess ExprBool
}

// Class returns ClassNetworkAccess.
func (*NetworkAccess) Class() string {
	return ClassNetworkAccess
}

// InplaceUpdateRequirement permits a tool to modify its input files and
// directories in place, rather than working on copies.
//
// It is a sharp tool: the specification warns that a workflow using it is no
// longer safely restartable or re-runnable, because an interrupted run may
// leave inputs in a partially modified state.
type InplaceUpdateRequirement struct {
	requirementBase

	// InplaceUpdate permits in-place modification. Required by the schema
	// and a plain boolean with no default, so the Go zero value — false —
	// carries the right meaning for an undeclared requirement.
	InplaceUpdate bool
}

// Class returns ClassInplaceUpdateRequirement.
func (*InplaceUpdateRequirement) Class() string {
	return ClassInplaceUpdateRequirement
}

// ToolTimeLimit caps how long a tool may run before the implementation must
// terminate it and treat the run as a permanent failure.
type ToolTimeLimit struct {
	requirementBase

	// Timelimit is the limit in seconds. Zero, or an expression evaluating
	// to zero, means no limit. Required by the schema. Spelled timelimit,
	// all lower case, in the document.
	Timelimit ExprLong
}

// Class returns ClassToolTimeLimit.
func (*ToolTimeLimit) Class() string {
	return ClassToolTimeLimit
}

// SubworkflowFeatureRequirement is a marker declaring that a workflow step may
// run a Workflow, rather than only a tool. It has no fields beyond its class.
type SubworkflowFeatureRequirement struct {
	requirementBase
}

// Class returns ClassSubworkflowFeatureRequirement.
func (*SubworkflowFeatureRequirement) Class() string {
	return ClassSubworkflowFeatureRequirement
}

// ScatterFeatureRequirement is a marker declaring that a workflow step may
// scatter over an input. It has no fields beyond its class.
type ScatterFeatureRequirement struct {
	requirementBase
}

// Class returns ClassScatterFeatureRequirement.
func (*ScatterFeatureRequirement) Class() string {
	return ClassScatterFeatureRequirement
}

// MultipleInputFeatureRequirement is a marker declaring that a sink — a step
// input or a workflow output — may draw from more than one source. It has no
// fields beyond its class.
type MultipleInputFeatureRequirement struct {
	requirementBase
}

// Class returns ClassMultipleInputFeatureRequirement.
func (*MultipleInputFeatureRequirement) Class() string {
	return ClassMultipleInputFeatureRequirement
}

// StepInputExpressionRequirement is a marker declaring that a workflow step
// input may use valueFrom. It has no fields beyond its class.
type StepInputExpressionRequirement struct {
	requirementBase
}

// Class returns ClassStepInputExpressionRequirement.
func (*StepInputExpressionRequirement) Class() string {
	return ClassStepInputExpressionRequirement
}

// RawRequirement is the fallback for a schema-valid requirements entry whose
// class this package does not model: the extension point through which a
// downstream package adds requirement classes of its own.
//
// Decoding never fails on one. Whether a document is schema-valid is
// pkg/salad's question and has already been answered by the time a
// RawRequirement exists, and whether an unrecognized requirement is fatal is a
// question about the runner's capabilities, not about the document. The CWL
// specification requires an implementation to fail on a requirement it cannot
// satisfy, and requirements.go is where that fail-closed decision is made —
// after a downstream package has had its chance to claim the class.
type RawRequirement struct {
	requirementBase

	// Node is the complete validated salad node for the requirement, ready
	// for a downstream typed decode.
	Node salad.Node

	// ClassIRI is the resolved class discriminator — an extension IRI in a
	// namespace the document declared.
	ClassIRI string
}

// Class returns the extension class IRI this requirement declared.
func (r *RawRequirement) Class() string {
	return r.ClassIRI
}
