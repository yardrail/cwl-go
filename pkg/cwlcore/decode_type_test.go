package cwlcore

import (
	"strings"
	"testing"
)

// operationHead is the smallest document that carries one input parameter, used
// to drive the type decoder from real document text. The parameter's type block
// is appended, already indented to sit under the parameter.
const operationHead = "class: Operation\ncwlVersion: v1.2\noutputs: []\ninputs:\n  - id: p\n"

// typeBlockHead opens the parameter's type field, for the cases whose type is an
// inline schema and so opens with a nested type discriminator of its own.
const typeBlockHead = "    type:\n"

// decodeParamType decodes the single input parameter's type out of a document
// built from operationHead plus block.
func decodeParamType(t *testing.T, block string) TypeRef {
	t.Helper()

	operation, ok := decodeSource(t, operationHead+block).(*Operation)
	if !ok {
		t.Fatal("decoded process is not a *Operation")
	}

	return operation.Inputs[0].Type
}

func TestDecodeTypeShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		block    string
		wantName string
		wantKind TypeKind
	}{
		{
			name:     "primitive",
			block:    "    type: string\n",
			wantKind: TypeKindPrimitive,
			wantName: PrimitiveString,
		},
		{
			name:     "primitive spelled with the cwl prefix",
			block:    "    type: \"cwl:File\"\n",
			wantKind: TypeKindPrimitive,
			wantName: PrimitiveFile,
		},
		{
			name:     "primitive spelled as a vocabulary IRI",
			block:    "    type: \"https://w3id.org/cwl/salad#Any\"\n",
			wantKind: TypeKindPrimitive,
			wantName: PrimitiveAny,
		},
		{
			name:     "reference to a named type",
			block:    "    type: \"#user_record\"\n",
			wantKind: TypeKindNamed,
			wantName: "#user_record",
		},
		{
			name:     "stdin shortcut",
			block:    "    type: stdin\n",
			wantKind: TypeKindStdin,
		},
		{
			name:     "stdout shortcut",
			block:    "    type: stdout\n",
			wantKind: TypeKindStdout,
		},
		{
			name:     "stderr shortcut",
			block:    "    type: stderr\n",
			wantKind: TypeKindStderr,
		},
		{
			name:     "union",
			block:    "    type: [\"null\", int]\n",
			wantKind: TypeKindUnion,
		},
		{
			name:     "inline array",
			block:    typeBlockHead + "      type: array\n      items: File\n",
			wantKind: TypeKindArray,
		},
		{
			name:     "inline record",
			block:    typeBlockHead + "      type: record\n      name: rec\n      fields: []\n",
			wantKind: TypeKindRecord,
		},
		{
			name:     "inline enum",
			block:    typeBlockHead + "      type: enum\n      name: en\n      symbols: [a]\n",
			wantKind: TypeKindEnum,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			decoded := decodeParamType(t, tc.block)

			assertEqual(t, "Kind()", decoded.Kind(), tc.wantKind)
			assertEqual(t, "Name()", decoded.Name(), tc.wantName)

			if decoded.Node() == nil {
				t.Error("Node() is nil, want the node the type was decoded from")
			}
		})
	}
}

func TestDecodeTypeOptional(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		block string
	}{
		{name: "quoted null", block: "    type: [\"null\", int]\n"},
		{name: "bare null", block: "    type: [null, int]\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			decoded := decodeParamType(t, tc.block)

			assertEqual(t, "IsOptional()", decoded.IsOptional(), true)
			assertEqual(t, "len(Options())", len(decoded.Options()), 2)
			assertEqual(t, "Options()[0].IsNull()", decoded.Options()[0].IsNull(), true)
			assertEqual(t, "Options()[1].Name()", decoded.Options()[1].Name(), PrimitiveInt)
		})
	}
}

func TestDecodeTypeInlineArray(t *testing.T) {
	t.Parallel()

	decoded := decodeParamType(t, typeBlockHead+"      type: array\n      name: pair\n      items: File\n")

	schema := decoded.Array()
	if schema == nil {
		t.Fatal("Array() is nil")
	}

	assertEqual(t, "Array().Name", schema.Name, "pair")
	assertEqual(t, "Array().Items.Name()", schema.Items.Name(), PrimitiveFile)
	assertEqual(t, "String()", decoded.String(), "File[]")
}

