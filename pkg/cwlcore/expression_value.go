package cwlcore

import (
	"fmt"
	"maps"
	"math"
)

// Conversion between typed *File/*Directory values and the map[string]any
// shape CWL expressions read. Unset fields are omitted, not zeroed.

// Map capacity hints for File (12 fields) and Directory (5 fields).
const (
	fileFieldCount      = 12
	directoryFieldCount = 5
)

// maxSafeInteger is 2^53, the JavaScript safe integer limit.
const maxSafeInteger = 1 << 53

// ToExpressionValue converts *File/*Directory values (at any depth) to map form.
// Values without filesystem types pass through unchanged.
func ToExpressionValue(value any) any {
	converted, _ := toExpressionValue(value)

	return converted
}

// toExpressionValue is ToExpressionValue reporting whether it changed anything.
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

// convertedList renders each element, reusing the original slice if unchanged.
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

// convertedMap renders each field, reusing the original map if unchanged.
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

// filesystemObject renders a FileOrDirectory as a map. Nil renders as nil.
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

// fileObject renders a File as the map an expression reads.
// Size and Contents are written only when set.
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

// directoryObject renders a Directory as the map an expression reads.
// Nil Listing is omitted; empty Listing is written as [].
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

// putNameFields writes basename, nameroot and nameext together, or none.
// Gated on basename because the other two are derived from it.
func putNameFields(object map[string]any, file *File) {
	if file.Basename == "" {
		return
	}

	object[keyBasename] = file.Basename
	object[keyNameroot] = file.Nameroot
	object[keyNameext] = file.Nameext
}

// putNonEmpty records a string field, omitting empty values.
func putNonEmpty(object map[string]any, key, value string) {
	if value != "" {
		object[key] = value
	}
}

// filesystemView returns the map view of a *File or *Directory for asMap.
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

// FromExpressionValue converts map-form File/Directory objects back to typed values.
// Dispatches on the class field; errors wrap ErrExpressionEval.
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

// fieldReader reads fields of a filesystem object, recording the first type error.
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

// entries reads a secondaryFiles or listing field.
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

// present reports whether the field exists and is non-null.
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

// fromFilesystemEntry converts one File or Directory entry from a list.
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
// Accepts floats with no fractional part up to 2^53.
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
