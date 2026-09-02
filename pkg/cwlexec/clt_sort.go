package cwlexec

import (
	"cmp"
	"strings"
)

// The sort key the specification assigns every command-line binding, and the ordering over it.
//
// Spec (Running a Command, "Input binding"): "the sort key is a list consisting of one or more
// numeric or string elements. Strings are sorted lexicographically based on UTF-8 encoding", and
// step 4, "Sort elements using the assigned sorting keys. Numeric entries sort before strings."
//
// So a key is a heterogeneous list, and comparing two keys compares element by element, with a
// numeric element always ordering before a string element. A key that is a strict prefix of
// another orders first, which is what puts an array's own binding — its prefix — ahead of the
// bindings of its items.

// keyElem is one element of a [sortKey]: either a number or a string. The two are kept in one
// comparable struct rather than an `any` because the whole point of the type is that a number and
// a string have a defined order between them, which a type switch at every comparison would only
// obscure.
type keyElem struct {
	// text is the string element, meaningful only when isText is true.
	text string

	// num is the numeric element, meaningful only when isText is false.
	num int64

	// isText selects which of the two above carries the element.
	isText bool
}

// numKey returns the numeric key element n. Positions and array indices are numeric.
func numKey(n int64) keyElem {
	return keyElem{text: "", num: n, isText: false}
}

// textKey returns the string key element s. Parameter and record field names are strings, and are
// what the specification's tie-break rule orders by.
func textKey(s string) keyElem {
	return keyElem{text: s, num: 0, isText: true}
}

// sortKey is a binding's full sort key, outermost level first.
type sortKey []keyElem

// child returns a new key extending k with elems, leaving k untouched.
//
// It copies rather than appending in place on purpose: one parent key is extended once per child,
// and a shared backing array would let the second child overwrite the first's elements.
func (k sortKey) child(elems ...keyElem) sortKey {
	extended := make(sortKey, 0, len(k)+len(elems))
	extended = append(extended, k...)

	return append(extended, elems...)
}

// compareKeys orders two sort keys, returning a negative number, zero, or a positive number as a
// sorts before, with, or after b.
//
// A key that is a prefix of the other sorts first. That falls out of comparing element by element
// and then by length, and it is the rule that puts a binding ahead of the bindings nested inside
// it.
func compareKeys(a, b sortKey) int {
	for index := range min(len(a), len(b)) {
		if order := compareElems(a[index], b[index]); order != 0 {
			return order
		}
	}

	return cmp.Compare(len(a), len(b))
}

// compareElems orders two key elements. Numeric elements sort before string ones; two numbers sort
// by value, and two strings lexicographically by UTF-8 code unit, which is exactly Go's string
// comparison.
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
