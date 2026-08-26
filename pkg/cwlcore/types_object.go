package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// File and Directory: the two CWL values that name something on a filesystem.
//
// These are values rather than document structure — they appear in a job's
// input object, in a tool's collected outputs, and as the result of an
// expression, far more often than they appear written out in a CWL document.
// They are modelled here anyway, for three reasons. The vendored schema defines
// them in Process.yml exactly like every other record. pkg/cwlexec needs them
// constantly, for staging, globbing, checksums, secondary files and output
// collection, and if they were not here it would grow its own idea of what a
// File is, which is precisely the sort of split that produces silent
// disagreement between two layers. And they give the file-format check in
// format.go a real contract to work against instead of an untyped map.
//
// Nothing here reads a filesystem. A File is a description of a file, and every
// field is exactly what the document or the runtime put there — none of the
// derived fields (basename, dirname, nameroot, nameext, checksum, size) is
// computed by this package. Populating them is the runner's job, and the
// specification is specific about when each must be filled in.

// Compile-time proof that both filesystem values satisfy FileOrDirectory.
var (
	_ FileOrDirectory = (*File)(nil)
	_ FileOrDirectory = (*Directory)(nil)
)

// Class discriminators for the two filesystem values. They are defined in terms
// of the CWLType symbols because they are the same strings: a File value's
// class field and the File type name are spelled identically, which is what
// lets `type: File` and `class: File` line up.
const (
	// ClassFile is the class of a File value.
	ClassFile = PrimitiveFile

	// ClassDirectory is the class of a Directory value.
	ClassDirectory = PrimitiveDirectory
)

// FileOrDirectory is a CWL value naming a filesystem object: a *File or a
// *Directory, and nothing else. The schema spells this union out by hand in
// both places it occurs — File.secondaryFiles and Directory.listing — which is
// what makes the two types mutually recursive.
//
// The interface is sealed by the unexported isFileOrDirectory method, so a type
// switch over it is exhaustive by construction:
//
//	switch v := object.(type) {
//	case *cwlcore.File:      // ...
//	case *cwlcore.Directory: // ...
//	}
type FileOrDirectory interface {
	// Class returns ClassFile or ClassDirectory.
	Class() string

	isFileOrDirectory()
}

// File describes a file, or a group of files when SecondaryFiles is populated,
// that a tool can open and read through the ordinary POSIX calls.
//
// A File is identified by Location, an IRI, or by Path, a filesystem path on
// the host running the tool. A File with neither is a *file literal*: it must
// carry Contents instead, and the runner creates it on disk when it is needed.
// That is why Contents is an OptString — an empty file literal writes
// `contents: ""`, which is a different thing from a File whose contents were
// never read.
type File struct {
	// Node is the validated salad node this value was decoded from, when it
	// came from a document rather than from the runtime. Nil otherwise.
	Node salad.Node

	// Location is an IRI identifying the file resource. It may be a relative
	// reference, resolved against the base IRI of the document it appears
	// in. Empty when the value is a file literal or carries only Path.
	Location string

	// Path is the local filesystem path at which the file is available when
	// the tool runs. Set by the implementation; the specification forbids
	// using it in any other context. Its final component must equal
	// Basename.
	Path string

	// Basename is the filename on disk, without any leading directory path,
	// and must not contain a slash. When absent from the document, the
	// implementation derives it from the final path component of Location.
	Basename string

	// Dirname is the directory containing the file, such that
	// Dirname + "/" + Basename == Path. Derived from Path by the
	// implementation before expressions are evaluated, and meaningful only
	// inside a CommandLineTool.
	Dirname string

	// Nameroot is the Basename with its extension removed, such that
	// Nameroot + Nameext == Basename. A leading period does not count as an
	// extension: ".cshrc" has nameroot ".cshrc" and an empty nameext.
	Nameroot string

	// Nameext is the Basename's extension, empty or beginning with a period
	// and containing at most one period, such that
	// Nameroot + Nameext == Basename.
	Nameext string

	// Checksum is a hash of the file's content, for integrity checks.
	// Currently it must be "sha1$" followed by a hexadecimal digest. An
	// implementation may skip computing it, but the ability to do so is
	// required to pass the CWL conformance suite.
	Checksum string

	// Format is an IRI naming the file's format, preferably a concept node
	// in an ontology named by the document's $schemas. Compatibility is
	// decided by exact match, owl:equivalentClass or rdfs:subClassOf; see
	// FormatOntology.
	Format string

	// Size is the file's size in bytes. It is an OptInt because 0 is an
	// ordinary value — an empty file — and must not be read as "not
	// computed".
	Size OptInt

	// Contents is the file's content as a literal. Setting it makes this a
	// file literal, which the runner creates on disk; it is also where a
	// loadContents read deposits the first 64 KiB of an existing file. It is
	// an OptString because "" is a legal value.
	Contents OptString

	// SecondaryFiles are files and directories that must be staged into the
	// same directory as this one — an index beside an alignment, say. An
	// entry may itself carry SecondaryFiles, and the same rules apply
	// recursively. Duplicate basenames within one list are an error.
	//
	// These are values. The *patterns* that decide which secondary files a
	// parameter requires are ParameterBase.SecondaryFiles, a different type.
	SecondaryFiles []FileOrDirectory
}

// Class returns ClassFile.
func (*File) Class() string {
	return ClassFile
}

// isFileOrDirectory seals the FileOrDirectory interface.
func (*File) isFileOrDirectory() {}

// Directory describes a directory to present to a tool.
//
// Like a File it is identified by Location or Path. A Directory with neither is
// a *directory literal* and must supply Listing, which the runner creates on
// disk. The entries of a Listing need bear no relation to each other's
// locations — they may be on different hosts — and staging them is the runner's
// problem.
//
// Its field set is deliberately much smaller than File's: the schema gives
// Directory only class, location, path, basename and listing. It has no size,
// no checksum, no format and no secondaryFiles.
type Directory struct {
	// Node is the validated salad node this value was decoded from, when it
	// came from a document rather than from the runtime. Nil otherwise.
	Node salad.Node

	// Location is an IRI identifying the directory resource. When Listing is
	// absent, the implementation uses this to retrieve the listing.
	Location string

	// Path is the local filesystem path at which the directory is available
	// when the tool runs. Set by the implementation; the specification
	// forbids using it in any other context.
	Path string

	// Basename is the directory's name without any leading path, and must
	// not contain a slash. When absent, the implementation derives it from
	// the final path component of Location.
	Basename string

	// Listing are the files and subdirectories this directory contains, each
	// named by its own Basename. Absent means the runner is expected to
	// fetch the listing from Location at runtime — which is not the same as
	// an empty directory, so a nil Listing must not be read as "no entries".
	//
	// Two entries sharing a basename are a fatal error, except that two
	// Directory entries sharing one are equivalent to a single subdirectory
	// with their listings recursively merged.
	Listing []FileOrDirectory
}

// Class returns ClassDirectory.
func (*Directory) Class() string {
	return ClassDirectory
}

// isFileOrDirectory seals the FileOrDirectory interface.
func (*Directory) isFileOrDirectory() {}
