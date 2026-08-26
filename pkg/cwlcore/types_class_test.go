package cwlcore

import (
	"slices"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// These tests read the vendored schema under schema/ and check the model
// against it directly, rather than against a transcription of it. A class
// constant that does not match the schema's spelling would not fail anything at
// build time: it would surface much later, as a requirement that silently never
// matches the document that declared it, or as a process class that falls back
// to RawProcess for no visible reason. Checking it here costs one file read.
//
// The schema files parsed here are the same ones embed.go embeds, read out of
// the same embed.FS, so the test cannot drift from what the binary ships.

// schemaFiles are the vendored documents that declare records. The Markdown
// documentation targets declare none, and CommonWorkflowLanguage.yml only
// imports the others. The salad metaschema base is included because CWLType
// extends its PrimitiveType enum, and so declares only File and Directory
// itself.
var schemaFiles = []string{
	"schema/Process.yml",
	"schema/CommandLineTool.yml",
	"schema/Workflow.yml",
	"schema/Operation.yml",
	"schema/salad/schema_salad/metaschema/metaschema_base.yml",
}

// schemaRecords parses the vendored schema files and returns every $graph entry
// that is a mapping, keyed in document order.
func schemaRecords(t *testing.T) []*salad.MapNode {
	t.Helper()

	records := make([]*salad.MapNode, 0, 64)

	for _, path := range schemaFiles {
		src, err := schemaFS.ReadFile(path)
		if err != nil {
			t.Fatalf("reading embedded %s: %v", path, err)
		}

		root, err := salad.Parse(path, src)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}

		records = append(records, graphEntries(t, path, root)...)
	}

	if len(records) == 0 {
		t.Fatal("no records found in the vendored schema")
	}

	return records
}

// graphEntries returns the mapping entries of a schema document's $graph.
func graphEntries(t *testing.T, path string, root salad.Node) []*salad.MapNode {
	t.Helper()

	doc, ok := salad.AsMap(root)
	if !ok {
		t.Fatalf("%s: root is %s, want a mapping", path, salad.NodeKind(root))
	}

	graph, ok := doc.Get("$graph")
	if !ok {
		t.Fatalf("%s: no $graph", path)
	}

	seq, ok := salad.AsSeq(graph)
	if !ok {
		t.Fatalf("%s: $graph is %s, want a sequence", path, salad.NodeKind(graph))
	}

	out := make([]*salad.MapNode, 0, seq.Len())

	for _, item := range seq.Items() {
		if rec, isMap := salad.AsMap(item); isMap {
			out = append(out, rec)
		}
	}

	return out
}

// localName strips any namespace prefix or IRI fragment path from a schema
// name, so that "cwl:DockerRequirement", "#DockerRequirement" and
// "DockerRequirement" all reduce to the short spelling the model uses.
func localName(name string) string {
	if i := strings.LastIndexAny(name, "#/"); i >= 0 {
		name = name[i+1:]
	}

	if i := strings.LastIndex(name, ":"); i >= 0 {
		name = name[i+1:]
	}

	return name
}

// extendsNames returns the local names a record extends. The schema writes
// extends either as a single string or as a sequence of them.
func extendsNames(rec *salad.MapNode) []string {
	value, ok := rec.Get("extends")
	if !ok {
		return nil
	}

	if name, isString := salad.AsString(value); isString {
		return []string{localName(name)}
	}

	seq, isSeq := salad.AsSeq(value)
	if !isSeq {
		return nil
	}

	names := make([]string, 0, seq.Len())

	for _, item := range seq.Items() {
		if name, isString := salad.AsString(item); isString {
			names = append(names, localName(name))
		}
	}

	return names
}

// extendsRecord reports whether rec extends the named record directly.
func extendsRecord(rec *salad.MapNode, name string) bool {
	return slices.Contains(extendsNames(rec), name)
}

// recordName returns a $graph entry's local name, and whether it has one.
func recordName(rec *salad.MapNode) (string, bool) {
	value, ok := rec.Get("name")
	if !ok {
		return "", false
	}

	name, ok := salad.AsString(value)
	if !ok {
		return "", false
	}

	return localName(name), true
}

// namesExtending collects the local names of every record extending parent.
func namesExtending(t *testing.T, parent string) []string {
	t.Helper()

	var names []string

	for _, rec := range schemaRecords(t) {
		if !extendsRecord(rec, parent) {
			continue
		}

		if name, ok := recordName(rec); ok {
			names = append(names, name)
		}
	}

	slices.Sort(names)

	return names
}

func TestRequirementClassConstantsMatchSchema(t *testing.T) {
	t.Parallel()

	declared := namesExtending(t, "ProcessRequirement")

	modelled := make([]string, 0, len(allRequirements()))

	for _, req := range allRequirements() {
		if _, isRaw := req.(*RawRequirement); isRaw {
			continue // the fallback has no schema record
		}

		modelled = append(modelled, req.Class())
	}

	slices.Sort(modelled)
	assertSameNames(t, "requirement", declared, modelled)
}

