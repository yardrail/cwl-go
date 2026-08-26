package cwlexec

import (
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// symbolRed is the enum symbol the type fixtures declare.
const symbolRed = "red"

// primitive is a shorthand for a CWLType symbol as a declared type.
func primitive(name string) cwlcore.TypeRef {
	return cwlcore.NewPrimitiveType(name)
}

// optional renders the `T?` shorthand, which decoding expands into a union with null.
func optional(inner cwlcore.TypeRef) cwlcore.TypeRef {
	return cwlcore.NewUnionType([]cwlcore.TypeRef{primitive(cwlcore.PrimitiveNull), inner})
}

// fileObject is a File value as an output object carries one.
func fileObject() map[string]any {
	return object("class", cwlcore.PrimitiveFile, "path", "/out/result.txt")
}

func TestCheckValueTypeAccepts(t *testing.T) {
	t.Parallel()

	record := cwlcore.NewRecordType(&cwlcore.RecordSchema{Fields: []cwlcore.RecordField{
		{Name: "file:///s#rec/count", Type: primitive(cwlcore.PrimitiveInt)},
		{Name: "file:///s#rec/note", Type: optional(primitive(cwlcore.PrimitiveString))},
	}})

	cases := []struct {
		value    any
		declared cwlcore.TypeRef
		name     string
	}{
		{name: "unset accepts anything", value: struct{}{}},
		{name: "named is not resolved here", value: 1, declared: cwlcore.NewNamedType("ex:Thing")},
		{name: "null", value: nil, declared: primitive(cwlcore.PrimitiveNull)},
		{name: "Any", value: 1, declared: primitive(cwlcore.PrimitiveAny)},
		{name: "boolean", value: true, declared: primitive(cwlcore.PrimitiveBoolean)},
		{name: "string", value: "s", declared: primitive(cwlcore.PrimitiveString)},
		{name: "long", value: int64(3), declared: primitive(cwlcore.PrimitiveLong)},
		{name: "double", value: 3.5, declared: primitive(cwlcore.PrimitiveDouble)},
		{name: cwlcore.PrimitiveFile, value: fileObject(), declared: primitive(cwlcore.PrimitiveFile)},
		{
			name: "classless object is the record it is checked as", value: object("path", "/p"),
			declared: primitive(cwlcore.PrimitiveDirectory),
		},
		{
			name: "stdout shortcut is a File", value: fileObject(),
			declared: cwlcore.NewShortcutType(cwlcore.TypeKindStdout),
		},
		{
			name: "array", value: list("a", "b"),
			declared: cwlcore.NewArrayType(&cwlcore.ArraySchema{Items: primitive(cwlcore.PrimitiveString)}),
		},
		{name: "array with no schema", value: list(1), declared: cwlcore.NewArrayType(nil)},
		{name: "record", value: object("count", 1, "note", "hi"), declared: record},
		{name: "record with an optional field omitted", value: object("count", 1), declared: record},
		{name: "record with no schema", value: object("k", 1), declared: cwlcore.NewRecordType(nil)},
		{
			name: "enum by short name", value: symbolRed,
			declared: cwlcore.NewEnumType(&cwlcore.EnumSchema{Symbols: []string{"file:///s#c/red"}}),
		},
		{name: "enum with no schema", value: "anything", declared: cwlcore.NewEnumType(nil)},
		{name: "optional union takes null", value: nil, declared: optional(primitive(cwlcore.PrimitiveString))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := checkValueType(tc.value, tc.declared)
			if err != nil {
				t.Fatalf("checkValueType(%v): unexpected error: %v", tc.value, err)
			}
		})
	}
}

