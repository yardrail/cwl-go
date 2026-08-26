package salad

import (
	"strings"
	"testing"
	"testing/fstest"
)

// testSchemaMount is where an inline schema fixture is mounted for loading.
const testSchemaMount = "file:///test-schema/"

// testSchemaBase is the base URI the inline schema fixtures declare, so that the
// names they define are predictable.
const testSchemaBase = "http://example.com/test#"

// Type expressions and the diagnostic the narrowing table is written against.
const (
	optionalString = nameString + "?"
	stringOrInt    = "[" + nameString + ", " + nameInt + "]"
	msgNotNarrow   = "does not narrow"
)

// resolveSchema loads an inline schema document exactly as LoadSchema does,
// stopping short of flattening it.
//
// skipLinks turns off link validation, which a fixture needs when the mistake it
// carries is one the loader would catch first: a type reference naming nothing is
// a broken link long before it is a flattening problem.
func resolveSchema(t *testing.T, src string, skipLinks bool) (Node, *Context) {
	t.Helper()

	fsys := fstest.MapFS{"schema.yml": &fstest.MapFile{Data: []byte(src)}}
	loader := NewLoader(
		WithFetcher(NewFSFetcher(fsys, testSchemaMount)),
		WithContext(saladBootstrapContext()),
		WithSkipLinkCheck(skipLinks),
	)

	doc, err := loader.Load(testSchemaMount + "schema.yml")
	if err != nil {
		t.Fatalf("loading the schema fixture: %v", err)
	}

	ctx, err := BuildContext(doc.Root, doc.Metadata)
	if err != nil {
		t.Fatalf("building the fixture context: %v", err)
	}

	return doc.Root, ctx
}

// flattenSource resolves and flattens an inline schema document, bypassing the
// metaschema validation LoadSchema performs so that a deliberately invalid
// fixture reaches the flattener.
func flattenSource(t *testing.T, src string) (*Schema, error) {
	t.Helper()

	root, ctx := resolveSchema(t, src, false)

	return Flatten(root, ctx)
}

// flattenUnlinked is flattenSource with link validation turned off, for fixtures
// whose mistake only the flattener is meant to report.
func flattenUnlinked(t *testing.T, src string) (*Schema, error) {
	t.Helper()

	root, ctx := resolveSchema(t, src, true)

	return Flatten(root, ctx)
}

// mustFlatten flattens a fixture the test requires to be valid.
func mustFlatten(t *testing.T, src string) *Schema {
	t.Helper()

	s, err := flattenSource(t, src)
	if err != nil {
		t.Fatalf("Flatten failed: %v", err)
	}

	return s
}

// namedRecord resolves a record the test requires the schema to define.
func namedRecord(t *testing.T, s *Schema, name string) *RecordType {
	t.Helper()

	r, ok := mustRecord(s, testSchemaBase+name)
	if !ok {
		t.Fatalf("the schema defines no record named %q; it defines %s", name, strings.Join(s.Names(), ", "))
	}

	return r
}

// fieldNamesOf lists a record's fields by short name, in order.
func fieldNamesOf(r *RecordType) []string {
	out := make([]string, 0, len(r.Fields))
	for _, f := range r.Fields {
		out = append(out, f.ShortName())
	}

	return out
}

// assertOrder compares an ordered list of names with what was expected.
func assertOrder(t *testing.T, what string, got, want []string) {
	t.Helper()

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s = [%s], want [%s]", what, strings.Join(got, ", "), strings.Join(want, ", "))
	}
}

// extendsFixture exercises single inheritance with an abstract base.
const extendsFixture = `
$base: "` + testSchemaBase + `"
$graph:
- name: Base
  type: record
  abstract: true
  fields:
    - name: alpha
      type: string
    - name: beta
      type: string?
- name: Derived
  type: record
  extends: Base
  documentRoot: true
  fields:
    - name: gamma
      type: int
`

