package salad

// fieldOverride records a field an extending record re-specified, so that the
// narrowing rule can be checked once the type graph is built and named
// references can be resolved.
type fieldOverride struct {
	inherited *MapNode
	own       *MapNode
	record    string
	base      string
}

// flattener applies extends and specialize to a set of resolved schema
// definitions.
//
// Definitions are flattened on demand and memoized, so a base declared after the
// record that extends it is still fully flattened first. schema-salad instead
// walks the document in order and inherits whatever state a base happens to be
// in, which quietly makes multi-level inheritance depend on declaration order.
type flattener struct {
	ctx       *Context
	raw       map[string]*MapNode
	flat      map[string]*MapNode
	visiting  map[string]bool
	order     []string
	overrides []fieldOverride
}

// newFlattener indexes the definitions by name, preserving declaration order. If
// a name repeats, the last definition wins and keeps the first one's position,
// matching how a Schema's name table resolves the same conflict.
func newFlattener(defs []*MapNode, ctx *Context) *flattener {
	f := &flattener{
		ctx:       ctx,
		raw:       make(map[string]*MapNode, len(defs)),
		flat:      make(map[string]*MapNode, len(defs)),
		visiting:  make(map[string]bool, len(defs)),
		order:     make([]string, 0, len(defs)),
		overrides: make([]fieldOverride, 0),
	}

	for _, def := range defs {
		name := definitionName(def)
		if _, dup := f.raw[name]; !dup {
			f.order = append(f.order, name)
		}

		f.raw[name] = def
	}

	return f
}

// definitions returns every definition flattened, in declaration order.
func (f *flattener) definitions() ([]*MapNode, *Error) {
	out := make([]*MapNode, 0, len(f.order))

	for _, name := range f.order {
		def, err := f.resolve(name, f.raw[name].Loc())
		if err != nil {
			return nil, err
		}

		out = append(out, def)
	}

	return out, nil
}

// resolve flattens one definition, memoizing the result. loc is where the
// definition was asked for, so that an undefined base is reported at the extends
// declaration that names it.
func (f *flattener) resolve(name string, loc SourceLine) (*MapNode, *Error) {
	if done, ok := f.flat[name]; ok {
		return done, nil
	}

	if f.visiting[name] {
		return nil, Errorf(loc, "the type %q extends itself, directly or through a cycle of base types", name)
	}

	def, ok := f.raw[name]
	if !ok {
		return nil, Errorf(loc, "extends names %q, which the schema does not define", name)
	}

	f.visiting[name] = true
	defer delete(f.visiting, name)

	out, err := f.expand(name, def)
	if err != nil {
		return nil, err
	}

	f.flat[name] = out

	return out, nil
}

// expand applies a definition's own extends declaration, if it has one.
func (f *flattener) expand(name string, def *MapNode) (*MapNode, *Error) {
	bases := stringList(nodeOrNil(def, keyExtends))
	if len(bases) == 0 {
		return def, nil
	}

	switch definitionKind(def) {
	case kindRecord:
		return f.expandRecord(name, def, bases)
	case kindEnum:
		return f.expandEnum(def, bases)
	default:
		return def, nil
	}
}

// expandRecord merges the fields a record inherits with the ones it declares.
//
// Inherited fields come first, in the order the base records declare them, with a
// record's specialize declaration applied to them; the record's own fields follow.
// A field the record re-specifies replaces the inherited one in place, keeping the
// inherited position.
func (f *flattener) expandRecord(name string, def *MapNode, bases []string) (*MapNode, *Error) {
	sub := newSubstitution(specializeMap(def), f.ctx)
	inherited := newFieldList()

	for _, base := range bases {
		baseFields, err := f.baseFields(def, base)
		if err != nil {
			return nil, err
		}

		for _, field := range baseFields {
			inherited.put(markInherited(sub.applyObject(field), base))
		}
	}

	own, err := fieldDefinitions(def)
	if err != nil {
		return nil, err
	}

	merged := f.merge(name, inherited, own)

	return def.With(MapEntry{Key: keyFields, Value: NewSeqNode(nodeLoc(nodeOrNil(def, keyFields)), merged)}), nil
}

// baseFields returns the flattened field definitions of one base record.
func (f *flattener) baseFields(def *MapNode, base string) ([]*MapNode, *Error) {
	baseDef, err := f.resolve(base, nodeLoc(nodeOrNil(def, keyExtends)))
	if err != nil {
		return nil, err
	}

	return fieldDefinitions(baseDef)
}

// merge combines the inherited fields with the record's own, recording each
// re-specified field so that its narrowing can be checked later.
func (f *flattener) merge(name string, inherited *fieldList, own []*MapNode) []Node {
	ownFields := newFieldList()
	for _, field := range own {
		ownFields.put(field)
	}

	out := make([]Node, 0, len(inherited.items)+len(ownFields.items))
	taken := make(map[string]bool, len(ownFields.items))

	for _, base := range inherited.items {
		short := fieldShortName(base)

		override, ok := ownFields.get(short)
		if !ok {
			out = append(out, base)

			continue
		}

		taken[short] = true

		f.recordOverride(name, base, override)

		out = append(out, override)
	}

	for _, field := range ownFields.items {
		if !taken[fieldShortName(field)] {
			out = append(out, field)
		}
	}

	return out
}

// recordOverride notes that a record re-specified an inherited field.
func (f *flattener) recordOverride(name string, inherited, own *MapNode) {
	base, _ := AsString(nodeOrNil(inherited, keyInheritedFrom))

	f.overrides = append(f.overrides, fieldOverride{
		inherited: inherited,
		own:       own,
		record:    name,
		base:      base,
	})
}

