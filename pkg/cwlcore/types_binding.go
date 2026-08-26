package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// The small structural records the rest of the model composes from: bindings,
// secondary-file patterns, environment definitions, software packages and
// directory entries. They are plain structs with exported fields, per the
// convention set out in types_value.go — only unions are opaque.

// InputBinding describes how a workflow-level input parameter's value is
// prepared. Its only field is loadContents; the command-line-specific fields
// live on CommandLineBinding, which extends it.
//
// It appears on WorkflowInputParameter, where the CWL v1.2 specification notes
// that "InputBinding is not used or allowed" as a tool binding — it is kept in
// the model because the schema declares it.
type InputBinding struct {
	// LoadContents requests that a File value's first 64 KiB be read into
	// its contents field before expressions run.
	LoadContents bool
}

// CommandLineBinding describes how a value becomes one or more command-line
// arguments, and appears on CommandInputParameter, on record/enum/array schema
// types, and as an entry of CommandLineTool.arguments.
//
// Two of its fields default to true rather than false, so they are OptBool
// rather than bool: read them with Separate.Or(true) and ShellQuote.Or(true).
type CommandLineBinding struct {
	// Prefix is the command-line flag emitted before the value, for example
	// "--threads".
	Prefix string

	// ItemSeparator joins the elements of an array value into a single
	// argument. When empty, each element becomes its own argument.
	ItemSeparator string

	// ValueFrom replaces the parameter's value for binding purposes. Left
	// unevaluated here; pkg/cwlexec evaluates it against the input object.
	ValueFrom Expression

	// Position orders this binding among its siblings. The schema default is
	// 0. It may be an expression, so it is an ExprLong rather than an int.
	Position ExprLong

	// Separate controls whether the prefix and the value are emitted as two
	// arguments (true) or concatenated into one (false). Schema default:
	// true.
	Separate OptBool

	// ShellQuote controls whether the argument is shell-quoted when a
	// ShellCommandRequirement is in scope. Schema default: true.
	ShellQuote OptBool

	// LoadContents requests that a File value's first 64 KiB be read into
	// its contents field. Inherited from InputBinding.
	LoadContents bool
}

// CommandOutputBinding describes how an output parameter's value is collected
// from the tool's output directory after it exits.
type CommandOutputBinding struct {
	// OutputEval is an expression computing the parameter's final value,
	// evaluated with self bound to the globbed files. Left unevaluated here.
	OutputEval Expression

	// LoadListing is how deeply a globbed Directory's listing is populated.
	LoadListing LoadListingEnum

	// Glob selects the files to collect, relative to the output directory.
	// The schema allows a single pattern, a list of them, or an expression
	// producing either; all three are normalized to this slice, with an
	// expression appearing as a single expression-bearing entry.
	Glob []Expression

	// LoadContents requests that each globbed File's first 64 KiB be read
	// into its contents field before OutputEval runs.
	LoadContents bool
}

// SecondaryFileSchema declares a file that must accompany a File value — an
// index next to an alignment, say — as a pattern applied to the primary file's
// name.
//
// The pattern language has two forms: a suffix, optionally prefixed by one or
// more "^" characters that each strip one extension from the primary file's
// name, or an expression producing a name, a File, or a list of either.
type SecondaryFileSchema struct {
	// Pattern is the secondary-file pattern. Required by the schema.
	Pattern Expression

	// Required declares whether a missing secondary file is an error. It may
	// be an expression, so it is an ExprBool. When unset, the CWL default
	// depends on context: required on an input, optional on an output.
	Required ExprBool
}

// EnvironmentDef is one entry of an EnvVarRequirement: an environment variable
// to set in the tool's execution environment.
type EnvironmentDef struct {
	// EnvName is the variable's name. Required by the schema.
	EnvName string

	// EnvValue is the variable's value, left unevaluated here because it may
	// be an expression.
	EnvValue Expression
}

// SoftwarePackage is one entry of a SoftwareRequirement: a software package the
// tool needs available in its execution environment.
type SoftwarePackage struct {
	// Package is the package name. Required by the schema.
	Package string

	// Version lists the acceptable versions. An empty slice means any
	// version is acceptable.
	Version []string

	// Specs lists IRIs identifying the package in resolvable registries —
	// RRID, bio.tools, Debian, and so on. They are advisory: an
	// implementation may use them to locate the package, and must not fail
	// merely because it does not recognize one.
	Specs []string
}

// Dirent is one entry of an InitialWorkDirRequirement listing: a file or
// directory to create in the tool's working directory before it runs.
//
// The schema declares Dirent in CommandLineTool.yml. It is reached through
// InitialWorkDirEntry rather than directly, because a listing entry may also be
// an expression or a File/Directory value.
type Dirent struct {
	// Entryname is the name the entry is created under, relative to the
	// output directory. When empty, the name comes from Entry's own basename,
	// which the schema requires Entry to supply in that case.
	Entryname Expression

	// Entry is the content to stage: a string written as a file's contents,
	// or an expression producing a File or Directory to place there.
	// Required by the schema.
	Entry Expression

	// Writable declares whether the tool may modify the staged entry. Schema
	// default: false, so the Go zero value is already correct.
	Writable bool
}

// RawHint is the fallback for a hints entry this package has no concrete type
// for — either an extension class, or a core requirement class appearing in a
// context where it is only advisory.
//
// Hints never fail decoding, by design: the CWL specification requires an
// implementation to ignore a hint it does not understand rather than reject the
// document. A downstream package recognizes its own classes by comparing Class
// and decoding Node itself.
type RawHint struct {
	// Node is the complete validated salad node for the hint, ready for a
	// downstream typed decode.
	Node salad.Node

	// ClassIRI is the resolved class discriminator, for example an extension
	// IRI in a namespace the document declared.
	ClassIRI string
}

// Class returns the hint's resolved class discriminator.
func (h *RawHint) Class() string {
	return h.ClassIRI
}
