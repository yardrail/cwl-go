package salad

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// cwlSchemaDir is where the CWL v1.2 schema this package is exercised against
// lives. It is read through a Fetcher and never modified: pkg/cwlcore owns it,
// and pkg/salad knows nothing about CWL beyond the fact that it is a large, real
// Schema Salad schema.
const cwlSchemaDir = "../cwlcore/schema"

// cwlSchemaMount is the synthetic base URL the CWL schema is mounted at for the
// duration of a test.
const cwlSchemaMount = "file:///cwl-schema/"

// wantCWLRoots are the record types the CWL v1.2 schema flags documentRoot, in
// the order the schema declares them.
const wantCWLRoots = "CommandLineTool, ExpressionTool, Workflow, Operation"

// metaschemaFetcher serves the embedded Salad metaschema.
func metaschemaFetcher() Fetcher {
	return NewFSFetcher(metaschemaFS, metaschemaMount)
}

// prettyFatal fails the test with the rendered diagnostic tree, which is what a
// reader needs when a whole schema fails to load.
func prettyFatal(t *testing.T, what string, err error) {
	t.Helper()

	t.Fatalf("%s:\n%s", what, asError(err).Pretty())
}

func TestMetaschemaLoads(t *testing.T) {
	t.Parallel()

	s, ctx, err := Metaschema()
	if err != nil {
		prettyFatal(t, "Metaschema", err)
	}

	if ctx == nil {
		t.Fatal("Metaschema returned no context")
	}

	wantRoots := []string{
		"SaladRecordSchema", "SaladEnumSchema", "SaladMapSchema", "SaladUnionSchema", "Documentation",
	}

	roots := make([]string, 0, len(s.DocumentRoots()))
	for _, r := range s.DocumentRoots() {
		roots = append(roots, shortName(r.Name))
	}

	assertOrder(t, "metaschema documentRoots", roots, wantRoots)

	anything, ok := s.Type(nameAny)
	if !ok || !isAnyType(anything) {
		t.Errorf("Any resolves to %v, want the Any primitive the metaschema's Any enum maps onto", anything)
	}
}

func TestMetaschemaIsMemoized(t *testing.T) {
	t.Parallel()

	first, firstCtx, err := Metaschema()
	if err != nil {
		prettyFatal(t, "Metaschema", err)
	}

	second, secondCtx, err := Metaschema()
	if err != nil {
		prettyFatal(t, "Metaschema", err)
	}

	if first != second || firstCtx != secondCtx {
		t.Error("Metaschema built the metaschema twice, but it is embedded and fixed")
	}
}

// TestMetaschemaSelfValidates is the canonical Schema Salad correctness check:
// the metaschema must load as a schema and validate as a document against itself.
// There is no external Salad conformance suite, so this stands in for one.
func TestMetaschemaSelfValidates(t *testing.T) {
	t.Parallel()

	ls, err := LoadSchema(metaschemaRef, WithFetcher(metaschemaFetcher()))
	if err != nil {
		prettyFatal(t, "loading the metaschema as a schema document", err)
	}

	if len(ls.Schema.DocumentRoots()) != 5 {
		t.Errorf("the self-loaded metaschema has %d documentRoot types, want 5", len(ls.Schema.DocumentRoots()))
	}

	if ls.Metadata == nil || !ls.Metadata.Has(dirNamespaces) {
		t.Error("the metaschema's $namespaces did not survive into the loaded schema's metadata")
	}

	ls.Loader = NewLoader(WithFetcher(metaschemaFetcher()), WithContext(ls.Context))

	_, err = ls.LoadAndValidate(metaschemaRef, Strict(true))
	if err != nil {
		prettyFatal(t, "validating the metaschema against itself", err)
	}
}

