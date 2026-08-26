package salad

import "testing"

// Names used by the hand-built type graphs the subtype tests compare.
const (
	typeRoot    = "https://example.com/s#Base"
	typeTier    = "https://example.com/s#Middle"
	typeTip     = "https://example.com/s#Leaf"
	typeColour  = "https://example.com/s#Colour"
	typeShade   = "https://example.com/s#Shade"
	symbolRed   = "https://example.com/s#Colour/red"
	symbolGreen = "https://example.com/s#Colour/green"
	symbolBlue  = "https://example.com/s#Colour/blue"
)

// enumType builds an enum of the given symbols.
func enumType(name string, symbols ...string) *EnumType {
	return &EnumType{Name: name, Symbols: symbols}
}

// subtypeSchema is the name table the record cases resolve base types through:
// Leaf extends Middle extends Base.
func subtypeSchema() *Schema {
	base := abstractRecord(typeRoot, field("v", Primitive(PrimitiveString)))
	middle := extending(abstractRecord(typeTier,
		field("v", Primitive(PrimitiveString)), field("m", Primitive(PrimitiveInt))), typeRoot)
	leaf := extending(record(typeTip,
		field("v", Primitive(PrimitiveString)),
		field("m", Primitive(PrimitiveInt)),
		field("l", Primitive(PrimitiveInt))), typeTier)

	return NewSchema([]Type{base, middle, leaf})
}

// namedType resolves a type the subtype tests require the schema to define.
func namedType(t *testing.T, s *Schema, name string) Type {
	t.Helper()

	found, ok := s.Type(name)
	if !ok {
		t.Fatalf("the schema defines no type named %q", name)
	}

	return found
}

func TestIsSubtypePrimitivesAndAny(t *testing.T) {
	t.Parallel()

	str := Primitive(PrimitiveString)
	anything := Primitive(PrimitiveAny)

	cases := []struct {
		name  string
		sub   Type
		super Type
		want  bool
	}{
		{name: "a primitive is a subtype of itself", sub: str, super: str, want: true},
		{name: "unrelated primitives are not", sub: Primitive(PrimitiveInt), super: str},
		{name: "int does not widen into long", sub: Primitive(PrimitiveInt), super: Primitive(PrimitiveLong)},
		{name: "any type narrows Any", sub: str, super: anything, want: true},
		{name: "a list narrows Any", sub: &ArrayType{Items: str}, super: anything, want: true},
		{name: "Any does not narrow a primitive", sub: anything, super: str},
		{name: "null does not narrow Any", sub: Primitive(PrimitiveNull), super: anything},
		{name: "an optional does not narrow Any", sub: optional(str), super: anything},
		{name: "an empty union does not narrow Any", sub: union(), super: anything},
		{name: "nothing is a subtype of nothing", sub: nil, super: nil, want: true},
		{name: "nothing is not a subtype of a type", sub: nil, super: str},
		{name: "a type is not a subtype of nothing", sub: str, super: nil},
	}

	runSubtypeCases(t, nil, cases)
}

func TestIsSubtypeUnionsArraysAndMaps(t *testing.T) {
	t.Parallel()

	str := Primitive(PrimitiveString)
	num := Primitive(PrimitiveInt)

	cases := []struct {
		name  string
		sub   Type
		super Type
		want  bool
	}{
		{name: "a required value narrows an optional", sub: str, super: optional(str), want: true},
		{name: "an optional does not narrow a required value", sub: optional(str), super: str},
		{name: "one member narrows the union", sub: num, super: union(str, num), want: true},
		{name: "a union narrows a reordering of itself", sub: union(str, num), super: union(num, str), want: true},
		{name: "a union does not narrow one of its members", sub: union(str, num), super: str},
		{
			name: "a plain type matching no union member does not narrow",
			sub:  str, super: union(num, Primitive(PrimitiveBoolean)),
		},
		{
			name: "a list narrows a list of a wider element",
			sub:  &ArrayType{Items: str}, super: &ArrayType{Items: optional(str)}, want: true,
		},
		{
			name: "a list of a wider element does not narrow",
			sub:  &ArrayType{Items: optional(str)}, super: &ArrayType{Items: str},
		},
		{name: "a list is not a map", sub: &ArrayType{Items: str}, super: &MapType{Values: str}},
		{
			name: "a map narrows a map of Any",
			sub:  &MapType{Values: str}, super: &MapType{Values: Primitive(PrimitiveAny)}, want: true,
		},
		{
			name: "a map of a wider value does not narrow",
			sub:  &MapType{Values: optional(str)}, super: &MapType{Values: str},
		},
	}

	runSubtypeCases(t, nil, cases)
}

