package cwlcore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// The tests in this file exercise the three entry points that need the embedded
// CWL schema: Load, LoadFile and Schema. They are the only part of decoding that
// validates anything, so they are also where a regression in pkg/salad's schema
// loading or validation surfaces from this layer.

func TestSchemaReturnsTheEmbeddedSchema(t *testing.T) {
	t.Parallel()

	schema, version := Schema()
	if schema == nil {
		t.Fatal("Schema() returned a nil schema")
	}

	if version != SchemaVersion() {
		t.Errorf("Schema() version = %q, want %q", version, SchemaVersion())
	}

	// The four core process classes are the schema's document roots, which is
	// what lets a document be validated without naming its type.
	if got, want := len(schema.DocumentRoots()), len(processClasses); got != want {
		t.Errorf("schema has %d document roots, want %d", got, want)
	}

	// The schema is loaded once and shared, so a second call is the same value.
	again, _ := Schema()
	if again != schema {
		t.Error("Schema() built the schema twice")
	}
}

// processClasses is the set of classes the schema must expose as document roots.
var processClasses = []string{
	ClassCommandLineTool,
	ClassExpressionTool,
	ClassWorkflow,
	ClassOperation,
}

func TestLoadFromBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fixture string
		class   string
	}{
		// command_line_tool.cwl carries several $(...) values: in an
		// argument, in a resource requirement and in an outputEval. It leads
		// this table rather than a document without them because a field the
		// schema types as Expression is an enum with one placeholder symbol,
		// so validating it by symbol match rejects every real CWL document.
		// This case is what catches that regression from this layer.
		{fixture: "command_line_tool.cwl", class: ClassCommandLineTool},
		{fixture: "plain_tool.cwl", class: ClassCommandLineTool},
		{fixture: "expression_tool.cwl", class: ClassExpressionTool},
		{fixture: "operation.cwl", class: ClassOperation},
		// workflow.cwl is a packed $graph whose step names a sibling and
		// whose outputs are drawn through a bare-string step out, both of
		// which reach the validator here.
		{fixture: "workflow.cwl", class: ClassWorkflow},
	}

	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			process, err := Load(t.Context(), fixtureSource(t, tc.fixture), fixturePath(tc.fixture))
			if err != nil {
				t.Fatalf("Load(%s): %v", tc.fixture, err)
			}

			if got := process.Class(); got != tc.class {
				t.Errorf("Class() = %q, want %q", got, tc.class)
			}
		})
	}
}

func TestLoadKeepsExpressionsUnevaluated(t *testing.T) {
	t.Parallel()

	const fixture = "command_line_tool.cwl"

	process, err := Load(t.Context(), fixtureSource(t, fixture), fixturePath(fixture))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	tool, ok := process.(*CommandLineTool)
	if !ok {
		t.Fatal("loaded process is not a *CommandLineTool")
	}

	// The point of loading this fixture through the validator is that its
	// expressions survive validation and arrive here exactly as written.
	assertEqual(t, "Arguments[1].Kind()", tool.Arguments[1].Kind(), ValueExpression)
	assertEqual(t, "Arguments[1].Expression()", string(tool.Arguments[1].Expression()), "$(inputs.message)")
	assertEqual(t, "Outputs[1].OutputBinding.OutputEval",
		string(tool.Outputs[1].OutputBinding.OutputEval), "$(self[0])")

	resources, ok := tool.Requirements[1].(*ResourceRequirement)
	if !ok {
		t.Fatalf("Requirements[1] is %T, want *ResourceRequirement", tool.Requirements[1])
	}

	assertEqual(t, "OutdirMin.Kind()", resources.OutdirMin.Kind(), ValueExpression)
}

func TestLoadFileSelectsMainFromAGraph(t *testing.T) {
	t.Parallel()

	process, err := LoadFile(t.Context(), fixturePath("graph_fragment_main.cwl"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	if got := process.Class(); got != ClassWorkflow {
		t.Errorf("Class() = %q, want %q", got, ClassWorkflow)
	}
}

func TestLoadFileSelectsAFragment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fragment string
		class    string
	}{
		{fragment: "#tool", class: ClassCommandLineTool},
		{fragment: "#main", class: ClassWorkflow},
	}

	for _, tc := range tests {
		t.Run(tc.fragment, func(t *testing.T) {
			t.Parallel()

			process, err := LoadFile(t.Context(), fixturePath("graph_fragment_main.cwl")+tc.fragment)
			if err != nil {
				t.Fatalf("LoadFile: %v", err)
			}

			if got := process.Class(); got != tc.class {
				t.Errorf("Class() = %q, want %q", got, tc.class)
			}

			// The selected process keeps the identifier it was addressed by,
			// resolved to an absolute one.
			if got := idFragment(process.Base().ID); got != strings.TrimPrefix(tc.fragment, "#") {
				t.Errorf("selected process id = %q, want the %q object", process.Base().ID, tc.fragment)
			}
		})
	}
}

func TestLoadFileSelectsAFragmentOfASingleProcessDocument(t *testing.T) {
	t.Parallel()

	// A fragment is not only for graphs: it addresses the root process too,
	// which is how a reference into a document that turned out not to be
	// packed still resolves.
	process, err := LoadFile(t.Context(), fixturePath("plain_tool.cwl")+"#plain")
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	if got := process.Class(); got != ClassCommandLineTool {
		t.Errorf("Class() = %q, want %q", got, ClassCommandLineTool)
	}
}

