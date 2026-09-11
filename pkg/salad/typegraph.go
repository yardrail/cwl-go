package salad

import "fmt"

// primitiveIRIKinds maps fully-qualified primitive IRIs to their kinds.
var primitiveIRIKinds = map[string]PrimitiveKind{
	saladNS + nameNull:  PrimitiveNull,
	saladNS + nameAny:   PrimitiveAny,
	xsdNS + nameBoolean: PrimitiveBoolean,
	xsdNS + nameInt:     PrimitiveInt,
	xsdNS + nameLong:    PrimitiveLong,
	xsdNS + nameFloat:   PrimitiveFloat,
	xsdNS + nameDouble:  PrimitiveDouble,
	xsdNS + nameString:  PrimitiveString,
}

// recordDef pairs a record shell with the definition its fields come from.
type recordDef struct {
	typ *RecordType
	def *MapNode
}

// typeBuilder converts flattened schema definitions into the type graph.
// Named types are declared as shells first to handle cycles.
type typeBuilder struct {
	ctx    *Context
	vocab  map[string]string
	byName map[string]Type
	short  map[string][]Type
	order  []Type
}

// newTypeBuilder starts a conversion against the vocabulary of ctx.
func newTypeBuilder(ctx *Context) *typeBuilder {
	return &typeBuilder{
		ctx:    ctx,
		vocab:  ctx.Vocab(),
		byName: make(map[string]Type),
		short:  make(map[string][]Type),
		order:  make([]Type, 0),
	}
}

// build converts flattened definitions into a Schema.
func (b *typeBuilder) build(defs []*MapNode) (*Schema, *Error) {
	records := b.declare(defs)

	for _, rec := range records {
		err := b.fillRecord(rec.typ, rec.def)
		if err != nil {
			return nil, err
		}
	}

	return NewSchema(b.order), nil
}

// declare enters named definitions into the name table and returns record shells.
func (b *typeBuilder) declare(defs []*MapNode) []recordDef {
	records := make([]recordDef, 0, len(defs))

	for _, def := range defs {
		switch definitionKind(def) {
		case kindRecord:
			r := newRecordShell(def)
			b.register(r.Name, r)
			records = append(records, recordDef{typ: r, def: def})
		case kindEnum:
			b.register(definitionName(def), newEnumType(def))
		default:
		}
	}

	return records
}

// register enters a named type into the name table.
func (b *typeBuilder) register(name string, t Type) {
	if name == "" {
		return
	}

	b.byName[name] = t

	short := shortName(name)
	b.short[short] = append(b.short[short], t)

	b.order = append(b.order, t)
}

// fillRecord builds a record's fields.
func (b *typeBuilder) fillRecord(r *RecordType, def *MapNode) *Error {
	defs, err := fieldDefinitions(def)
	if err != nil {
		return err
	}

	fields := make([]*Field, 0, len(defs))

	for _, fd := range defs {
		f, ferr := b.buildField(fd)
		if ferr != nil {
			return Group(fd.Loc(), fieldContext(shortName(r.Name), fieldShortName(fd)), ferr)
		}

		fields = append(fields, f)
	}

	r.Fields = fields

	return nil
}

// buildField converts one field definition.
func (b *typeBuilder) buildField(fd *MapNode) (*Field, *Error) {
	name := definitionName(fd)
	if name == "" {
		return nil, Errorf(fd.Loc(), "a field definition must have a name")
	}

	t, err := b.buildType(nodeOrNil(fd, keyType))
	if err != nil {
		return nil, err
	}

	pred, _ := b.ctx.Term(shortName(name))
	inherited, _ := AsString(nodeOrNil(fd, keyInheritedFrom))

	return &Field{
		Type:          t,
		Default:       nodeOrNil(fd, keyDefault),
		JSONLDPred:    pred,
		Name:          name,
		InheritedFrom: inherited,
		Doc:           stringList(nodeOrNil(fd, keyDoc)),
		Optional:      acceptsNull(t),
	}, nil
}

// buildType converts a type expression.
func (b *typeBuilder) buildType(n Node) (Type, *Error) {
	switch v := n.(type) {
	case *ScalarNode:
		name, ok := v.AsString()
		if !ok {
			return nil, Errorf(v.Loc(), "a type must be a name or a type definition, but this one is %s", describe(v))
		}

		return b.resolveRef(name, v.Loc())
	case *SeqNode:
		return b.buildUnion(v)
	case *MapNode:
		return b.buildDefinition(v)
	default:
		return nil, Errorf(nodeLoc(n), "a type is required here, but there is %s", describe(n))
	}
}