func TestIsSubtypeEnums(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		sub   Type
		super Type
		want  bool
	}{
		{
			name:  "fewer symbols narrow more",
			sub:   enumType(typeShade, symbolRed, symbolGreen),
			super: enumType(typeColour, symbolRed, symbolGreen, symbolBlue),
			want:  true,
		},
		{
			name:  "more symbols do not narrow fewer",
			sub:   enumType(typeShade, symbolRed, symbolGreen, symbolBlue),
			super: enumType(typeColour, symbolRed, symbolGreen),
		},
		{
			name:  "the same enum by name narrows itself",
			sub:   enumType(typeColour, symbolRed),
			super: enumType(typeColour, symbolBlue),
			want:  true,
		},
		{
			// IsSubtype's own generic contract: this is the structural comparison
			// used everywhere except checkOverride's enum-field-override rule,
			// which bypasses IsSubtype for enums entirely (see overrideNarrows in
			// flatten.go) and is tested separately in flatten_test.go.
			name:  "symbols are matched by short name across scopes",
			sub:   enumType(typeShade, "https://example.com/other#Shade/red"),
			super: enumType(typeColour, symbolRed),
			want:  true,
		},
		{name: "an enum is not a record", sub: enumType(typeColour, symbolRed), super: record(typeRoot)},
		{name: "a record is not an enum", sub: record(typeRoot), super: enumType(typeColour, symbolRed)},
	}

	runSubtypeCases(t, nil, cases)
}

func TestIsSubtypeRecordsFollowExtends(t *testing.T) {
	t.Parallel()

	s := subtypeSchema()
	base := namedType(t, s, typeRoot)
	middle := namedType(t, s, typeTier)
	leaf := namedType(t, s, typeTip)

	cases := []struct {
		name  string
		sub   Type
		super Type
		want  bool
	}{
		{name: "a record narrows its direct base", sub: middle, super: base, want: true},
		{name: "a record narrows a base two levels up", sub: leaf, super: base, want: true},
		{name: "a base does not narrow what extends it", sub: base, super: leaf},
		{name: "a record narrows itself", sub: leaf, super: leaf, want: true},
	}

	runSubtypeCases(t, s, cases)
}

// TestRecordNarrowsSameNameDifferentInstances covers recordNarrows' own
// sameTypeName reflexive check with two distinct *RecordType values (not the
// same Go pointer, so check()'s own "sub == super" identity short-circuit
// cannot be what answers it) that happen to carry the same fully-qualified
// name.
func TestRecordNarrowsSameNameDifferentInstances(t *testing.T) {
	t.Parallel()

	a := record(typeTip, field("v", Primitive(PrimitiveString)))
	b := record(typeTip, field("different", Primitive(PrimitiveInt)))

	if a == b {
		t.Fatal("test setup: a and b must be distinct instances")
	}

	if !(*Schema)(nil).IsSubtype(a, b) {
		t.Error("two distinct records sharing the same name must narrow each other by name alone")
	}
}

