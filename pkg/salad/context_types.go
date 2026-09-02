package salad

// Field names a Schema Salad schema document uses to describe its types. They
// are spelled once here because BuildContext reads them out of documents that
// may or may not have been through identifier resolution.
const (
	keyName            = "name"
	keyType            = "type"
	keyFields          = "fields"
	keySymbols         = "symbols"
	keyInVocab         = "inVocab"
	keyJSONLDPredicate = "jsonldPredicate"
)

// The type declarations a Schema Salad schema document may contain.
const (
	kindRecord        = "record"
	kindEnum          = "enum"
	kindArray         = "array"
	kindMap           = "map"
	kindUnion         = "union"
	kindDocumentation = "documentation"
)

// typeKinds is the set of type declarations a schema document may contain. A
// mapping is treated as a type definition only when its type field names one of
// them, which keeps identifier-map-shaped field tables from being mistaken for
// type definitions.
var typeKinds = map[string]bool{
	kindRecord:        true,
	kindEnum:          true,
	kindArray:         true,
	kindMap:           true,
	kindUnion:         true,
	kindDocumentation: true,
}

// addTypeTree registers every type definition reachable from n, including the
// named types nested inside field types, so that their names and symbols enter
// the vocabulary.
func (c *Context) addTypeTree(n Node) *Error {
	switch v := n.(type) {
	case *SeqNode:
		for _, item := range v.Items() {
			err := c.addTypeTree(item)
			if err != nil {
				return err
			}
		}
	case *MapNode:
		return c.addTypeMap(v)
	default:
	}

	return nil
}

// addTypeMap registers a mapping that may be a $graph wrapper, a type
// definition, or merely a container of nested type definitions.
func (c *Context) addTypeMap(m *MapNode) *Error {
	if graph, ok := m.Get(dirGraph); ok {
		return c.addTypeTree(graph)
	}

	if isTypeDefinition(m) {
		err := c.registerType(m)
		if err != nil {
			return err
		}
	}

	for _, val := range m.All() {
		err := c.addTypeTree(val)
		if err != nil {
			return err
		}
	}

	return nil
}

// registerType adds one type definition's own name, symbols and fields to the
// context. Nested definitions are left to the surrounding tree walk.
func (c *Context) registerType(m *MapNode) *Error {
	err := c.registerTypeName(m)
	if err != nil {
		return err
	}

	kind, _ := AsString(nodeOrNil(m, keyType))
	switch shortName(kind) {
	case kindEnum:
		c.registerSymbols(m)
	case kindRecord:
		c.registerFields(m)
	default:
	}

	return nil
}

// registerTypeName adds a named type's own name to the vocabulary, honouring
// inVocab: false.
func (c *Context) registerTypeName(m *MapNode) *Error {
	name, ok := AsString(nodeOrNil(m, keyName))
	if !ok || name == "" {
		return nil
	}

	if inVocab, isBool := AsScalar(nodeOrNil(m, keyInVocab)); isBool && inVocab.IsBool() && !inVocab.AsBool() {
		return nil
	}

	iri := c.expandPrefix(name)

	return c.putVocabTerm(shortName(iri), iri, m.Loc())
}

// registerSymbols adds an enum's symbols to the vocabulary under their short
// names, which is how documents spell them.
//
// This stays on the lenient putVocab path, like registerField: an
// unqualified symbol's default IRI is scoped under its own enum's name (e.g.
// ".../BaseEnum/green" vs ".../MoreEnum/green"), and a subtype enum
// legitimately re-lists a symbol it shares with a base it extends (see
// expandEnum in flatten.go) — that is a normal part of the extends/merge
// mechanism, not a collision.
func (c *Context) registerSymbols(m *MapNode) {
	seq, ok := AsSeq(nodeOrNil(m, keySymbols))
	if !ok {
		return
	}

	for _, item := range seq.Items() {
		sym, isStr := AsString(item)
		if !isStr {
			continue
		}

		iri := c.expandPrefix(sym)
		c.putVocab(shortName(iri), iri)
	}
}

// registerFields adds a record's fields to the term table. The fields entry may
// be a sequence of field definitions or, in an unresolved document, an
// identifier map keyed by field name.
func (c *Context) registerFields(m *MapNode) {
	fields := nodeOrNil(m, keyFields)

	if seq, ok := AsSeq(fields); ok {
		c.registerFieldList(seq)

		return
	}

	if idmap, ok := AsMap(fields); ok {
		c.registerFieldMap(idmap)
	}
}

// registerFieldList registers the fields of a record written as a sequence of
// field definitions.
func (c *Context) registerFieldList(seq *SeqNode) {
	for _, item := range seq.Items() {
		field, ok := AsMap(item)
		if !ok {
			continue
		}

		name, _ := AsString(nodeOrNil(field, keyName))
		c.registerField(name, field)
	}
}

