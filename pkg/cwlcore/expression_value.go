package cwlcore

import (
	"fmt"
	"maps"
	"math"
)

// Conversion between the typed filesystem values and the object shape a CWL
// expression reads.
//
// The two representations exist for different jobs and neither can replace the
// other. Inside the engine a file is a *File: a struct with an OptInt size that
// can tell "empty" from "not measured", a sealed FileOrDirectory union, and a
// compiler that catches a misspelt field. Inside an expression a file is
// whatever `$(inputs.f.basename)` can reach, and the specification defines that
// as a plain object with a class discriminator — the same shape a document
// writes and the same shape JSON.stringify produces.
//
// So the evaluator converts at its boundary rather than asking every caller to
// hand it maps. That is also what keeps the specification's promise that a
// parameter reference means the same thing whether it is resolved natively or
// by the JavaScript engine: both paths read the object these functions build.
//
// Absence is the delicate part throughout. A field the runtime has not filled
// in is left out of the object entirely, so it reads as undefined rather than
// as a zero: an unmeasured size must not surface as 0 bytes, an unread
// directory listing must not surface as an empty directory, and unloaded
// contents must not surface as an empty file.

// Field counts of the two filesystem objects, used only to size the maps. Per
// the schema a File has twelve fields and a Directory five; the specification
// gives Directory no size, checksum, format or secondaryFiles.
const (
	fileFieldCount      = 12
	directoryFieldCount = 5
)

// maxSafeInteger is 2^53, past which a JavaScript number stops being an exact
// integer. It bounds what FromExpressionValue will read back as one, because
// beyond it the value an expression produced is already approximate.
const maxSafeInteger = 1 << 53

// ToExpressionValue renders value in the shape a CWL expression reads: a *File
// or *Directory — at any depth, inside lists, records and secondaryFiles —
// becomes a string-keyed object with a class field, and everything else is
// returned unchanged.
//
// The evaluator applies this to whatever a parameter reference resolves to, so
// callers rarely need it directly. It is exported for the ones that build a
// parameter context by hand, or that want the object form for their own
// reasons, so that there is one definition of what a File looks like to an
// expression rather than one per package.
//
// A value containing no typed filesystem values is returned as it is, not
// copied.
func ToExpressionValue(value any) any {
	converted, _ := toExpressionValue(value)

	return converted
}

// toExpressionValue is ToExpressionValue reporting whether it changed
// anything, which is what lets the collection cases skip allocating for the
// overwhelmingly common input that holds no File at all.
func toExpressionValue(value any) (any, bool) {
	switch typed := value.(type) {
	case FileOrDirectory:
		return filesystemObject(typed), true
	case File:
		return filesystemObject(&typed), true
	case Directory:
		return filesystemObject(&typed), true
	case []FileOrDirectory:
		return filesystemList(typed), true
	case []any:
		return convertedList(typed)
	case map[string]any:
		return convertedMap(typed)
	default:
		return value, false
	}
}

// convertedList renders each element, keeping the original slice when none of
// them needed it.
func convertedList(list []any) (any, bool) {
	var converted []any

	for i, item := range list {
		next, changed := toExpressionValue(item)
		if !changed {
			continue
		}

		if converted == nil {
			converted = make([]any, len(list))
			copy(converted, list)
		}

		converted[i] = next
	}

	if converted == nil {
		return list, false
	}

	return converted, true
}

// convertedMap renders each field, keeping the original map when none of them
// needed it.
func convertedMap(object map[string]any) (any, bool) {
	var converted map[string]any

	for key, item := range object {
		next, changed := toExpressionValue(item)
		if !changed {
			continue
		}

		if converted == nil {
			converted = make(map[string]any, len(object))
			maps.Copy(converted, object)
		}

		converted[key] = next
	}

	if converted == nil {
		return object, false
	}

	return converted, true
}

