package cwlexec

import (
	"errors"
	"fmt"
	"slices"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Errors reported while checking externally-supplied outputs against a step's declared types.
var (
	// ErrOutputType reports a value that does not match the type its output port declares.
	ErrOutputType = errors.New("output value does not match its declared type")

	// ErrUndeclaredResumedOutput reports a resumed output object naming a port the step does not
	// declare. Dropping it silently would discard a value the caller believed it was supplying,
	// and the usual cause is a misspelling of one of the two names.
	ErrUndeclaredResumedOutput = errors.New("resumed outputs name a port the step does not declare")
)

// checkDeclaredOutputs validates an externally-supplied output object against a step's declared
// output ports and returns the object projected onto exactly those ports.
//
// This is the trust boundary for resumed data. Everything else in a run either came out of the
// document or out of a handler this engine dispatched; a [ResumedStep]'s outputs came from the
// caller's durable store, possibly after a process restart and a schema change, and are the
// least-trusted input in the system. Checking them here means a mistyped resumed value fails at the
// point of injection, naming the port, rather than at some downstream step that cannot explain it.
//
// A declared port the object omits becomes null, and is checked like any other value: an optional
// port accepts it and a required one does not.
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

// checkValueType reports whether value inhabits the declared type.
//
// Two kinds are accepted unconditionally, and both for the same reason — there is nothing here to
// check against, so rejecting would be guessing. An unset type is what a port of an extension class
// that declares none has; a named type is a reference into a SchemaDefRequirement, which this
// package does not resolve.
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

// checkPrimitive reports whether value inhabits one CWLType symbol.
func checkPrimitive(value any, name string) error {
	if primitiveMatches(value, name) {
		return nil
	}

	return fmt.Errorf("%w: want %s, got %s", ErrOutputType, name, cwlcore.TypeName(value))
}

// primitiveMatches is the per-symbol test behind [checkPrimitive].
//
// The integer symbols are not range-checked and the two float symbols are not distinguished,
// because a value that has been through a JSON round trip — which is what persisting a RunState
// does — no longer carries the distinction. What is checked is the shape: a number is not a string
// and an object is not either.
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

// isFilesystemObject reports whether value is an object whose class names the wanted CWL
// filesystem record. An object with no class is accepted as the record it is being checked
// against, which is what the specification's own `class` default amounts to.
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

// checkArray reports whether value is an array whose every item inhabits the item type.
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

// checkRecord reports whether value is an object whose fields inhabit the record's field types.
// A field the object omits is checked as null, so an optional field may be left out and a required
// one may not.
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

// checkEnum reports whether value is one of the enum's symbols. Symbols are resolved identifiers,
// so they are compared by short name, which is how a document writes them.
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

// checkUnion reports whether value inhabits at least one member of the union.
//
// Members are probed silently and only the summary is reported: a union of five members would
// otherwise produce five explanations of which exactly one matters, and the reader cannot tell
// which. The declared type renders itself, which names the alternatives that were tried.
func checkUnion(value any, declared cwlcore.TypeRef) error {
	for _, option := range declared.Options() {
		if checkValueType(value, option) == nil {
			return nil
		}
	}

	return fmt.Errorf("%w: want %s, got %s", ErrOutputType, declared.String(), cwlcore.TypeName(value))
}
