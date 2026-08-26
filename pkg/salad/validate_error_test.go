package salad

import (
	"strings"
	"testing"
)

// Names used by the abstract-record and link schemas below.
const (
	typeProcess = "https://example.com/s#Process"
	typeScript  = "https://example.com/s#Script"
	typeMiddle  = "https://example.com/s#Middle"
	typeDeep    = "https://example.com/s#Deep"
	keyRun      = "run"
	keySource   = "source"
	docExtra    = "v: a\nextra: 1\n"
	docForeign  = "v: a\nex:thing: 1\n"
	docGoneLink = "id: main\nsource: gone\n"
	predSource  = "https://example.com/s#Doc/source"
)

// unionSchema is a documentRoot record whose only field accepts three
// alternatives, which is enough to exercise the per-member explanation.
func unionSchema() *Schema {
	inner := record(typeInner, field("count", Primitive(PrimitiveInt)))
	doc := rootRecord(typeDoc, field("v", union(Primitive(PrimitiveInt), Primitive(PrimitiveBoolean), inner)))

	return NewSchema([]Type{doc, inner})
}

func TestValidateUnionExplainsEveryMember(t *testing.T) {
	t.Parallel()

	err := unionSchema().Validate(mustParse(t, testFile, docHello))
	if err == nil {
		t.Fatal("a value matching no union member must not validate")
	}

	se := mustSaladError(t, err)

	fieldErr := se.Children[0]
	if !strings.Contains(fieldErr.Msg, `the "v" field`) {
		t.Fatalf("want the field context first, got %q:\n%s", fieldErr.Msg, se.Pretty())
	}

	unionErr := fieldErr.Children[0]
	if len(unionErr.Children) != 3 {
		t.Fatalf("want one child per union member, got %d:\n%s", len(unionErr.Children), se.Pretty())
	}

	for i, want := range []string{"tried int, but", "tried boolean, but", "tried Inner, but"} {
		if unionErr.Children[i].Msg != want {
			t.Errorf("child %d = %q, want %q", i, unionErr.Children[i].Msg, want)
		}
	}
}

func TestValidateUnionPrettyRendering(t *testing.T) {
	t.Parallel()

	err := unionSchema().Validate(mustParse(t, testFile, docHello))

	want := strings.Join([]string{
		`a.yml:1:4: the "v" field is not valid, because`,
		`  a.yml:1:4: the value is a string, but one of int, boolean, Inner was expected`,
		`    tried int, but`,
		`      a.yml:1:4: the value is a string, but int was expected`,
		`    tried boolean, but`,
		`      a.yml:1:4: the value is a string, but boolean was expected`,
		`    tried Inner, but`,
		`      a.yml:1:4: the value is a string, but Inner was expected`,
	}, "\n")

	if got := mustSaladError(t, err).Pretty(); got != want {
		t.Errorf("Pretty() =\n%s\n\nwant\n%s", got, want)
	}
}

func TestValidateOptionalUnionSkipsTheNullMember(t *testing.T) {
	t.Parallel()

	s := singleFieldSchema(optional(Primitive(PrimitiveInt)))

	err := s.Validate(mustParse(t, testFile, docHello))
	if err == nil {
		t.Fatal("a string is not an optional int")
	}

	got := mustSaladError(t, err).Pretty()
	if strings.Contains(got, "tried") {
		t.Errorf("an optional field should collapse to a direct diagnostic, got:\n%s", got)
	}

	assertMentions(t, err, "the value is a string, but int was expected")
}

func TestValidateNullAgainstNonOptionalUnion(t *testing.T) {
	t.Parallel()

	s := singleFieldSchema(union(Primitive(PrimitiveInt), Primitive(PrimitiveString)))

	err := s.Validate(mustParse(t, testFile, docNullV))
	if err == nil {
		t.Fatal("null does not belong to a union without null")
	}

	assertMentions(t, err, "the value is null, but one of int, string was expected")
}

