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

// TestValidateDiagSuppressesAWarningWhileProbingAUnion reaches diag's
// v.quiet branch: a union with more than one alternative forces the second
// alternative to be probed (quiet), and a matching record with an unknown
// field would only ever produce a warning (not strict), so the warning is
// dropped silently rather than built and then discarded.
func TestValidateDiagSuppressesAWarningWhileProbingAUnion(t *testing.T) {
	t.Parallel()

	withExtra := record(typeInner, field("count", Primitive(PrimitiveInt)))
	s := singleFieldSchema(union(Primitive(PrimitiveBoolean), withExtra))

	err := s.Validate(mustParse(t, testFile, "v:\n  count: 1\n  extra: 1\n"))
	if err != nil {
		t.Fatalf("the record alternative should match, tolerating the unknown field: %v", err)
	}
}

// TestValidateCheckMapQuietBranch reaches checkMap's v.quiet short-circuit: a
// map type with an invalid entry, probed as one alternative of a union with
// more than one member.
func TestValidateCheckMapQuietBranch(t *testing.T) {
	t.Parallel()

	s := singleFieldSchema(union(Primitive(PrimitiveBoolean), &MapType{Values: Primitive(PrimitiveInt)}))

	err := s.Validate(mustParse(t, testFile, "v: {a: x}\n"))
	if err == nil {
		t.Fatal("a map with an invalid entry satisfies neither union member")
	}
}

// TestMustRecordOnAnUndefinedName covers mustRecord's own Type() lookup
// failure directly: every caller in this package only ever passes a name
// straight out of Schema.Names(), which always resolves, so this branch is
// only reachable by calling mustRecord with a name the schema does not
// define at all.
func TestMustRecordOnAnUndefinedName(t *testing.T) {
	t.Parallel()

	s := NewSchema([]Type{rootRecord(typeDoc, field("v", Primitive(PrimitiveString)))})

	if _, ok := mustRecord(s, "Nope"); ok {
		t.Error("mustRecord must report false for a name the schema does not define")
	}
}

// TestMustRecordSkipsANonRecordType reaches identifierKeys' type-assertion
// guard: a schema whose Names() includes a top-level enum alongside records
// must not panic walking it for identifier fields.
func TestMustRecordSkipsANonRecordType(t *testing.T) {
	t.Parallel()

	id := field("id", optional(Primitive(PrimitiveString)))
	id.JSONLDPred = &TermDef{ID: jsonldID, Type: jsonldID}

	enum := &EnumType{Name: typeInner, Symbols: []string{typeInner + "/a"}}
	s := NewSchema([]Type{rootRecord(typeDoc, id), enum})

	err := s.Validate(mustParse(t, testFile, "id: main\n"))
	if err != nil {
		t.Fatalf("a schema with a non-record type alongside records must still validate: %v", err)
	}
}

// TestCheckLinkIgnoresAMappingValue reaches checkLink's default arm: a link
// field whose value is a mapping (neither a scalar nor a sequence) is left
// alone rather than checked as a link.
func TestCheckLinkIgnoresAMappingValue(t *testing.T) {
	t.Parallel()

	id := field("id", optional(Primitive(PrimitiveString)))
	id.JSONLDPred = &TermDef{ID: jsonldID, Type: jsonldID}

	src := field(keySource, optional(record(typeInner, field("nested", Primitive(PrimitiveString)))))
	src.JSONLDPred = sourcePred()

	s := NewSchema([]Type{rootRecord(typeDoc, id, src)})

	err := s.Validate(mustParse(t, testFile, "id: main\nsource:\n  nested: x\n"), Strict(true))
	if err != nil {
		t.Fatalf("a mapping-valued link field must be traversed, not checked as a link: %v", err)
	}
}

// TestCheckLinkTargetIgnoresAnEmptyString reaches checkLinkTarget's early
// return: an empty-string link value is never checked.
func TestCheckLinkTargetIgnoresAnEmptyString(t *testing.T) {
	t.Parallel()

	err := linkSchema(sourcePred()).Validate(mustParse(t, testFile, "id: main\nsource: \"\"\n"), Strict(true))
	if err != nil {
		t.Errorf("an empty-string link value must not be checked, got %v", err)
	}
}

// TestCheckAlternativesRejectsAnEmptyList white-box calls checkAlternatives
// directly with no candidates at all, which only ever happens defensively:
// every real caller supplies at least one alternative.
func TestCheckAlternativesRejectsAnEmptyList(t *testing.T) {
	t.Parallel()

	v := newValidator(NewSchema(nil), nil)

	err := v.checkAlternatives(nil, NewStringNode(SourceLine{}, "x"), "header")
	if err == nil {
		t.Fatal("checkAlternatives with no candidates must fail")
	}
}

// TestCheckAlternativesQuietAfterExhaustingANestedUnion reaches the "if
// v.quiet { return errNoMatch }" branch of checkAlternatives: a union nested
// inside another union, probed while the outer union is itself being probed,
// so the inner union's own checkAlternatives call runs with v.quiet already
// true when every one of its candidates has failed.
func TestCheckAlternativesQuietAfterExhaustingANestedUnion(t *testing.T) {
	t.Parallel()

	inner := union(Primitive(PrimitiveString), Primitive(PrimitiveBoolean))
	s := singleFieldSchema(union(Primitive(PrimitiveInt), inner))

	err := s.Validate(mustParse(t, testFile, "v: {}\n"))
	if err == nil {
		t.Fatal("a mapping satisfies none of int, string or boolean")
	}
}

// TestChildrenOfOfABareShortName reaches childrenOf's own short-name
// early-return: a record whose Name already IS its own short form (no "#" or
// "/" at all) so shortName(r.Name) == r.Name and the merge with
// v.subtypes[short] would be redundant.
func TestChildrenOfOfABareShortName(t *testing.T) {
	t.Parallel()

	process := abstractRecord("Process", field("id", optional(Primitive(PrimitiveString))))
	tool := extending(record("Tool", field("baseCommand", Primitive(PrimitiveString))), "Process")

	s := NewSchema([]Type{rootRecord(typeInner, field(keyRun, process)), process, tool})

	err := s.Validate(mustParse(t, testFile, "run:\n  baseCommand: echo\n"), Strict(true))
	if err != nil {
		t.Fatalf("a subtype of a bare-short-named abstract base should still satisfy it: %v", err)
	}
}

// TestTypeLabelOfAnAnonymousRecordOrEnum reaches typeLabelAt's defensive
// default arm: an anonymous (unnamed) record or enum is neither an
// ArrayType, MapType nor UnionType, so it falls all the way through.
func TestTypeLabelOfAnAnonymousRecordOrEnum(t *testing.T) {
	t.Parallel()

	if got := typeLabel(&RecordType{}); got != nameUnknown {
		t.Errorf("typeLabel(anonymous record) = %q, want %q", got, nameUnknown)
	}

	if got := typeLabel(&EnumType{}); got != nameUnknown {
		t.Errorf("typeLabel(anonymous enum) = %q, want %q", got, nameUnknown)
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
