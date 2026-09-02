package salad

import (
	"errors"
	"testing"
)

// msgTypeRequired is the diagnostic a type expression that is missing altogether
// produces.
const msgTypeRequired = "a type is required here"

// errForeign stands in for an error from outside this package.
var errForeign = errors.New("something else went wrong")

func TestFlattenResolvesReferencesThroughTheVocabularyAndShortNames(t *testing.T) {
	t.Parallel()

	// Link validation is off so that the references stay written as the short
	// names the type builder has to resolve for itself.
	s, err := flattenUnlinked(t, `
$base: "`+testSchemaBase+`"
$graph:
- name: Thing
  type: record
  fields:
    - name: v
      type: string
- name: Hidden
  type: record
  inVocab: false
  fields:
    - name: v
      type: string
- name: User
  type: record
  documentRoot: true
  fields:
    - name: byVocabulary
      type: Thing
    - name: byShortName
      type: Hidden
`)
	if err != nil {
		t.Fatalf("Flatten failed: %v", err)
	}

	user := namedRecord(t, s, "User")

	for field, want := range map[string]string{
		"byVocabulary": "Thing",
		"byShortName":  "Hidden",
	} {
		f, ok := user.Field(field)
		if !ok {
			t.Fatalf("User has no field %q", field)
		}

		target, found := s.Type(testSchemaBase + want)
		if !found {
			t.Fatalf("the schema defines no %s", want)
		}

		if f.Type != target {
			t.Errorf("field %q resolves to %s, want %s", field, typeLabel(f.Type), want)
		}
	}
}

func TestBuildUnionReportsAMalformedMember(t *testing.T) {
	t.Parallel()

	_, err := flattenUnlinked(t,
		"$graph:\n- name: R\n  type: record\n  fields:\n    - name: v\n      type: [string, Nope]\n")
	assertErrorContains(t, err, `the type "Nope" is not defined`)
}

// TestResolveRefMatchesAFullyQualifiedIRI reaches namedType's primitiveIRIKinds
// lookup. It has to be built by hand, bypassing the loader entirely: the
// standard resolution path collapses a fully-qualified primitive IRI written
// in a @vocab-typed field straight back to its short vocabulary term (that is
// what ExpandVocabTerm is for), so no document the loader resolves ever hands
// the type builder the IRI form to resolve in the first place.
func TestResolveRefMatchesAFullyQualifiedIRI(t *testing.T) {
	t.Parallel()

	field := NewMapNode(SourceLine{}, []MapEntry{
		{Key: keyName, Value: NewStringNode(SourceLine{}, "v")},
		{Key: keyType, Value: NewStringNode(SourceLine{}, "http://www.w3.org/2001/XMLSchema#string")},
	})
	def := NewMapNode(SourceLine{}, []MapEntry{
		{Key: keyName, Value: NewStringNode(SourceLine{}, testSchemaBase+"R")},
		{Key: keyType, Value: NewStringNode(SourceLine{}, kindRecord)},
		{Key: keyFields, Value: NewSeqNode(SourceLine{}, []Node{field})},
	})

	s, err := Flatten(NewSeqNode(SourceLine{}, []Node{def}), saladBootstrapContext())
	if err != nil {
		t.Fatalf("Flatten failed: %v", err)
	}

	r, ok := mustRecord(s, testSchemaBase+"R")
	if !ok {
		t.Fatalf("the schema defines no R; it defines %v", s.Names())
	}

	f, ok := r.Field("v")
	if !ok {
		t.Fatal("R has no field v")
	}

	p, isPrim := f.Type.(*PrimitiveType)
	if !isPrim || p.Kind != PrimitiveString {
		t.Errorf("field v resolved to %s, want the string primitive via its fully-qualified IRI", typeLabel(f.Type))
	}
}

func TestFlattenBuildsAnonymousInlineTypes(t *testing.T) {
	t.Parallel()

	s := mustFlatten(t, `
$base: "`+testSchemaBase+`"
$graph:
- name: Holder
  type: record
  documentRoot: true
  fields:
    - name: anonymous
      type:
        fields:
          - name: v
            type: string
        type: record
`)

	anonymous, ok := namedRecord(t, s, "Holder").Field("anonymous")
	if !ok {
		t.Fatal("Holder has no anonymous field")
	}

	if anonymous.Type.TypeName() != "" {
		t.Errorf("an inline record with no name reports the name %q", anonymous.Type.TypeName())
	}

	if len(s.Names()) != 1 {
		t.Errorf("an anonymous inline record was entered into the name table: %v", s.Names())
	}
}