// filesystemObject renders one member of the FileOrDirectory union. A nil
// pointer inside the interface renders as the null value rather than panicking:
// a malformed job object must not take the process down.
func filesystemObject(object FileOrDirectory) any {
	switch typed := object.(type) {
	case *File:
		if typed == nil {
			return nil
		}

		return fileObject(typed)
	case *Directory:
		if typed == nil {
			return nil
		}

		return directoryObject(typed)
	default:
		return nil
	}
}

// filesystemList renders a secondaryFiles or listing slice.
func filesystemList(entries []FileOrDirectory) []any {
	list := make([]any, len(entries))
	for i, entry := range entries {
		list[i] = filesystemObject(entry)
	}

	return list
}

// fileObject renders a File as the object an expression reads.
//
// Size and Contents are written only when set. They are an OptInt and an
// OptString precisely because 0 and "" are ordinary values there — an empty
// file, and an empty file literal — so emitting the zero for an absent field
// would fabricate a measurement the runtime never made.
//
// The three name fields are the exception, and they travel as a group; see
// [putNameFields].
func fileObject(file *File) map[string]any {
	object := make(map[string]any, fileFieldCount)
	object[keyClass] = ClassFile

	putNonEmpty(object, keyLocation, file.Location)
	putNonEmpty(object, keyPath, file.Path)
	putNameFields(object, file)
	putNonEmpty(object, keyDirname, file.Dirname)
	putNonEmpty(object, keyChecksum, file.Checksum)
	putNonEmpty(object, keyFormat, file.Format)

	if file.Size.IsSet() {
		object[keySize] = file.Size.Int()
	}

	if file.Contents.IsSet() {
		object[keyContents] = file.Contents.Value()
	}

	if file.SecondaryFiles != nil {
		object[keySecondaryFiles] = filesystemList(file.SecondaryFiles)
	}

	return object
}

// directoryObject renders a Directory as the object an expression reads.
//
// A nil Listing is omitted, an empty one is written as []. The distinction is
// the specification's: absent means the runner has not fetched the listing
// from Location yet, which is not the same as a directory that is empty.
func directoryObject(dir *Directory) map[string]any {
	object := make(map[string]any, directoryFieldCount)
	object[keyClass] = ClassDirectory

	putNonEmpty(object, keyLocation, dir.Location)
	putNonEmpty(object, keyPath, dir.Path)
	putNonEmpty(object, keyBasename, dir.Basename)

	if dir.Listing != nil {
		object[keyListing] = filesystemList(dir.Listing)
	}

	return object
}

// putNameFields writes basename, nameroot and nameext together, or writes none
// of them.
//
// nameext is the one string field of a File whose empty value means something:
// it is the extension of a name that has none. Process.yml, nameroot: "The
// basename root such that `nameroot + nameext == basename`, and `nameext` is
// empty or begins with a period" — so for a file called README the identity
// only holds if nameext is present and empty, and an expression writing
// `$(self.nameroot).idx$(self.nameext)` needs it to substitute rather than
// fail on a missing key.
//
// That is also what the reference implementation produces. cwltool's
// normalizeFilesDirs (utils.py) does `nr, ne = os.path.splitext(d["basename"])`
// and assigns both unconditionally; os.path.splitext("README") is
// ("README", ""), and a Python dict stores the empty string as a present key.
//
// The group is gated on the basename because the other two are derived from it:
// a File whose basename the runtime has not settled has no name to split, and
// must not be given an empty one.
func putNameFields(object map[string]any, file *File) {
	if file.Basename == "" {
		return
	}

	object[keyBasename] = file.Basename
	object[keyNameroot] = file.Nameroot
	object[keyNameext] = file.Nameext
}

// putNonEmpty records a field, omitting it when the runtime has not filled it
// in. Every string field of a File and a Directory is an identifier or a
// derived name, and none of them has a meaningful empty value, so empty and
// absent are the same thing here.
func putNonEmpty(object map[string]any, key, value string) {
	if value != "" {
		object[key] = value
	}
}