// expandEnum prepends the symbols an enum inherits to the ones it declares,
// dropping any symbol that is already present.
func (f *flattener) expandEnum(def *MapNode, bases []string) (*MapNode, *Error) {
	symbols := make([]Node, 0)
	seen := make(map[string]bool)

	for _, base := range bases {
		baseDef, err := f.resolve(base, nodeLoc(nodeOrNil(def, keyExtends)))
		if err != nil {
			return nil, err
		}

		symbols = appendSymbols(symbols, seen, nodeOrNil(baseDef, keySymbols))
	}

	own := nodeOrNil(def, keySymbols)
	symbols = appendSymbols(symbols, seen, own)

	return def.With(MapEntry{Key: keySymbols, Value: NewSeqNode(nodeLoc(own), symbols)}), nil
}

// checkNarrowing verifies that every re-specified field narrows the type it was
// inherited with, which is the one thing the specification says an extending
// record may do to an inherited field.
func (f *flattener) checkNarrowing(s *Schema, b *typeBuilder) *Error {
	for _, ov := range f.overrides {
		err := f.checkOverride(s, b, ov)
		if err != nil {
			return err
		}
	}

	return nil
}

// checkOverride verifies one re-specified field.
func (f *flattener) checkOverride(s *Schema, b *typeBuilder, ov fieldOverride) *Error {
	super, err := b.buildType(nodeOrNil(ov.inherited, keyType))
	if err != nil {
		return err
	}

	sub, err := b.buildType(nodeOrNil(ov.own, keyType))
	if err != nil {
		return err
	}

	if s.IsSubtype(sub, super) {
		return nil
	}

	return Errorf(nodeLoc(nodeOrNil(ov.own, keyType)),
		"%s re-specifies the field %q inherited from %s, but %s does not narrow %s",
		shortName(ov.record), fieldShortName(ov.own), shortName(ov.base), typeLabel(sub), typeLabel(super))
}

// markInherited records which base record a field was copied down from, unless it
// already carries that note from a base of its own.
func markInherited(field *MapNode, base string) *MapNode {
	if field.Has(keyInheritedFrom) {
		return field
	}

	return field.With(MapEntry{Key: keyInheritedFrom, Value: NewStringNode(field.Loc(), base)})
}

// appendSymbols appends the symbols of one enum, skipping those already seen.
//
// Symbols are compared by short name, because that is what the specification
// matches a document value against: an inherited symbol and a symbol the
// extending enum restates resolve to different IRIs, one scoped to each enum, but
// they are the same symbol.
func appendSymbols(dst []Node, seen map[string]bool, symbols Node) []Node {
	seq, ok := AsSeq(symbols)
	if !ok {
		return dst
	}

	for _, item := range seq.Items() {
		sym, isStr := AsString(item)
		if !isStr || seen[shortName(sym)] {
			continue
		}

		seen[shortName(sym)] = true

		dst = append(dst, item)
	}

	return dst
}

// fieldList is an ordered set of field definitions keyed by short name. Merging
// inherited fields needs both properties: order is significant, and a field
// redeclared by a later base replaces the earlier one where it already sits.
type fieldList struct {
	index map[string]int
	items []*MapNode
}

// newFieldList builds an empty field list.
func newFieldList() *fieldList {
	return &fieldList{index: make(map[string]int), items: make([]*MapNode, 0)}
}

// put adds a field, replacing any field of the same short name in place.
func (l *fieldList) put(field *MapNode) {
	short := fieldShortName(field)

	if i, ok := l.index[short]; ok {
		l.items[i] = field

		return
	}

	l.index[short] = len(l.items)
	l.items = append(l.items, field)
}

// get looks a field up by short name.
func (l *fieldList) get(short string) (*MapNode, bool) {
	i, ok := l.index[short]
	if !ok {
		return nil, false
	}

	return l.items[i], true
}

// collectDefinitions gathers the top-level type definitions of a resolved schema
// document, in declaration order.
func collectDefinitions(n Node) ([]*MapNode, *Error) {
	return appendDefinitions(make([]*MapNode, 0), n)
}

// appendDefinitions walks a schema document, appending the type definitions it
// declares. A $graph wrapper is descended into; anything that is not a type
// definition is skipped, which is how a documentation-only entry or a stray
// directive is tolerated.
func appendDefinitions(dst []*MapNode, n Node) ([]*MapNode, *Error) {
	switch v := n.(type) {
	case *SeqNode:
		return appendDefinitionSeq(dst, v)
	case *MapNode:
		return appendDefinitionMap(dst, v)
	default:
		return dst, nil
	}
}

// appendDefinitionSeq appends the definitions of every item of a sequence.
func appendDefinitionSeq(dst []*MapNode, seq *SeqNode) ([]*MapNode, *Error) {
	for _, item := range seq.Items() {
		out, err := appendDefinitions(dst, item)
		if err != nil {
			return nil, err
		}

		dst = out
	}

	return dst, nil
}

// appendDefinitionMap appends one mapping, which is either a $graph wrapper or a
// type definition.
func appendDefinitionMap(dst []*MapNode, m *MapNode) ([]*MapNode, *Error) {
	if graph, ok := m.Get(dirGraph); ok {
		return appendDefinitions(dst, graph)
	}

	if !isTypeDefinition(m) {
		return dst, nil
	}

	if definitionName(m) == "" {
		return nil, Errorf(m.Loc(), "a top-level %s definition must have a name", definitionKind(m))
	}

	return append(dst, m), nil
}
