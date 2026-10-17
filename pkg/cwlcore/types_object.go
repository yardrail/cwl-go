package cwlcore

import (
	"net/url"
	"path"
	"path/filepath"

	"github.com/yardrail/cwl-go/pkg/salad"
)

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

const schemeFile = "file"

// IsRemoteScheme reports whether p begins with a non-file URI scheme.
func IsRemoteScheme(p string) bool {
	parsed, err := url.Parse(p)
	if err != nil || parsed == nil {
		return false
	}

	return parsed.Scheme != "" && parsed.Scheme != schemeFile
}

func fileURI(local string) string {
	uri := url.URL{
		Scheme:      schemeFile,
		Opaque:      "",
		User:        nil,
		Host:        "",
		Path:        local,
		RawPath:     "",
		OmitHost:    false,
		ForceQuery:  false,
		RawQuery:    "",
		Fragment:    "",
		RawFragment: "",
	}

	return uri.String()
}

func pathDirname(p string) string {
	if p == "" {
		return ""
	}

	return filepath.Dir(p)
}

// PathBasename returns the final component of a path or URI.
func PathBasename(p string) string {
	if p == "" {
		return ""
	}

	if IsRemoteScheme(p) {
		return path.Base(p)
	}

	return filepath.Base(p)
}

// File describes a CWL file value. Use [SetPath] or [SetPathAndLocation]
// to set path-related fields; direct field access is via getters.
type File struct {
	Node     salad.Node
	path     string
	location string
	Basename string
	dirname  string
	Nameroot string
	Nameext  string
	Checksum string
	Format   string
	Size     OptInt
	Contents OptString

	SecondaryFiles []FileOrDirectory
}

// NewFileAtPath returns a File with path, location, dirname, and
// basename all derived from the given path.
func NewFileAtPath(p string) *File {
	f := &File{
		Node:           nil,
		path:           "",
		location:       "",
		Basename:       PathBasename(p),
		dirname:        "",
		Nameroot:       "",
		Nameext:        "",
		Checksum:       "",
		Format:         "",
		Size:           OptInt{value: 0, set: false},
		Contents:       OptString{value: "", set: false},
		SecondaryFiles: nil,
	}
	f.SetPath(p)

	return f
}

// Path returns the file's resolved filesystem path.
func (f *File) Path() string { return f.path }

// Location returns the file's URI.
func (f *File) Location() string { return f.location }

// Dirname returns the directory component of the file's path.
func (f *File) Dirname() string { return f.dirname }

// SetPath sets path and derives location and dirname atomically.
func (f *File) SetPath(p string) {
	f.path = p
	f.dirname = pathDirname(p)

	switch {
	case p == "":
		// leave location unchanged
	case IsRemoteScheme(p):
		f.location = p
	default:
		f.location = fileURI(p)
	}
}

// SetLocation sets location independently of path. Use only during
// document loading or expression evaluation where the CWL source
// provides location as a distinct value.
func (f *File) SetLocation(loc string) {
	f.location = loc
}

// SetPathAndLocation sets both path and location independently,
// plus derives dirname from path. Use for document loading and
// expression retyping where both fields come from the source.
func (f *File) SetPathAndLocation(p, loc string) {
	f.path = p
	f.location = loc
	f.dirname = pathDirname(p)
}

// Class returns ClassFile.
func (*File) Class() string {
	return ClassFile
}

// isFileOrDirectory seals FileOrDirectory.
func (*File) isFileOrDirectory() {}

// Directory describes a CWL directory value. Use [SetPath] or
// [SetPathAndLocation] to set path-related fields.
type Directory struct {
	Node     salad.Node
	path     string
	location string
	Basename string
	// Listing is nil when unset (fetch from Location), not when empty.
	Listing []FileOrDirectory
}

// NewDirectoryAtPath returns a Directory with path, location, and
// basename all derived from the given path.
func NewDirectoryAtPath(p string) *Directory {
	d := &Directory{
		Node:     nil,
		path:     "",
		location: "",
		Basename: PathBasename(p),
		Listing:  nil,
	}
	d.SetPath(p)

	return d
}

// Path returns the directory's resolved filesystem path.
func (d *Directory) Path() string { return d.path }

// Location returns the directory's URI.
func (d *Directory) Location() string { return d.location }

// SetPath sets path and derives location atomically.
func (d *Directory) SetPath(p string) {
	d.path = p

	switch {
	case p == "":
		// leave location unchanged
	case IsRemoteScheme(p):
		d.location = p
	default:
		d.location = fileURI(p)
	}
}

// SetLocation sets location independently of path.
func (d *Directory) SetLocation(loc string) {
	d.location = loc
}

// SetPathAndLocation sets both path and location independently.
func (d *Directory) SetPathAndLocation(p, loc string) {
	d.path = p
	d.location = loc
}

// Class returns ClassDirectory.
func (*Directory) Class() string {
	return ClassDirectory
}

// isFileOrDirectory seals FileOrDirectory.
func (*Directory) isFileOrDirectory() {}
