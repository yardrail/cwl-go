package salad

import "fmt"

// checkArray validates that n is a sequence whose every item matches the array's
// item type.
func (v *validator) checkArray(a *ArrayType, n Node) *Error {
	seq, ok := AsSeq(n)
	if !ok {
		return v.wrongType(n, typeLabel(a))
	}

	children := make([]*Error, 0, seq.Len())

	for i, item := range seq.All() {
		e := v.check(a.Items, item)
		if e == nil {
			continue
		}

		if v.quiet {
			return errNoMatch
		}

		children = append(children, v.group(nodeLoc(item), itemContext(i), e))
	}

	return v.group(nodeLoc(n), "", children...)
}

// checkMap validates that n is a mapping whose every value matches the map's
// value type. Keys are strings by construction, so only the values are checked.
func (v *validator) checkMap(mt *MapType, n Node) *Error {
	m, ok := AsMap(n)
	if !ok {
		return v.wrongType(n, typeLabel(mt))
	}

	children := make([]*Error, 0, m.Len())

	for key, value := range m.All() {
		e := v.check(mt.Values, value)
		if e == nil {
			continue
		}

		if v.quiet {
			return errNoMatch
		}

		children = append(children, v.group(nodeLoc(value), entryContext(key), e))
	}

	return v.group(nodeLoc(n), "", children...)
}

// entryContext is the context line introducing a diagnostic about one entry of a
// mapping.
func entryContext(key string) string {
	return fmt.Sprintf("the %q entry is not valid, because", key)
}