func TestCheckValueTypeRejects(t *testing.T) {
	t.Parallel()

	record := cwlcore.NewRecordType(&cwlcore.RecordSchema{Fields: []cwlcore.RecordField{
		{Name: "file:///s#rec/count", Type: primitive(cwlcore.PrimitiveInt)},
	}})

	cases := []struct {
		value    any
		declared cwlcore.TypeRef
		name     string
	}{
		{name: "null against string", value: nil, declared: primitive(cwlcore.PrimitiveString)},
		{name: "Any against null", value: nil, declared: primitive(cwlcore.PrimitiveAny)},
		{name: "number against boolean", value: 1, declared: primitive(cwlcore.PrimitiveBoolean)},
		{name: "string against int", value: "3", declared: primitive(cwlcore.PrimitiveInt)},
		{name: "boolean against string", value: true, declared: primitive(cwlcore.PrimitiveString)},
		{
			name: "Directory against File", value: object("class", cwlcore.PrimitiveDirectory),
			declared: primitive(cwlcore.PrimitiveFile),
		},
		{name: "string against File", value: "/p", declared: primitive(cwlcore.PrimitiveFile)},
		{name: "unknown symbol", value: 1, declared: primitive("Complex")},
		{
			name: "scalar against array", value: 1,
			declared: cwlcore.NewArrayType(&cwlcore.ArraySchema{Items: primitive(cwlcore.PrimitiveInt)}),
		},
		{
			name: "array item of the wrong type", value: list(1, "two"),
			declared: cwlcore.NewArrayType(&cwlcore.ArraySchema{Items: primitive(cwlcore.PrimitiveInt)}),
		},
		{name: "scalar against record", value: 1, declared: record},
		{name: "record field of the wrong type", value: object("count", "many"), declared: record},
		{
			name: "non-string against enum", value: 1,
			declared: cwlcore.NewEnumType(&cwlcore.EnumSchema{Symbols: []string{symbolRed}}),
		},
		{
			name: "symbol not declared", value: "blue",
			declared: cwlcore.NewEnumType(&cwlcore.EnumSchema{Symbols: []string{symbolRed}}),
		},
		{name: "no union member matches", value: 1, declared: optional(primitive(cwlcore.PrimitiveString))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := checkValueType(tc.value, tc.declared)
			if err == nil {
				t.Fatalf("checkValueType(%v, %s): want an error, got none", tc.value, tc.declared)
			}
		})
	}
}

func TestCheckDeclaredOutputsFillsAnOmittedOptionalPort(t *testing.T) {
	t.Parallel()

	step := &plannedStep{
		id:  "s1",
		out: []string{portA, portB},
		outTypes: map[string]cwlcore.TypeRef{
			portA: primitive(cwlcore.PrimitiveString),
			portB: optional(primitive(cwlcore.PrimitiveInt)),
		},
	}

	checked, err := checkDeclaredOutputs(step, object(portA, "yes"))
	if err != nil {
		t.Fatalf("checkDeclaredOutputs: unexpected error: %v", err)
	}

	assertDeepEqual(t, "checked", checked, object(portA, "yes", portB, nil))
}

func TestCheckDeclaredOutputsRejectsAnOmittedRequiredPort(t *testing.T) {
	t.Parallel()

	step := &plannedStep{
		id:       "s1",
		out:      []string{portA},
		outTypes: map[string]cwlcore.TypeRef{portA: primitive(cwlcore.PrimitiveString)},
	}

	_, err := checkDeclaredOutputs(step, make(map[string]any))
	assertErrorIs(t, "checkDeclaredOutputs", err, ErrOutputType)
}

func TestCheckDeclaredOutputsNamesEveryUndeclaredPort(t *testing.T) {
	t.Parallel()

	step := &plannedStep{id: "s1", out: []string{portA}, outTypes: make(map[string]cwlcore.TypeRef)}

	_, err := checkDeclaredOutputs(step, object(portA, 1, "zeta", 2, "alpha", 3))
	assertErrorIs(t, "checkDeclaredOutputs", err, ErrUndeclaredResumedOutput)

	if got := err.Error(); got == "" || !containsAll(got, "alpha", "zeta") {
		t.Fatalf("error = %q, want it to name every undeclared port", got)
	}
}

// containsAll reports whether text mentions every fragment.
func containsAll(text string, fragments ...string) bool {
	for _, fragment := range fragments {
		found := false

		for index := 0; index+len(fragment) <= len(text); index++ {
			if text[index:index+len(fragment)] == fragment {
				found = true

				break
			}
		}

		if !found {
			return false
		}
	}

	return true
}
