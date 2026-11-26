package cwlexec

import (
	"maps"
	"reflect"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// Narrowing whole-numbered floats to integers where the declared type is int or long.
//
// A value decoded by encoding/json into any (a job order built in Go, a connector's response, a
// run state restored from JSON) holds every number as a float64, so an int input of 3 arrives as
// 3.0 and renders as "3.0" on a command line and in $(inputs.x). Coercing against the declared
// type restores the int64 a typed job-order load would have produced.

// coerceIntegers narrows every whole float in value whose declared type is int or long to an int64.
// Values of other types, and floats with a fractional part, are returned unchanged. Containers are
// copied rather than modified in place, so a caller's shared value is never mutated.
func coerceIntegers(value any, declared cwlcore.TypeRef) any {
	if value == nil {
		return nil
	}

	switch declared.Kind() {
	case cwlcore.TypeKindPrimitive:
		return coercePrimitive(value, declared.Name())
	case cwlcore.TypeKindArray:
		return coerceArray(value, declared)
	case cwlcore.TypeKindRecord:
		return coerceRecord(value, declared)
	case cwlcore.TypeKindUnion:
		return coerceUnion(value, declared)
	default:
		return value
	}
}

// coerceIntegerInputs narrows the values of object against the declared types, by input name.
func coerceIntegerInputs(object map[string]any, types map[string]cwlcore.TypeRef) map[string]any {
	coerced := make(map[string]any, len(object))

	for name, value := range object {
		declared, ok := types[name]
		if !ok {
			coerced[name] = value

			continue
		}

		coerced[name] = coerceIntegers(value, declared)
	}

	return coerced
}

// coercePrimitive narrows a whole float to an int64 when name is int or long.
func coercePrimitive(value any, name string) any {
	if name != cwlcore.PrimitiveInt && name != cwlcore.PrimitiveLong {
		return value
	}

	if _, isDecimal := value.(salad.Decimal); isDecimal {
		return value
	}

	reflected := reflect.ValueOf(value)
	if !reflected.CanFloat() {
		return value
	}

	number, ok := floatAsInt(reflected.Float())
	if !ok {
		return value
	}

	return number
}

// coerceArray narrows each item of a list against the array's item type.
func coerceArray(value any, declared cwlcore.TypeRef) any {
	items, isArray := value.([]any)
	schema := declared.Array()

	if !isArray || schema == nil {
		return value
	}

	coerced := make([]any, len(items))
	for index, item := range items {
		coerced[index] = coerceIntegers(item, schema.Items)
	}

	return coerced
}

// coerceRecord narrows each declared field of an object against the field's type.
func coerceRecord(value any, declared cwlcore.TypeRef) any {
	object, isObject := value.(map[string]any)
	schema := declared.Record()

	if !isObject || schema == nil {
		return value
	}

	coerced := make(map[string]any, len(object))
	maps.Copy(coerced, object)

	for index := range schema.Fields {
		field := &schema.Fields[index]
		name := ShortName(field.Name)

		if current, present := object[name]; present {
			coerced[name] = coerceIntegers(current, field.Type)
		}
	}

	return coerced
}

// coerceUnion narrows value against the first union member it matches, the member
// [checkValueType] would accept it as.
func coerceUnion(value any, declared cwlcore.TypeRef) any {
	for _, option := range declared.Options() {
		if checkValueType(value, option) == nil {
			return coerceIntegers(value, option)
		}
	}

	return value
}
