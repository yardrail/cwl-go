package salad

import (
	"fmt"
	"strings"
)

// maxListedFields bounds how many field names appear in "expected one of" messages.
const maxListedFields = 12

// reservedFieldPrefixes marks keys as directives ($ and @), not record fields.
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

// checkField validates one declared field. Missing fields are treated as null.
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

// lookupField finds a field's value by full or short name.
func lookupField(f *Field, m *MapNode) (Node, bool) {
	if n, ok := m.Get(f.Name); ok {
		return n, true
	}

	return m.Get(f.ShortName())
}

// acceptsNull reports whether a type admits null (i.e. is optional).
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

// checkUnknownFields reports keys the record does not declare.
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

// isForeignProperty reports whether a key has a namespace prefix.
func isForeignProperty(key string) bool {
	return strings.IndexByte(key, ':') > 0
}

// fieldNames lists a record's field names for diagnostic messages.
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
