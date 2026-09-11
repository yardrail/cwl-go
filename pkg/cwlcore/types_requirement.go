package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// Core CWL v1.2 requirement types. All fields are unevaluated; scoping is in requirements.go.

// Requirement class discriminators.
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

// Compile-time interface checks.
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

// requirementBase seals ProcessRequirement. Zero-size embed.
type requirementBase struct{}

// isRequirement seals the ProcessRequirement interface.
func (requirementBase) isRequirement() {}

// InlineJavascriptRequirement enables ${...} JavaScript expressions.
type InlineJavascriptRequirement struct {
	requirementBase

	// ExpressionLib holds JavaScript fragments evaluated before each expression.
	ExpressionLib []string
}

// Class returns ClassInlineJavascriptRequirement.
func (*InlineJavascriptRequirement) Class() string {
	return ClassInlineJavascriptRequirement
}

// SchemaDefRequirement declares named types for parameters to reference.
type SchemaDefRequirement struct {
	requirementBase

	// Types are the declared type schemas as validated salad nodes.
	Types []salad.Node
}

// Class returns ClassSchemaDefRequirement.
func (*SchemaDefRequirement) Class() string {
	return ClassSchemaDefRequirement
}

// LoadListingRequirement sets the default Directory listing depth.
type LoadListingRequirement struct {
	requirementBase

	// LoadListing is the default listing depth.
	LoadListing LoadListingEnum
}

// Class returns ClassLoadListingRequirement.
func (*LoadListingRequirement) Class() string {
	return ClassLoadListingRequirement
}

// DockerRequirement declares a software container for the tool.
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

	// DockerImageID is the image identifier to run or tag to apply.
	DockerImageID string

	// DockerOutputDirectory overrides the container's output directory mount path.
	DockerOutputDirectory string
}

// Class returns ClassDockerRequirement.
func (*DockerRequirement) Class() string {
	return ClassDockerRequirement
}

// SoftwareRequirement declares required software packages.
type SoftwareRequirement struct {
	requirementBase

	// Packages are the required packages.
	Packages []SoftwarePackage
}

// Class returns ClassSoftwareRequirement.
func (*SoftwareRequirement) Class() string {
	return ClassSoftwareRequirement
}

// InitialWorkDirRequirement stages files into the working directory.
type InitialWorkDirRequirement struct {
	requirementBase

	// Listing is what to stage: a list of entries or an expression.
	Listing InitialWorkDirListing
}

// Class returns ClassInitialWorkDirRequirement.
func (*InitialWorkDirRequirement) Class() string {
	return ClassInitialWorkDirRequirement
}

// EnvVarRequirement sets environment variables for the tool.
type EnvVarRequirement struct {
	requirementBase

	// EnvDef are the variables to set.
	EnvDef []EnvironmentDef
}

// Class returns ClassEnvVarRequirement.
func (*EnvVarRequirement) Class() string {
	return ClassEnvVarRequirement
}

// ShellCommandRequirement enables shell interpretation of the command line.
type ShellCommandRequirement struct {
	requirementBase
}

// Class returns ClassShellCommandRequirement.
func (*ShellCommandRequirement) Class() string {
	return ClassShellCommandRequirement
}

// ResourceRequirement declares CPU, memory and disk bounds.
// Unset fields mean not specified, not zero.
type ResourceRequirement struct {
	requirementBase

	// CoresMin is the minimum CPU cores (may be fractional).
	CoresMin ResourceValue

	// CoresMax is the maximum reserved number of CPU cores.
	CoresMax ResourceValue

	// RAMMin is the minimum RAM in MiB.
	RAMMin ResourceValue

	// RAMMax is the maximum RAM in MiB.
	RAMMax ResourceValue

	// TmpdirMin is the minimum temp directory space in MiB.
	TmpdirMin ResourceValue

	// TmpdirMax is the maximum temp directory space in MiB.
	TmpdirMax ResourceValue

	// OutdirMin is the minimum output directory space in MiB.
	OutdirMin ResourceValue

	// OutdirMax is the maximum output directory space in MiB.
	OutdirMax ResourceValue
}

// Class returns ClassResourceRequirement.
func (*ResourceRequirement) Class() string {
	return ClassResourceRequirement
}

// WorkReuse controls result caching of previous identical runs.
type WorkReuse struct {
	requirementBase

	// EnableReuse permits reuse. Schema default is true.
	EnableReuse ExprBool
}

// Class returns ClassWorkReuse.
func (*WorkReuse) Class() string {
	return ClassWorkReuse
}

// NetworkAccess declares whether the tool needs network access.
type NetworkAccess struct {
	requirementBase

	// NetworkAccess grants network access.
	NetworkAccess ExprBool
}

// Class returns ClassNetworkAccess.
func (*NetworkAccess) Class() string {
	return ClassNetworkAccess
}

// InplaceUpdateRequirement permits in-place modification of input files.
type InplaceUpdateRequirement struct {
	requirementBase

	// InplaceUpdate permits in-place modification.
	InplaceUpdate bool
}

// Class returns ClassInplaceUpdateRequirement.
func (*InplaceUpdateRequirement) Class() string {
	return ClassInplaceUpdateRequirement
}

// ToolTimeLimit caps how long a tool may run.
type ToolTimeLimit struct {
	requirementBase

	// Timelimit is the limit in seconds. Zero means no limit.
	Timelimit ExprLong
}

// Class returns ClassToolTimeLimit.
func (*ToolTimeLimit) Class() string {
	return ClassToolTimeLimit
}

// SubworkflowFeatureRequirement permits steps to run Workflows.
type SubworkflowFeatureRequirement struct {
	requirementBase
}

// Class returns ClassSubworkflowFeatureRequirement.
func (*SubworkflowFeatureRequirement) Class() string {
	return ClassSubworkflowFeatureRequirement
}

// ScatterFeatureRequirement permits steps to scatter.
type ScatterFeatureRequirement struct {
	requirementBase
}

// Class returns ClassScatterFeatureRequirement.
func (*ScatterFeatureRequirement) Class() string {
	return ClassScatterFeatureRequirement
}

// MultipleInputFeatureRequirement permits sinks to have multiple sources.
type MultipleInputFeatureRequirement struct {
	requirementBase
}

// Class returns ClassMultipleInputFeatureRequirement.
func (*MultipleInputFeatureRequirement) Class() string {
	return ClassMultipleInputFeatureRequirement
}

// StepInputExpressionRequirement permits valueFrom on step inputs.
type StepInputExpressionRequirement struct {
	requirementBase
}

// Class returns ClassStepInputExpressionRequirement.
func (*StepInputExpressionRequirement) Class() string {
	return ClassStepInputExpressionRequirement
}

// RawRequirement is the fallback for unmodeled requirement classes (extensions).
type RawRequirement struct {
	requirementBase

	// Node is the validated salad node for downstream decoding.
	Node salad.Node

	// ClassIRI is the resolved extension class IRI.
	ClassIRI string
}

// Class returns the extension class IRI this requirement declared.
func (r *RawRequirement) Class() string {
	return r.ClassIRI
}
