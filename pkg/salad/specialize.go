package salad

// Keys of a schema node whose value is itself a type expression. A specialize
// rewrite descends exactly these, which is what confines it to type references
// and keeps it away from documentation, defaults and jsonldPredicate values.
var typeStructureKeys = []string{keyType, keyItems, keyFields, keyValues, keyNames}

// substitution rewrites type references according to a record's specialize
// declaration.
//
// It is a real substitution over the resolved node tree, not the string search
// and replace over rendered types that schema-salad performs: a reference is only
// rewritten where the schema says a type belongs, so a documentation string or a
// default value that happens to spell a type name is never touched.
type substitution struct {
	spec  map[string]string
	vocab map[string]string
}

// newSubstitution builds the substitution a record's specialize map describes.
// The context supplies the vocabulary, so that a reference written as a short
// name matches a specializeFrom resolved to a full IRI.
func newSubstitution(spec map[string]string, ctx *Context) *substitution {
	return &substitution{spec: spec, vocab: ctx.Vocab()}
}

// empty reports whether the substitution would rewrite nothing.
func (s *substitution) empty() bool {
	return len(s.spec) == 0
}

// apply returns n with every type reference the substitution names replaced.
// Nodes with nothing to rewrite are returned as they are.
func (s *substitution) apply(n Node) Node {
	if s.empty() {
		return n
	}

	switch v := n.(type) {
	case *MapNode:
		return s.applyMap(v)
	case *SeqNode:
		return s.applySeq(v)
	case *ScalarNode:
		return s.applyScalar(v)
	default:
		return n
	}
}

// applyObject applies the substitution to a mapping, which is what a field or a
// type definition is.
func (s *substitution) applyObject(m *MapNode) *MapNode {
	if s.empty() {
		return m
	}

	return s.applyMap(m)
}

// applyMap rewrites the type-bearing entries of a mapping, leaving every other
// entry untouched.
func (s *substitution) applyMap(m *MapNode) *MapNode {
	out := m

	for _, key := range typeStructureKeys {
		val, ok := m.Get(key)
		if !ok {
			continue
		}

		replaced := s.apply(val)
		if replaced != val {
			out = out.With(MapEntry{Key: key, Value: replaced})
		}
	}

	return out
}

// applySeq rewrites every item of a sequence, which is what a union of types or a
// list of field definitions is.
func (s *substitution) applySeq(seq *SeqNode) Node {
	items := make([]Node, 0, seq.Len())
	changed := false

	for _, item := range seq.Items() {
		replaced := s.apply(item)
		changed = changed || replaced != item

		items = append(items, replaced)
	}

	if !changed {
		return seq
	}

	return NewSeqNode(seq.Loc(), items)
}

// applyScalar rewrites a type reference written as a name.
func (s *substitution) applyScalar(n *ScalarNode) Node {
	name, ok := n.AsString()
	if !ok {
		return n
	}

	to, ok := s.lookup(name)
	if !ok {
		return n
	}

	return NewStringNode(n.Loc(), to)
}

// lookup finds the replacement for a type reference, matching it both as written
// and as the IRI the vocabulary expands it to.
func (s *substitution) lookup(name string) (string, bool) {
	if to, ok := s.spec[name]; ok {
		return to, true
	}

	iri, ok := s.vocab[name]
	if !ok {
		return "", false
	}

	to, ok := s.spec[iri]

	return to, ok
}