// loadCWLSchema loads the vendored CWL v1.2 schema, which LoadSchema validates
// against the Salad metaschema on the way in.
func loadCWLSchema(t *testing.T) *LoadedSchema {
	t.Helper()

	_, statErr := os.Stat(filepath.Join(cwlSchemaDir, "CommonWorkflowLanguage.yml"))
	if statErr != nil {
		t.Fatalf("the vendored CWL schema is not where this test expects it: %v", statErr)
	}

	fetcher := NewFSFetcher(os.DirFS(cwlSchemaDir), cwlSchemaMount)

	ls, err := LoadSchema(cwlSchemaMount+"CommonWorkflowLanguage.yml", WithFetcher(fetcher))
	if err != nil {
		prettyFatal(t, "loading the CWL v1.2 schema", err)
	}

	return ls
}

// TestCWLSchemaValidatesAgainstTheMetaschema is the end-to-end proof that the
// whole stack handles a real, large schema: the CWL v1.2 schema is resolved,
// validated against the Salad metaschema, and flattened.
func TestCWLSchemaValidatesAgainstTheMetaschema(t *testing.T) {
	t.Parallel()

	ls := loadCWLSchema(t)

	roots := make([]string, 0, len(ls.Schema.DocumentRoots()))
	for _, r := range ls.Schema.DocumentRoots() {
		roots = append(roots, shortName(r.Name))
	}

	if got := strings.Join(roots, ", "); got != wantCWLRoots {
		t.Errorf("CWL documentRoots = [%s], want [%s]", got, wantCWLRoots)
	}

	if len(ls.Schema.Names()) < 100 {
		t.Errorf("the flattened CWL schema defines only %d types, which is far too few", len(ls.Schema.Names()))
	}
}

func TestCWLSchemaFieldsCarryTheirPredicateAndOptionality(t *testing.T) {
	t.Parallel()

	ls := loadCWLSchema(t)
	fields := 0

	for _, name := range ls.Schema.Names() {
		r, ok := mustRecord(ls.Schema, name)
		if !ok {
			continue
		}

		fields += len(r.Fields)

		assertFieldsWired(t, r)
	}

	if fields < 300 {
		t.Errorf("the flattened CWL schema has only %d fields, which is far too few", fields)
	}
}

// assertFieldsWired checks the two things the flattener wires onto every field:
// the jsonldPredicate the validator drives link checking from, and the
// optionality flag, which must agree with what the field's type says.
func assertFieldsWired(t *testing.T, r *RecordType) {
	t.Helper()

	for _, f := range r.Fields {
		if f.JSONLDPred == nil {
			t.Errorf("field %q carries no jsonldPredicate, so link checking would be inert for it", f.Name)
		}

		if f.Optional != acceptsNull(f.Type) {
			t.Errorf("field %q: Optional = %v, but its type says %v", f.Name, f.Optional, acceptsNull(f.Type))
		}
	}
}

func TestLoadAndValidateACommandLineTool(t *testing.T) {
	t.Parallel()

	ls := loadCWLSchema(t)

	const tool = `cwlVersion: v1.2
class: CommandLineTool
baseCommand: echo
inputs:
  message:
    type: string
    inputBinding: {position: 1}
outputs: []
`

	dir := t.TempDir()
	path := filepath.Join(dir, "tool.cwl")

	err := os.WriteFile(path, []byte(tool), 0o600)
	if err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	doc, err := ls.LoadAndValidate(path, Strict(true))
	if err != nil {
		prettyFatal(t, "validating a CommandLineTool against the CWL schema", err)
	}

	m, ok := AsMap(doc.Root)
	if !ok {
		t.Fatalf("the resolved document is %s, want a mapping", NodeKind(doc.Root))
	}

	if class, _ := AsString(nodeOrNil(m, "class")); class != "CommandLineTool" {
		t.Errorf("the resolved document has class %q, want CommandLineTool", class)
	}
}

