package cwlcore

import (
	"testing"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// assertTypeKind fails unless the given TypeKind matches.
func assertTypeKind(t *testing.T, got, want TypeKind) {
	t.Helper()

	if got != want {
		t.Errorf("Kind() = %v, want %v", got, want)
	}
}

func TestTypeRefZeroValueIsUnset(t *testing.T) {
	t.Parallel()

	var ref TypeRef

	assertTypeKind(t, ref.Kind(), TypeKindUnset)

	if ref.IsSet() {
		t.Error("IsSet() = true on the zero TypeRef")
	}

	if ref.IsNull() || ref.IsOptional() {
		t.Error("zero TypeRef claims to be null or optional")
	}
}

func TestTypeRefZeroValueHasNoPayload(t *testing.T) {
	t.Parallel()

	var ref TypeRef

	if ref.Name() != "" {
		t.Errorf("Name() = %q, want empty", ref.Name())
	}

	if ref.Record() != nil || ref.Enum() != nil || ref.Array() != nil {
		t.Error("a schema payload leaked out of the zero TypeRef")
	}

	if ref.Options() != nil || ref.Node() != nil {
		t.Error("options or node leaked out of the zero TypeRef")
	}
}

func TestTypeRefNamedKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		wantName string
		ref      TypeRef
		want     TypeKind
	}{
		{
			name: kindNamePrimitive, ref: NewPrimitiveType(PrimitiveFile),
			want: TypeKindPrimitive, wantName: PrimitiveFile,
		},
		{
			name: kindNameNamed, ref: NewNamedType("#user_type"),
			want: TypeKindNamed, wantName: "#user_type",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertTypeKind(t, tc.ref.Kind(), tc.want)

			if !tc.ref.IsSet() {
				t.Error("IsSet() = false on a constructed TypeRef")
			}

			if tc.ref.Name() != tc.wantName {
				t.Errorf("Name() = %q, want %q", tc.ref.Name(), tc.wantName)
			}
		})
	}
}

func TestTypeRefSchemaKinds(t *testing.T) {
	t.Parallel()

	record := &RecordSchema{Name: "#rec", Fields: []RecordField{{Name: "#rec/a"}}}
	enum := &EnumSchema{Name: "#en", Symbols: []string{"#en/x", "#en/y"}}
	array := &ArraySchema{Items: NewPrimitiveType(PrimitiveString)}

	recordRef := NewRecordType(record)
	assertTypeKind(t, recordRef.Kind(), TypeKindRecord)

	if recordRef.Record() != record {
		t.Error("record payload did not round-trip")
	}

	enumRef := NewEnumType(enum)
	assertTypeKind(t, enumRef.Kind(), TypeKindEnum)

	if enumRef.Enum() != enum {
		t.Error("enum payload did not round-trip")
	}

	arrayRef := NewArrayType(array)
	assertTypeKind(t, arrayRef.Kind(), TypeKindArray)

	if arrayRef.Array() != array {
		t.Error("array payload did not round-trip")
	}
}

func TestTypeRefPayloadAccessorsAreKindGated(t *testing.T) {
	t.Parallel()

	// The three schema kinds share one payload field, so reading the wrong
	// accessor must return nil rather than a mistyped pointer.
	ref := NewRecordType(&RecordSchema{})

	if ref.Enum() != nil || ref.Array() != nil || ref.Options() != nil {
		t.Error("a record TypeRef answered a non-record accessor")
	}

	union := NewUnionType([]TypeRef{NewPrimitiveType(PrimitiveInt)})
	if union.Record() != nil || union.Enum() != nil || union.Array() != nil {
		t.Error("a union TypeRef answered a schema accessor")
	}
}

func TestTypeRefShortcuts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind TypeKind
		want TypeKind
	}{
		{name: kindNameStdin, kind: TypeKindStdin, want: TypeKindStdin},
		{name: kindNameStdout, kind: TypeKindStdout, want: TypeKindStdout},
		{name: kindNameStderr, kind: TypeKindStderr, want: TypeKindStderr},
		{name: "a non-shortcut kind yields the zero TypeRef", kind: TypeKindRecord, want: TypeKindUnset},
		{name: "unset yields the zero TypeRef", kind: TypeKindUnset, want: TypeKindUnset},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertTypeKind(t, NewShortcutType(tc.kind).Kind(), tc.want)
		})
	}
}

func TestTypeRefOptional(t *testing.T) {
	t.Parallel()

	// `File?` arrives here already expanded by Schema Salad into [null, File].
	optional := NewUnionType([]TypeRef{
		NewPrimitiveType(PrimitiveNull),
		NewPrimitiveType(PrimitiveFile),
	})

	if !optional.IsOptional() {
		t.Error("a union containing null is not reported optional")
	}

	if optional.IsNull() {
		t.Error("a union is not itself the null type")
	}

	required := NewUnionType([]TypeRef{
		NewPrimitiveType(PrimitiveString),
		NewPrimitiveType(PrimitiveInt),
	})

	if required.IsOptional() {
		t.Error("a union with no null member is reported optional")
	}
}

func TestTypeRefNullPrimitive(t *testing.T) {
	t.Parallel()

	// A bare null primitive is null but not a union, so not "optional".
	null := NewPrimitiveType(PrimitiveNull)
	if !null.IsNull() {
		t.Error("the null primitive does not report IsNull")
	}

	if null.IsOptional() {
		t.Error("the null primitive reports IsOptional")
	}

	if NewPrimitiveType(PrimitiveFile).IsOptional() {
		t.Error("a bare primitive is reported optional")
	}
}

