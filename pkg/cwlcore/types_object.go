package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// File and Directory value types. Pure data; no filesystem access.

// Compile-time interface assertions.
var (
	_ FileOrDirectory = (*File)(nil)
	_ FileOrDirectory = (*Directory)(nil)
)

const (
	// ClassFile is the class string for File values.
	ClassFile = PrimitiveFile
	// ClassDirectory is the class string for Directory values.
	ClassDirectory = PrimitiveDirectory
)

// FileOrDirectory is the `File | Directory` sealed interface.
type FileOrDirectory interface {
	// Class returns ClassFile or ClassDirectory.
	Class() string

	isFileOrDirectory()
}

// File describes a CWL file value.
type File struct {
	Node           salad.Node
	Location       string
	Path           string
	Basename       string
	Dirname        string
	Nameroot       string
	Nameext        string
	Checksum       string
	Format         string
	Size           OptInt
	Contents       OptString
	SecondaryFiles []FileOrDirectory
}

// Class returns ClassFile.
func (*File) Class() string {
	return ClassFile
}

// isFileOrDirectory seals FileOrDirectory.
func (*File) isFileOrDirectory() {}

// Directory describes a CWL directory value.
type Directory struct {
	Node     salad.Node
	Location string
	Path     string
	Basename string
	// Listing is nil when unset (fetch from Location), not when empty.
	Listing []FileOrDirectory
}

// Class returns ClassDirectory.
func (*Directory) Class() string {
	return ClassDirectory
}

// isFileOrDirectory seals FileOrDirectory.
func (*Directory) isFileOrDirectory() {}
