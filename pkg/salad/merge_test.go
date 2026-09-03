package salad

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

const mergeSchemaBase = `
$base: "` + testSchemaBase + `"
$graph:
- name: Process
  type: record
  abstract: true
  documentRoot: true
  fields:
    - name: id
      type: string?
    - name: inputs
      type: string
- name: CommandLineTool
  type: record
  extends: Process
  documentRoot: true
  fields:
    - name: class
      type: {type: enum, symbols: [CommandLineTool]}
      jsonldPredicate: {_id: "@type", _type: "@vocab"}
`

const (
	mergeSchemaExtMount = "file:///ext-schema/"
	mergeSchemaExtFile  = "ext.yml"
	mergeSchemaExtBase  = "http://example.com/ext#"
)

const mergeSchemaExt = `
$base: "` + mergeSchemaExtBase + `"
$namespaces:
  test: "` + testSchemaBase + `"
$graph:
- name: ConnectorAction
  type: record
  extends: "` + testSchemaBase + `Process"
  documentRoot: true
  fields:
    - name: class
      type: {type: enum, symbols: [ConnectorAction]}
      jsonldPredicate: {_id: "@type", _type: "@vocab"}
    - name: capability
      type: string
`

func loadedFromInline(t *testing.T, mount, file, src string) *LoadedSchema {
	t.Helper()

	ls := resolvedFromInline(t, mount, file, src)

	schema, err := Flatten(ls.SchemaDoc, ls.Context)
	if err != nil {
		t.Fatalf("flattening the fixture: %v", err)
	}

	ls.Schema = schema
	ls.Loader = NewLoader(WithContext(ls.Context))

	return ls
}

func resolvedFromInline(t *testing.T, mount, file, src string) *LoadedSchema {
	t.Helper()

	fsys := fstest.MapFS{file: &fstest.MapFile{Data: []byte(src), Mode: 0, ModTime: time.Time{}, Sys: nil}}
	loader := NewLoader(
		WithFetcher(NewFSFetcher(fsys, mount)),
		WithContext(saladBootstrapContext()),
	)

	doc, err := loader.Load(mount + file)
	if err != nil {
		t.Fatalf("loading the schema fixture: %v", err)
	}

	ctx, err := BuildContext(doc.Root, doc.Metadata)
	if err != nil {
		t.Fatalf("building the fixture context: %v", err)
	}

	return &LoadedSchema{
		Schema:    nil,
		Context:   ctx,
		Loader:    nil,
		Metadata:  doc.Metadata,
		SchemaDoc: doc.Root,
	}
}

type mergeFixtures struct {
	base   *LoadedSchema
	ext    *LoadedSchema
	merged *LoadedSchema
}

func mergeFixture(t *testing.T) mergeFixtures {
	t.Helper()

	base := loadedFromInline(t, testSchemaMount, testSchemaFile, mergeSchemaBase)
	ext := resolvedFromInline(t, mergeSchemaExtMount, mergeSchemaExtFile, mergeSchemaExt)

	merged, err := MergeSchemas(base, ext)
	if err != nil {
		prettyFatal(t, "MergeSchemas", err)
	}

	return mergeFixtures{base: base, ext: ext, merged: merged}
}

func TestMergeSchemasDocumentRoots(t *testing.T) {
	t.Parallel()

	f := mergeFixture(t)
	merged := f.merged

	roots := merged.Schema.DocumentRoots()
	names := make([]string, 0, len(roots))

	for _, r := range roots {
		names = append(names, shortName(r.Name))
	}

	if len(roots) < 2 {
		t.Fatalf("DocumentRoots = %v, want at least Process subtypes and ConnectorAction", names)
	}

	found := false

	for _, n := range names {
		if n == "ConnectorAction" {
			found = true
		}
	}

	if !found {
		t.Errorf("DocumentRoots = %v, want ConnectorAction among them", names)
	}
}

func TestMergeSchemasInheritedFields(t *testing.T) {
	t.Parallel()

	merged := mergeFixture(t).merged

	ca, ok := mustRecord(merged.Schema, mergeSchemaExtBase+"ConnectorAction")
	if !ok {
		t.Fatalf("the merged schema has no ConnectorAction; it defines: %s",
			strings.Join(merged.Schema.Names(), ", "))
	}

	fieldNames := fieldNamesOf(ca)

	for _, want := range []string{"id", "inputs", fieldClass, "capability"} {
		found := false

		for _, got := range fieldNames {
			if got == want {
				found = true
			}
		}

		if !found {
			t.Errorf("ConnectorAction fields = %v, missing %q", fieldNames, want)
		}
	}
}

