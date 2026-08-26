package cwlcore

import (
	"strings"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// Resolving a named type against the SchemaDefRequirement declarations in scope.
//
// SchemaDefRequirement.Types are carried as validated salad nodes rather than as
// decoded TypeRefs, because one declaration may refer by name to another
// declared alongside it: a node decoded in isolation cannot follow that edge,
// and the set of nodes to follow it through is a property of the requirement
// scope rather than of any single node. This file is where the two halves are
// put back together, and it is the only thing standing between a
// TypeKindNamed TypeRef and the schema it names.

// ResolveSchemaDef resolves the named type name against the SchemaDefRequirement
// in scope, returning the type it names and whether the scope declares one.
//
// The reference may be spelled however identifier resolution left it — "rec",
// "#rec", or "file:///tool.cwl#rec" all name the same declaration — and the same
// goes for the name each declaration gives itself.
//
// The result is resolved through: a declaration whose fields, array items or
// union members refer to other declarations comes back with those substituted
// too, so a caller can walk the returned TypeRef without resolving anything
// further. The one exception is a recursive type. A declaration that reaches
// itself cannot be expanded indefinitely, so the edge that closes the cycle is
// left as the TypeKindNamed reference it was written as, which both terminates
// the expansion and tells the caller exactly where the cycle is.
//
// A nil scope, a scope with no SchemaDefRequirement, and a name no declaration
// matches all report false.
func ResolveSchemaDef(scope *RequirementScope, name string) (TypeRef, bool) {
	types := schemaDefTypes(scope)
	if len(types) == 0 {
		return TypeRef{}, false
	}

	resolver := &schemaDefResolver{types: types, active: make(map[string]bool, len(types))}

	return resolver.byName(name)
}

// ResolveTypeRef resolves every named reference inside t against the
// SchemaDefRequirement in scope, leaving anything it cannot resolve as it is.
//
// It is ResolveSchemaDef applied to a type a parameter already carries rather
// than to a bare name, which is what a consumer walking a process's parameters
// wants: a parameter typed as a SchemaDef record, or as an array or union of
// them, comes back with the real schemas in place and its nested inputBindings
// reachable. A type that names nothing declared is returned unchanged, so this
// is safe to call on every parameter.
func ResolveTypeRef(scope *RequirementScope, t TypeRef) TypeRef {
	types := schemaDefTypes(scope)
	if len(types) == 0 {
		return t
	}

	resolver := &schemaDefResolver{types: types, active: make(map[string]bool, len(types))}

	return resolver.substitute(t)
}

// schemaDefTypes returns the type declarations of the SchemaDefRequirement in
// effect for the scope, or nil when the scope declares none.
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

// schemaDefResolver expands named references against one set of declarations.
//
// active holds the names currently being expanded, which is what stops a
// recursive declaration from expanding forever. It is a resolver rather than a
// pair of free functions purely so that this set travels with the recursion.
type schemaDefResolver struct {
	active map[string]bool
	types  []salad.Node
}

// byName expands the declaration called name.
func (r *schemaDefResolver) byName(name string) (TypeRef, bool) {
	node, found := r.lookup(name)
	if !found {
		return TypeRef{}, false
	}

	key := typeNameKey(name)
	if r.active[key] {
		// The cycle closes here. A recursive type has no finite expansion,
		// so the reference stands for itself.
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
		// A primitive, an enum, a standard-stream shortcut or the unset
		// zero value: nothing inside any of them can name a declaration.
		return t
	}
}

// substituteNamed expands one reference, leaving it alone when nothing in scope
// declares it — a parameter may name a type this package does not resolve, and
// that is the caller's business rather than an error here.
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

// substituteRecord expands every field type of a record, copying the schema
// rather than editing it so that the decoded original is left alone.
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

// typeNameKey reduces a type name to the short form that every spelling of the
// same type shares: the last "/"-separated segment of the identifier's fragment,
// or of its path when it has no fragment.
//
// It is this package's spelling of the rule cwltool applies wherever a CWL
// identifier is matched by name, and it is what lets a parameter written
// "type: rec" find a declaration whose own name resolved to
// "file:///tool.cwl#rec".
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
