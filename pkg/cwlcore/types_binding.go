package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// Structural records: bindings, secondary-file patterns, environment defs, etc.

// InputBinding carries loadContents for workflow-level input parameters.
type InputBinding struct {
	LoadContents bool
}

// CommandLineBinding describes how a value becomes command-line arguments.
type CommandLineBinding struct {
	Prefix        string
	ItemSeparator string
	ValueFrom     Expression
	Position      ExprLong
	Separate      OptBool // schema default: true
	ShellQuote    OptBool // schema default: true
	LoadContents  bool
}

// CommandOutputBinding describes how an output is collected from the output directory.
type CommandOutputBinding struct {
	OutputEval   Expression
	LoadListing  LoadListingEnum
	Glob         []Expression
	LoadContents bool
}

// SecondaryFileSchema declares a file that must accompany a primary File value.
type SecondaryFileSchema struct {
	Pattern  Expression
	Required ExprBool
}

// EnvironmentDef is an environment variable entry in EnvVarRequirement.
type EnvironmentDef struct {
	EnvName  string
	EnvValue Expression
}

// SoftwarePackage is an entry in SoftwareRequirement.
type SoftwarePackage struct {
	Package string
	Version []string
	Specs   []string
}

// Dirent is an entry to create in the working directory before tool execution.
type Dirent struct {
	Entryname Expression
	Entry     Expression
	Writable  bool
}

// RawHint is a hints entry with an unrecognized class.
type RawHint struct {
	Node     salad.Node
	ClassIRI string
}

// Class returns the hint's class discriminator.
func (h *RawHint) Class() string {
	return h.ClassIRI
}
