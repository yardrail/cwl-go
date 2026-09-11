package salad

import "strconv"

// canonicalKey renders a node as a deterministic string for structural equality.
func canonicalKey(n Node) string {
	return string(appendCanonical(make([]byte, 0), n))
}

// nodeEqual reports whether two nodes have the same structure and values.
func nodeEqual(a, b Node) bool {
	return canonicalKey(a) == canonicalKey(b)
}

// appendCanonical appends the canonical rendering of n to dst.
func appendCanonical(dst []byte, n Node) []byte {
	switch v := n.(type) {
	case *MapNode:
		return appendCanonicalMap(dst, v)
	case *SeqNode:
		return appendCanonicalSeq(dst, v)
	case *ScalarNode:
		return appendCanonicalScalar(dst, v)
	default:
		return append(dst, '~')
	}
}

// appendCanonicalMap renders a mapping, preserving key order.
func appendCanonicalMap(dst []byte, m *MapNode) []byte {
	dst = append(dst, '{')

	for key, val := range m.All() {
		dst = strconv.AppendQuote(dst, key)
		dst = append(dst, ':')
		dst = appendCanonical(dst, val)
		dst = append(dst, ',')
	}

	return append(dst, '}')
}

// appendCanonicalSeq renders a sequence.
func appendCanonicalSeq(dst []byte, s *SeqNode) []byte {
	dst = append(dst, '[')

	for _, item := range s.Items() {
		dst = appendCanonical(dst, item)
		dst = append(dst, ',')
	}

	return append(dst, ']')
}

// appendCanonicalScalar renders a leaf value, tagged with its kind to avoid collisions.
func appendCanonicalScalar(dst []byte, s *ScalarNode) []byte {
	dst = append(dst, s.Kind().String()...)
	dst = append(dst, '(')
	dst = strconv.AppendQuote(dst, s.String())

	return append(dst, ')')
}
