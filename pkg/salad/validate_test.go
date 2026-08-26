package salad

import (
	"errors"
	"strings"
	"testing"
)

// Names used by the hand-built schemas these tests validate against.
const (
	typeDoc   = "https://example.com/s#Doc"
	typeInner = "https://example.com/s#Inner"
	litTrue   = "true"
	litHello  = "hello"
	docNullV  = "v: null\n"
	docHello  = "v: hello\n"
)

// field builds a record field of the given type.
func field(name string, t Type) *Field {
	return &Field{Name: name, Type: t}
}

// optional builds the ["null", t] union that Schema Salad uses for an optional value.
func optional(t Type) *UnionType {
	return &UnionType{Options: []Type{Primitive(PrimitiveNull), t}}
}

// union builds a union of the given alternatives.
func union(opts ...Type) *UnionType {
	return &UnionType{Options: opts}
}

// record builds a concrete, non-root record.
func record(name string, fields ...*Field) *RecordType {
	return &RecordType{Name: name, Fields: fields}
}

// rootRecord builds a concrete record flagged documentRoot.
func rootRecord(name string, fields ...*Field) *RecordType {
	return &RecordType{Name: name, Fields: fields, DocumentRoot: true}
}

// abstractRecord builds an abstract record, which never validates on its own.
func abstractRecord(name string, fields ...*Field) *RecordType {
	return &RecordType{Name: name, Fields: fields, Abstract: true}
}

// extending builds a record that extends the named base types.
func extending(r *RecordType, bases ...string) *RecordType {
	out := *r
	out.Extends = bases

	return &out
}

// singleFieldSchema builds a one-record documentRoot schema whose only field,
// "v", has the type under test.
func singleFieldSchema(t Type) *Schema {
	return NewSchema([]Type{rootRecord(typeDoc, field("v", t))})
}

// mustSaladError recovers the *Error tree from an error the package returned.
func mustSaladError(t *testing.T, err error) *Error {
	t.Helper()

	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("error %v is not a *salad.Error", err)
	}

	return se
}

// assertMentions fails unless the rendered error tree contains every want.
func assertMentions(t *testing.T, err error, want ...string) {
	t.Helper()

	got := mustSaladError(t, err).Pretty()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("error does not mention %q:\n%s", w, got)
		}
	}
}

func TestValidateRejectsSchemaWithoutDocumentRoots(t *testing.T) {
	t.Parallel()

	s := NewSchema([]Type{record(typeDoc, field("name", Primitive(PrimitiveString)))})

	err := s.Validate(mustParse(t, testFile, "name: x\n"))
	if err == nil {
		t.Fatal("a schema with no documentRoot type must not validate a document")
	}

	assertMentions(t, err, "documentRoot")
}

func TestValidatePicksTheMatchingDocumentRoot(t *testing.T) {
	t.Parallel()

	s := NewSchema([]Type{
		rootRecord(typeDoc, field("kind", Primitive(PrimitiveString))),
		rootRecord(typeInner, field("count", Primitive(PrimitiveInt))),
	})

	err := s.Validate(mustParse(t, testFile, "count: 3\n"))
	if err != nil {
		t.Fatalf("the second documentRoot candidate should have matched: %v", err)
	}
}

func TestValidateExplainsEveryDocumentRootCandidate(t *testing.T) {
	t.Parallel()

	s := NewSchema([]Type{
		rootRecord(typeDoc, field("kind", Primitive(PrimitiveString))),
		rootRecord(typeInner, field("count", Primitive(PrimitiveInt))),
	})

	err := s.Validate(mustParse(t, testFile, "count: nope\n"), Strict(true))
	if err == nil {
		t.Fatal("a document matching no documentRoot type must not validate")
	}

	se := mustSaladError(t, err)
	if len(se.Children) != 2 {
		t.Fatalf("want one child per documentRoot candidate, got %d:\n%s", len(se.Children), se.Pretty())
	}

	assertMentions(t, err,
		"tried Doc, but", `the required field "kind" is missing`,
		"tried Inner, but", `the "count" field is not valid`,
		"the value is a string, but int was expected",
	)
}

func TestValidateRejectsNonMappingDocument(t *testing.T) {
	t.Parallel()

	s := singleFieldSchema(Primitive(PrimitiveString))

	err := s.Validate(mustParse(t, testFile, "just a string\n"))
	if err == nil {
		t.Fatal("a scalar document must not validate")
	}

	assertMentions(t, err, "a mapping or a sequence of mappings")
}

