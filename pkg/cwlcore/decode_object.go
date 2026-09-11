package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// Decoding File, Directory, and Dirent values from validated salad nodes.

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

// directory decodes a Directory value. Nil listing means "fetch from location".
func (d *decoder) directory(m *salad.MapNode) *Directory {
	return &Directory{
		Node:     m,
		Location: d.text(m, keyLocation),
		Path:     d.text(m, keyPath),
		Basename: d.text(m, keyBasename),
		Listing:  decodeEach(d.listItems(m, keyListing, "", ""), d.fileOrDirectory),
	}
}

// dirent decodes a Dirent from an InitialWorkDirRequirement.
func (d *decoder) dirent(m *salad.MapNode) *Dirent {
	return &Dirent{
		Entryname: d.expression(m, keyEntryname),
		Entry:     d.expression(m, keyEntry),
		Writable:  d.flag(m, keyWritable),
	}
}
