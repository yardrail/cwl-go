package cwlexec

import (
	"errors"
	"fmt"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Collecting record-typed outputs field by field. If the record's binding has an outputEval,
// the expression produces the whole record instead.

// ErrOutputUnbound reports a required record field with no outputBinding.
var ErrOutputUnbound = errors.New("record output field declares no outputBinding")

// outTarget projects the collection-relevant fields shared by parameters and record fields.
type outTarget struct {
	binding        *cwlcore.CommandOutputBinding
	secondaryFiles []cwlcore.SecondaryFileSchema
	format         []cwlcore.Expression
	typ            cwlcore.TypeRef
}

// parameterTarget projects an output parameter into an [outTarget], resolving SchemaDefRequirement types.
func (c *outputCollector) parameterTarget(param *cwlcore.CommandOutputParameter) *outTarget {
	return &outTarget{
		binding:        param.OutputBinding,
		secondaryFiles: param.SecondaryFiles,
		format:         param.Format,
		typ:            cwlcore.ResolveTypeRef(c.scope, param.Type),
	}
}

// outFieldTarget projects a record field into an [outTarget].
func outFieldTarget(field *cwlcore.RecordField) *outTarget {
	return &outTarget{
		binding:        field.OutputBinding,
		secondaryFiles: field.SecondaryFiles,
		format:         field.Format,
		typ:            field.Type,
	}
}

// outRecordShape is the record schema a type reaches and the number of array levels wrapping it.
type outRecordShape struct {
	schema *cwlcore.RecordSchema // nil if no record
	depth  int                   // array nesting levels
}

// record collects a record from its fields' bindings, wrapping in array levels per the type.
func (c *outputCollector) record(shape outRecordShape) (any, error) {
	object, err := c.recordFields(shape.schema)
	if err != nil {
		return nil, err
	}

	value := any(object)
	for range shape.depth {
		value = []any{value}
	}

	return value, nil
}

// recordFields collects every field of a record schema into a map keyed by short name.
func (c *outputCollector) recordFields(schema *cwlcore.RecordSchema) (map[string]any, error) {
	object := make(map[string]any, len(schema.Fields))

	for index := range schema.Fields {
		field := &schema.Fields[index]
		name := ShortName(field.Name)

		value, err := c.recordField(field)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", name, err)
		}

		object[name] = value
	}

	return object, nil
}

// recordField collects one field. Nested records recurse; non-record fields need a binding.
func (c *outputCollector) recordField(field *cwlcore.RecordField) (any, error) {
	target := outFieldTarget(field)

	shape := outRecordType(target.typ)
	if shape.schema != nil && !outEvaluatesWhole(target.binding) {
		return c.record(shape)
	}

	if target.binding == nil {
		return nil, outUnbound(target.typ)
	}

	return c.bound(target)
}

// outEvaluatesWhole reports whether a binding has an outputEval that produces the whole value.
func outEvaluatesWhole(binding *cwlcore.CommandOutputBinding) bool {
	return binding != nil && binding.OutputEval != ""
}

// outUnbound reports an error for a required unbound field. Optional fields return nil.
func outUnbound(declared cwlcore.TypeRef) error {
	if declared.IsOptional() {
		return nil
	}

	return fmt.Errorf("%w: type %s admits no null", ErrOutputUnbound, declared)
}

// outRecordType finds the record schema a type reaches, descending through arrays and unions.
func outRecordType(declared cwlcore.TypeRef) outRecordShape {
	switch declared.Kind() {
	case cwlcore.TypeKindRecord:
		return outRecordShape{schema: declared.Record(), depth: 0}
	case cwlcore.TypeKindArray:
		return outArrayRecordType(declared)
	case cwlcore.TypeKindUnion:
		return outUnionRecordType(declared)
	default:
		return outRecordShape{schema: nil, depth: 0}
	}
}

// outArrayRecordType finds the record within an array type, one level deeper.
func outArrayRecordType(declared cwlcore.TypeRef) outRecordShape {
	schema := declared.Array()
	if schema == nil {
		return outRecordShape{schema: nil, depth: 0}
	}

	shape := outRecordType(schema.Items)
	if shape.schema == nil {
		return outRecordShape{schema: nil, depth: 0}
	}

	shape.depth++

	return shape
}

// outUnionRecordType finds the first record among a union's members.
func outUnionRecordType(declared cwlcore.TypeRef) outRecordShape {
	for _, option := range declared.Options() {
		shape := outRecordType(option)
		if shape.schema != nil {
			return shape
		}
	}

	return outRecordShape{schema: nil, depth: 0}
}