func TestValidateSequenceDocument(t *testing.T) {
	t.Parallel()

	s := singleFieldSchema(Primitive(PrimitiveString))

	err := s.Validate(mustParse(t, testFile, "- v: a\n- v: b\n"))
	if err != nil {
		t.Fatalf("a sequence of valid mappings should validate: %v", err)
	}

	err = s.Validate(mustParse(t, testFile, "- v: a\n- v: 3\n"))
	if err == nil {
		t.Fatal("a sequence with an invalid entry must not validate")
	}

	assertMentions(t, err, "item 1 is not valid")
}

func TestValidatePrimitives(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		kind  PrimitiveKind
		valid bool
	}{
		{name: "string", kind: PrimitiveString, value: litHello, valid: true},
		{name: "string given int", kind: PrimitiveString, value: "3", valid: false},
		{name: "boolean", kind: PrimitiveBoolean, value: litTrue, valid: true},
		{name: "boolean given string", kind: PrimitiveBoolean, value: `"` + litTrue + `"`, valid: false},
		{name: "int", kind: PrimitiveInt, value: "3", valid: true},
		{name: "int given bool", kind: PrimitiveInt, value: litTrue, valid: false},
		{name: "long", kind: PrimitiveLong, value: "3", valid: true},
		{name: "float given int", kind: PrimitiveFloat, value: "3", valid: true},
		{name: "float", kind: PrimitiveFloat, value: "3.5", valid: true},
		{name: "double", kind: PrimitiveDouble, value: "3.5", valid: true},
		{name: "double given string", kind: PrimitiveDouble, value: "x", valid: false},
		{name: nameNull, kind: PrimitiveNull, value: nameNull, valid: true},
		{name: "null given value", kind: PrimitiveNull, value: "1", valid: false},
		{name: "Any given value", kind: PrimitiveAny, value: "1", valid: true},
		{name: "Any given null", kind: PrimitiveAny, value: nameNull, valid: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := singleFieldSchema(Primitive(tc.kind))

			err := s.Validate(mustParse(t, testFile, "v: "+tc.value+"\n"))
			if (err == nil) != tc.valid {
				t.Fatalf("validity = %v, want %v (%v)", err == nil, tc.valid, err)
			}
		})
	}
}

func TestValidateNumericFit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		kind  PrimitiveKind
		valid bool
	}{
		{name: "int max", kind: PrimitiveInt, value: "2147483647", valid: true},
		{name: "int min", kind: PrimitiveInt, value: "-2147483648", valid: true},
		{name: "int overflow", kind: PrimitiveInt, value: "2147483648", valid: false},
		{name: "int underflow", kind: PrimitiveInt, value: "-2147483649", valid: false},
		{name: "long max", kind: PrimitiveLong, value: "9223372036854775807", valid: true},
		{name: "long min", kind: PrimitiveLong, value: "-9223372036854775808", valid: true},
		{name: "long overflow", kind: PrimitiveLong, value: "9223372036854775808", valid: false},
		{name: "long overflow far", kind: PrimitiveLong, value: "1.0e+30", valid: false},
		{name: "int given integral float", kind: PrimitiveInt, value: "3.0", valid: false},
		{name: "long given integral float", kind: PrimitiveLong, value: "3.0", valid: false},
		{name: "float overflow", kind: PrimitiveFloat, value: "1.0e+300", valid: false},
		{name: "double holds 1e300", kind: PrimitiveDouble, value: "1.0e+300", valid: true},
		{name: "float holds infinity", kind: PrimitiveFloat, value: ".inf", valid: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := singleFieldSchema(Primitive(tc.kind))

			err := s.Validate(mustParse(t, testFile, "v: "+tc.value+"\n"))
			if (err == nil) != tc.valid {
				t.Fatalf("validity = %v, want %v (%v)", err == nil, tc.valid, err)
			}
		})
	}
}

func TestValidateNumericFitMessages(t *testing.T) {
	t.Parallel()

	overflow := singleFieldSchema(Primitive(PrimitiveInt)).Validate(mustParse(t, testFile, "v: 2147483648\n"))
	assertMentions(t, overflow, "does not fit in int")

	notWhole := singleFieldSchema(Primitive(PrimitiveInt)).Validate(mustParse(t, testFile, "v: 3.5\n"))
	assertMentions(t, notWhole, "whole number written without a decimal point")

	tooBigForLong := singleFieldSchema(Primitive(PrimitiveLong)).Validate(mustParse(t, testFile, "v: 1.0e+30\n"))
	assertMentions(t, tooBigForLong, "does not fit in long")
}

func TestValidateMissingAndNullFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		typ   Type
		name  string
		doc   string
		valid bool
	}{
		{name: "missing required", doc: "other: 1\n", typ: Primitive(PrimitiveString), valid: false},
		{name: "missing optional", doc: "other: 1\n", typ: optional(Primitive(PrimitiveString)), valid: true},
		{name: "explicit null required", doc: docNullV, typ: Primitive(PrimitiveString), valid: false},
		{name: "explicit null optional", doc: docNullV, typ: optional(Primitive(PrimitiveString)), valid: true},
		{name: "null type accepts null", doc: docNullV, typ: Primitive(PrimitiveNull), valid: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := singleFieldSchema(tc.typ)

			err := s.Validate(mustParse(t, testFile, tc.doc))
			if (err == nil) != tc.valid {
				t.Fatalf("validity = %v, want %v (%v)", err == nil, tc.valid, err)
			}
		})
	}
}

func TestValidateMissingFieldMessage(t *testing.T) {
	t.Parallel()

	s := singleFieldSchema(Primitive(PrimitiveString))

	err := s.Validate(mustParse(t, testFile, "other: 1\n"))
	assertMentions(t, err, `the required field "v" is missing`, "it must be string")
}

func TestValidateEnum(t *testing.T) {
	t.Parallel()

	enum := &EnumType{
		Name:    "https://example.com/s#Kind",
		Symbols: []string{"https://example.com/s#Kind/draft", "https://example.com/s#Kind/final"},
	}
	s := singleFieldSchema(enum)

	err := s.Validate(mustParse(t, testFile, "v: final\n"))
	if err != nil {
		t.Fatalf("a short-name symbol should match: %v", err)
	}

	err = s.Validate(mustParse(t, testFile, "v: https://example.com/s#Kind/draft\n"))
	if err != nil {
		t.Fatalf("a fully-qualified symbol should match: %v", err)
	}

	err = s.Validate(mustParse(t, testFile, "v: pending\n"))
	if err == nil {
		t.Fatal("a value outside the symbol set must not validate")
	}

	assertMentions(t, err, `the value "pending" is not a symbol of Kind`, "draft, final")
}

// expressionEnum is an enum shaped like the one Schema Salad validation rule 9
// singles out: named Expression, with a single placeholder symbol.
func expressionEnum() *EnumType {
	return &EnumType{
		Name:    "https://example.com/s#Expression",
		Symbols: []string{"https://example.com/s#ExpressionPlaceholder"},
	}
}

func TestValidateExpressionEnum(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		doc   string
		valid bool
	}{
		{name: "parameter reference", doc: "v: $(inputs.foo)\n", valid: true},
		{name: "expression body", doc: "v: ${return 1;}\n", valid: true},
		{name: "interpolated reference", doc: "v: prefix-$(runtime.outdir)/out\n", valid: true},
		{name: "declared placeholder symbol", doc: "v: ExpressionPlaceholder\n", valid: true},
		{name: "plain string", doc: "v: not an expression\n", valid: false},
		{name: "dollar without a bracket", doc: "v: $PATH\n", valid: false},
		{name: "number", doc: "v: 3\n", valid: false},
		{name: "mapping", doc: "v: {a: 1}\n", valid: false},
		{name: "sequence", doc: "v: [a]\n", valid: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := singleFieldSchema(expressionEnum())

			err := s.Validate(mustParse(t, testFile, tc.doc))
			if (err == nil) != tc.valid {
				t.Fatalf("validity = %v, want %v (%v)", err == nil, tc.valid, err)
			}
		})
	}
}

func TestValidateExpressionRuleIsScopedToTheTypeName(t *testing.T) {
	t.Parallel()

	other := &EnumType{
		Name:    "https://example.com/s#Kind",
		Symbols: []string{"https://example.com/s#Kind/draft"},
	}
	s := singleFieldSchema(other)

	err := s.Validate(mustParse(t, testFile, "v: $(inputs.foo)\n"))
	if err == nil {
		t.Fatal("rule 9 must apply only to an enum named Expression")
	}

	assertMentions(t, err, `the value "$(inputs.foo)" is not a symbol of Kind`)
}

func TestValidateExpressionMessages(t *testing.T) {
	t.Parallel()

	s := singleFieldSchema(expressionEnum())

	plain := s.Validate(mustParse(t, testFile, "v: not an expression\n"))
	assertMentions(t, plain,
		`the value "not an expression" contains no parameter reference or expression`,
		"$(...) or ${...}", "which is what Expression accepts")

	number := s.Validate(mustParse(t, testFile, "v: 3\n"))
	assertMentions(t, number, "the value is an int, but Expression was expected")
}

