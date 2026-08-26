package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// File, Directory and Dirent: the CWL values the schema defines as records, and
// so the only ones this package decodes into typed structs. Every other runtime
// value stays a salad.Node, because the schema types it as Any.
//
// Nothing here reads a filesystem. Each field is exactly what the document put
// there; none of the derived fields is computed.

// Keys of the two filesystem values and of a Dirent.
const (
	keyLocation  = "location"
	keyPath      = "path"
	keyBasename  = "basename"
	keyDirname   = "dirname"
	keyNameroot  = "nameroot"
	keyNameext   = "nameext"
	keyChecksum  = "checksum"
	keySize      = "size"
	keyContents  = "contents"
	keyListing   = "listing"
	keyEntry     = "entry"
	keyEntryname = "entryname"
	keyWritable  = "writable"
)

// fileOrDirectory decodes a value the schema types as `File | Directory`.
func (d *decoder) fileOrDirectory(node salad.Node) FileOrDirectory {
	m := d.mapping(node, "a File or Directory value")
	if m == nil {
		return nil
	}

	switch shortName(d.text(m, keyClass)) {
	case ClassFile:
		return d.file(m)
	case ClassDirectory:
		return d.directory(m)
	default:
		d.failf(m.Loc(), "a filesystem value must declare a class of %q or %q", ClassFile, ClassDirectory)

		return nil
	}
}

// file decodes a File value.
//
// size is an OptInt and contents an OptString because zero and "" are ordinary
// values there — an empty file, and an empty file literal — and must not be read
// as "not computed" and "not loaded".
func (d *decoder) file(m *salad.MapNode) *File {
	return &File{
		Node:           m,
		Location:       d.text(m, keyLocation),
		Path:           d.text(m, keyPath),
		Basename:       d.text(m, keyBasename),
		Dirname:        d.text(m, keyDirname),
		Nameroot:       d.text(m, keyNameroot),
		Nameext:        d.text(m, keyNameext),
		Checksum:       d.text(m, keyChecksum),
		Format:         d.text(m, keyFormat),
		Size:           d.optInt(m, keySize),
		Contents:       d.optText(m, keyContents),
		SecondaryFiles: decodeEach(d.oneOrMany(m, keySecondaryFiles), d.fileOrDirectory),
	}
}

// directory decodes a Directory value.
//
// An absent listing decodes to nil rather than to an empty slice, because the
// two mean different things: nil says the runner is expected to fetch the
// listing from the location, and empty says the directory has no entries.
func (d *decoder) directory(m *salad.MapNode) *Directory {
	return &Directory{
		Node:     m,
		Location: d.text(m, keyLocation),
		Path:     d.text(m, keyPath),
		Basename: d.text(m, keyBasename),
		Listing:  decodeEach(d.listItems(m, keyListing, "", ""), d.fileOrDirectory),
	}
}

// dirent decodes a Dirent: one entry an InitialWorkDirRequirement stages into
// the tool's working directory.
func (d *decoder) dirent(m *salad.MapNode) *Dirent {
	return &Dirent{
		Entryname: d.expression(m, keyEntryname),
		Entry:     d.expression(m, keyEntry),
		Writable:  d.flag(m, keyWritable),
	}
}