func TestLoadAndValidateRejectsAnInvalidDocument(t *testing.T) {
	t.Parallel()

	ls := loadCWLSchema(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "tool.cwl")

	err := os.WriteFile(path, []byte("cwlVersion: v1.2\nclass: CommandLineTool\n"), 0o600)
	if err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	_, err = ls.LoadAndValidate(path, Strict(true))
	if err == nil {
		t.Fatal("a CommandLineTool with neither inputs nor outputs validated")
	}

	assertMentions(t, err, "is not valid, because", "inputs")
}

// exampleSchemaMount is where the specification's example schemas are mounted.
const exampleSchemaMount = "file:///examples/"

// TestLoadSchemaRunsASpecExample loads one of the specification's preprocessing
// examples through the full two-call flow, which the loader-level example test
// cannot: the schema is validated against the metaschema and flattened, and the
// source document is then validated against the flattened schema, not merely
// resolved.
func TestLoadSchemaRunsASpecExample(t *testing.T) {
	t.Parallel()

	fetcher := NewFSFetcher(os.DirFS(exampleDir), exampleSchemaMount)

	ls, err := LoadSchema(exampleSchemaMount+"map_res_schema.yml", WithFetcher(fetcher))
	if err != nil {
		prettyFatal(t, "loading map_res_schema.yml", err)
	}

	ls.Loader = NewLoader(WithFetcher(fetcher), WithContext(ls.Context))

	doc, err := ls.LoadAndValidate(exampleSchemaMount+"map_res_src.yml", Strict(true))
	if err != nil {
		prettyFatal(t, "validating map_res_src.yml", err)
	}

	want := readFixture(t, examplePath("map_res_proc.yml"))
	if !nodeEqual(doc.Root, want) {
		t.Errorf("the validated document does not match map_res_proc.yml\n got: %s\nwant: %s",
			canonicalKey(doc.Root), canonicalKey(want))
	}
}

// TestLoadMetaschemaFromReportsLoadFailure exercises loadMetaschemaFrom's
// Loader.Load error branch directly, against a file system that has none of
// the metaschema's files. metaschema() is a process-wide [sync.OnceValue], so
// this can only be reached by calling loadMetaschemaFrom directly, bypassing
// the memoized singleton entirely.
func TestLoadMetaschemaFromReportsLoadFailure(t *testing.T) {
	t.Parallel()

	got := loadMetaschemaFrom(fstest.MapFS{})
	if got.err == nil {
		t.Fatal("loadMetaschemaFrom must report a Loader.Load failure against an empty file system")
	}

	if !strings.Contains(got.err.Error(), "loading the built-in Schema Salad metaschema") {
		t.Errorf("error = %v, want it to name the loading stage", got.err)
	}
}

// TestLoadMetaschemaFromReportsFlattenFailure exercises loadMetaschemaFrom's
// Flatten error branch: a metaschema entry point that loads fine but flattens
// to something broken.
func TestLoadMetaschemaFromReportsFlattenFailure(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"metaschema/metaschema.yml": &fstest.MapFile{
			Data: []byte("$base: " + testSchemaBase + "\n" +
				"$graph:\n- name: A\n  type: record\n  extends: B\n- name: B\n  type: record\n  extends: A\n"),
		},
	}

	got := loadMetaschemaFrom(fsys)
	if got.err == nil {
		t.Fatal("loadMetaschemaFrom must report a Flatten failure on a broken metaschema")
	}

	if !strings.Contains(got.err.Error(), "flattening the built-in Schema Salad metaschema") {
		t.Errorf("error = %v, want it to name the flattening stage", got.err)
	}
}

func TestLoadSchemaReportsAnUnreadableReference(t *testing.T) {
	t.Parallel()

	_, err := LoadSchema(testSchemaMount+"missing.yml", WithFetcher(NewFSFetcher(fstest.MapFS{}, testSchemaMount)))
	if err == nil {
		t.Fatal("loading a schema that does not exist succeeded")
	}

	if !strings.Contains(err.Error(), "missing.yml") {
		t.Errorf("error = %q, want it to name the missing document", err)
	}
}