// abstractSchema models the shape CWL's Process depends on: an abstract base
// with concrete subtypes, one of them reached through a second abstract level.
func abstractSchema() *Schema {
	process := abstractRecord(typeProcess, field("id", optional(Primitive(PrimitiveString))))
	tool := extending(record(typeDoc, field("baseCommand", Primitive(PrimitiveString))), typeProcess)
	script := extending(record(typeScript, field("expression", Primitive(PrimitiveString))), typeProcess)
	middle := extending(abstractRecord(typeMiddle), typeProcess)
	deep := extending(record(typeDeep, field("deeply", Primitive(PrimitiveString))), typeMiddle)
	root := rootRecord(typeInner, field(keyRun, process))

	return NewSchema([]Type{root, process, tool, script, middle, deep})
}

func TestValidateAbstractRecordExpandsToConcreteSubtypes(t *testing.T) {
	t.Parallel()

	s := abstractSchema()

	tests := []struct {
		name string
		doc  string
	}{
		{name: "direct subtype", doc: "run:\n  baseCommand: echo\n"},
		{name: "sibling subtype", doc: "run:\n  expression: 1+1\n"},
		{name: "subtype of a nested abstract", doc: "run:\n  deeply: yes\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := s.Validate(mustParse(t, testFile, tc.doc), Strict(true))
			if err != nil {
				t.Fatalf("a concrete subtype should satisfy the abstract field: %v", err)
			}
		})
	}
}

func TestValidateAbstractRecordExplainsEverySubtype(t *testing.T) {
	t.Parallel()

	err := abstractSchema().Validate(mustParse(t, testFile, "run:\n  nothing: here\n"), Strict(true))
	if err == nil {
		t.Fatal("a value matching no concrete subtype must not validate")
	}

	assertMentions(t, err,
		"no concrete subtype of Process matches",
		"tried Doc, but", "tried Script, but", "tried Deep, but",
	)
}

// TestChildrenOfMatchesASubtypeDeclaredByShortName covers childrenOf's merge
// branch: the base's Name is a fully-qualified IRI, but the subtype declares
// extends using the base's short name, so indexExtends only ever registers it
// under that short name and childrenOf has to look it up there too.
func TestChildrenOfMatchesASubtypeDeclaredByShortName(t *testing.T) {
	t.Parallel()

	process := abstractRecord(typeProcess, field("id", optional(Primitive(PrimitiveString))))

	// extending sets Extends to exactly what is passed, so "Process" here is the
	// base's short name rather than its full IRI.
	tool := extending(record(typeDoc, field("baseCommand", Primitive(PrimitiveString))), "Process")

	s := NewSchema([]Type{rootRecord(typeInner, field(keyRun, process)), process, tool})

	err := s.Validate(mustParse(t, testFile, "run:\n  baseCommand: echo\n"), Strict(true))
	if err != nil {
		t.Fatalf("a subtype declared by the base's short name should still satisfy it: %v", err)
	}
}

// TestEnsureSubtypeIndexSkipsANonRecordType covers ensureSubtypeIndex's
// type-assertion guard: a schema whose Names() includes a top-level enum
// alongside abstract records must not panic building the subtype index.
func TestEnsureSubtypeIndexSkipsANonRecordType(t *testing.T) {
	t.Parallel()

	process := abstractRecord(typeProcess, field("id", optional(Primitive(PrimitiveString))))
	tool := extending(record(typeDoc, field("baseCommand", Primitive(PrimitiveString))), typeProcess)
	enum := &EnumType{Name: typeColour, Symbols: []string{typeColour + "/red"}}

	s := NewSchema([]Type{rootRecord(typeInner, field(keyRun, process)), process, tool, enum})

	err := s.Validate(mustParse(t, testFile, "run:\n  baseCommand: echo\n"), Strict(true))
	if err != nil {
		t.Fatalf("a non-record type alongside abstract records must not break subtype indexing: %v", err)
	}
}

func TestValidateAbstractRecordWithoutSubtypes(t *testing.T) {
	t.Parallel()

	process := abstractRecord(typeProcess, field("id", optional(Primitive(PrimitiveString))))
	s := NewSchema([]Type{rootRecord(typeInner, field(keyRun, process)), process})

	err := s.Validate(mustParse(t, testFile, "run: {}\n"))
	if err == nil {
		t.Fatal("an abstract type with no concrete subtype cannot be satisfied")
	}

	assertMentions(t, err, "Process is abstract and the schema declares no concrete subtype")
}

func TestValidateUnknownFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		doc   string
		opts  []ValidateOption
		valid bool
	}{
		{name: "tolerated by default", doc: docExtra, valid: true},
		{name: "rejected under Strict", doc: docExtra, opts: []ValidateOption{Strict(true)}, valid: false},
		{name: "foreign tolerated by default", doc: docForeign, valid: true},
		{
			name:  "foreign survives Strict alone",
			doc:   docForeign,
			opts:  []ValidateOption{Strict(true)},
			valid: true,
		},
		{
			name:  "foreign rejected under StrictForeign",
			doc:   docForeign,
			opts:  []ValidateOption{StrictForeign(true)},
			valid: false,
		},
		{name: "directives ignored", doc: "v: a\n$namespaces: {}\n", opts: []ValidateOption{Strict(true)}, valid: true},
		{name: "keywords ignored", doc: "v: a\n\"@id\": x\n", opts: []ValidateOption{Strict(true)}, valid: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := singleFieldSchema(Primitive(PrimitiveString))

			err := s.Validate(mustParse(t, testFile, tc.doc), tc.opts...)
			if (err == nil) != tc.valid {
				t.Fatalf("validity = %v, want %v (%v)", err == nil, tc.valid, err)
			}
		})
	}
}

func TestValidateUnknownFieldMessages(t *testing.T) {
	t.Parallel()

	s := singleFieldSchema(Primitive(PrimitiveString))

	plain := s.Validate(mustParse(t, testFile, docExtra), Strict(true))
	assertMentions(t, plain, `the field "extra" is not declared by Doc`, "expected one of: v")

	foreign := s.Validate(mustParse(t, testFile, docForeign), StrictForeign(true))
	assertMentions(t, foreign, `the field "ex:thing" comes from a foreign vocabulary`)
}

func TestValidateSourceLineOnNestedFailure(t *testing.T) {
	t.Parallel()

	inner := record(typeInner, field("value", Primitive(PrimitiveInt)))
	s := NewSchema([]Type{rootRecord(typeDoc, field("items", &ArrayType{Items: inner})), inner})

	src := "items:\n" + // line 1
		"  - value: 1\n" + // line 2
		"  - value: oops\n" // line 3

	err := s.Validate(mustParse(t, testFile, src))
	if err == nil {
		t.Fatal("a bad item must not validate")
	}

	leaves := mustSaladError(t, err).Leaves()
	if len(leaves) != 1 {
		t.Fatalf("want one leaf, got %d:\n%s", len(leaves), mustSaladError(t, err).Pretty())
	}

	assertLoc(t, leaves[0].Loc, testFile, 3, 12)
}

func TestValidateTerminatesOnASelfReferentialUnion(t *testing.T) {
	t.Parallel()

	cyclic := &UnionType{}
	cyclic.Options = []Type{cyclic}
	s := singleFieldSchema(cyclic)

	err := s.Validate(mustParse(t, testFile, "v: 1\n"))
	if err == nil {
		t.Fatal("a union that only contains itself cannot be satisfied")
	}

	assertMentions(t, err, "self-referential")
}

func TestValidateWalksASelfReferentialRecord(t *testing.T) {
	t.Parallel()

	node := record(typeInner)
	node.Fields = []*Field{
		field("child", optional(node)),
		field("label", Primitive(PrimitiveString)),
	}

	s := NewSchema([]Type{rootRecord(typeDoc, field("root", node)), node})
	src := "root:\n  label: a\n  child:\n    label: b\n    child: null\n"

	err := s.Validate(mustParse(t, testFile, src), Strict(true))
	if err != nil {
		t.Fatalf("a recursive record should validate a finite document: %v", err)
	}
}