func TestValidateExpressionInsideAUnion(t *testing.T) {
	t.Parallel()

	s := singleFieldSchema(union(Primitive(PrimitiveInt), expressionEnum()))

	err := s.Validate(mustParse(t, testFile, "v: $(inputs.n)\n"))
	if err != nil {
		t.Fatalf("an expression should satisfy the Expression member of a union: %v", err)
	}

	err = s.Validate(mustParse(t, testFile, "v: plain\n"))
	if err == nil {
		t.Fatal("a plain string satisfies neither member")
	}

	assertMentions(t, err, "tried int, but", "tried Expression, but", "contains no parameter reference")
}

func TestValidateArrayAndMap(t *testing.T) {
	t.Parallel()

	strArray := &ArrayType{Items: Primitive(PrimitiveString)}
	intMap := &MapType{Values: Primitive(PrimitiveInt)}

	tests := []struct {
		typ   Type
		name  string
		doc   string
		valid bool
	}{
		{name: "sequence of strings", doc: "v: [a, b]\n", typ: strArray, valid: true},
		{name: "array of wrong items", doc: "v: [a, 2]\n", typ: strArray, valid: false},
		{name: "array given scalar", doc: "v: a\n", typ: strArray, valid: false},
		{name: "empty array", doc: "v: []\n", typ: strArray, valid: true},
		{name: "mapping of ints", doc: "v: {a: 1}\n", typ: intMap, valid: true},
		{name: "map of wrong values", doc: "v: {a: x}\n", typ: intMap, valid: false},
		{name: "map given sequence", doc: "v: [1]\n", typ: intMap, valid: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := singleFieldSchema(tc.typ)

			err := s.Validate(mustParse(t, testFile, tc.doc))
			if (err == nil) != tc.valid {
				t.Fatalf("validity = %v, want %v (%v)", err == nil, tc.valid, err)
			}
		})
	}
}

func TestValidateArrayAndMapNameTheOffendingEntry(t *testing.T) {
	t.Parallel()

	arr := singleFieldSchema(&ArrayType{Items: Primitive(PrimitiveString)})
	assertMentions(t, arr.Validate(mustParse(t, testFile, "v: [a, 2]\n")), "item 1 is not valid")

	m := singleFieldSchema(&MapType{Values: Primitive(PrimitiveInt)})
	assertMentions(t, m.Validate(mustParse(t, testFile, "v: {a: x}\n")), `the "a" entry is not valid`)
}

func TestValidateAgainstUnknownType(t *testing.T) {
	t.Parallel()

	s := NewSchema([]Type{rootRecord(typeDoc, field("v", Primitive(PrimitiveString)))})

	err := s.ValidateAgainst("Nope", mustParse(t, testFile, "v: a\n"))
	if err == nil {
		t.Fatal("validating against an undefined type must fail")
	}

	assertMentions(t, err, `the schema defines no type named "Nope"`)
}

func TestValidateAgainstResolvesShortNames(t *testing.T) {
	t.Parallel()

	s := NewSchema([]Type{rootRecord(typeDoc, field("v", Primitive(PrimitiveString)))})

	err := s.ValidateAgainst(typeDoc, mustParse(t, testFile, "v: a\n"))
	if err != nil {
		t.Fatalf("an exact name should resolve: %v", err)
	}

	err = s.ValidateAgainst("Doc", mustParse(t, testFile, "v: a\n"))
	if err != nil {
		t.Fatalf("a short name should resolve: %v", err)
	}
}

func TestValidateAgainstRejectsAmbiguousShortNames(t *testing.T) {
	t.Parallel()

	s := NewSchema([]Type{
		record("https://a.example/s#Doc", field("v", Primitive(PrimitiveString))),
		record("https://b.example/s#Doc", field("v", Primitive(PrimitiveInt))),
	})

	err := s.ValidateAgainst("Doc", mustParse(t, testFile, "v: a\n"))
	if err == nil {
		t.Fatal("an ambiguous short name must not resolve")
	}

	assertMentions(t, err, "is ambiguous")
}

func TestValidateAgainstBypassesTheDocumentRootGate(t *testing.T) {
	t.Parallel()

	s := NewSchema([]Type{record(typeInner, field("v", Primitive(PrimitiveString)))})

	err := s.ValidateAgainst(typeInner, mustParse(t, testFile, "v: a\n"))
	if err != nil {
		t.Fatalf("ValidateAgainst should not require a documentRoot type: %v", err)
	}
}