func TestFlattenExtendsMergesFieldsBaseFirst(t *testing.T) {
	t.Parallel()

	s := mustFlatten(t, extendsFixture)
	derived := namedRecord(t, s, "Derived")

	assertOrder(t, "Derived fields", fieldNamesOf(derived), []string{"alpha", "beta", "gamma"})

	for _, f := range derived.Fields[:2] {
		if f.InheritedFrom != testSchemaBase+"Base" {
			t.Errorf("field %q inherited from %q, want %q", f.ShortName(), f.InheritedFrom, testSchemaBase+"Base")
		}
	}

	if derived.Fields[2].InheritedFrom != "" {
		t.Errorf("a field the record declares itself reports inherited from %q", derived.Fields[2].InheritedFrom)
	}
}

func TestFlattenRecordsAbstractAndDocumentRoot(t *testing.T) {
	t.Parallel()

	s := mustFlatten(t, extendsFixture)

	base := namedRecord(t, s, "Base")
	if !base.Abstract || base.IsDocumentRoot() {
		t.Errorf("Base: abstract = %v, documentRoot = %v; want true, false", base.Abstract, base.DocumentRoot)
	}

	derived := namedRecord(t, s, "Derived")
	if derived.Abstract || !derived.IsDocumentRoot() {
		t.Errorf("Derived: abstract = %v, documentRoot = %v; want false, true", derived.Abstract, derived.DocumentRoot)
	}

	roots := s.DocumentRoots()
	if len(roots) != 1 || roots[0] != derived {
		t.Errorf("DocumentRoots() = %v, want only Derived", roots)
	}
}

func TestFlattenFieldOptionalMatchesType(t *testing.T) {
	t.Parallel()

	s := mustFlatten(t, extendsFixture)

	for _, name := range s.Names() {
		r, ok := mustRecord(s, name)
		if !ok {
			continue
		}

		for _, f := range r.Fields {
			if f.Optional != acceptsNull(f.Type) {
				t.Errorf("field %q: Optional = %v, but its type %s says %v",
					f.Name, f.Optional, typeLabel(f.Type), acceptsNull(f.Type))
			}
		}
	}
}

func TestFlattenPopulatesJSONLDPredicate(t *testing.T) {
	t.Parallel()

	s := mustFlatten(t, `
$base: "`+testSchemaBase+`"
$graph:
- name: Linked
  type: record
  documentRoot: true
  fields:
    - name: id
      type: string
      jsonldPredicate: "@id"
    - name: target
      type: string
      jsonldPredicate:
        _id: "http://example.com/target"
        _type: "@id"
`)

	linked := namedRecord(t, s, "Linked")

	id, ok := linked.Field("id")
	if !ok || id.JSONLDPred == nil || id.JSONLDPred.ID != keywordID {
		t.Fatalf("the id field carries %+v, want a predicate whose ID is @id", id.JSONLDPred)
	}

	target, ok := linked.Field("target")
	if !ok || target.JSONLDPred == nil || !target.JSONLDPred.IsLink() {
		t.Fatalf("the target field carries %+v, want a link predicate", target.JSONLDPred)
	}

	if !isLinkField(target) {
		t.Error("the target field is not recognized as a link field, so link checking would be inert")
	}
}

// multiLevelFixture declares the most derived record first, so that flattening
// cannot depend on declaration order.
const multiLevelFixture = `
$base: "` + testSchemaBase + `"
$graph:
- name: Third
  type: record
  extends: Second
  fields:
    - name: c
      type: string
- name: Second
  type: record
  extends: First
  fields:
    - name: b
      type: string
- name: First
  type: record
  fields:
    - name: a
      type: string
`

func TestFlattenMultiLevelInheritance(t *testing.T) {
	t.Parallel()

	s := mustFlatten(t, multiLevelFixture)

	assertOrder(t, "Third fields", fieldNamesOf(namedRecord(t, s, "Third")), []string{"a", "b", "c"})
	assertOrder(t, "Second fields", fieldNamesOf(namedRecord(t, s, "Second")), []string{"a", "b"})
}