// TestLoadSchemaReportsAFlattenFailure covers LoadSchema's own Flatten error
// branch: a document shaped enough to validate against the metaschema (which
// checks structure, not semantics) but whose extends declarations form a
// cycle, which only Flatten itself catches.
func TestLoadSchemaReportsAFlattenFailure(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		testSchemaFile: &fstest.MapFile{
			Data: []byte("$base: " + testSchemaBase + "\n" +
				"$graph:\n- name: A\n  type: record\n  extends: B\n- name: B\n  type: record\n  extends: A\n"),
		},
	}

	_, err := LoadSchema(testSchemaMount+testSchemaFile, WithFetcher(NewFSFetcher(fsys, testSchemaMount)))
	if err == nil || !strings.Contains(err.Error(), "extends itself") {
		t.Errorf("LoadSchema on a cyclic schema = %v, want it to report the Flatten failure", err)
	}
}

func TestLoadSchemaRejectsADocumentThatIsNotASchema(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{testSchemaFile: &fstest.MapFile{Data: []byte("hello: world\n")}}

	_, err := LoadSchema(testSchemaMount+testSchemaFile, WithFetcher(NewFSFetcher(fsys, testSchemaMount)))
	if err == nil {
		t.Fatal("a document that is not a schema loaded as one")
	}

	assertMentions(t, err, "is not a valid Schema Salad schema, because")
}

// stubFailFetcher is a Fetcher whose FetchText and Normalize both fail, for
// reaching the error branches LoadSchema and LoadAndValidate delegate to their
// Loader.
type stubFailFetcher struct{}

var errStubFetch = errors.New("stub fetch failure")

func (stubFailFetcher) FetchText(string) ([]byte, error) { return nil, errStubFetch }
func (stubFailFetcher) Exists(string) bool               { return false }
func (stubFailFetcher) Normalize(_, _ string) (string, error) {
	return "", errStubFetch
}

func TestLoadSchemaReportsALoaderFailure(t *testing.T) {
	t.Parallel()

	_, err := LoadSchema("anything", WithFetcher(stubFailFetcher{}))
	if err == nil || !strings.Contains(err.Error(), errStubFetch.Error()) {
		t.Errorf("LoadSchema with a failing fetcher = %v, want it to report the fetch failure", err)
	}
}

func TestLoadAndValidateReportsALoaderFailure(t *testing.T) {
	t.Parallel()

	ls := &LoadedSchema{
		Schema: NewSchema([]Type{rootRecord(typeDoc, field("v", Primitive(PrimitiveString)))}),
		Loader: NewLoader(WithFetcher(stubFailFetcher{})),
	}

	_, err := ls.LoadAndValidate("anything")
	if err == nil {
		t.Error("LoadAndValidate must report a Load failure from its Loader")
	}
}

func TestLoadAndValidateWithoutASchema(t *testing.T) {
	t.Parallel()

	var ls *LoadedSchema

	_, err := ls.LoadAndValidate(testFile)
	if err == nil {
		t.Error("a nil LoadedSchema validated a document")
	}

	_, err = (&LoadedSchema{}).LoadAndValidate(testFile)
	if err == nil {
		t.Error("an empty LoadedSchema validated a document")
	}
}

