package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// The CWL type language.
//
// A type arrives as a string naming a CWLType symbol or a declared type, a
// sequence meaning a union, or a mapping holding an inline record, enum or array
// schema. The `T?` and `T[]` shorthands never reach this layer: pkg/salad
// expands them while resolving the document, so an optional type arrives already
// spelled as the union [null, T].

// primitiveTypeNames is the set of CWLType symbols, in the short spelling
// TypeRef.Name uses. A type name outside it is a reference to a type declared
// elsewhere — by a SchemaDefRequirement, or by an inline schema that named
// itself — which decoding records but does not resolve.
var primitiveTypeNames = map[string]bool{
	PrimitiveNull:      true,
	PrimitiveBoolean:   true,
	PrimitiveInt:       true,
	PrimitiveLong:      true,
	PrimitiveFloat:     true,
	PrimitiveDouble:    true,
	PrimitiveString:    true,
	PrimitiveFile:      true,
	PrimitiveDirectory: true,
	PrimitiveAny:       true,
}

// typeRef decodes a type expression.
func (d *decoder) typeRef(node salad.Node) TypeRef {
	switch value := node.(type) {
	case *salad.ScalarNode:
		return d.namedTypeRef(value)
	case *salad.SeqNode:
		return NewUnionType(decodeEach(value.Items(), d.typeRef)).WithNode(value)
	case *salad.MapNode:
		return d.inlineTypeRef(value)
	default:
		// A nil node, which is what an absent type field reads as. The
		// Node interface is sealed, so there is no fourth shape.
		return TypeRef{payload: nil, node: nil, name: "", kind: 0}
	}
}

// namedTypeRef decodes a type written as a name: a CWLType symbol, one of the
// three standard-stream shortcuts, or a reference to a declared type.
func (d *decoder) namedTypeRef(node salad.Node) TypeRef {
	// A union member written as a bare YAML null rather than the quoted
	// string "null" means the same type. pkg/salad's type DSL produces the
	// quoted spelling, but a document may be written either way.
	if salad.IsNull(node) {
		return NewPrimitiveType(PrimitiveNull).WithNode(node)
	}

	name, ok := salad.AsString(node)
	if !ok {
		d.failf(nodeLoc(node), "a type name must be a string, but it is %s", salad.NodeKind(node))

		return TypeRef{payload: nil, node: nil, name: "", kind: 0}
	}

	return typeRefForName(shortName(name)).WithNode(node)
}

// typeRefForName builds the TypeRef a type name selects.
func typeRefForName(name string) TypeRef {
	switch name {
	case keyStdin:
		return NewShortcutType(TypeKindStdin)
	case keyStdout:
		return NewShortcutType(TypeKindStdout)
	case keyStderr:
		return NewShortcutType(TypeKindStderr)
	default:
	}

	if primitiveTypeNames[name] {
		return NewPrimitiveType(name)
	}

	return NewNamedType(name)
}

// inlineTypeRef decodes a type written as an inline schema.
func (d *decoder) inlineTypeRef(m *salad.MapNode) TypeRef {
	switch shortName(d.text(m, keyType)) {
	case kindNameRecord:
		return NewRecordType(d.recordSchema(m)).WithNode(m)
	case kindNameEnum:
		return NewEnumType(d.enumSchema(m)).WithNode(m)
	case kindNameArray:
		return NewArrayType(d.arraySchema(m)).WithNode(m)
	default:
		d.failf(m.Loc(), "an inline type schema must declare a type of %q, %q or %q",
			kindNameRecord, kindNameEnum, kindNameArray)

		return TypeRef{payload: nil, node: nil, name: "", kind: 0}
	}
}

// recordSchema decodes an inline record type.
func (d *decoder) recordSchema(m *salad.MapNode) *RecordSchema {
	return &RecordSchema{
		Node:         m,
		InputBinding: d.commandLineBinding(fieldNode(m, keyInputBinding)),
		Name:         d.text(m, keyName),
		Label:        d.text(m, keyLabel),
		Doc:          d.textList(m, keyDoc),
		Fields:       decodeEach(d.listItems(m, keyFields, keyName, keyType), d.recordField),
	}
}

// enumSchema decodes an inline enum type.
func (d *decoder) enumSchema(m *salad.MapNode) *EnumSchema {
	return &EnumSchema{
		Node:         m,
		InputBinding: d.commandLineBinding(fieldNode(m, keyInputBinding)),
		Name:         d.text(m, keyName),
		Label:        d.text(m, keyLabel),
		Doc:          d.textList(m, keyDoc),
		Symbols:      d.textList(m, keySymbols),
	}
}

// arraySchema decodes an inline array type.
func (d *decoder) arraySchema(m *salad.MapNode) *ArraySchema {
	return &ArraySchema{
		Node:         m,
		InputBinding: d.commandLineBinding(fieldNode(m, keyInputBinding)),
		Name:         d.text(m, keyName),
		Label:        d.text(m, keyLabel),
		Doc:          d.textList(m, keyDoc),
		Items:        d.typeRef(fieldNode(m, keyItems)),
	}
}

// recordField decodes one field of an inline record schema.
//
// The model flattens the schema's four record-field variants into one type, so a
// field carries both binding kinds; at most one is ever populated, according to
// which side of the process the enclosing schema sits on.
func (d *decoder) recordField(node salad.Node) RecordField {
	m := d.mapping(node, "a record field")

	return RecordField{
		Node:           node,
		InputBinding:   d.commandLineBinding(fieldNode(m, keyInputBinding)),
		OutputBinding:  d.commandOutputBinding(fieldNode(m, keyOutputBinding)),
		Name:           d.text(m, keyName),
		Label:          d.text(m, keyLabel),
		LoadListing:    LoadListingEnum(d.text(m, keyLoadListing)),
		Doc:            d.textList(m, keyDoc),
		Type:           d.typeRef(fieldNode(m, keyType)),
		SecondaryFiles: d.secondaryFiles(m),
		Format:         d.expressionList(m, keyFormat),
		LoadContents:   d.flag(m, keyLoadContents),
		Streamable:     d.flag(m, keyStreamable),
	}
}
