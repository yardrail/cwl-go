package salad

import (
	"reflect"
	"testing"
)

// vocabSchema exercises every path BuildContext walks: namespaces, an enum's
// symbols, a record's fields in sequence form, a record's fields in identifier
// map form, a nested named enum, and inVocab: false.
const vocabSchema = `
$namespaces:
  acid: http://example.com/acid#
$schemas:
  - http://example.com/onto.xml
$graph:
  - name: Colors
    type: enum
    symbols: ["acid:red"]
  - name: Hidden
    type: record
    inVocab: false
    fields: []
  - name: Thing
    type: record
    fields:
      - name: id
        type: string
        jsonldPredicate: "@id"
      - name: voc
        type: string
        jsonldPredicate:
          _type: "@vocab"
          refScope: 2
      - name: plain
        type: string
        jsonldPredicate: acid:plain
  - name: Raw
    type: record
    fields:
      kind:
        type:
          name: Kind_name
          symbols: ["acid:kindly"]
          type: enum
        jsonldPredicate:
          _id: "acid:kind"
          _type: "@vocab"
          subscope: inner
          noLinkCheck: true
`

func TestBuildContextReadsTheExplicitContext(t *testing.T) {
	t.Parallel()

	ctx := schemaContext(t, vocabSchema)

	wantNS := map[string]string{"acid": testAcidNS}
	if got := ctx.Namespaces(); !reflect.DeepEqual(got, wantNS) {
		t.Errorf("Namespaces() = %v, want %v", got, wantNS)
	}

	wantSchemas := []string{"http://example.com/onto.xml"}
	if got := ctx.Schemas(); !reflect.DeepEqual(got, wantSchemas) {
		t.Errorf("Schemas() = %v, want %v", got, wantSchemas)
	}
}

func TestBuildContextCollectsTheVocabulary(t *testing.T) {
	t.Parallel()

	vocab := schemaContext(t, vocabSchema).Vocab()

	for term, want := range map[string]string{
		"Colors":    "Colors",
		testRed:     testAcidRed,
		"kindly":    testAcidNS + "kindly",
		"Kind_name": "Kind_name",
		testPlain:   testAcidNS + "plain",
		"acid":      testAcidNS,
	} {
		if got := vocab[term]; got != want {
			t.Errorf("vocab[%q] = %q, want %q", term, got, want)
		}
	}

	if _, present := vocab["Hidden"]; present {
		t.Error("a type declaring inVocab: false must not enter the vocabulary")
	}
}

func TestBuildContextFindsIdentifierFields(t *testing.T) {
	t.Parallel()

	ctx := schemaContext(t, vocabSchema)

	term, ok := ctx.Term("id")
	if !ok || !term.IsIdentifier {
		t.Fatalf("term for id = %+v, want an identifier", term)
	}

	if got := ctx.identifierFields(); !reflect.DeepEqual(got, []string{"id"}) {
		t.Errorf("identifierFields() = %v, want [id]", got)
	}
}

func TestBuildContextReadsPredicateModifiers(t *testing.T) {
	t.Parallel()

	ctx := schemaContext(t, vocabSchema)

	term, ok := ctx.Term("kind")
	if !ok {
		t.Fatal("the term for a field declared in identifier map form is missing")
	}

	want := TermDef{
		ID:          testAcidNS + "kind",
		Type:        keywordVocab,
		Subscope:    "inner",
		NoLinkCheck: true,
	}
	if *term != want {
		t.Errorf("term = %+v, want %+v", *term, want)
	}

	scoped, ok := ctx.Term("voc")
	if !ok || !scoped.ScopedRef || scoped.RefScope != 2 {
		t.Errorf("term for voc = %+v, want refScope 2", scoped)
	}
}

func TestShortname(t *testing.T) {
	t.Parallel()

	var ctx *Context

	cases := []struct {
		in   string
		want string
	}{
		{in: testBaseOne + "/two", want: testTwo},
		{in: testBaseOne, want: testOne},
		{in: testBaseURI, want: "base"},
		{in: testPlain, want: testPlain},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			if got := ctx.Shortname(tc.in); got != tc.want {
				t.Errorf("Shortname(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNilContextIsUsable(t *testing.T) {
	t.Parallel()

	var ctx *Context

	if got := ctx.ExpandURL(testOne, testBaseURI); got != "http://example.com/one" {
		t.Errorf("ExpandURL on a nil context = %q", got)
	}

	if len(ctx.Vocab()) != 0 || len(ctx.Namespaces()) != 0 || len(ctx.Schemas()) != 0 {
		t.Error("a nil context must report empty tables")
	}

	if _, ok := ctx.Term("anything"); ok {
		t.Error("a nil context must define no terms")
	}
}

func TestBootstrapContextReadsTheMetaschema(t *testing.T) {
	t.Parallel()

	ctx := saladBootstrapContext()

	term, ok := ctx.Term(keyName)
	if !ok || !term.IsIdentifier {
		t.Error("the bootstrap context must treat name as the object identifier")
	}

	fields, ok := ctx.Term(keyFields)
	if !ok || fields.MapSubject != keyName || fields.MapPredicate != keyType {
		t.Errorf("the bootstrap term for fields = %+v, want the name/type identifier map", fields)
	}

	if !ctx.hasVocabTerm(kindRecord) || !ctx.hasVocabTerm(nameString) {
		t.Error("the bootstrap vocabulary must know the type declaration keywords and the primitives")
	}
}