func TestFlattenDiamondInheritanceKeepsOneCopy(t *testing.T) {
	t.Parallel()

	s := mustFlatten(t, `
$base: "`+testSchemaBase+`"
$graph:
- name: Top
  type: record
  fields:
    - name: shared
      type: string
- name: Left
  type: record
  extends: Top
  fields:
    - name: left
      type: string
- name: Right
  type: record
  extends: Top
  fields:
    - name: right
      type: string
- name: Bottom
  type: record
  extends: [Left, Right]
  fields:
    - name: bottom
      type: string
`)

	assertOrder(t, "Bottom fields", fieldNamesOf(namedRecord(t, s, "Bottom")),
		[]string{"shared", "left", "right", "bottom"})
}

func TestFlattenSpecializeRewritesNestedReferences(t *testing.T) {
	t.Parallel()

	s := mustFlatten(t, `
$base: "`+testSchemaBase+`"
$graph:
- name: Item
  type: record
  fields:
    - name: v
      type: string
- name: SpecialItem
  type: record
  extends: Item
- name: Holder
  type: record
  fields:
    - name: items
      type: Item[]?
- name: SpecialHolder
  type: record
  extends: Holder
  specialize:
    Item: SpecialItem
`)

	items, ok := namedRecord(t, s, "SpecialHolder").Field("items")
	if !ok {
		t.Fatal("SpecialHolder has no items field")
	}

	special, found := s.Type(testSchemaBase + "SpecialItem")
	if !found {
		t.Fatal("the schema defines no SpecialItem")
	}

	if elem := arrayElementOf(items.Type); elem != special {
		t.Errorf("SpecialHolder.items has element type %s, want SpecialItem", typeLabel(elem))
	}

	plain, ok := namedRecord(t, s, "Holder").Field("items")
	if !ok {
		t.Fatal("Holder has no items field")
	}

	if elem := arrayElementOf(plain.Type); elem == special {
		t.Error("specializing SpecialHolder also rewrote the base record it inherits from")
	}
}

// arrayElementOf digs the element type out of an optional list of something.
func arrayElementOf(t Type) Type {
	u, ok := t.(*UnionType)
	if !ok {
		return nil
	}

	for _, opt := range u.Options {
		if a, isArray := opt.(*ArrayType); isArray {
			return a.Items
		}
	}

	return nil
}