func TestValidateTerminatesOnACyclicAbstractHierarchy(t *testing.T) {
	t.Parallel()

	base := abstractRecord(typeProcess)
	base.Extends = []string{typeMiddle}
	middle := extending(abstractRecord(typeMiddle), typeProcess)
	s := NewSchema([]Type{rootRecord(typeInner, field(keyRun, base)), base, middle})

	err := s.Validate(mustParse(t, testFile, "run: {}\n"))
	if err == nil {
		t.Fatal("a cycle of abstract types yields no concrete subtype")
	}

	assertMentions(t, err, "no concrete subtype")
}

// linkSchema declares an identifier field and a link field pointing at it.
func linkSchema(pred *TermDef) *Schema {
	id := field("id", optional(Primitive(PrimitiveString)))
	id.JSONLDPred = &TermDef{ID: jsonldID, Type: jsonldID}

	src := field(keySource, optional(Primitive(PrimitiveString)))
	src.JSONLDPred = pred

	return NewSchema([]Type{rootRecord(typeDoc, id, src)})
}

// sourcePred is the jsonldPredicate of an ordinary link field.
func sourcePred() *TermDef {
	return &TermDef{ID: predSource, Type: jsonldID}
}

func TestValidateLinks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pred  *TermDef
		name  string
		doc   string
		opts  []ValidateOption
		valid bool
	}{
		{name: "resolved link", pred: sourcePred(), doc: "id: main\nsource: main\n", valid: true},
		{name: "unresolved link is advisory", pred: sourcePred(), doc: docGoneLink, valid: true},
		{
			name:  "unresolved link is fatal under Strict",
			pred:  sourcePred(),
			doc:   docGoneLink,
			opts:  []ValidateOption{Strict(true)},
			valid: false,
		},
		{
			name:  "absolute references are left to the loader",
			pred:  sourcePred(),
			doc:   "id: main\nsource: file:///elsewhere.yml\n",
			opts:  []ValidateOption{Strict(true)},
			valid: true,
		},
		{
			name:  "an identity field is never checked",
			pred:  &TermDef{ID: predSource, Type: jsonldID, Identity: true},
			doc:   docGoneLink,
			opts:  []ValidateOption{Strict(true)},
			valid: true,
		},
		{
			name:  "a noLinkCheck field is never checked",
			pred:  &TermDef{ID: predSource, Type: jsonldID, NoLinkCheck: true},
			doc:   docGoneLink,
			opts:  []ValidateOption{Strict(true)},
			valid: true,
		},
		{
			name:  "a field that does not resolve to an identifier is never checked",
			pred:  &TermDef{ID: predSource, Type: "@vocab"},
			doc:   docGoneLink,
			opts:  []ValidateOption{Strict(true)},
			valid: true,
		},
		{
			name:  "a field that is not a link is never checked",
			pred:  nil,
			doc:   docGoneLink,
			opts:  []ValidateOption{Strict(true)},
			valid: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := linkSchema(tc.pred).Validate(mustParse(t, testFile, tc.doc), tc.opts...)
			if (err == nil) != tc.valid {
				t.Fatalf("validity = %v, want %v (%v)", err == nil, tc.valid, err)
			}
		})
	}
}

func TestValidateLinksResolveForwardsAndInLists(t *testing.T) {
	t.Parallel()

	id := field("id", optional(Primitive(PrimitiveString)))
	id.JSONLDPred = &TermDef{ID: jsonldID, Type: jsonldID}

	src := field(keySource, optional(&ArrayType{Items: Primitive(PrimitiveString)}))
	src.JSONLDPred = sourcePred()

	step := record(typeInner, id, src)
	s := NewSchema([]Type{rootRecord(typeDoc, field("steps", &ArrayType{Items: step})), step})

	doc := "steps:\n" +
		"  - id: first\n" +
		"    source: [second]\n" +
		"  - id: second\n"

	err := s.Validate(mustParse(t, testFile, doc), Strict(true))
	if err != nil {
		t.Fatalf("a forward reference in a list should resolve: %v", err)
	}

	bad := "steps:\n  - id: first\n    source: [second, missing]\n"

	err = s.Validate(mustParse(t, testFile, bad), Strict(true))
	if err == nil {
		t.Fatal("an unresolved link in a list must be reported under Strict")
	}

	assertMentions(t, err, `the link "missing" refers to no identifier declared in this document`)
}