// filesystemView returns the object view of a typed filesystem value. It is
// what lets the evaluator resolve a segment against a *File without the caller
// having converted anything: asMap consults it, which in turn gives the JSON
// encoder and TypeName the same understanding for free.
func filesystemView(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case *File:
		if typed == nil {
			return nil, false
		}

		return fileObject(typed), true
	case File:
		return fileObject(&typed), true
	case *Directory:
		if typed == nil {
			return nil, false
		}

		return directoryObject(typed), true
	case Directory:
		return directoryObject(&typed), true
	default:
		return nil, false
	}
}

// FromExpressionValue is the inverse of ToExpressionValue: it converts the
// objects an expression produced back into typed *File and *Directory values,
// recursively, and returns everything else unchanged.
//
// An object is recognised as a filesystem value by its class field, exactly as
// the specification says a document is read. An object with no class is an
// ordinary record and is walked for nested filesystem values.
//
// It reports an error wrapping ErrExpressionEval when an object claims to be a
// File or a Directory but does not hold up — a size that is not a whole
// number, a listing that is not a list, an entry that declares no class. Those
// are permanent failures: the expression ran, and produced something the engine
// cannot use.
func FromExpressionValue(value any) (any, error) {
	switch typed := value.(type) {
	case []any:
		return fromExpressionList(typed)
	case map[string]any:
		return fromExpressionObject(typed)
	default:
		return value, nil
	}
}

// fromExpressionList converts every element.
func fromExpressionList(list []any) (any, error) {
	converted := make([]any, len(list))

	for i, item := range list {
		value, err := FromExpressionValue(item)
		if err != nil {
			return nil, err
		}

		converted[i] = value
	}

	return converted, nil
}

// fromExpressionObject dispatches on the class discriminator.
func fromExpressionObject(object map[string]any) (any, error) {
	switch objectText(object, keyClass) {
	case ClassFile:
		return newFileValue(object)
	case ClassDirectory:
		return newDirectoryValue(object)
	default:
		return fromExpressionFields(object)
	}
}

// fromExpressionFields converts the members of an ordinary record.
func fromExpressionFields(object map[string]any) (any, error) {
	converted := make(map[string]any, len(object))

	for key, item := range object {
		value, err := FromExpressionValue(item)
		if err != nil {
			return nil, err
		}

		converted[key] = value
	}

	return converted, nil
}

// newFileValue builds a File from its object form.
func newFileValue(object map[string]any) (any, error) {
	reader := &fieldReader{object: object, err: nil}

	file := &File{
		Node:           nil,
		Location:       reader.text(keyLocation),
		Path:           reader.text(keyPath),
		Basename:       reader.text(keyBasename),
		Dirname:        reader.text(keyDirname),
		Nameroot:       reader.text(keyNameroot),
		Nameext:        reader.text(keyNameext),
		Checksum:       reader.text(keyChecksum),
		Format:         reader.text(keyFormat),
		Size:           reader.wholeNumber(keySize),
		Contents:       reader.optText(keyContents),
		SecondaryFiles: reader.entries(keySecondaryFiles),
	}

	if reader.err != nil {
		return nil, reader.err
	}

	return file, nil
}

// newDirectoryValue builds a Directory from its object form.
func newDirectoryValue(object map[string]any) (any, error) {
	reader := &fieldReader{object: object, err: nil}

	dir := &Directory{
		Node:     nil,
		Location: reader.text(keyLocation),
		Path:     reader.text(keyPath),
		Basename: reader.text(keyBasename),
		Listing:  reader.entries(keyListing),
	}

	if reader.err != nil {
		return nil, reader.err
	}

	return dir, nil
}

// fieldReader reads the fields of a filesystem object, remembering the first
// one that was the wrong type. Accumulating rather than returning at each
// field is what keeps the two constructors above readable as the field lists
// they are.
type fieldReader struct {
	object map[string]any
	err    error
}