// TestExtendsTransitivelySkipsARepeatedBaseName covers the seen-name dedup
// branch of extendsTransitively's BFS: a record whose extends list names the
// same base twice must not walk it twice.
func TestExtendsTransitivelySkipsARepeatedBaseName(t *testing.T) {
	t.Parallel()

	base := abstractRecord(typeRoot, field("v", Primitive(PrimitiveString)))
	dup := extending(record(typeShade, field("v", Primitive(PrimitiveString))), typeRoot, typeRoot)
	leaf := record(typeTip, field("v", Primitive(PrimitiveString)), field("extra", Primitive(PrimitiveInt)))

	s := NewSchema([]Type{base, dup, leaf})

	if s.IsSubtype(dup, leaf) {
		t.Error("a record extending the same base twice must not spuriously narrow an unrelated record")
	}
}

func TestIsSubtypeAgainstAnAnonymousRecord(t *testing.T) {
	t.Parallel()

	s := subtypeSchema()
	leaf := namedType(t, s, typeTip)

	// An anonymous record's Name is "", so extendsTransitively's guard against
	// an empty superName must stop it from being (mis)treated as a base every
	// record extends; leaf must then be judged structurally, and it lacks the
	// field anon requires.
	anon := record("", field("nope", Primitive(PrimitiveString)))

	if s.IsSubtype(leaf, anon) {
		t.Error("a record must not narrow an anonymous record it neither names nor structurally matches")
	}
}

func TestIsSubtypeRecordsStructurally(t *testing.T) {
	t.Parallel()

	str := Primitive(PrimitiveString)

	cases := []struct {
		name  string
		sub   Type
		super Type
		want  bool
	}{
		{
			name:  "supplying every required field at a narrower type narrows",
			sub:   record(typeTip, field("v", str), field("w", Primitive(PrimitiveInt))),
			super: record(typeRoot, field("v", optional(str)), field("w", Primitive(PrimitiveInt))),
			want:  true,
		},
		{
			name:  "an optional field the narrower record omits is not required",
			sub:   record(typeTip, field("v", str)),
			super: record(typeRoot, field("v", str), field("w", optional(str))),
			want:  true,
		},
		{
			name:  "a required field the narrower record omits does not narrow",
			sub:   record(typeTip, field("v", str)),
			super: record(typeRoot, field("v", str), field("w", Primitive(PrimitiveInt))),
		},
		{
			name:  "a field of an incompatible type does not narrow",
			sub:   record(typeTip, field("v", Primitive(PrimitiveInt))),
			super: record(typeRoot, field("v", str)),
		},
		{
			name:  "a field the wider record does not declare does not narrow",
			sub:   record(typeTip, field("z", str)),
			super: record(typeRoot, field("v", str)),
		},
	}

	runSubtypeCases(t, nil, cases)
}

func TestIsSubtypeTerminatesOnASelfReferentialGraph(t *testing.T) {
	t.Parallel()

	left := record(typeTip)
	left.Fields = []*Field{field("self", left)}

	right := record(typeRoot)
	right.Fields = []*Field{field("self", right)}

	if !(*Schema)(nil).IsSubtype(left, right) {
		t.Error("two identically shaped self-referential records do not compare as subtypes")
	}
}

func TestIsSubtypeWithoutANameTable(t *testing.T) {
	t.Parallel()

	leaf := extending(record(typeTip, field("v", Primitive(PrimitiveString))), typeRoot)
	base := abstractRecord(typeRoot, field("v", Primitive(PrimitiveString)))

	if !(*Schema)(nil).IsSubtype(leaf, base) {
		t.Error("a record does not narrow the base it names, even though extends names it directly")
	}

	orphan := extending(record(typeTip), typeTier)
	if (*Schema)(nil).IsSubtype(orphan, base) {
		t.Error("a record narrows a base it neither names nor structurally matches")
	}
}

// runSubtypeCases runs one table of subtype comparisons against s, which may be
// nil to check that the walk copes without a name table.
func runSubtypeCases(t *testing.T, s *Schema, cases []struct {
	name  string
	sub   Type
	super Type
	want  bool
},
) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := s.IsSubtype(tc.sub, tc.super); got != tc.want {
				t.Errorf("IsSubtype(%s, %s) = %v, want %v", typeLabel(tc.sub), typeLabel(tc.super), got, tc.want)
			}
		})
	}
}
