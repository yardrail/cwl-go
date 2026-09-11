package cwlcli

import (
	"fmt"
	"strconv"
	"strings"
)

// Text renderer constants.
const (
	textIndent   = "  "
	textBullet   = "- "
	textContinue = "  "
	emptyObject  = "{}"
	emptySeq     = "[]"
	nullText     = "null"
)

// Text renders v as a YAML-like indented outline.
func Text(v any) string {
	return strings.Join(textLines(v), "\n")
}

// textLines renders v as unindented lines.
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

// objectLines renders an object as "key: value" lines.
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

// sliceLines renders a slice as bulleted items.
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

// scalarText renders a leaf value. Empty or multiline strings are quoted.
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