func TestFlattenReportsBrokenDefinitions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a top-level definition without a name",
			src:  "$graph:\n- type: record\n  fields: []\n",
			want: "must have a name",
		},
		{
			name: "a field type that names nothing",
			src:  "$graph:\n- name: R\n  type: record\n  fields:\n    - name: v\n      type: Nope\n",
			want: `the type "Nope" is not defined`,
		},
		{
			name: "an inline definition that declares no known kind",
			src:  "$graph:\n- name: R\n  type: record\n  fields:\n    - name: v\n      type: {type: heap}\n",
			want: "does not declare a type",
		},
		{
			name: "an extends naming an undefined base",
			src: "$base: \"" + testSchemaBase + "\"\n$graph:\n- name: R\n  type: record\n" +
				"  extends: " + testSchemaBase + "Absent\n",
			want: "which the schema does not define",
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

func TestFlattenReportsAnUnresolvedFieldMap(t *testing.T) {
	t.Parallel()

	fields := NewMapNode(SourceLine{}, entries("v", "string"))
	def := NewMapNode(SourceLine{}, []MapEntry{
		{Key: keyName, Value: NewStringNode(SourceLine{}, testSchemaBase+"R")},
		{Key: keyType, Value: NewStringNode(SourceLine{}, kindRecord)},
		{Key: keyFields, Value: fields},
	})

	_, err := Flatten(NewSeqNode(SourceLine{}, []Node{def}), saladBootstrapContext())
	assertErrorContains(t, err, "must be resolved before it is flattened")
}

func TestFlattenReportsAFieldWithoutAName(t *testing.T) {
	t.Parallel()

	def := NewMapNode(SourceLine{}, []MapEntry{
		{Key: keyName, Value: NewStringNode(SourceLine{}, testSchemaBase+"R")},
		{Key: keyType, Value: NewStringNode(SourceLine{}, kindRecord)},
		{Key: keyFields, Value: NewSeqNode(SourceLine{}, []Node{
			NewMapNode(SourceLine{}, entries(keyType, nameString)),
		})},
	})

	_, err := Flatten(NewSeqNode(SourceLine{}, []Node{def}), saladBootstrapContext())
	assertErrorContains(t, err, "must have a name")
}

func TestFlattenIgnoresWhatIsNotADefinition(t *testing.T) {
	t.Parallel()

	s, err := Flatten(NewSeqNode(SourceLine{}, []Node{
		NewStringNode(SourceLine{}, "stray"),
		NewMapNode(SourceLine{}, entries("note", "not a type definition")),
	}), saladBootstrapContext())
	if err != nil {
		t.Fatalf("Flatten failed on a document with no definitions: %v", err)
	}

	if len(s.Names()) != 0 {
		t.Errorf("Flatten produced %v from a document that defines no type", s.Names())
	}
}

func TestFlattenBuildsMapsUnionsAndInlineTypes(t *testing.T) {
	t.Parallel()

	s := mustFlatten(t, `
$base: "`+testSchemaBase+`"
$graph:
- name: Holder
  type: record
  documentRoot: true
  fields:
    - name: table
      type: {type: map, values: string}
    - name: choice
      type: {type: union, names: [string, int]}
    - name: single
      type: {type: union, names: string}
    - name: inline
      type:
        name: Inline
        type: record
        fields:
          - name: v
            type: string
    - name: shade
      type:
        name: Shade
        type: enum
        symbols: [dark, light]
`)

	holder := namedRecord(t, s, "Holder")

	assertKind[*MapType](t, holder, "table")
	assertKind[*UnionType](t, holder, "choice")
	assertKind[*UnionType](t, holder, "single")
	assertKind[*RecordType](t, holder, "inline")
	assertKind[*EnumType](t, holder, "shade")

	for _, name := range []string{"Holder/inline/Inline", "Holder/shade/Shade"} {
		if _, ok := s.Type(testSchemaBase + name); !ok {
			t.Errorf("the inline type %s is not in the name table; it defines %s", name, strings.Join(s.Names(), ", "))
		}
	}
}

// assertKind checks that a record's field has a type of the expected kind.
func assertKind[T Type](t *testing.T, r *RecordType, name string) {
	t.Helper()

	f, ok := r.Field(name)
	if !ok {
		t.Fatalf("%s has no field %q", shortName(r.Name), name)
	}

	if _, isKind := f.Type.(T); !isKind {
		var want T

		t.Errorf("field %q is a %T, want a %T", name, f.Type, want)
	}
}
