package salad

import (
	"fmt"
	"strings"
)

// maxListedFields bounds how many field names an "expected one of" message
// spells out before it trails off, so that a record with dozens of fields does
// not bury the diagnostic that matters.
const maxListedFields = 12

// reservedFieldPrefixes are the leading characters that mark a key as a
// directive rather than a field. The specification says "other directives
// beginning with $ must be ignored", and JSON-LD keywords beginning with @ are
// likewise not fields of the record.
const reservedFieldPrefixes = "$@"

// checkRecord validates n against a record type.
func (v *validator) checkRecord(r *RecordType, n Node) *Error {
	if r.Abstract {
		return v.checkAbstract(r, n)
	}

	m, ok := AsMap(n)
	if !ok {
		return v.wrongType(n, typeLabel(r))
	}

	children := make([]*Error, 0, len(r.Fields))

	for _, f := range r.Fields {
		e := v.checkField(f, m)
		if e == nil {
			continue
		}

		if v.quiet {
			return errNoMatch
		}

		children = append(children, e)
	}

	children = append(children, v.checkUnknownFields(r, m)...)

	return v.group(nodeLoc(n), "", children...)
}

// checkField validates one declared field of a record against the value the
// document supplies for it, if any.
//
// A field the document omits is considered null, per the specification, and is
// therefore valid exactly when the field's type accepts null. Absence is
// reported as a missing field rather than as a type mismatch against nothing,
// because that is what the reader has to fix.
func (v *validator) checkField(f *Field, m *MapNode) *Error {
	value, present := lookupField(f, m)
	if !present {
		if acceptsNull(f.Type) {
			return nil
		}

		return v.fail(m.Loc(), "the required field %q is missing; it must be %s", f.ShortName(), typeLabel(f.Type))
	}

	return v.group(
		nodeLoc(value),
		fmt.Sprintf(msgFieldContext, f.ShortName()),
		v.check(f.Type, value),
		v.checkLink(f, value),
	)
}

// lookupField finds the value a document supplies for a field, under either the
// field's full identifier or its short name.
func lookupField(f *Field, m *MapNode) (Node, bool) {
	if n, ok := m.Get(f.Name); ok {
		return n, true
	}

	return m.Get(f.ShortName())
}

// acceptsNull reports whether a declared type admits a null value, which is how
// Schema Salad spells an optional field.
func acceptsNull(t Type) bool {
	switch tt := t.(type) {
	case *PrimitiveType:
		return tt.Kind == PrimitiveNull
	case *UnionType:
		return tt.HasNull()
	default:
		return false
	}
}

// checkUnknownFields reports every key of the document that the record does not
// declare.
func (v *validator) checkUnknownFields(r *RecordType, m *MapNode) []*Error {
	out := make([]*Error, 0)

	for key, value := range m.All() {
		if isReservedKey(key) {
			continue
		}

		if _, ok := r.Field(key); ok {
			continue
		}

		out = append(out, v.reportUnknownField(r, key, value))
	}

	return out
}

// reportUnknownField raises the diagnostic for one unrecognized key.
//
// A key carrying a namespace prefix is a property of a foreign vocabulary: the
// schema cannot say anything about it, so it is governed by StrictForeign. Any
// other unrecognized key is a plain mistake in a field name, and is governed by
// Strict.
func (v *validator) reportUnknownField(r *RecordType, key string, value Node) *Error {
	loc := nodeLoc(value)

	if isForeignProperty(key) {
		return v.diag(v.foreignSeverity(), loc,
			"the field %q comes from a foreign vocabulary and is not declared by %s", key, typeLabel(r))
	}

	return v.diag(v.strictSeverity(), loc,
		"the field %q is not declared by %s; expected one of: %s", key, typeLabel(r), fieldNames(r))
}

// isReservedKey reports whether a key is a directive rather than a field.
func isReservedKey(key string) bool {
	return key != "" && strings.ContainsRune(reservedFieldPrefixes, rune(key[0]))
}

// isForeignProperty reports whether a key names a property of another
// vocabulary, which Schema Salad spells with a namespace prefix.
func isForeignProperty(key string) bool {
	return strings.IndexByte(key, ':') > 0
}

// fieldNames lists a record's field names for an "expected one of" message,
// trailing off after maxListedFields of them.
func fieldNames(r *RecordType) string {
	names := make([]string, 0, len(r.Fields))

	for _, f := range r.Fields {
		if len(names) == maxListedFields {
			names = append(names, labelEllipsis)

			break
		}

		names = append(names, f.ShortName())
	}

	return strings.Join(names, ", ")
}
