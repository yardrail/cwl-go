package salad

// Keys of a schema definition that the flattener and the type builder read
// directly. The keys a schema document uses to describe its types are declared in
// context_types.go; these are the ones only this stage needs.
const (
	keyExtends        = "extends"
	keySpecialize     = "specialize"
	keySpecializeFrom = "specializeFrom"
	keySpecializeTo   = "specializeTo"
	keyAbstract       = "abstract"
	keyDocumentRoot   = "documentRoot"
	keyDoc            = "doc"
	keyDefault        = "default"
	keyValues         = "values"
	keyNames          = "names"

	// keyInheritedFrom records, on a field the flattener copied down from a base
	// record, which base it came from. It is written onto the flattened
	// definitions this package builds internally, never onto a user document, and
	// is spelled the way schema-salad spells it.
	keyInheritedFrom = "inherited_from"
)

// definitionKind returns the kind of type a definition declares — "record",
// "enum", "documentation" and so on — reduced to its short name, since a schema
// may spell it either as a vocabulary term or as a full IRI.
func definitionKind(m *MapNode) string {
	kind, ok := AsString(nodeOrNil(m, keyType))
	if !ok {
		return ""
	}

	return shortName(kind)
}

// definitionName returns a definition's declared name.
func definitionName(m *MapNode) string {
	name, _ := AsString(nodeOrNil(m, keyName))

	return name
}

// fieldShortName returns the short name a field definition is known by, which is
// how documents spell it and how an extending record refers to it.
func fieldShortName(m *MapNode) string {
	return shortName(definitionName(m))
}

// fieldDefinitions returns a record definition's field definitions in
// declaration order.
//
// The fields entry must already be a sequence: an identifier map is expanded by
// the loader, well before flattening, so a mapping here means the definition was
// never resolved.
func fieldDefinitions(m *MapNode) ([]*MapNode, *Error) {
	val, ok := m.Get(keyFields)
	if !ok || IsNull(val) {
		return make([]*MapNode, 0), nil
	}

	seq, ok := AsSeq(val)
	if !ok {
		return nil, Errorf(val.Loc(),
			"the fields of %s must be a sequence of field definitions, but they are %s; "+
				"a schema must be resolved before it is flattened", shortName(definitionName(m)), describe(val))
	}

	out := make([]*MapNode, 0, seq.Len())

	for _, item := range seq.Items() {
		field, isMap := AsMap(item)
		if !isMap {
			return nil, Errorf(item.Loc(), "a field definition must be a mapping, but this one is %s", describe(item))
		}

		out = append(out, field)
	}

	return out, nil
}

// stringList reads a value that a schema may spell either as one string or as a
// list of them, such as extends or doc.
func stringList(n Node) []string {
	out := make([]string, 0, 1)

	if s, ok := AsString(n); ok {
		return append(out, s)
	}

	seq, ok := AsSeq(n)
	if !ok {
		return out
	}

	for _, item := range seq.Items() {
		if s, isStr := AsString(item); isStr {
			out = append(out, s)
		}
	}

	return out
}

// flagAt reports whether a definition sets a boolean flag such as abstract or
// documentRoot.
func flagAt(m *MapNode, key string) bool {
	s, ok := AsScalar(nodeOrNil(m, key))

	return ok && s.IsBool() && s.AsBool()
}

// specializeMap reads a record's specialize declaration into the from-to table a
// substitution is built from.
func specializeMap(m *MapNode) map[string]string {
	out := make(map[string]string)

	val, ok := m.Get(keySpecialize)
	if !ok {
		return out
	}

	if entry, isMap := AsMap(val); isMap {
		addSpecializeEntry(out, entry)

		return out
	}

	seq, isSeq := AsSeq(val)
	if !isSeq {
		return out
	}

	for _, item := range seq.Items() {
		if entry, isMap := AsMap(item); isMap {
			addSpecializeEntry(out, entry)
		}
	}

	return out
}

// addSpecializeEntry records one specializeFrom / specializeTo pair.
func addSpecializeEntry(out map[string]string, entry *MapNode) {
	from, hasFrom := AsString(nodeOrNil(entry, keySpecializeFrom))
	to, hasTo := AsString(nodeOrNil(entry, keySpecializeTo))

	if hasFrom && hasTo {
		out[from] = to
	}
}