// text reads a string field, absent as "".
func (r *fieldReader) text(key string) string {
	raw, present := r.present(key)
	if !present {
		return ""
	}

	value, ok := raw.(string)
	if !ok {
		r.fail(key, typeNameString, raw)

		return ""
	}

	return value
}

// optText reads a string field that distinguishes absent from empty.
func (r *fieldReader) optText(key string) OptString {
	raw, present := r.present(key)
	if !present {
		return OptString{value: "", set: false}
	}

	value, ok := raw.(string)
	if !ok {
		r.fail(key, typeNameString, raw)

		return OptString{value: "", set: false}
	}

	return NewOptString(value)
}

// wholeNumber reads an integer field that distinguishes absent from zero.
func (r *fieldReader) wholeNumber(key string) OptInt {
	raw, present := r.present(key)
	if !present {
		return OptInt{value: 0, set: false}
	}

	value, ok := asWholeNumber(raw)
	if !ok {
		r.fail(key, "a whole number", raw)

		return OptInt{value: 0, set: false}
	}

	return NewOptInt(value)
}

// entries reads a secondaryFiles or listing field. An absent field stays nil
// and an empty list stays empty, because for a listing the two mean different
// things.
func (r *fieldReader) entries(key string) []FileOrDirectory {
	raw, present := r.present(key)
	if !present {
		return nil
	}

	list, ok := asList(raw)
	if !ok {
		r.fail(key, typeNameList, raw)

		return nil
	}

	entries := make([]FileOrDirectory, 0, len(list))

	for _, item := range list {
		entry, err := fromFilesystemEntry(item)
		if err != nil {
			r.record(err)

			return nil
		}

		entries = append(entries, entry)
	}

	return entries
}

// present reports whether the object supplied the field at all. A null is
// treated as absent, which is how an expression spells "I did not set this".
func (r *fieldReader) present(key string) (any, bool) {
	raw, ok := r.object[key]

	return raw, ok && raw != nil
}

// fail records a field of the wrong type.
func (r *fieldReader) fail(key, want string, raw any) {
	r.record(fmt.Errorf("%w: %s must be %s, not %s", ErrExpressionEval, key, want, TypeName(raw)))
}

// record keeps the first error seen.
func (r *fieldReader) record(err error) {
	if r.err == nil {
		r.err = err
	}
}

// fromFilesystemEntry converts one member of a secondaryFiles or listing list,
// which may be either a File or a Directory and may nest further.
func fromFilesystemEntry(value any) (FileOrDirectory, error) {
	object, ok := asMap(value)
	if !ok {
		return nil, fmt.Errorf("%w: a %s or %s entry must be an object, not %s",
			ErrExpressionEval, ClassFile, ClassDirectory, TypeName(value))
	}

	converted, err := fromExpressionObject(object)
	if err != nil {
		return nil, err
	}

	entry, ok := converted.(FileOrDirectory)
	if !ok {
		return nil, fmt.Errorf("%w: a %s or %s entry must declare its class",
			ErrExpressionEval, ClassFile, ClassDirectory)
	}

	return entry, nil
}

// objectText reads a string field of an object, or "" for anything else.
func objectText(object map[string]any, key string) string {
	value, ok := object[key].(string)
	if !ok {
		return ""
	}

	return value
}

// asWholeNumber reads a JSON number as an exact integer.
//
// A float is accepted when it has no fractional part and is small enough to be
// exact, because JavaScript has one number type and an expression that
// computed a size arrives holding a float. Beyond 2^53 it refuses: the value
// is already approximate, and silently truncating it would invent a file size.
func asWholeNumber(value any) (int64, bool) {
	switch number := value.(type) {
	case int:
		return int64(number), true
	case int32:
		return int64(number), true
	case int64:
		return number, true
	case float64:
		if number == math.Trunc(number) && math.Abs(number) <= maxSafeInteger {
			return int64(number), true
		}
	default:
	}

	return 0, false
}
