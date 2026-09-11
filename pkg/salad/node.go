package salad

import "iter"

// Node is a sealed union of *MapNode, *SeqNode and *ScalarNode, carrying source locations.
type Node interface {
	// Loc reports where in the source document this value came from.
	Loc() SourceLine
	isNode()
}

// Document is a fully-resolved Schema Salad document.
type Document struct {
	// Root is the resolved document tree, with all $import/$include references spliced in.
	Root Node
	// Metadata holds the top-level $namespaces, $schemas and $base directives.
	// It may be nil when the document declared none.
	Metadata *MapNode
	// BaseURI is the normalized URL this document was loaded from.
	BaseURI string
}

// MapEntry is one key/value pair of a MapNode, in document order.
type MapEntry struct {
	Value Node
	Key   string
}

// MapNode is an ordered, string-keyed map. Immutable after construction.
type MapNode struct {
	index   map[string]int
	entries []MapEntry
	loc     SourceLine
}

var _ Node = (*MapNode)(nil)

// NewMapNode builds a MapNode from entries. Duplicate keys keep first position, last value.
func NewMapNode(loc SourceLine, entries []MapEntry) *MapNode {
	m := &MapNode{
		loc:     loc,
		entries: make([]MapEntry, 0, len(entries)),
		index:   make(map[string]int, len(entries)),
	}
	for _, e := range entries {
		m.set(e)
	}

	return m
}

// Loc reports where in the source document this map came from.
func (m *MapNode) Loc() SourceLine {
	if m == nil {
		return SourceLine{
			File:  "",
			Start: Position{Line: 0, Column: 0, Offset: 0},
			End:   Position{Line: 0, Column: 0, Offset: 0},
		}
	}

	return m.loc
}

// Len returns the number of entries. It is 0 for a nil map.
func (m *MapNode) Len() int {
	if m == nil {
		return 0
	}

	return len(m.entries)
}

// Get returns the value bound to key, and whether the key is present.
func (m *MapNode) Get(key string) (Node, bool) {
	if m == nil {
		return nil, false
	}

	i, ok := m.index[key]
	if !ok {
		return nil, false
	}

	return m.entries[i].Value, true
}

// Has reports whether key is present.
func (m *MapNode) Has(key string) bool {
	_, ok := m.Get(key)

	return ok
}

// Keys returns the keys in document order. The result is a fresh slice.
func (m *MapNode) Keys() []string {
	keys := make([]string, 0, m.Len())
	if m == nil {
		return keys
	}

	for _, e := range m.entries {
		keys = append(keys, e.Key)
	}

	return keys
}

// Entries returns the key/value pairs in document order. The result is a fresh slice.
func (m *MapNode) Entries() []MapEntry {
	out := make([]MapEntry, 0, m.Len())
	if m == nil {
		return out
	}

	return append(out, m.entries...)
}

// All iterates the entries in document order.
func (m *MapNode) All() iter.Seq2[string, Node] {
	return func(yield func(string, Node) bool) {
		if m == nil {
			return
		}

		for _, e := range m.entries {
			if !yield(e.Key, e.Value) {
				return
			}
		}
	}
}

// With returns a copy with entries added or replaced. m is not modified.
func (m *MapNode) With(entries ...MapEntry) *MapNode {
	out := NewMapNode(m.Loc(), m.Entries())
	for _, e := range entries {
		out.set(e)
	}

	return out
}

// Without returns a copy with the named keys removed. m is not modified.
func (m *MapNode) Without(keys ...string) *MapNode {
	drop := make(map[string]bool, len(keys))
	for _, k := range keys {
		drop[k] = true
	}

	kept := make([]MapEntry, 0, m.Len())
	for _, e := range m.Entries() {
		if !drop[e.Key] {
			kept = append(kept, e)
		}
	}

	return NewMapNode(m.Loc(), kept)
}

func (m *MapNode) set(e MapEntry) {
	if i, ok := m.index[e.Key]; ok {
		m.entries[i] = e

		return
	}

	m.index[e.Key] = len(m.entries)
	m.entries = append(m.entries, e)
}

func (m *MapNode) isNode() {}

// SeqNode is an ordered list of values.
type SeqNode struct {
	items []Node
	loc   SourceLine
}

var _ Node = (*SeqNode)(nil)

// NewSeqNode builds a SeqNode located at loc from items, preserving their order.
func NewSeqNode(loc SourceLine, items []Node) *SeqNode {
	return &SeqNode{loc: loc, items: append(make([]Node, 0, len(items)), items...)}
}

// Loc reports where in the source document this list came from.
func (s *SeqNode) Loc() SourceLine {
	if s == nil {
		return SourceLine{
			File:  "",
			Start: Position{Line: 0, Column: 0, Offset: 0},
			End:   Position{Line: 0, Column: 0, Offset: 0},
		}
	}

	return s.loc
}

// Len returns the number of items. It is 0 for a nil list.
func (s *SeqNode) Len() int {
	if s == nil {
		return 0
	}

	return len(s.items)
}

// At returns the item at index i, or nil when i is out of range.
func (s *SeqNode) At(i int) Node {
	if i < 0 || i >= s.Len() {
		return nil
	}

	return s.items[i]
}

// Items returns the items in order. The result is a fresh slice.
func (s *SeqNode) Items() []Node {
	out := make([]Node, 0, s.Len())
	if s == nil {
		return out
	}

	return append(out, s.items...)
}

// All iterates the items in order, yielding each item's index alongside it.
func (s *SeqNode) All() iter.Seq2[int, Node] {
	return func(yield func(int, Node) bool) {
		if s == nil {
			return
		}

		for i, item := range s.items {
			if !yield(i, item) {
				return
			}
		}
	}
}

func (s *SeqNode) isNode() {}

// AsMap returns n as a *MapNode, and whether n is one.
func AsMap(n Node) (*MapNode, bool) {
	m, ok := n.(*MapNode)

	return m, ok
}

// AsSeq returns n as a *SeqNode, and whether n is one.
func AsSeq(n Node) (*SeqNode, bool) {
	s, ok := n.(*SeqNode)

	return s, ok
}

// AsScalar returns n as a *ScalarNode, and whether n is one.
func AsScalar(n Node) (*ScalarNode, bool) {
	s, ok := n.(*ScalarNode)

	return s, ok
}

// AsString returns the string value of n, and whether n is a string scalar.
func AsString(n Node) (string, bool) {
	s, ok := AsScalar(n)
	if !ok {
		return "", false
	}

	return s.AsString()
}

// IsNull reports whether n is absent (a nil Node) or the null scalar.
func IsNull(n Node) bool {
	if n == nil {
		return true
	}

	s, ok := AsScalar(n)

	return ok && s.IsNull()
}

// NodeKind returns a human-readable name for the kind of n.
func NodeKind(n Node) string {
	switch v := n.(type) {
	case *MapNode:
		return nameMapping
	case *SeqNode:
		return nameSequence
	case *ScalarNode:
		return v.Kind().String()
	default:
		return nameNothing
	}
}