// buildUnion converts a list of alternatives, preserving their order.
func (b *typeBuilder) buildUnion(seq *SeqNode) (Type, *Error) {
	options := make([]Type, 0, seq.Len())

	for _, item := range seq.Items() {
		t, err := b.buildType(item)
		if err != nil {
			return nil, err
		}

		options = append(options, t)
	}

	return &UnionType{Options: options}, nil
}

// buildDefinition converts an inline type definition.
func (b *typeBuilder) buildDefinition(m *MapNode) (Type, *Error) {
	switch definitionKind(m) {
	case kindArray:
		return b.buildArray(m)
	case kindMap:
		return b.buildMap(m)
	case kindUnion:
		return b.buildNames(m)
	case kindRecord:
		return b.buildInlineRecord(m)
	case kindEnum:
		return b.buildInlineEnum(m)
	default:
		return nil, Errorf(m.Loc(),
			"%q does not declare a type; expected one of record, enum, array, map or union", definitionKind(m))
	}
}

// buildArray converts an array declaration.
func (b *typeBuilder) buildArray(m *MapNode) (Type, *Error) {
	items, err := b.buildType(nodeOrNil(m, keyItems))
	if err != nil {
		return nil, err
	}

	return &ArrayType{Items: items}, nil
}

// buildMap converts a map declaration.
func (b *typeBuilder) buildMap(m *MapNode) (Type, *Error) {
	values, err := b.buildType(nodeOrNil(m, keyValues))
	if err != nil {
		return nil, err
	}

	return &MapType{Values: values}, nil
}

// buildNames converts a union declaration listed under "names".
func (b *typeBuilder) buildNames(m *MapNode) (Type, *Error) {
	val := nodeOrNil(m, keyNames)
	if seq, ok := AsSeq(val); ok {
		return b.buildUnion(seq)
	}

	t, err := b.buildType(val)
	if err != nil {
		return nil, err
	}

	return &UnionType{Options: []Type{t}}, nil
}

// buildInlineRecord converts an inline record, deduplicating by name.
func (b *typeBuilder) buildInlineRecord(m *MapNode) (Type, *Error) {
	if t, ok := b.byName[definitionName(m)]; ok {
		return t, nil
	}

	r := newRecordShell(m)
	b.register(r.Name, r)

	return r, b.fillRecord(r, m)
}

// buildInlineEnum converts an enum written inline.
func (b *typeBuilder) buildInlineEnum(m *MapNode) (Type, *Error) {
	if t, ok := b.byName[definitionName(m)]; ok {
		return t, nil
	}

	e := newEnumType(m)
	b.register(definitionName(m), e)

	return e, nil
}

// resolveRef resolves a type reference by name, vocabulary IRI, or short name.
func (b *typeBuilder) resolveRef(name string, loc SourceLine) (Type, *Error) {
	if t, ok := b.namedType(name); ok {
		return t, nil
	}

	if iri, ok := b.vocab[name]; ok {
		if t, found := b.namedType(iri); found {
			return t, nil
		}
	}

	if candidates := b.short[shortName(name)]; len(candidates) == 1 {
		return candidates[0], nil
	}

	return nil, Errorf(loc, "the type %q is not defined by the schema", name)
}

// namedType resolves an exact type name, including primitives.
func (b *typeBuilder) namedType(name string) (Type, bool) {
	if k, ok := PrimitiveKindOf(name); ok {
		return Primitive(k), true
	}

	if k, ok := primitiveIRIKinds[name]; ok {
		return Primitive(k), true
	}

	if t, ok := b.byName[name]; ok && t != nil {
		return t, true
	}

	return nil, false
}

// newRecordShell builds a record with everything but its fields.
func newRecordShell(def *MapNode) *RecordType {
	return &RecordType{
		Name:         definitionName(def),
		Fields:       make([]*Field, 0),
		Doc:          stringList(nodeOrNil(def, keyDoc)),
		Extends:      stringList(nodeOrNil(def, keyExtends)),
		DocumentRoot: flagAt(def, keyDocumentRoot),
		Abstract:     flagAt(def, keyAbstract),
	}
}

// newEnumType builds an enum. The metaschema's "Any" enum maps to [PrimitiveAny].
func newEnumType(def *MapNode) Type {
	name := definitionName(def)
	if isAnyName(name) {
		return Primitive(PrimitiveAny)
	}

	return &EnumType{
		Name:    name,
		Symbols: stringList(nodeOrNil(def, keySymbols)),
		Doc:     stringList(nodeOrNil(def, keyDoc)),
		Extends: stringList(nodeOrNil(def, keyExtends)),
	}
}

// isAnyName reports whether a type name is "Any" (term or IRI).
func isAnyName(name string) bool {
	return name == nameAny || name == saladNS+nameAny
}

// fieldContext formats a diagnostic context line for a field of a type.
func fieldContext(record, field string) string {
	return fmt.Sprintf("the field %q of %s is not valid, because", field, record)
}