// registerFieldMap registers the fields of a record written as an identifier map
// keyed by field name, which is how an unresolved schema document spells them.
func (c *Context) registerFieldMap(idmap *MapNode) {
	for key, val := range idmap.All() {
		field, ok := AsMap(val)
		if !ok {
			field = NewMapNode(val.Loc(), nil)
		}

		if name, hasName := AsString(nodeOrNil(field, keyName)); hasName {
			key = name
		}

		c.registerField(key, field)
	}
}

// registerField derives one field's term definition and records it under the
// field's short name.
func (c *Context) registerField(name string, field *MapNode) {
	iri := c.expandPrefix(name)

	short := shortName(iri)
	if short == "" {
		return
	}

	def := c.termFor(field, iri)
	if _, exists := c.terms[short]; !exists {
		c.terms[short] = def
	}

	if !def.IsIdentifier {
		c.putVocab(short, def.ID)
	}
}

// termFor builds a TermDef from a field's jsonldPredicate, defaulting the
// predicate IRI to the field's own absolute name when none is declared.
func (c *Context) termFor(field *MapNode, fieldIRI string) *TermDef {
	def := &TermDef{
		ID:                absoluteOrEmpty(fieldIRI),
		Type:              "",
		Subscope:          "",
		MapSubject:        "",
		MapPredicate:      "",
		RefScope:          0,
		Identity:          false,
		Noconvert:         false,
		NoLinkCheck:       false,
		TypeDSL:           false,
		SecondaryFilesDSL: false,
		ScopedRef:         false,
		IsIdentifier:      false,
	}

	pred := nodeOrNil(field, keyJSONLDPredicate)
	if pred == nil {
		return def
	}

	if s, ok := AsString(pred); ok {
		def.ID = c.expandPrefix(s)
		def.IsIdentifier = s == keywordID

		return def
	}

	m, ok := AsMap(pred)
	if !ok {
		return def
	}

	c.applyPredicateMap(def, m)

	return def
}

// applyPredicateMap copies the object form of a jsonldPredicate onto def.
func (c *Context) applyPredicateMap(def *TermDef, m *MapNode) {
	for key, val := range m.All() {
		applyPredicateEntry(c, def, predicateKey(key), val)
	}
}

// predicateKey normalizes a jsonldPredicate key: the schema spells the JSON-LD
// keywords with a leading underscore, as in _id and _type.
func predicateKey(key string) string {
	if len(key) > 1 && key[0] == '_' {
		return "@" + key[1:]
	}

	return key
}

// applyPredicateEntry copies one jsonldPredicate entry onto def.
func applyPredicateEntry(c *Context, def *TermDef, key string, val Node) {
	switch key {
	case keywordID:
		if s, ok := AsString(val); ok {
			def.ID = c.expandPrefix(s)
		}
	case "refScope":
		if n, ok := AsScalar(val); ok {
			if v, isInt := n.AsInt(); isInt {
				def.RefScope, def.ScopedRef = int(v), true
			}
		}
	default:
		applyPredicateText(def, key, val)
		applyPredicateFlag(def, key, val)
	}
}

// applyPredicateText copies the string-valued jsonldPredicate modifiers onto def.
func applyPredicateText(def *TermDef, key string, val Node) {
	switch key {
	case keywordType:
		def.Type, _ = AsString(val)
	case "subscope":
		def.Subscope, _ = AsString(val)
	case "mapSubject":
		def.MapSubject, _ = AsString(val)
	case "mapPredicate":
		def.MapPredicate, _ = AsString(val)
	default:
	}
}

// applyPredicateFlag copies the boolean jsonldPredicate modifiers onto def.
func applyPredicateFlag(def *TermDef, key string, val Node) {
	flag := false
	if s, ok := AsScalar(val); ok {
		flag = s.AsBool()
	}

	switch key {
	case "identity":
		def.Identity = flag
	case "noLinkCheck":
		def.NoLinkCheck = flag
	case "typeDSL":
		def.TypeDSL = flag
	case "secondaryFilesDSL":
		def.SecondaryFilesDSL = flag
	case "noconvert":
		def.Noconvert = flag
	default:
	}
}

// isTypeDefinition reports whether m declares a Schema Salad type.
func isTypeDefinition(m *MapNode) bool {
	kind, ok := AsString(nodeOrNil(m, keyType))
	if !ok {
		return false
	}

	return typeKinds[shortName(kind)]
}

// absoluteOrEmpty returns name when it is an absolute IRI, and "" otherwise. An
// unresolved short name is not a usable predicate IRI.
func absoluteOrEmpty(name string) string {
	if hasScheme(name) {
		return name
	}

	return ""
}
