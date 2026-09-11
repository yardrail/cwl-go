package cwlcore

import (
	"strings"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// Resolving named types against SchemaDefRequirement declarations.

// ResolveSchemaDef resolves a named type against the SchemaDefRequirement in scope.
// Recursive types leave the cycle edge as TypeKindNamed.
func ResolveSchemaDef(scope *RequirementScope, name string) (TypeRef, bool) {
	types := schemaDefTypes(scope)
	if len(types) == 0 {
		return TypeRef{payload: nil, node: nil, name: "", kind: 0}, false
	}

	resolver := &schemaDefResolver{types: types, active: make(map[string]bool, len(types))}

	return resolver.byName(name)
}

// ResolveTypeRef resolves all named references inside t against the SchemaDefRequirement in scope.
func ResolveTypeRef(scope *RequirementScope, t TypeRef) TypeRef {
	types := schemaDefTypes(scope)
	if len(types) == 0 {
		return t
	}

	resolver := &schemaDefResolver{types: types, active: make(map[string]bool, len(types))}

	return resolver.substitute(t)
}

// schemaDefTypes returns SchemaDefRequirement type declarations from scope, or nil.
func schemaDefTypes(scope *RequirementScope) []salad.Node {
	if scope == nil {
		return nil
	}

	requirement, found, _ := scope.GetRequirement(ClassSchemaDefRequirement)
	if !found {
		return nil
	}

	declared, ok := requirement.(*SchemaDefRequirement)
	if !ok {
		return nil
	}

	return declared.Types
}

// schemaDefResolver expands named references against a set of declarations.
// active tracks in-progress expansions to detect recursive types.
type schemaDefResolver struct {
	active map[string]bool
	types  []salad.Node
}

// byName expands the declaration called name.
func (r *schemaDefResolver) byName(name string) (TypeRef, bool) {
	node, found := r.lookup(name)
	if !found {
		return TypeRef{payload: nil, node: nil, name: "", kind: 0}, false
	}

	key := typeNameKey(name)
	if r.active[key] {
		// Recursive cycle — return the named reference as-is.
		return NewNamedType(name).WithNode(node), true
	}

	r.active[key] = true
	resolved := r.substitute(newDecoder().typeRef(node))
	delete(r.active, key)

	return resolved, true
}

// lookup finds the declaration called name.
func (r *schemaDefResolver) lookup(name string) (salad.Node, bool) {
	key := typeNameKey(name)

	for _, node := range r.types {
		m, ok := salad.AsMap(node)
		if !ok {
			continue
		}

		declared := lenientText(m, keyName)
		if declared == name || typeNameKey(declared) == key {
			return node, true
		}
	}

	return nil, false
}

// substitute replaces every named reference reachable from t.
func (r *schemaDefResolver) substitute(t TypeRef) TypeRef {
	switch t.Kind() {
	case TypeKindNamed:
		return r.substituteNamed(t)
	case TypeKindUnion:
		return r.substituteUnion(t)
	case TypeKindArray:
		return r.substituteArray(t)
	case TypeKindRecord:
		return r.substituteRecord(t)
	default:
		// Primitive, enum, or zero value — nothing to substitute.
		return t
	}
}

// substituteNamed expands one named reference, leaving it alone if not declared.
func (r *schemaDefResolver) substituteNamed(t TypeRef) TypeRef {
	resolved, found := r.byName(t.Name())
	if !found {
		return t
	}

	return resolved
}

// substituteUnion expands every member of a union.
func (r *schemaDefResolver) substituteUnion(t TypeRef) TypeRef {
	options := t.Options()

	replaced := make([]TypeRef, 0, len(options))
	for _, option := range options {
		replaced = append(replaced, r.substitute(option))
	}

	return NewUnionType(replaced).WithNode(t.Node())
}

// substituteArray expands an array's item type.
func (r *schemaDefResolver) substituteArray(t TypeRef) TypeRef {
	schema := t.Array()
	if schema == nil {
		return t
	}

	replaced := *schema
	replaced.Items = r.substitute(schema.Items)

	return NewArrayType(&replaced).WithNode(t.Node())
}

// substituteRecord expands every field type of a record.
func (r *schemaDefResolver) substituteRecord(t TypeRef) TypeRef {
	schema := t.Record()
	if schema == nil {
		return t
	}

	fields := make([]RecordField, len(schema.Fields))
	copy(fields, schema.Fields)

	for i := range fields {
		fields[i].Type = r.substitute(fields[i].Type)
	}

	replaced := *schema
	replaced.Fields = fields

	return NewRecordType(&replaced).WithNode(t.Node())
}

// typeNameKey reduces a type name to its short form for matching.
func typeNameKey(name string) string {
	key := shortName(name)

	if _, fragment, ok := strings.Cut(key, "#"); ok {
		key = fragment
	}

	if i := strings.LastIndex(key, "/"); i >= 0 {
		key = key[i+1:]
	}

	return key
}