// TestFlattenReusesAnInlineRecordAlreadyDeclared builds the definitions by hand,
// because a document that names the same object twice is a duplicate-identifier
// error long before it reaches the flattener. The flattener still has to cope: an
// inline definition restating a declared name is that type, not a second copy.
func TestFlattenReusesAnInlineRecordAlreadyDeclared(t *testing.T) {
	t.Parallel()

	name := testSchemaBase + "Holder"

	inline := NewMapNode(SourceLine{}, entries(keyName, name, keyType, kindRecord))
	self := NewMapNode(SourceLine{}, []MapEntry{
		{Key: keyName, Value: NewStringNode(SourceLine{}, name+"/self")},
		{Key: keyType, Value: inline},
	})
	def := NewMapNode(SourceLine{}, []MapEntry{
		{Key: keyName, Value: NewStringNode(SourceLine{}, name)},
		{Key: keyType, Value: NewStringNode(SourceLine{}, kindRecord)},
		{Key: keyFields, Value: NewSeqNode(SourceLine{}, []Node{self})},
	})

	s, err := Flatten(NewSeqNode(SourceLine{}, []Node{def}), saladBootstrapContext())
	if err != nil {
		t.Fatalf("Flatten failed: %v", err)
	}

	holder, ok := mustRecord(s, name)
	if !ok {
		t.Fatalf("the schema defines no Holder; it defines %v", s.Names())
	}

	if holder.Fields[0].Type != Type(holder) {
		t.Error("an inline record restating a declared name built a second copy of that type")
	}
}

func TestFlattenReportsAMalformedType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a field with no type at all",
			src:  "$graph:\n- name: R\n  type: record\n  fields:\n    - name: v\n",
			want: msgTypeRequired,
		},
		{
			name: "a type written as a number",
			src:  "$graph:\n- name: R\n  type: record\n  fields:\n    - name: v\n      type: {type: array, items: 3}\n",
			want: "a type must be a name or a type definition",
		},
		{
			name: "an inline definition with no type at all",
			src:  "$graph:\n- name: R\n  type: record\n  fields:\n    - name: v\n      type: {name: Nested}\n",
			want: "does not declare a type",
		},
		{
			name: "a map with no value type",
			src:  "$graph:\n- name: R\n  type: record\n  fields:\n    - name: v\n      type: {type: map}\n",
			want: msgTypeRequired,
		},
		{
			name: "a union with no member types",
			src:  "$graph:\n- name: R\n  type: record\n  fields:\n    - name: v\n      type: {type: union}\n",
			want: msgTypeRequired,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := flattenUnlinked(t, tc.src)
			assertErrorContains(t, err, tc.want)
		})
	}
}

func TestFlattenAcceptsAGraphWrapper(t *testing.T) {
	t.Parallel()

	root, ctx := resolveSchema(t, `
$base: "`+testSchemaBase+`"
$graph:
- name: R
  type: record
  documentRoot: true
  fields:
    - name: v
      type: string
`, false)

	wrapped := NewMapNode(SourceLine{}, []MapEntry{{Key: dirGraph, Value: root}})

	s, err := Flatten(wrapped, ctx)
	if err != nil {
		t.Fatalf("Flatten failed on a $graph wrapper: %v", err)
	}

	if len(s.DocumentRoots()) != 1 {
		t.Errorf("flattening a $graph wrapper produced %d documentRoot types, want 1", len(s.DocumentRoots()))
	}
}

func TestSpecializeToleratesUnusualDeclarations(t *testing.T) {
	t.Parallel()

	empty := newSubstitution(make(map[string]string), nil)
	if !empty.empty() {
		t.Error("a substitution with no entries does not report itself empty")
	}

	node := NewStringNode(SourceLine{}, "Thing")
	if empty.apply(node) != Node(node) {
		t.Error("an empty substitution rewrote a node")
	}

	sub := newSubstitution(map[string]string{"Thing": "Replacement"}, nil)
	if sub.apply(nil) != nil {
		t.Error("a substitution invented a node from nothing")
	}

	number, ok := AsScalar(sub.apply(NewIntNode(SourceLine{}, 3)))
	if !ok || number.String() != "3" {
		t.Error("a substitution rewrote a value that is not a type name")
	}
}

func TestSpecializeMapReadsBothSpellings(t *testing.T) {
	t.Parallel()

	entry := NewMapNode(SourceLine{}, entries(keySpecializeFrom, "A", keySpecializeTo, "B"))

	cases := []struct {
		name  string
		value Node
		want  int
	}{
		{name: "a single mapping", value: entry, want: 1},
		{name: "a sequence of mappings", value: NewSeqNode(SourceLine{}, []Node{entry}), want: 1},
		{
			name:  "a sequence of anything else",
			value: NewSeqNode(SourceLine{}, []Node{NewStringNode(SourceLine{}, "x")}),
		},
		{name: "a value of the wrong shape", value: NewStringNode(SourceLine{}, "x")},
		{name: "an incomplete mapping", value: NewMapNode(SourceLine{}, entries(keySpecializeFrom, "A"))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := NewMapNode(SourceLine{}, []MapEntry{{Key: keySpecialize, Value: tc.value}})
			if got := len(specializeMap(def)); got != tc.want {
				t.Errorf("specializeMap read %d entries, want %d", got, tc.want)
			}
		})
	}

	if got := len(specializeMap(NewMapNode(SourceLine{}, nil))); got != 0 {
		t.Errorf("a record with no specialize declaration produced %d entries", got)
	}
}

func TestAsErrorWrapsAForeignError(t *testing.T) {
	t.Parallel()

	e := asError(errForeign)
	if e == nil || e.Msg != errForeign.Error() {
		t.Errorf("asError(%v) = %v, want a leaf carrying the message", errForeign, e)
	}

	nested := Errorf(SourceLine{}, "the real diagnostic")
	if asError(Group(SourceLine{}, "context", nested)).Msg != "context" {
		t.Error("asError did not recover the diagnostic tree of one of this package's errors")
	}
}
