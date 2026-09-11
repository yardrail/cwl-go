package salad

// Schema definition keys used by the flattener and type builder.
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

	// keyInheritedFrom records which base record a flattened field was inherited from.
	keyInheritedFrom = "inherited_from"
)

// definitionKind returns the type kind (e.g. "record", "enum") as a short name.
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

// fieldShortName returns the short name of a field definition.
func fieldShortName(m *MapNode) string {
	return shortName(definitionName(m))
}

// fieldDefinitions returns a record's field definitions in declaration order.
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

// stringList reads a value as one string or a list of strings.
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

// flagAt reports whether a definition sets a boolean flag.
func flagAt(m *MapNode, key string) bool {
	s, ok := AsScalar(nodeOrNil(m, key))

	return ok && s.IsBool() && s.AsBool()
}

// specializeMap reads a record's specialize declaration into a from-to map.
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
