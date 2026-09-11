package cwlexec

import (
	"cmp"
	"strings"
)

// Sort keys for command-line bindings.
// Keys are heterogeneous lists; numbers sort before strings, prefix keys sort first.

// keyElem is one element of a [sortKey]: either a number or a string.
type keyElem struct {
	text   string // Meaningful when isText is true.
	num    int64  // Meaningful when isText is false.
	isText bool
}

// numKey returns the numeric key element n. Positions and array indices are numeric.
func numKey(n int64) keyElem {
	return keyElem{text: "", num: n, isText: false}
}

// textKey returns the string key element s.
func textKey(s string) keyElem {
	return keyElem{text: s, num: 0, isText: true}
}

// sortKey is a binding's full sort key, outermost level first.
type sortKey []keyElem

// child returns a new key extending k with elems. Copies to avoid shared-backing mutations.
func (k sortKey) child(elems ...keyElem) sortKey {
	extended := make(sortKey, 0, len(k)+len(elems))
	extended = append(extended, k...)

	return append(extended, elems...)
}

// compareKeys orders two sort keys element by element. Prefix keys sort first.
func compareKeys(a, b sortKey) int {
	for index := range min(len(a), len(b)) {
		if order := compareElems(a[index], b[index]); order != 0 {
			return order
		}
	}

	return cmp.Compare(len(a), len(b))
}

// compareElems orders two key elements. Numbers sort before strings.
func compareElems(a, b keyElem) int {
	switch {
	case a.isText && b.isText:
		return strings.Compare(a.text, b.text)
	case a.isText:
		return 1
	case b.isText:
		return -1
	default:
		return cmp.Compare(a.num, b.num)
	}
}