func TestFlattenNarrowingAcceptedAndWideningRejected(t *testing.T) {
	t.Parallel()

	const fixture = `
$base: "` + testSchemaBase + `"
$graph:
- name: Base
  type: record
  fields:
    - name: v
      type: %s
- name: Derived
  type: record
  extends: Base
  fields:
    - name: v
      type: %s
`

	cases := []struct {
		name    string
		base    string
		derived string
		wantErr string
	}{
		{name: "narrows an optional to a required", base: optionalString, derived: nameString},
		{name: "narrows a union to one member", base: stringOrInt, derived: nameInt},
		{name: "restates the inherited type", base: nameString, derived: nameString},
		{name: "widens a required to an optional", base: nameString, derived: optionalString, wantErr: msgNotNarrow},
		{name: "widens to an unrelated type", base: nameString, derived: nameInt, wantErr: msgNotNarrow},
		{name: "widens one member to a union", base: nameInt, derived: stringOrInt, wantErr: msgNotNarrow},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := flattenSource(t, strings.Replace(strings.Replace(fixture,
				"%s", tc.base, 1), "%s", tc.derived, 1))

			assertErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestFlattenEnumExtendsMergesSymbols(t *testing.T) {
	t.Parallel()

	s := mustFlatten(t, `
$base: "`+testSchemaBase+`"
$graph:
- name: BaseEnum
  type: enum
  symbols: [red, green]
- name: MoreEnum
  type: enum
  extends: BaseEnum
  symbols: [green, blue]
`)

	more, ok := s.Type(testSchemaBase + "MoreEnum")
	if !ok {
		t.Fatal("the schema defines no MoreEnum")
	}

	e, ok := more.(*EnumType)
	if !ok {
		t.Fatalf("MoreEnum is a %T, want an enum", more)
	}

	short := make([]string, 0, len(e.Symbols))
	for _, sym := range e.Symbols {
		short = append(short, shortName(sym))
	}

	assertOrder(t, "MoreEnum symbols", short, []string{"red", "green", "blue"})
}

func TestFlattenReportsExtendsCycle(t *testing.T) {
	t.Parallel()

	_, err := flattenSource(t, `
$base: "`+testSchemaBase+`"
$graph:
- name: A
  type: record
  extends: B
- name: B
  type: record
  extends: A
`)

	assertErrorContains(t, err, "extends itself")
}

func TestFlattenIsDeterministic(t *testing.T) {
	t.Parallel()

	const fixture = `
$base: "` + testSchemaBase + `"
$graph:
- name: Colour
  type: enum
  symbols: [red, green, blue]
- name: Base
  type: record
  fields:
    - name: one
      type: string
    - name: two
      type: [string, int, "null"]
    - name: three
      type: Colour
- name: Middle
  type: record
  extends: Base
  fields:
    - name: four
      type: string
- name: Leaf
  type: record
  extends: Middle
  documentRoot: true
  fields:
    - name: five
      type: string
`

	first := renderSchema(mustFlatten(t, fixture))

	for i := range 8 {
		if next := renderSchema(mustFlatten(t, fixture)); next != first {
			t.Fatalf("flattening run %d differs:\n got: %s\nwant: %s", i, next, first)
		}
	}
}

// renderSchema writes out every ordered collection of a schema, so that two runs
// can be compared for the ordering a Go map would scramble.
func renderSchema(s *Schema) string {
	lines := make([]string, 0, len(s.Names()))

	for _, name := range s.Names() {
		t, _ := s.Type(name)
		lines = append(lines, name+":"+renderType(t, 0))
	}

	return strings.Join(lines, "\n")
}

// renderType renders one type's structure, cutting off below a fixed depth so
// that a cyclic graph still renders.
func renderType(t Type, depth int) string {
	if depth > renderDepth {
		return labelEllipsis
	}

	switch tt := t.(type) {
	case *RecordType:
		return "record(" + tt.Name + " " + strings.Join(renderFields(tt.Fields, depth), " ") + ")"
	case *EnumType:
		return "enum(" + tt.Name + " " + strings.Join(tt.Symbols, ",") + ")"
	case *UnionType:
		return "union(" + strings.Join(renderTypes(tt.Options, depth), "|") + ")"
	case *ArrayType:
		return "array(" + renderType(tt.Items, depth+1) + ")"
	case *MapType:
		return "map(" + renderType(tt.Values, depth+1) + ")"
	default:
		return t.TypeName()
	}
}

// renderDepth bounds how far renderType descends into a cyclic type graph.
const renderDepth = 3

// renderFields renders a record's fields in order.
func renderFields(fields []*Field, depth int) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f.Name+"="+renderType(f.Type, depth+1))
	}

	return out
}

// renderTypes renders a list of types in order.
func renderTypes(types []Type, depth int) []string {
	out := make([]string, 0, len(types))
	for _, t := range types {
		out = append(out, renderType(t, depth+1))
	}

	return out
}

// assertErrorContains checks an error against what a case expects: no error when
// want is empty, and a message mentioning want otherwise.
func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()

	if want == "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		return
	}

	if err == nil {
		t.Fatalf("expected an error mentioning %q, got none", want)
	}

	got := asError(err).Pretty()
	if !strings.Contains(got, want) {
		t.Errorf("error does not mention %q:\n%s", want, got)
	}
}
