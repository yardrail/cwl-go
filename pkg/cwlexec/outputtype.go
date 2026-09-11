package cwlexec

import (
	"errors"
	"fmt"
	"slices"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

var (
	// ErrOutputType reports a value that does not match its declared output type.
	ErrOutputType = errors.New("output value does not match its declared type")

	// ErrUndeclaredResumedOutput reports a resumed output naming an undeclared port.
	ErrUndeclaredResumedOutput = errors.New("resumed outputs name a port the step does not declare")
)

// checkDeclaredOutputs validates outputs against declared types and projects to declared ports.
func checkDeclaredOutputs(step *plannedStep, outputs map[string]any) (map[string]any, error) {
	undeclared := make([]string, 0, len(outputs))

	for name := range outputs {
		if !slices.Contains(step.out, name) {
			undeclared = append(undeclared, name)
		}
	}

	if len(undeclared) > 0 {
		slices.Sort(undeclared)

		return nil, fmt.Errorf("%w: step %q, %q", ErrUndeclaredResumedOutput, step.id, undeclared)
	}

	checked := make(map[string]any, len(step.out))

	for _, name := range step.out {
		value := outputs[name]

		err := checkValueType(value, step.outTypes[name])
		if err != nil {
			return nil, fmt.Errorf("step %q port %q: %w", step.id, name, err)
		}

		checked[name] = value
	}

	return checked, nil
}

// checkValueType reports whether value matches the declared type.
func checkValueType(value any, declared cwlcore.TypeRef) error {
	switch declared.Kind() {
	case cwlcore.TypeKindPrimitive:
		return checkPrimitive(value, declared.Name())
	case cwlcore.TypeKindStdin, cwlcore.TypeKindStdout, cwlcore.TypeKindStderr:
		return checkPrimitive(value, cwlcore.PrimitiveFile)
	case cwlcore.TypeKindArray:
		return checkArray(value, declared)
	case cwlcore.TypeKindRecord:
		return checkRecord(value, declared)
	case cwlcore.TypeKindEnum:
		return checkEnum(value, declared)
	case cwlcore.TypeKindUnion:
		return checkUnion(value, declared)
	default:
		return nil
	}
}

// checkPrimitive checks value against a single CWL primitive type.
func checkPrimitive(value any, name string) error {
	if primitiveMatches(value, name) {
		return nil
	}

	return fmt.Errorf("%w: want %s, got %s", ErrOutputType, name, cwlcore.TypeName(value))
}

// primitiveMatches tests whether value matches a CWL primitive type by shape.
func primitiveMatches(value any, name string) bool {
	switch name {
	case cwlcore.PrimitiveNull:
		return value == nil
	case cwlcore.PrimitiveAny:
		return value != nil
	case cwlcore.PrimitiveBoolean:
		_, ok := value.(bool)

		return ok
	case cwlcore.PrimitiveString:
		_, ok := value.(string)

		return ok
	case cwlcore.PrimitiveInt, cwlcore.PrimitiveLong, cwlcore.PrimitiveFloat, cwlcore.PrimitiveDouble:
		_, ok := asNumber(value)

		return ok
	case cwlcore.PrimitiveFile, cwlcore.PrimitiveDirectory:
		return isFilesystemObject(value, name)
	default:
		return false
	}
}

// isFilesystemObject reports whether value is an object with a matching class (or no class).
func isFilesystemObject(value any, name string) bool {
	object, isObject := value.(map[string]any)
	if !isObject {
		return false
	}

	class, declared := object["class"]
	if !declared {
		return true
	}

	return class == name
}

// checkArray checks that value is an array with items matching the declared item type.
func checkArray(value any, declared cwlcore.TypeRef) error {
	items, isArray := value.([]any)
	if !isArray {
		return fmt.Errorf("%w: want an array, got %s", ErrOutputType, cwlcore.TypeName(value))
	}

	schema := declared.Array()
	if schema == nil {
		return nil
	}

	for index, item := range items {
		err := checkValueType(item, schema.Items)
		if err != nil {
			return fmt.Errorf("item %d: %w", index, err)
		}
	}

	return nil
}

// checkRecord checks that value is an object whose fields match the record's field types.
func checkRecord(value any, declared cwlcore.TypeRef) error {
	object, isObject := value.(map[string]any)
	if !isObject {
		return fmt.Errorf("%w: want a record, got %s", ErrOutputType, cwlcore.TypeName(value))
	}

	schema := declared.Record()
	if schema == nil {
		return nil
	}

	for index := range schema.Fields {
		field := &schema.Fields[index]
		name := ShortName(field.Name)

		err := checkValueType(object[name], field.Type)
		if err != nil {
			return fmt.Errorf("field %q: %w", name, err)
		}
	}

	return nil
}

// checkEnum checks that value is one of the enum's declared symbols.
func checkEnum(value any, declared cwlcore.TypeRef) error {
	symbol, isString := value.(string)
	if !isString {
		return fmt.Errorf("%w: want an enum symbol, got %s", ErrOutputType, cwlcore.TypeName(value))
	}

	schema := declared.Enum()
	if schema == nil {
		return nil
	}

	for _, candidate := range schema.Symbols {
		if candidate == symbol || ShortName(candidate) == symbol {
			return nil
		}
	}

	return fmt.Errorf("%w: %q is not one of the declared symbols", ErrOutputType, symbol)
}

// checkUnion checks that value matches at least one member of the union.
func checkUnion(value any, declared cwlcore.TypeRef) error {
	for _, option := range declared.Options() {
		if checkValueType(value, option) == nil {
			return nil
		}
	}

	return fmt.Errorf("%w: want %s, got %s", ErrOutputType, declared.String(), cwlcore.TypeName(value))
}