func TestProcessClassConstantsMatchSchema(t *testing.T) {
	t.Parallel()

	declared := namesExtending(t, "Process")

	modelled := make([]string, 0, len(allProcesses()))

	for _, proc := range allProcesses() {
		if _, isRaw := proc.(*RawProcess); isRaw {
			continue // the fallback has no schema record
		}

		modelled = append(modelled, proc.Class())
	}

	slices.Sort(modelled)
	assertSameNames(t, "process class", declared, modelled)
}

// assertSameNames reports every name the schema declares that the model does
// not carry, and vice versa.
func assertSameNames(t *testing.T, what string, declared, modelled []string) {
	t.Helper()

	inModel := make(map[string]bool, len(modelled))
	for _, name := range modelled {
		inModel[name] = true
	}

	for _, name := range declared {
		if !inModel[name] {
			t.Errorf("schema declares %s %q but the model has no type for it", what, name)
		}
	}

	inSchema := make(map[string]bool, len(declared))
	for _, name := range declared {
		inSchema[name] = true
	}

	for _, name := range modelled {
		if !inSchema[name] {
			t.Errorf("the model carries %s %q but the vendored schema declares no such record", what, name)
		}
	}
}

// TestRequirementClassSymbolsMatchSchema checks each requirement's Class
// against the symbol the schema pins its own class field to, which is the value
// a document actually writes. This is a stricter check than the record name:
// the two happen to agree throughout CWL v1.2, and this test is what would
// catch it if a future revision made them differ.
func TestRequirementClassSymbolsMatchSchema(t *testing.T) {
	t.Parallel()

	symbols := requirementClassSymbols(t)

	for _, req := range allRequirements() {
		if _, isRaw := req.(*RawRequirement); isRaw {
			continue
		}

		symbol, ok := symbols[req.Class()]
		if !ok {
			t.Errorf("%T: schema record %q pins no class symbol", req, req.Class())

			continue
		}

		if symbol != req.Class() {
			t.Errorf("%T: Class() = %q but the schema pins class to %q", req, req.Class(), symbol)
		}
	}
}

// requirementClassSymbols maps each requirement record's name to the single
// symbol its class field is pinned to.
func requirementClassSymbols(t *testing.T) map[string]string {
	t.Helper()

	symbols := make(map[string]string, 32)

	for _, rec := range schemaRecords(t) {
		name, ok := recordName(rec)
		if !ok || !extendsRecord(rec, "ProcessRequirement") {
			continue
		}

		if symbol, found := classFieldSymbol(rec); found {
			symbols[name] = symbol
		}
	}

	return symbols
}

// classFieldSymbol returns the single symbol the record's class field is pinned
// to, in its local spelling.
func classFieldSymbol(rec *salad.MapNode) (string, bool) {
	field, ok := classField(rec)
	if !ok {
		return "", false
	}

	typeNode, ok := field.Get("type")
	if !ok {
		return "", false
	}

	typeMap, ok := salad.AsMap(typeNode)
	if !ok {
		return "", false
	}

	symbols, ok := typeMap.Get("symbols")
	if !ok {
		return "", false
	}

	seq, ok := salad.AsSeq(symbols)
	if !ok || seq.Len() != 1 {
		return "", false
	}

	symbol, ok := salad.AsString(seq.At(0))
	if !ok {
		return "", false
	}

	return localName(symbol), true
}

// classField returns the record's class field definition. The schema writes
// fields either as a sequence of {name, type} mappings or as a mapping keyed by
// field name, and both spellings occur in the vendored files.
func classField(rec *salad.MapNode) (*salad.MapNode, bool) {
	fields, ok := rec.Get("fields")
	if !ok {
		return nil, false
	}

	if asMap, isMap := salad.AsMap(fields); isMap {
		field, found := asMap.Get("class")
		if !found {
			return nil, false
		}

		return salad.AsMap(field)
	}

	seq, isSeq := salad.AsSeq(fields)
	if !isSeq {
		return nil, false
	}

	return findNamedField(seq, "class")
}

// findNamedField returns the entry of a sequence-form fields list whose name
// matches.
func findNamedField(seq *salad.SeqNode, name string) (*salad.MapNode, bool) {
	for _, item := range seq.Items() {
		field, ok := salad.AsMap(item)
		if !ok {
			continue
		}

		got, ok := field.Get("name")
		if !ok {
			continue
		}

		if text, isString := salad.AsString(got); isString && text == name {
			return field, true
		}
	}

	return nil, false
}

// recordFieldNames returns the field names a record declares, in document
// order. The schema writes fields either as a sequence of {name, type} mappings
// or as a mapping keyed by field name, and both spellings occur in the vendored
// files — File uses the sequence form, LoadListingRequirement the mapping form.
func recordFieldNames(rec *salad.MapNode) []string {
	fields, ok := rec.Get("fields")
	if !ok {
		return nil
	}

	if asMap, isMap := salad.AsMap(fields); isMap {
		return asMap.Keys()
	}

	seq, isSeq := salad.AsSeq(fields)
	if !isSeq {
		return nil
	}

	names := make([]string, 0, seq.Len())

	for _, item := range seq.Items() {
		field, isFieldMap := salad.AsMap(item)
		if !isFieldMap {
			continue
		}

		value, hasName := field.Get("name")
		if !hasName {
			continue
		}

		if name, isString := salad.AsString(value); isString {
			names = append(names, name)
		}
	}

	return names
}