func TestTypeRefOptionOrderIsPreserved(t *testing.T) {
	t.Parallel()

	// Union member order is significant: CWL resolves a value against a
	// union by trying the members in the order the document wrote them.
	want := []string{PrimitiveNull, PrimitiveString, PrimitiveInt}

	options := make([]TypeRef, 0, len(want))
	for _, name := range want {
		options = append(options, NewPrimitiveType(name))
	}

	union := NewUnionType(options)
	for i, name := range want {
		if got := union.Options()[i].Name(); got != name {
			t.Errorf("Options()[%d].Name() = %q, want %q", i, got, name)
		}
	}
}

func TestTypeRefWithNode(t *testing.T) {
	t.Parallel()

	node := salad.NewStringNode(salad.SourceLine{}, PrimitiveFile)

	ref := NewPrimitiveType(PrimitiveFile)
	if ref.Node() != nil {
		t.Error("a freshly constructed TypeRef already carries a node")
	}

	withNode := ref.WithNode(node)
	if withNode.Node() != node {
		t.Error("WithNode did not attach the node")
	}

	if withNode.Kind() != TypeKindPrimitive || withNode.Name() != PrimitiveFile {
		t.Error("WithNode changed the type")
	}

	// WithNode returns a copy; the original is untouched.
	if ref.Node() != nil {
		t.Error("WithNode mutated its receiver")
	}
}

func TestTypeRefString(t *testing.T) {
	t.Parallel()

	arrayOfFile := NewArrayType(&ArraySchema{Items: NewPrimitiveType(PrimitiveFile)})

	tests := []struct {
		name string
		want string
		ref  TypeRef
	}{
		{name: kindNameUnset, ref: TypeRef{}, want: kindNameUnset},
		{name: kindNamePrimitive, ref: NewPrimitiveType(PrimitiveInt), want: PrimitiveInt},
		{name: kindNameNamed, ref: NewNamedType("#my_rec"), want: "#my_rec"},
		{name: kindNameArray, ref: arrayOfFile, want: "File[]"},
		{name: "array with no schema", ref: NewArrayType(nil), want: "[]"},
		{name: kindNameRecord, ref: NewRecordType(&RecordSchema{}), want: kindNameRecord},
		{name: kindNameEnum, ref: NewEnumType(&EnumSchema{}), want: kindNameEnum},
		{name: kindNameStdout, ref: NewShortcutType(TypeKindStdout), want: kindNameStdout},
		{
			name: "optional array",
			ref:  NewUnionType([]TypeRef{NewPrimitiveType(PrimitiveNull), arrayOfFile}),
			want: "null|File[]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertString(t, tc.ref.String(), tc.want)
		})
	}
}

func TestTypeKindString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want string
		kind TypeKind
	}{
		{kind: TypeKindUnset, want: kindNameUnset},
		{kind: TypeKindPrimitive, want: kindNamePrimitive},
		{kind: TypeKindRecord, want: kindNameRecord},
		{kind: TypeKindEnum, want: kindNameEnum},
		{kind: TypeKindArray, want: kindNameArray},
		{kind: TypeKindUnion, want: kindNameUnion},
		{kind: TypeKindNamed, want: kindNameNamed},
		{kind: TypeKindStdin, want: kindNameStdin},
		{kind: TypeKindStdout, want: kindNameStdout},
		{kind: TypeKindStderr, want: kindNameStderr},
		{kind: TypeKind(99), want: "TypeKind(?)"},
	}

	for _, tc := range tests {
		if got := tc.kind.String(); got != tc.want {
			t.Errorf("TypeKind(%d).String() = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

// TestPrimitiveConstantsMatchSchema checks the CWLType spellings against the
// symbols the vendored schema declares, allowing for the namespace prefixes the
// schema writes them with.
func TestPrimitiveConstantsMatchSchema(t *testing.T) {
	t.Parallel()

	declared := make(map[string]bool, 16)

	for _, rec := range schemaRecords(t) {
		name, ok := recordName(rec)
		if !ok || (name != "CWLType" && name != "PrimitiveType") {
			continue
		}

		for _, symbol := range enumSymbols(rec) {
			declared[symbol] = true
		}
	}

	// Any is declared as its own enum rather than as a PrimitiveType symbol,
	// so it is deliberately not in this list.
	primitives := []string{
		PrimitiveNull, PrimitiveBoolean, PrimitiveInt, PrimitiveLong,
		PrimitiveFloat, PrimitiveDouble, PrimitiveString,
		PrimitiveFile, PrimitiveDirectory,
	}

	for _, name := range primitives {
		if !declared[name] {
			t.Errorf("the model has primitive %q but the vendored schema declares no such CWLType symbol", name)
		}
	}

	if len(declared) != len(primitives) {
		t.Errorf("schema declares %d CWLType symbols, the model carries %d", len(declared), len(primitives))
	}
}

// enumSymbols returns the local names of an enum record's symbols.
func enumSymbols(rec *salad.MapNode) []string {
	value, ok := rec.Get("symbols")
	if !ok {
		return nil
	}

	seq, ok := salad.AsSeq(value)
	if !ok {
		return nil
	}

	out := make([]string, 0, seq.Len())

	for _, item := range seq.Items() {
		if symbol, isString := salad.AsString(item); isString {
			out = append(out, localName(symbol))
		}
	}

	return out
}
