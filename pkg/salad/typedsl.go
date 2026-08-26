package salad

import "strings"

// Field names the type DSL and the secondary files DSL expand into.
const (
	keyItems    = "items"
	keyPattern  = "pattern"
	keyRequired = "required"
	typeArray   = "array"
)

// expandDSLFields applies the type DSL and the secondary files DSL to every
// field of an object whose term declares one.
//
// Both DSLs run before identifier resolution, so that the objects they generate
// are resolved like any other part of the document.
func expandDSLFields(m *MapNode, ctx *Context) (*MapNode, error) {
	out := m

	for key, val := range m.All() {
		term, ok := ctx.Term(key)
		if !ok {
			continue
		}

		expanded, err := expandDSLValue(val, term)
		if err != nil {
			return nil, Group(val.Loc(), "field "+key, err)
		}

		if expanded != val {
			out = out.With(MapEntry{Key: key, Value: expanded})
		}
	}

	return out, nil
}

// expandDSLValue applies whichever DSL a term declares, if any.
func expandDSLValue(val Node, term *TermDef) (Node, *Error) {
	switch {
	case term.TypeDSL:
		return expandTypeDSL(val)
	case term.SecondaryFilesDSL:
		return expandSecondaryFilesDSL(val), nil
	default:
		return val, nil
	}
}

// expandTypeDSL expands the shorthand type syntax: a trailing "?" makes the type
// a union with null, a trailing "[]" makes it an array, and "[]?" does both.
//
// A list of types is expanded item by item and then flattened one level, so that
// the unions the shorthand introduces merge into the surrounding union rather
// than nesting inside it.
func expandTypeDSL(val Node) (Node, *Error) {
	if s, ok := AsString(val); ok {
		expanded, err := expandTypeName(s, val.Loc())
		if err != nil {
			return nil, err
		}

		if seq, isSeq := AsSeq(expanded); isSeq {
			return flattenTypes(seq), nil
		}

		return expanded, nil
	}

	seq, ok := AsSeq(val)
	if !ok {
		return val, nil
	}

	items := make([]Node, 0, seq.Len())

	for _, item := range seq.Items() {
		expanded, err := expandTypeItem(item)
		if err != nil {
			return nil, err
		}

		items = append(items, expanded)
	}

	return flattenTypes(NewSeqNode(seq.Loc(), items)), nil
}

// expandTypeItem expands one member of a type list, leaving non-string members
// alone.
func expandTypeItem(item Node) (Node, *Error) {
	s, ok := AsString(item)
	if !ok {
		return item, nil
	}

	return expandTypeName(s, item.Loc())
}

// expandTypeName expands one type name written in the shorthand syntax.
func expandTypeName(name string, loc SourceLine) (Node, *Error) {
	if open := strings.IndexByte(name, '['); open >= 0 && name[open:] != "[]" && name[open:] != "[]?" {
		return nil, Errorf(loc, "type DSL: %q: [] must come at the end of the type name", name)
	}

	base, optional := strings.CutSuffix(name, "?")
	if base == "" {
		return NewStringNode(loc, name), nil
	}

	item := base

	var expanded Node = NewStringNode(loc, base)

	if trimmed, isArray := strings.CutSuffix(base, "[]"); isArray {
		item = trimmed
		expanded = NewMapNode(loc, []MapEntry{
			{Key: keyType, Value: NewStringNode(loc, typeArray)},
			{Key: keyItems, Value: NewStringNode(loc, trimmed)},
		})
	}

	if item == "" {
		return NewStringNode(loc, name), nil
	}

	if !optional {
		return expanded, nil
	}

	return NewSeqNode(loc, []Node{NewStringNode(loc, nameNull), expanded}), nil
}

// flattenTypes merges nested unions into the enclosing one and drops repeated
// members, preserving first-occurrence order.
func flattenTypes(seq *SeqNode) Node {
	items := make([]Node, 0, seq.Len())
	seen := make(map[string]bool, seq.Len())

	for _, item := range seq.Items() {
		if nested, ok := AsSeq(item); ok {
			items = appendUnique(items, seen, nested.Items())

			continue
		}

		items = appendUnique(items, seen, []Node{item})
	}

	return NewSeqNode(seq.Loc(), items)
}

// appendUnique appends the members of add that are not already present.
func appendUnique(items []Node, seen map[string]bool, add []Node) []Node {
	for _, item := range add {
		key := canonicalKey(item)
		if seen[key] {
			continue
		}

		seen[key] = true

		items = append(items, item)
	}

	return items
}

// expandSecondaryFilesDSL turns a secondary file pattern written as a string
// into the object form, where a trailing "?" means the file is not required.
func expandSecondaryFilesDSL(val Node) Node {
	if s, ok := AsString(val); ok {
		return secondaryFileObject(s, val.Loc())
	}

	seq, ok := AsSeq(val)
	if !ok {
		return val
	}

	items := make([]Node, 0, seq.Len())

	for _, item := range seq.Items() {
		if s, isStr := AsString(item); isStr {
			items = append(items, secondaryFileObject(s, item.Loc()))

			continue
		}

		items = append(items, item)
	}

	return NewSeqNode(seq.Loc(), items)
}

// secondaryFileObject builds the pattern/required object form of one secondary
// file pattern. required is null unless the pattern ended in "?", which makes
// the file explicitly optional.
func secondaryFileObject(pattern string, loc SourceLine) Node {
	var required Node = NewNullNode(loc)

	trimmed, optional := strings.CutSuffix(pattern, "?")
	if optional {
		required = NewBoolNode(loc, false)
	}

	return NewMapNode(loc, []MapEntry{
		{Key: keyPattern, Value: NewStringNode(loc, trimmed)},
		{Key: keyRequired, Value: required},
	})
}
