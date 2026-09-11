package salad

// Keys whose values are type expressions, traversed by specialize rewrites.
var typeStructureKeys = []string{keyType, keyItems, keyFields, keyValues, keyNames}

// substitution rewrites type references per a record's specialize declaration.
type substitution struct {
	spec  map[string]string
	vocab map[string]string
}

// newSubstitution builds a substitution from the specialize map and vocabulary.
func newSubstitution(spec map[string]string, ctx *Context) *substitution {
	return &substitution{spec: spec, vocab: ctx.Vocab()}
}

// empty reports whether the substitution would rewrite nothing.
func (s *substitution) empty() bool {
	return len(s.spec) == 0
}

// apply returns n with matching type references replaced.
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

// applyObject applies the substitution to a mapping.
func (s *substitution) applyObject(m *MapNode) *MapNode {
	if s.empty() {
		return m
	}

	return s.applyMap(m)
}

// applyMap rewrites type-bearing entries of a mapping.
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

// applySeq rewrites every item of a sequence.
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

// lookup finds the replacement for a type reference by name or vocabulary IRI.
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
