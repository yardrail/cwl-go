package salad

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
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

// buildFixtureContext loads a schema document assembled from one or more
// files exactly as LoadSchema does, and returns BuildContext's raw error
// instead of failing the test — for fixtures expected to be rejected while
// the vocabulary is built. files must include testSchemaFile as the entry
// point. Unlike schemaContext (which calls BuildContext directly on an
// unresolved document), this goes through the full Loader/resolver pipeline
// so that type names are absolutized against each file's own $base, the same
// way LoadSchema's real callers see them.
func buildFixtureContext(t *testing.T, files map[string]string) (*Context, error) {
	t.Helper()

	fsys := make(fstest.MapFS, len(files))
	for name, src := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(src)}
	}

	loader := NewLoader(
		WithFetcher(NewFSFetcher(fsys, testSchemaMount)),
		WithContext(saladBootstrapContext()),
		WithSkipLinkCheck(true),
	)

	doc, err := loader.Load(testSchemaMount + testSchemaFile)
	if err != nil {
		t.Fatalf("loading the schema fixture: %v", err)
	}

	return BuildContext(doc.Root, doc.Metadata)
}

// assertCollisionMentions checks a buildFixtureContext result against what a
// case expects: no error when want is nil, and an error mentioning every
// substring in want otherwise.
func assertCollisionMentions(t *testing.T, err error, want []string) {
	t.Helper()

	if want == nil {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		return
	}

	if err == nil {
		t.Fatalf("expected an error mentioning %q, got none", want)
	}

	got := mustSaladError(t, err).Pretty()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("error does not mention %q:\n%s", w, got)
		}
	}
}

func TestBuildContextDetectsPredicateCollisions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		files map[string]string
		want  []string // substrings the error must mention; nil means no error
	}{
		{
			name: "two record type names collide across bases",
			files: map[string]string{
				testSchemaFile: "$graph:\n  - $import: cwl-part.yml\n  - $import: yr-part.yml\n",
				"cwl-part.yml": "$base: \"https://example.com/cwl#\"\n" +
					"$graph:\n  - name: Workflow\n    type: record\n    fields: []\n",
				"yr-part.yml": "$base: \"https://example.com/yr#\"\n" +
					"$graph:\n  - name: Workflow\n    type: record\n    fields: []\n",
			},
			want: []string{
				"Predicate collision on Workflow",
				"https://example.com/cwl#Workflow",
				"https://example.com/yr#Workflow",
			},
		},
		{
			name: "two bases with disjoint type names build cleanly",
			files: map[string]string{
				testSchemaFile: "$graph:\n  - $import: cwl-part.yml\n  - $import: yr-part.yml\n",
				"cwl-part.yml": "$base: \"https://example.com/cwl#\"\n" +
					"$graph:\n  - name: Workflow\n    type: record\n    fields: []\n",
				"yr-part.yml": "$base: \"https://example.com/yr#\"\n" +
					"$graph:\n  - name: Task\n    type: record\n    fields: []\n",
			},
			want: nil,
		},
		{
			name: "an enum symbol reused across bases still builds cleanly",
			files: map[string]string{
				testSchemaFile: "$graph:\n  - $import: cwl-enum.yml\n  - $import: yr-enum.yml\n",
				"cwl-enum.yml": "$base: \"https://example.com/cwl#\"\n" +
					"$graph:\n  - name: Colour\n    type: enum\n    symbols: [red, green]\n",
				"yr-enum.yml": "$base: \"https://example.com/yr#\"\n" +
					"$graph:\n  - name: Shade\n    type: enum\n    symbols: [red, blue]\n",
			},
			want: nil,
		},
		{
			name: "a field predicate reused across bases without an explicit jsonldPredicate still builds cleanly",
			files: map[string]string{
				testSchemaFile: "$graph:\n  - $import: cwl-field.yml\n  - $import: yr-field.yml\n",
				"cwl-field.yml": "$base: \"https://example.com/cwl#\"\n$graph:\n" +
					"  - name: Foo\n    type: record\n    fields:\n      - name: label\n        type: string\n",
				"yr-field.yml": "$base: \"https://example.com/yr#\"\n$graph:\n" +
					"  - name: Bar\n    type: record\n    fields:\n      - name: label\n        type: string\n",
			},
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := buildFixtureContext(t, tc.files)

			assertCollisionMentions(t, err, tc.want)
		})
	}
}

