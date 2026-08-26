package salad

import (
	"slices"
	"testing"
)

func checkPrimitiveKind(t *testing.T, name string, kind PrimitiveKind) {
	t.Helper()

	if got := kind.String(); got != name {
		t.Errorf("String() = %q, want %q", got, name)
	}

	got, ok := PrimitiveKindOf(name)
	if !ok || got != kind {
		t.Errorf("PrimitiveKindOf(%q) = (%v, %v), want (%v, true)", name, got, ok, kind)
	}

	if p := Primitive(kind); p.Kind != kind || p.TypeName() != name {
		t.Errorf("Primitive(%v) = %+v, want kind %v named %q", kind, p, kind, name)
	}
}

func TestPrimitiveKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind PrimitiveKind
	}{
		{name: nameNull, kind: PrimitiveNull},
		{name: nameBoolean, kind: PrimitiveBoolean},
		{name: nameInt, kind: PrimitiveInt},
		{name: nameLong, kind: PrimitiveLong},
		{name: nameFloat, kind: PrimitiveFloat},
		{name: nameDouble, kind: PrimitiveDouble},
		{name: nameString, kind: PrimitiveString},
		{name: nameAny, kind: PrimitiveAny},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkPrimitiveKind(t, tc.name, tc.kind)
		})
	}
}

func TestPrimitiveKindUnknown(t *testing.T) {
	t.Parallel()

	if _, ok := PrimitiveKindOf("CommandLineTool"); ok {
		t.Error("PrimitiveKindOf accepted a non-primitive name")
	}

	outOfRange := PrimitiveKind(len(primitiveKindNames) + 1)
	if got := outOfRange.String(); got != nameUnknown {
		t.Errorf("String() = %q, want %q", got, nameUnknown)
	}

	if got := Primitive(outOfRange).TypeName(); got != nameUnknown {
		t.Errorf("Primitive(out-of-range).TypeName() = %q, want %q", got, nameUnknown)
	}
}

func TestPrimitiveIsShared(t *testing.T) {
	t.Parallel()

	first := Primitive(PrimitiveString)
	second := Primitive(PrimitiveString)

	if first != second {
		t.Error("Primitive should return a shared immutable value per kind")
	}
}

func TestAnonymousTypesHaveNoName(t *testing.T) {
	t.Parallel()

	anon := []Type{
		&ArrayType{Items: Primitive(PrimitiveString)},
		&MapType{Values: Primitive(PrimitiveInt)},
		&UnionType{Options: []Type{Primitive(PrimitiveNull), Primitive(PrimitiveString)}},
	}
	for _, tc := range anon {
		if got := tc.TypeName(); got != "" {
			t.Errorf("%T.TypeName() = %q, want the empty string", tc, got)
		}
	}
}

func TestUnionHasNull(t *testing.T) {
	t.Parallel()

	optional := &UnionType{Options: []Type{Primitive(PrimitiveNull), Primitive(PrimitiveString)}}
	if !optional.HasNull() {
		t.Error("HasNull() = false for a union containing null")
	}

	required := &UnionType{Options: []Type{Primitive(PrimitiveString), Primitive(PrimitiveInt)}}
	if required.HasNull() {
		t.Error("HasNull() = true for a union without null")
	}

	var nilUnion *UnionType
	if nilUnion.HasNull() {
		t.Error("HasNull() = true for a nil union")
	}
}

func TestUnionOptionsKeepOrder(t *testing.T) {
	t.Parallel()

	u := &UnionType{Options: []Type{
		Primitive(PrimitiveNull),
		&ArrayType{Items: Primitive(PrimitiveString)},
		Primitive(PrimitiveString),
	}}
	want := []string{nameNull, "", nameString}

	got := make([]string, 0, len(u.Options))
	for _, opt := range u.Options {
		got = append(got, opt.TypeName())
	}

	if !slices.Equal(got, want) {
		t.Errorf("option names = %v, want %v", got, want)
	}
}

func TestEnumHasSymbolMatchesShortName(t *testing.T) {
	t.Parallel()

	// Symbols hold fully-qualified IRIs, but documents spell them short.
	e := &EnumType{
		Name: "https://w3id.org/cwl/cwl#CWLVersion",
		Symbols: []string{
			"https://w3id.org/cwl/cwl#CWLVersion/v1_2",
			"https://w3id.org/cwl/cwl#CWLVersion/v1_1",
			"https://example.org/plain",
		},
	}

	tests := []struct {
		name string
		sym  string
		want bool
	}{
		{name: "short name", sym: "v1_2", want: true},
		{name: "full IRI", sym: "https://w3id.org/cwl/cwl#CWLVersion/v1_1", want: true},
		{name: "short name of a path-only IRI", sym: "plain", want: true},
		{name: "different fragment, same short name", sym: "https://other.example/x#Other/v1_2", want: true},
		{name: "unknown symbol", sym: "v1_0", want: false},
		{name: "empty", sym: "", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := e.HasSymbol(tc.sym); got != tc.want {
				t.Errorf("HasSymbol(%q) = %v, want %v", tc.sym, got, tc.want)
			}
		})
	}
}

func TestEnumNilIsSafe(t *testing.T) {
	t.Parallel()

	var nilEnum *EnumType
	if nilEnum.HasSymbol("x") || nilEnum.TypeName() != "" {
		t.Error("a nil EnumType should have no symbols and no name")
	}
}

