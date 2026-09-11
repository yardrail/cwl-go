package cwlexec

import (
	"slices"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// relistBinding fills mode into binding unless it already sets loadListing.
func relistBinding(binding *cwlcore.CommandOutputBinding, mode cwlcore.LoadListingEnum) *cwlcore.CommandOutputBinding {
	if binding == nil || binding.LoadListing != "" {
		return binding
	}

	relisted := *binding
	relisted.LoadListing = mode

	return &relisted
}

// relistType fills mode into output bindings within a type, descending recursively.
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

// relistRecord fills mode into a record's field bindings and field types.
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

// relistArray fills mode into an array's element type.
func relistArray(declared cwlcore.TypeRef, mode cwlcore.LoadListingEnum) cwlcore.TypeRef {
	schema := declared.Array()
	if schema == nil {
		return declared
	}

	relisted := *schema
	relisted.Items = relistType(schema.Items, mode)

	return cwlcore.NewArrayType(&relisted).WithNode(declared.Node())
}

// relistOptions fills mode into each union member.
func relistOptions(options []cwlcore.TypeRef, mode cwlcore.LoadListingEnum) []cwlcore.TypeRef {
	relisted := make([]cwlcore.TypeRef, 0, len(options))
	for _, option := range options {
		relisted = append(relisted, relistType(option, mode))
	}

	return relisted
}

// loadListingDefault returns the LoadListingRequirement mode, if any.
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