func TestMergeSchemasExtensionValidates(t *testing.T) {
	t.Parallel()

	merged := mergeFixture(t).merged

	const doc = `class: ConnectorAction
capability: something
id: test
inputs: x`

	parsed, perr := Parse("test.yml", []byte(doc))
	if perr != nil {
		t.Fatalf("parsing: %v", perr)
	}

	resolved, lerr := merged.Loader.LoadNode(parsed, "test.yml")
	if lerr != nil {
		t.Fatalf("resolving: %v", lerr)
	}

	verr := merged.Schema.Validate(resolved.Root)
	if verr != nil {
		t.Errorf("validate against merged schema: %v", verr)
	}
}

func TestMergeSchemasExtensionFailsAgainstBase(t *testing.T) {
	t.Parallel()

	base := mergeFixture(t).base

	const doc = `class: ConnectorAction
capability: something
id: test
inputs: x`

	parsed, perr := Parse("test.yml", []byte(doc))
	if perr != nil {
		t.Fatalf("parsing: %v", perr)
	}

	resolved, lerr := base.Loader.LoadNode(parsed, "test.yml")
	if lerr != nil {
		return
	}

	verr := base.Schema.Validate(resolved.Root)
	if verr == nil {
		t.Error("expected validation to reject ConnectorAction against the base schema")
	}
}

func TestMergeSchemasNilSchemaDoc(t *testing.T) {
	t.Parallel()

	f := mergeFixture(t)
	noDoc := &LoadedSchema{Schema: f.base.Schema, Context: f.base.Context, Loader: nil, Metadata: nil, SchemaDoc: nil}

	_, err := MergeSchemas(noDoc, f.ext)
	if !errors.Is(err, ErrMissingSchemaDoc) {
		t.Errorf("MergeSchemas(nil SchemaDoc) = %v, want ErrMissingSchemaDoc", err)
	}
}

func TestLoadExtensionSchema(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "ext.yml")

	err := os.WriteFile(path, []byte(mergeSchemaBase), 0o600)
	if err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	ls, err := LoadExtensionSchema(path)
	if err != nil {
		prettyFatal(t, "LoadExtensionSchema", err)
	}

	if ls.SchemaDoc == nil {
		t.Error("SchemaDoc is nil")
	}

	if ls.Context == nil {
		t.Error("Context is nil")
	}

	if ls.Schema != nil {
		t.Error("Schema should be nil for an extension schema")
	}
}

func TestLoadExtensionSchemaRejectsLoadError(t *testing.T) {
	t.Parallel()

	_, err := LoadExtensionSchema("file:///does/not/exist.yml")
	if err == nil {
		t.Error("expected an error for a nonexistent file")
	}
}

func TestLoadExtensionSchemaRejectsInvalidSchema(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yml")

	err := os.WriteFile(path, []byte("- name: Foo\n  type: record\n  bogus: true\n"), 0o600)
	if err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	_, err = LoadExtensionSchema(path)
	if err == nil {
		t.Error("expected an error for an invalid schema")
	}
}

func TestMergeSchemasRejectsNamelessDefinition(t *testing.T) {
	t.Parallel()

	base := loadedFromInline(t, testSchemaMount, testSchemaFile, mergeSchemaBase)

	namelessDef := NewMapNode(
		SourceLine{
			File:  "",
			Start: Position{Line: 0, Column: 0, Offset: 0},
			End:   Position{Line: 0, Column: 0, Offset: 0},
		},
		[]MapEntry{
			{
				Key: "type",
				Value: NewStringNode(
					SourceLine{
						File:  "",
						Start: Position{Line: 0, Column: 0, Offset: 0},
						End:   Position{Line: 0, Column: 0, Offset: 0},
					},
					"record",
				),
			},
		},
	)
	extWithBadDoc := &LoadedSchema{
		Schema:    nil,
		Context:   newContext(),
		Loader:    nil,
		Metadata:  nil,
		SchemaDoc: namelessDef,
	}

	_, err := MergeSchemas(base, extWithBadDoc)
	if err == nil {
		t.Error("expected an error for a nameless type definition")
	}
}