func TestDecodeTypeInlineRecord(t *testing.T) {
	t.Parallel()

	const block = typeBlockHead + `      type: record
      name: point
      label: a point
      doc: two coordinates
      fields:
        - name: x
          type: int
          doc: [the abscissa]
          inputBinding:
            prefix: "-x"
        - name: y
          type: int
          secondaryFiles: .idx
          streamable: true
          loadContents: true
          loadListing: no_listing
`

	decoded := decodeParamType(t, block)

	schema := decoded.Record()
	if schema == nil {
		t.Fatal("Record() is nil")
	}

	assertEqual(t, "Record().Name", schema.Name, "point")
	assertEqual(t, "Record().Label", schema.Label, "a point")
	assertEqual(t, "len(Record().Doc)", len(schema.Doc), 1)
	assertEqual(t, "len(Record().Fields)", len(schema.Fields), 2)

	// Field order is document order, because it decides the order of the
	// command-line arguments a record input produces.
	assertEqual(t, "Fields[0].Name", schema.Fields[0].Name, "x")
	assertEqual(t, "Fields[0].Type.Name()", schema.Fields[0].Type.Name(), PrimitiveInt)
	assertEqual(t, "Fields[0].Doc[0]", schema.Fields[0].Doc[0], "the abscissa")
	assertEqual(t, "Fields[0].InputBinding.Prefix", schema.Fields[0].InputBinding.Prefix, "-x")
	assertEqual(t, "Fields[1].Name", schema.Fields[1].Name, "y")
	assertEqual(t, "Fields[1].SecondaryFiles[0].Pattern", string(schema.Fields[1].SecondaryFiles[0].Pattern), ".idx")
	assertEqual(t, "Fields[1].Streamable", schema.Fields[1].Streamable, true)
	assertEqual(t, "Fields[1].LoadContents", schema.Fields[1].LoadContents, true)
	assertEqual(t, "Fields[1].LoadListing", schema.Fields[1].LoadListing, LoadListingNone)

	if schema.Fields[1].Node == nil {
		t.Error("Fields[1].Node is nil, want the node the field was decoded from")
	}
}

func TestDecodeTypeInlineRecordFieldsAsIdentifierMap(t *testing.T) {
	t.Parallel()

	const block = typeBlockHead + `      type: record
      fields:
        y: int
        x: string
`

	schema := decodeParamType(t, block).Record()
	if schema == nil {
		t.Fatal("Record() is nil")
	}

	// The identifier map is expanded in sorted key order, and the bare value
	// becomes the field's type through the schema's mapPredicate.
	assertEqual(t, "len(Fields)", len(schema.Fields), 2)
	assertEqual(t, "Fields[0].Name", schema.Fields[0].Name, "x")
	assertEqual(t, "Fields[0].Type.Name()", schema.Fields[0].Type.Name(), PrimitiveString)
	assertEqual(t, "Fields[1].Name", schema.Fields[1].Name, "y")
	assertEqual(t, "Fields[1].Type.Name()", schema.Fields[1].Type.Name(), PrimitiveInt)
}

func TestDecodeTypeInlineEnum(t *testing.T) {
	t.Parallel()

	const block = typeBlockHead + `      type: enum
      name: colour
      symbols: [red, green, blue]
      inputBinding:
        prefix: "--colour"
`

	schema := decodeParamType(t, block).Enum()
	if schema == nil {
		t.Fatal("Enum() is nil")
	}

	assertEqual(t, "Enum().Name", schema.Name, "colour")
	assertEqual(t, "Enum().Symbols", strings.Join(schema.Symbols, ","), "red,green,blue")
	assertEqual(t, "Enum().InputBinding.Prefix", schema.InputBinding.Prefix, "--colour")
}

func TestDecodeTypeNestedUnionOfInlineSchemas(t *testing.T) {
	t.Parallel()

	const block = typeBlockHead + `      - "null"
      - type: array
        items:
          type: enum
          symbols: [a, b]
`

	decoded := decodeParamType(t, block)

	assertEqual(t, "Kind()", decoded.Kind(), TypeKindUnion)
	assertEqual(t, "IsOptional()", decoded.IsOptional(), true)

	inner := decoded.Options()[1]
	assertEqual(t, "Options()[1].Kind()", inner.Kind(), TypeKindArray)
	assertEqual(t, "Options()[1].Array().Items.Kind()", inner.Array().Items.Kind(), TypeKindEnum)
}

func TestDecodeTypeAbsent(t *testing.T) {
	t.Parallel()

	// A parameter with no type is not schema-valid, but decoding must still
	// produce a well-defined zero rather than inventing one.
	decoded := decodeParamType(t, "    label: untyped\n")

	assertEqual(t, "IsSet()", decoded.IsSet(), false)
	assertEqual(t, "Kind()", decoded.Kind(), TypeKindUnset)
}
