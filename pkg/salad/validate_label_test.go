package salad

import (
	"strings"
	"testing"
)

func TestTypeLabel(t *testing.T) {
	t.Parallel()

	strArray := &ArrayType{Items: Primitive(PrimitiveString)}

	tests := []struct {
		typ  Type
		name string
		want string
	}{
		{name: "primitive", typ: Primitive(PrimitiveInt), want: nameInt},
		{name: "record by short name", typ: record(typeDoc), want: "Doc"},
		{name: "array type", typ: strArray, want: "an array of string"},
		{name: "map type", typ: &MapType{Values: Primitive(PrimitiveInt)}, want: "a map of int"},
		{name: "union", typ: union(Primitive(PrimitiveInt), strArray), want: "one of int, an array of string"},
		{name: "optional collapses", typ: optional(Primitive(PrimitiveInt)), want: nameInt},
		{name: "union of only null", typ: union(Primitive(PrimitiveNull)), want: nameNull},
		{name: "absent type", typ: nil, want: nameNothing},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := typeLabel(tc.typ); got != tc.want {
				t.Errorf("typeLabel() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTypeLabelElidesDeepNesting(t *testing.T) {
	t.Parallel()

	deep := &ArrayType{Items: &ArrayType{Items: &ArrayType{Items: &ArrayType{Items: Primitive(PrimitiveInt)}}}}

	got := typeLabel(deep)
	if !strings.HasSuffix(got, labelEllipsis) {
		t.Errorf("typeLabel() = %q, want it to trail off with %q", got, labelEllipsis)
	}
}

func TestTypeLabelOfASelfReferentialUnionTerminates(t *testing.T) {
	t.Parallel()

	cyclic := &UnionType{}
	cyclic.Options = []Type{Primitive(PrimitiveInt), cyclic}

	if got := typeLabel(cyclic); got == "" {
		t.Error("typeLabel of a cyclic union should still render something")
	}
}

func TestFieldAndSymbolListsTrailOff(t *testing.T) {
	t.Parallel()

	fields := make([]*Field, 0, maxListedFields+3)
	symbols := make([]string, 0, maxListedSymbols+3)

	for i := range maxListedFields + 3 {
		fields = append(fields, field(string(rune('a'+i)), Primitive(PrimitiveString)))
	}

	for i := range maxListedSymbols + 3 {
		symbols = append(symbols, string(rune('a'+i)))
	}

	if got := fieldNames(record(typeDoc, fields...)); !strings.HasSuffix(got, labelEllipsis) {
		t.Errorf("fieldNames() = %q, want it to trail off", got)
	}

	if got := symbolNames(&EnumType{Name: typeDoc, Symbols: symbols}); !strings.HasSuffix(got, labelEllipsis) {
		t.Errorf("symbolNames() = %q, want it to trail off", got)
	}
}

func TestHasURIScheme(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ref  string
		want bool
	}{
		{ref: "file:///a.yml", want: true},
		{ref: "https://example.com/a", want: true},
		{ref: "s3://bucket/key", want: true},
		{ref: "view-source:http://x", want: true},
		{ref: "plain", want: false},
		{ref: "#fragment", want: false},
		{ref: "not a scheme:thing", want: false},
		{ref: "9lives:thing", want: false},
		{ref: ":leading", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.ref, func(t *testing.T) {
			t.Parallel()

			if got := hasURIScheme(tc.ref); got != tc.want {
				t.Errorf("hasURIScheme(%q) = %v, want %v", tc.ref, got, tc.want)
			}
		})
	}
}

func TestAcceptsNull(t *testing.T) {
	t.Parallel()

	tests := []struct {
		typ  Type
		name string
		want bool
	}{
		{name: "null primitive", typ: Primitive(PrimitiveNull), want: true},
		{name: "other primitive", typ: Primitive(PrimitiveString), want: false},
		{name: "optional union", typ: optional(Primitive(PrimitiveString)), want: true},
		{name: "closed union", typ: union(Primitive(PrimitiveString)), want: false},
		{name: "record type", typ: record(typeDoc), want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := acceptsNull(tc.typ); got != tc.want {
				t.Errorf("acceptsNull() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidateRejectsAFieldWithNoDeclaredType(t *testing.T) {
	t.Parallel()

	s := singleFieldSchema(nil)

	err := s.Validate(mustParse(t, testFile, "v: a\n"))
	if err == nil {
		t.Fatal("a field whose schema declares no type cannot be validated")
	}

	assertMentions(t, err, "declares no type")
}

func TestValidateProbesCollectionsInsideAUnion(t *testing.T) {
	t.Parallel()

	strArray := &ArrayType{Items: Primitive(PrimitiveString)}
	intMap := &MapType{Values: Primitive(PrimitiveInt)}
	s := singleFieldSchema(union(strArray, intMap))

	err := s.Validate(mustParse(t, testFile, "v: [a, b]\n"))
	if err != nil {
		t.Fatalf("the array member should have matched: %v", err)
	}

	err = s.Validate(mustParse(t, testFile, "v: {a: 1}\n"))
	if err != nil {
		t.Fatalf("the map member should have matched: %v", err)
	}

	err = s.Validate(mustParse(t, testFile, "v: [a, 2]\n"))
	if err == nil {
		t.Fatal("neither member accepts a sequence holding an int")
	}

	assertMentions(t, err, "tried an array of string, but", "tried a map of int, but", "item 1 is not valid")
}

func TestValidateRejectsAnUnknownPrimitiveKind(t *testing.T) {
	t.Parallel()

	s := singleFieldSchema(&PrimitiveType{Kind: PrimitiveKind(len(primitiveKindNames) + 1)})

	err := s.Validate(mustParse(t, testFile, "v: a\n"))
	if err == nil {
		t.Fatal("a primitive the package does not know cannot be satisfied")
	}

	assertMentions(t, err, nameUnknown)
}

func TestValidateRejectsAnEmptyUnion(t *testing.T) {
	t.Parallel()

	s := singleFieldSchema(union())

	err := s.Validate(mustParse(t, testFile, "v: a\n"))
	if err == nil {
		t.Fatal("a union with no members cannot be satisfied")
	}
}

func TestValidateReportsWarningsOnlyAlongsideRealErrors(t *testing.T) {
	t.Parallel()

	s := singleFieldSchema(Primitive(PrimitiveString))

	err := s.Validate(mustParse(t, testFile, "v: 3\nextra: 1\n"))
	if err == nil {
		t.Fatal("a type mismatch must invalidate the document")
	}

	se := mustSaladError(t, err)

	var warnings int

	for _, leaf := range se.Leaves() {
		if leaf.Warning {
			warnings++
		}
	}

	if warnings != 1 {
		t.Errorf("want the tolerated unknown field carried along as a warning, got %d:\n%s", warnings, se.Pretty())
	}
}
