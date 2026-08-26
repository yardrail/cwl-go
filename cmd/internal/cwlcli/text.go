package cwlcli

import (
	"fmt"
	"strconv"
	"strings"
)

// The text renderer's fixed vocabulary: one indent level, the sequence bullet
// and the continuation that keeps a bulleted item's later lines aligned under
// its first, and the markers for a composite with nothing in it.
const (
	textIndent   = "  "
	textBullet   = "- "
	textContinue = "  "
	emptyObject  = "{}"
	emptySeq     = "[]"
	nullText     = "null"
)

// Text renders v as an indented outline, in the same order [JSON] would use.
//
// The shape is deliberately YAML-like, because a CWL developer reading a dump
// of a CWL document already reads YAML all day. It is a rendering, not a
// serialization: it round-trips nothing and must not be parsed.
func Text(v any) string {
	return strings.Join(textLines(v), "\n")
}

// textLines renders v as unindented lines. A single-line result is one the
// caller may inline after a key or a bullet; a multi-line one must be nested.
func textLines(v any) []string {
	switch t := v.(type) {
	case *Object:
		return objectLines(t)
	case []any:
		return sliceLines(t)
	default:
		return []string{scalarText(v)}
	}
}

// objectLines renders an object as "key: value" lines, nesting any value that
// does not fit on one line under its key.
func objectLines(o *Object) []string {
	if o.Len() == 0 {
		return []string{emptyObject}
	}

	out := make([]string, 0, o.Len())

	for _, entry := range o.Entries() {
		sub := textLines(entry.Value)
		if len(sub) == 1 && !isCollection(entry.Value) {
			out = append(out, entry.Key+": "+sub[0])

			continue
		}

		out = append(out, entry.Key+":")
		for _, line := range sub {
			out = append(out, textIndent+line)
		}
	}

	return out
}

// sliceLines renders a slice as bulleted items, aligning the later lines of a
// multi-line item under the first.
func sliceLines(items []any) []string {
	if len(items) == 0 {
		return []string{emptySeq}
	}

	out := make([]string, 0, len(items))

	for _, item := range items {
		for i, line := range textLines(item) {
			if i == 0 {
				out = append(out, textBullet+line)

				continue
			}

			out = append(out, textContinue+line)
		}
	}

	return out
}

// isCollection reports whether v is a non-empty object or slice.
//
// A one-entry collection renders on one line, and inlining it after its key
// would spell a list the same way as a scalar — "intent: - urn:x" reads as
// neither. Collections therefore nest whatever their length, and only the
// empty markers, which are unambiguous, stay inline.
func isCollection(v any) bool {
	switch t := v.(type) {
	case *Object:
		return t.Len() > 0
	case []any:
		return len(t) > 0
	default:
		return false
	}
}

// scalarText renders a leaf value. A string is written bare, which is what
// makes the output readable, except when writing it bare would be ambiguous —
// when it is empty or spans lines — in which case it is quoted.
func scalarText(v any) string {
	if v == nil {
		return nullText
	}

	s, ok := v.(string)
	if !ok {
		return fmt.Sprintf("%v", v)
	}

	if s == "" || strings.ContainsAny(s, "\n\r") {
		return strconv.Quote(s)
	}

	return s
}