func TestLoadFileRejectsAnUnknownFragment(t *testing.T) {
	t.Parallel()

	_, err := LoadFile(t.Context(), fixturePath("graph_fragment_main.cwl")+"#nope")
	if err == nil {
		t.Fatal("LoadFile succeeded on a fragment the document does not declare, want an error")
	}

	// The error names both what was asked for and what the document offers.
	for _, want := range []string{"#nope", "tool", graphMainName} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestLoadDocumentReturnsTheResolvedTree(t *testing.T) {
	t.Parallel()

	const fixture = "graph_fragment_main.cwl"

	doc, err := LoadDocument(t.Context(), fixtureSource(t, fixture), fixturePath(fixture))
	if err != nil {
		t.Fatalf("LoadDocument: %v", err)
	}

	if doc.Root == nil {
		t.Fatal("LoadDocument returned a document with no root")
	}

	// The document is what DecodeAll consumes, which is the reason for
	// exposing it: Decode alone can only reach the graph's entry point.
	all, err := DecodeAll(doc)
	if err != nil {
		t.Fatalf("DecodeAll: %v", err)
	}

	if len(all) != 2 {
		t.Errorf("DecodeAll found %d processes, want 2", len(all))
	}
}

func TestLoadFileDocumentIgnoresAFragment(t *testing.T) {
	t.Parallel()

	// The fragment selects an object inside the document, so it has no
	// bearing on the document this returns.
	doc, err := LoadFileDocument(t.Context(), fixturePath("graph_fragment_main.cwl")+"#tool")
	if err != nil {
		t.Fatalf("LoadFileDocument: %v", err)
	}

	all, err := DecodeAll(doc)
	if err != nil {
		t.Fatalf("DecodeAll: %v", err)
	}

	if len(all) != 2 {
		t.Errorf("DecodeAll found %d processes, want the whole graph of 2", len(all))
	}
}

func TestLoadedSchemaCarriesTheLoader(t *testing.T) {
	t.Parallel()

	loaded, err := LoadedSchema()
	if err != nil {
		t.Fatalf("LoadedSchema: %v", err)
	}

	if loaded.Loader == nil || loaded.Context == nil || loaded.Schema == nil {
		t.Fatalf("LoadedSchema returned %+v, want a loader, a context and a schema", loaded)
	}

	schema, _ := Schema()
	if loaded.Schema != schema {
		t.Error("LoadedSchema and Schema disagree about the schema")
	}

	// It is the same memoized value every time, so the flatten is paid once.
	again, err := LoadedSchema()
	if err != nil {
		t.Fatalf("LoadedSchema: %v", err)
	}

	if again != loaded {
		t.Error("LoadedSchema built the schema twice")
	}
}

func TestValidateOptionsAreReachable(t *testing.T) {
	t.Parallel()

	// A field the schema does not declare is an advisory by default, because
	// the loader has already done the strict part of the job. Strict is how a
	// caller asks for it to be fatal instead.
	const src = "class: Operation\ncwlVersion: v1.2\ninputs: []\noutputs: []\nbogusField: 42\n"

	_, err := Load(t.Context(), []byte(src), "bogus.cwl")
	if err != nil {
		t.Errorf("Load rejected an undeclared field by default: %v", err)
	}

	_, err = Load(t.Context(), []byte(src), "bogus.cwl", salad.Strict(true))
	if err == nil {
		t.Fatal("Load with salad.Strict(true) accepted an undeclared field, want an error")
	}

	var rejected *salad.Error
	if !errors.As(err, &rejected) {
		t.Fatalf("strict error %v is not a *salad.Error", err)
	}

	if !strings.Contains(rejected.Pretty(), "bogusField") {
		t.Errorf("strict error does not name the offending field:\n%s", rejected.Pretty())
	}

	// The option reaches the URI form and the document form too.
	_, err = LoadDocument(t.Context(), []byte(src), "bogus.cwl", salad.Strict(true))
	if err == nil {
		t.Error("LoadDocument with salad.Strict(true) accepted an undeclared field")
	}

	_, err = LoadFile(t.Context(), fixturePath("plain_tool.cwl"), salad.Strict(true))
	if err != nil {
		t.Errorf("LoadFile with salad.Strict(true) rejected a valid document: %v", err)
	}
}

// fixturePath locates a fixture under testdata/decode.
func fixturePath(name string) string {
	return filepath.Join("testdata", "decode", name)
}

// fixtureSource reads a fixture's bytes.
func fixtureSource(t *testing.T, name string) []byte {
	t.Helper()

	src, err := os.ReadFile(fixturePath(name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}

	return src
}

func BenchmarkSchemaLoadAndFlatten(b *testing.B) {
	// Deliberately bypasses the sync.OnceValues memoization: the cost worth
	// measuring is the one paid once, at first use, by every entry point that
	// validates a document.
	for b.Loop() {
		_, err := loadEmbeddedSchema(schemaSetV12)
		if err != nil {
			b.Fatalf("loading the embedded schema: %v", err)
		}
	}
}
