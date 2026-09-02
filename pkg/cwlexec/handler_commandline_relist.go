package cwlexec

import (
	"slices"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// relistBinding returns binding with mode filled in, unless it set `loadListing` itself — in which
// case the binding wins, which is the first step of the precedence — or there is no binding to fill
// in at all.
func relistBinding(binding *cwlcore.CommandOutputBinding, mode cwlcore.LoadListingEnum) *cwlcore.CommandOutputBinding {
	if binding == nil || binding.LoadListing != "" {
		return binding
	}

	relisted := *binding
	relisted.LoadListing = mode

	return &relisted
}

// relistType fills mode into every output binding reachable inside a declared type, descending
// through arrays, unions and record fields.
//
// A record field carries an outputBinding of its own, so a Directory-typed field is subject to the
// same three-step precedence as a top-level output and must inherit the requirement the same way.
// The walk terminates because [cwlcore.ResolveTypeRef] refuses to expand a type into itself, so the
// resolved graph is finite.
func relistType(declared cwlcore.TypeRef, mode cwlcore.LoadListingEnum) cwlcore.TypeRef {
	switch declared.Kind() {
	case cwlcore.TypeKindRecord:
		return relistRecord(declared, mode)
	case cwlcore.TypeKindArray:
		return relistArray(declared, mode)
	case cwlcore.TypeKindUnion:
		return cwlcore.NewUnionType(relistOptions(declared.Options(), mode)).WithNode(declared.Node())
	default:
		return declared
	}
}

// relistRecord fills mode into each of a record's field bindings, and into the types those fields
// are themselves declared as.
func relistRecord(declared cwlcore.TypeRef, mode cwlcore.LoadListingEnum) cwlcore.TypeRef {
	schema := declared.Record()
	if schema == nil {
		return declared
	}

	relisted := *schema
	relisted.Fields = slices.Clone(schema.Fields)

	for index := range relisted.Fields {
		field := &relisted.Fields[index]
		field.OutputBinding = relistBinding(field.OutputBinding, mode)
		field.Type = relistType(field.Type, mode)
	}

	return cwlcore.NewRecordType(&relisted).WithNode(declared.Node())
}

// relistArray fills mode into the type an array's elements are declared as.
func relistArray(declared cwlcore.TypeRef, mode cwlcore.LoadListingEnum) cwlcore.TypeRef {
	schema := declared.Array()
	if schema == nil {
		return declared
	}

	relisted := *schema
	relisted.Items = relistType(schema.Items, mode)

	return cwlcore.NewArrayType(&relisted).WithNode(declared.Node())
}

// relistOptions fills mode into every member of a union.
func relistOptions(options []cwlcore.TypeRef, mode cwlcore.LoadListingEnum) []cwlcore.TypeRef {
	relisted := make([]cwlcore.TypeRef, 0, len(options))
	for _, option := range options {
		relisted = append(relisted, relistType(option, mode))
	}

	return relisted
}

// loadListingDefault resolves the LoadListingRequirement in effect for a scope.
func loadListingDefault(scope *cwlcore.RequirementScope) (cwlcore.LoadListingEnum, bool) {
	if scope == nil {
		return "", false
	}

	requirement, found, _ := scope.GetRequirement(cwlcore.ClassLoadListingRequirement)
	if !found {
		return "", false
	}

	typed, ok := requirement.(*cwlcore.LoadListingRequirement)
	if !ok {
		return "", false
	}

	return typed.LoadListing, true
}