func TestShortName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		id   string
		want string
	}{
		{name: "fragment with path", id: "https://w3id.org/cwl/cwl#Process/inputs", want: fieldInputs},
		{name: "fragment without path", id: "https://w3id.org/cwl/cwl#Process", want: "Process"},
		{name: "path only", id: "https://example.org/a/b/c", want: "c"},
		{name: "empty fragment falls back to the path", id: "https://example.org/a/b#", want: "b"},
		{name: "bare name", id: "name", want: "name"},
		{name: "empty", id: "", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := shortName(tc.id); got != tc.want {
				t.Errorf("shortName(%q) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}

func TestRecordFieldLookup(t *testing.T) {
	t.Parallel()

	inputs := &Field{Name: "https://w3id.org/cwl/cwl#Process/inputs", Type: Primitive(PrimitiveString)}
	outputs := &Field{Name: "outputs", Type: Primitive(PrimitiveString), InheritedFrom: "Process"}
	rec := &RecordType{Name: "https://w3id.org/cwl/cwl#Tool", Fields: []*Field{inputs, outputs}}

	if got, ok := rec.Field("https://w3id.org/cwl/cwl#Process/inputs"); !ok || got != inputs {
		t.Error("exact-name lookup failed")
	}

	if got, ok := rec.Field(fieldInputs); !ok || got != inputs {
		t.Error("short-name lookup failed")
	}

	if got, ok := rec.Field("https://other.example/x#Y/outputs"); !ok || got != outputs {
		t.Error("short-name lookup of a locally-declared field failed")
	}

	if _, ok := rec.Field("missing"); ok {
		t.Error("lookup of an absent field succeeded")
	}

	if got := inputs.ShortName(); got != fieldInputs {
		t.Errorf("ShortName() = %q, want %q", got, fieldInputs)
	}
}

func TestRecordNilIsSafe(t *testing.T) {
	t.Parallel()

	var nilRec *RecordType
	if _, ok := nilRec.Field("x"); ok {
		t.Error("a nil RecordType should have no fields")
	}

	if nilRec.IsDocumentRoot() || nilRec.TypeName() != "" {
		t.Error("a nil RecordType should be neither named nor a document root")
	}

	var nilField *Field
	if nilField.ShortName() != "" {
		t.Error("a nil Field should have no short name")
	}
}

func TestRecordFieldsKeepOrder(t *testing.T) {
	t.Parallel()

	names := []string{"class", "id", fieldInputs, "outputs", "requirements", "hints"}

	fields := make([]*Field, 0, len(names))
	for _, name := range names {
		fields = append(fields, &Field{Name: name, Type: Primitive(PrimitiveString)})
	}

	rec := &RecordType{Fields: fields}

	got := make([]string, 0, len(rec.Fields))
	for _, f := range rec.Fields {
		got = append(got, f.Name)
	}

	if !slices.Equal(got, names) {
		t.Errorf("field order = %v, want %v", got, names)
	}
}

func TestNewSchema(t *testing.T) {
	t.Parallel()

	tool := &RecordType{Name: typeTool, DocumentRoot: true}
	workflow := &RecordType{Name: "Workflow", DocumentRoot: true}
	param := &RecordType{Name: "Parameter"}
	version := &EnumType{Name: "Version", Symbols: []string{"v1_2"}}
	anon := &ArrayType{Items: Primitive(PrimitiveString)}

	s := NewSchema([]Type{tool, param, version, anon, workflow})

	wantNames := []string{typeTool, "Parameter", "Version", "Workflow"}
	if got := s.Names(); !slices.Equal(got, wantNames) {
		t.Errorf("Names() = %v, want %v", got, wantNames)
	}

	if got, ok := s.Type("Version"); !ok || got != Type(version) {
		t.Error("Type() did not resolve a named enum")
	}

	if _, ok := s.Type("Nope"); ok {
		t.Error("Type() resolved an undefined name")
	}

	roots := s.DocumentRoots()
	if len(roots) != 2 || roots[0] != tool || roots[1] != workflow {
		t.Errorf("DocumentRoots() = %v, want Tool then Workflow in declaration order", roots)
	}
}

func TestNewSchemaRedefinition(t *testing.T) {
	t.Parallel()

	first := &RecordType{Name: typeTool}
	second := &RecordType{Name: typeTool, DocumentRoot: true}
	other := &RecordType{Name: "Other"}

	s := NewSchema([]Type{first, other, second})

	if got, want := s.Names(), []string{typeTool, "Other"}; !slices.Equal(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}

	if got, ok := s.Type(typeTool); !ok || got != Type(second) {
		t.Error("the last definition of a name should win")
	}

	if roots := s.DocumentRoots(); len(roots) != 1 || roots[0] != second {
		t.Errorf("DocumentRoots() = %v, want just the redefined Tool", roots)
	}
}

func TestSchemaAccessorsReturnCopies(t *testing.T) {
	t.Parallel()

	s := NewSchema([]Type{&RecordType{Name: typeTool, DocumentRoot: true}})

	names := s.Names()
	names[0] = "tampered"

	if got := s.Names(); got[0] != typeTool {
		t.Errorf("Names() shares its backing array, got %v", got)
	}

	roots := s.DocumentRoots()
	roots[0] = nil

	if got := s.DocumentRoots(); got[0] == nil {
		t.Error("DocumentRoots() shares its backing array")
	}
}

func TestSchemaNilIsSafe(t *testing.T) {
	t.Parallel()

	var s *Schema
	if _, ok := s.Type("x"); ok {
		t.Error("a nil Schema should define no types")
	}

	if len(s.Names()) != 0 || len(s.DocumentRoots()) != 0 {
		t.Error("a nil Schema should be empty")
	}
}