func TestNilContextTermHelpers(t *testing.T) {
	t.Parallel()

	var c *Context

	if got := c.termOf("anything"); got != emptyTerm {
		t.Errorf("termOf on a nil context = %v, want emptyTerm", got)
	}

	if got := c.identifierFields(); got != nil {
		t.Errorf("identifierFields on a nil context = %v, want nil", got)
	}

	if c.hasVocabTerm("anything") {
		t.Error("hasVocabTerm on a nil context must report false")
	}

	if _, ok := c.vocabTermFor("http://example.com/x"); ok {
		t.Error("vocabTermFor on a nil context must report false")
	}
}

func TestAddSchemasReadsAList(t *testing.T) {
	t.Parallel()

	const src = `
$schemas:
  - http://example.com/one.xml
  - http://example.com/two.xml
$graph: []
`

	ctx := schemaContext(t, src)

	want := []string{"http://example.com/one.xml", "http://example.com/two.xml"}
	if got := ctx.Schemas(); !reflect.DeepEqual(got, want) {
		t.Errorf("Schemas() = %v, want %v", got, want)
	}
}

func TestAddSchemasReadsABareString(t *testing.T) {
	t.Parallel()

	const src = `
$schemas: http://example.com/onto.xml
$graph: []
`

	ctx := schemaContext(t, src)

	want := []string{"http://example.com/onto.xml"}
	if got := ctx.Schemas(); !reflect.DeepEqual(got, want) {
		t.Errorf("Schemas() = %v, want %v", got, want)
	}
}

func TestRegisterSymbolsSkipsWhatIsNotUsable(t *testing.T) {
	t.Parallel()

	const src = `
$graph:
  - name: NoSymbols
    type: enum
  - name: MixedSymbols
    type: enum
    symbols: [red, 3, green]
`

	ctx := schemaContext(t, src)

	vocab := ctx.Vocab()
	for _, want := range []string{testRed, litGreen} {
		if _, ok := vocab[want]; !ok {
			t.Errorf("vocab is missing %q: %v", want, vocab)
		}
	}
}

func TestRegisterFieldListSkipsNonMappingItems(t *testing.T) {
	t.Parallel()

	const src = `
$graph:
  - name: Thing
    type: record
    fields:
      - name: v
        type: string
      - just a string
`

	ctx := schemaContext(t, src)

	if _, ok := ctx.Term("v"); !ok {
		t.Error("the well-formed field must still be registered")
	}
}

func TestRegisterFieldMapShorthandAndOwnName(t *testing.T) {
	t.Parallel()

	const src = `
$graph:
  - name: Thing
    type: record
    fields:
      shorthand: string
      keyed:
        name: renamed
        type: string
`

	ctx := schemaContext(t, src)

	if _, ok := ctx.Term("shorthand"); !ok {
		t.Error("a shorthand field entry (bare type string) must still be registered")
	}

	if _, ok := ctx.Term("renamed"); !ok {
		t.Error("a field's own name must override the identifier map key")
	}
}

func TestRegisterFieldEmptyShortNameIsSkipped(t *testing.T) {
	t.Parallel()

	const src = `
$graph:
  - name: Thing
    type: record
    fields:
      - name: "#"
        type: string
`

	// registerField must not panic and must not register a term for the empty
	// short name.
	ctx := schemaContext(t, src)

	if _, ok := ctx.Term(""); ok {
		t.Error("a field whose short name is empty must not be registered")
	}
}

func TestTermForRejectsANonStringNonMapPredicate(t *testing.T) {
	t.Parallel()

	const src = `
$graph:
  - name: Thing
    type: record
    fields:
      - name: v
        type: string
        jsonldPredicate: 5
`

	ctx := schemaContext(t, src)

	term, ok := ctx.Term("v")
	if !ok {
		t.Fatal("the field must still be registered")
	}

	if term.IsIdentifier || term.Type != "" {
		t.Errorf("a non-string, non-map jsonldPredicate must be ignored, got %+v", term)
	}
}

func TestApplyPredicateFlagNoconvert(t *testing.T) {
	t.Parallel()

	const src = `
$graph:
  - name: Thing
    type: record
    fields:
      - name: v
        type: string
        jsonldPredicate:
          noconvert: true
`

	ctx := schemaContext(t, src)

	term, ok := ctx.Term("v")
	if !ok || !term.Noconvert {
		t.Errorf("term = %+v, ok = %v; want Noconvert true", term, ok)
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
