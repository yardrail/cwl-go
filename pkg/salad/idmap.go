package salad

import "slices"

// expandIdentifierMaps rewrites identifier-map fields into sequences.
func (r *resolver) expandIdentifierMaps(m *MapNode, sc scope) (*MapNode, error) {
	out := m

	for key, val := range m.All() {
		term, ok := sc.ctx.Term(key)
		if !ok || term.MapSubject == "" {
			continue
		}

		expanded, err := expandIdentifierMap(key, term, val)
		if err != nil {
			return nil, err
		}

		if expanded != val {
			out = out.With(MapEntry{Key: key, Value: expanded})
		}
	}

	return out, nil
}

// expandIdentifierMap turns one identifier map into a sorted sequence.
func expandIdentifierMap(field string, term *TermDef, val Node) (Node, error) {
	m, ok := AsMap(val)
	if !ok || m.Has(dirImport) || m.Has(dirInclude) {
		return val, nil
	}

	keys := m.Keys()
	slices.Sort(keys)

	items := make([]Node, 0, len(keys))

	for _, key := range keys {
		entry, err := identifierMapEntry(field, term, key, mustEntry(m, key))
		if err != nil {
			return nil, err
		}

		items = append(items, entry)
	}

	return NewSeqNode(m.Loc(), items), nil
}

// identifierMapEntry builds one expanded item, moving the key into mapSubject.
func identifierMapEntry(field string, term *TermDef, key string, val Node) (Node, error) {
	obj, ok := AsMap(val)
	if !ok {
		if term.MapPredicate == "" {
			return nil, Errorf(val.Loc(),
				"field %q: the value of %q is a %s, and %q declares no mapPredicate to assign it to",
				field, key, NodeKind(val), field)
		}

		obj = NewMapNode(val.Loc(), []MapEntry{{Key: term.MapPredicate, Value: val}})
	}

	if existing, has := obj.Get(term.MapSubject); has && !IsNull(existing) {
		return nil, Errorf(obj.Loc(), "field %q: the value of %q already has a %q field", field, key, term.MapSubject)
	}

	return obj.With(MapEntry{Key: term.MapSubject, Value: NewStringNode(val.Loc(), key)}), nil
}

// mustEntry reads a key the caller has already established is present.
func mustEntry(m *MapNode, key string) Node {
	val, ok := m.Get(key)
	if !ok {
		return NewNullNode(m.Loc())
	}

	return val
}
