package salad

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

// memPrefix is the synthetic directory the in-memory test documents live in.
const memPrefix = "file:///mem/"

// memFetcher serves documents from a map, so that loader tests are hermetic and
// exercise the injectable Fetcher seam at the same time.
type memFetcher struct {
	docs map[string]string
}

var _ Fetcher = memFetcher{}

// newMemFetcher builds a fetcher over documents named relative to memPrefix.
func newMemFetcher(docs map[string]string) memFetcher {
	out := make(map[string]string, len(docs))
	for name, body := range docs {
		out[memPrefix+name] = body
	}

	return memFetcher{docs: out}
}

func (f memFetcher) FetchText(docURL string) ([]byte, error) {
	body, ok := f.docs[dropFragment(docURL)]
	if !ok {
		return nil, Errorf(SourceLine{File: docURL}, "no such document %s", docURL)
	}

	return []byte(body), nil
}

func (f memFetcher) Exists(docURL string) bool {
	_, ok := f.docs[dropFragment(docURL)]

	return ok
}

func (f memFetcher) Normalize(base, ref string) (string, error) {
	if base == "" {
		base = memPrefix
	}

	return normalizeURL(base, ref)
}

// schemaContext builds a Context straight from an unresolved schema document,
// which is enough for the small schemas the loader tests declare.
func schemaContext(t *testing.T, src string) *Context {
	t.Helper()

	node := mustParse(t, "schema.yml", src)

	meta, _ := AsMap(node)

	ctx, err := BuildContext(node, meta)
	if err != nil {
		t.Fatalf("BuildContext failed: %v", err)
	}

	return ctx
}

// identSchema declares a single record whose id field is the object identifier.
const identSchema = `
$graph:
  - name: Thing
    type: record
    documentRoot: true
    fields:
      - name: id
        type: string
        jsonldPredicate: "@id"
`

// loadMem resolves name out of an in-memory document set.
func loadMem(t *testing.T, ctx *Context, docs map[string]string, name string) (*Document, error) {
	t.Helper()

	loader := NewLoader(
		WithFetcher(newMemFetcher(docs)),
		WithContext(ctx),
		WithSkipLinkCheck(true),
	)

	return loader.Load(name)
}

func TestImportSplicesResolvedSubtrees(t *testing.T) {
	t.Parallel()

	docs := map[string]string{
		"root.yml":   "id: root\nchild:\n  $import: child.yml\n",
		docChild:     "id: kid\nnested:\n  id: grandkid\n",
		"branch.yml": "- id: one\n- id: two\n",
		"list.yml":   "$graph:\n  - $import: branch.yml\n  - id: three\n",
	}
	ctx := schemaContext(t, identSchema)

	doc, err := loadMem(t, ctx, docs, "root.yml")
	if err != nil {
		t.Fatalf("Load(root.yml): %v", err)
	}

	child := mustMap(t, mustGet(t, mustMap(t, doc.Root), "child"))
	if id, _ := AsString(mustGet(t, child, "id")); id != memPrefix+docChild+"#kid" {
		t.Errorf("imported id = %q, want the identifier scoped to the imported document", id)
	}

	if child.Loc().File != memPrefix+docChild {
		t.Errorf("spliced node reports file %q, want the imported document", child.Loc().File)
	}
}

func TestImportIntoSequenceIsFlattened(t *testing.T) {
	t.Parallel()

	docs := map[string]string{
		"branch.yml": "- id: one\n- id: two\n",
		"list.yml":   "$graph:\n  - $import: branch.yml\n  - id: three\n",
	}

	doc, err := loadMem(t, schemaContext(t, identSchema), docs, "list.yml")
	if err != nil {
		t.Fatalf("Load(list.yml): %v", err)
	}

	seq := mustSeq(t, doc.Root)
	if seq.Len() != 3 {
		t.Fatalf("sequence has %d items, want the imported sequence flattened into it", seq.Len())
	}

	for i, want := range []string{testOne, testTwo, "three"} {
		item := mustMap(t, seq.At(i))
		if id, _ := AsString(mustGet(t, item, "id")); !strings.HasSuffix(id, "#"+want) {
			t.Errorf("item %d id = %q, want one ending in %q", i, id, want)
		}
	}
}

func TestImportOfDocumentFragment(t *testing.T) {
	t.Parallel()

	docs := map[string]string{
		"lib.yml": "$graph:\n  - id: alpha\n  - id: beta\n",
		docMain:   "id: main\npicked:\n  $import: lib.yml#beta\n",
	}

	doc, err := loadMem(t, schemaContext(t, identSchema), docs, docMain)
	if err != nil {
		t.Fatalf("Load(%s): %v", docMain, err)
	}

	picked := mustMap(t, mustGet(t, mustMap(t, doc.Root), "picked"))
	if id, _ := AsString(mustGet(t, picked, "id")); id != memPrefix+"lib.yml#beta" {
		t.Errorf("fragment import selected %q, want the object named by the fragment", id)
	}
}

func TestIncludeIsRawTextAndNeverParsed(t *testing.T) {
	t.Parallel()

	raw := "key: value\n- this is not a list item\n"
	docs := map[string]string{
		docMain:     "id: main\ntext:\n  $include: notes.txt\n",
		"notes.txt": raw,
	}

	doc, err := loadMem(t, schemaContext(t, identSchema), docs, docMain)
	if err != nil {
		t.Fatalf("Load(%s): %v", docMain, err)
	}

	text, ok := AsString(mustGet(t, mustMap(t, doc.Root), "text"))
	if !ok {
		t.Fatalf("$include produced a %s, want a string scalar", NodeKind(mustGet(t, mustMap(t, doc.Root), "text")))
	}

	if text != raw {
		t.Errorf("$include text = %q, want the file's bytes verbatim", text)
	}
}

func TestImportCycleIsReported(t *testing.T) {
	t.Parallel()

	docs := map[string]string{
		"a.yml": "$graph:\n  - $import: b.yml\n",
		"b.yml": "$graph:\n  - $import: a.yml\n",
	}

	_, err := loadMem(t, schemaContext(t, identSchema), docs, "a.yml")
	if err == nil {
		t.Fatal("an import cycle must be an error")
	}

	if !strings.Contains(err.Error(), "$import cycle") {
		t.Errorf("error = %v, want it to name the cycle", err)
	}

	for _, want := range []string{"a.yml", "b.yml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to name %s", err, want)
		}
	}
}

func TestDuplicateAbsoluteIdentifierIsReported(t *testing.T) {
	t.Parallel()

	docs := map[string]string{"dup.yml": "$graph:\n  - id: same\n  - id: same\n"}

	_, err := loadMem(t, schemaContext(t, identSchema), docs, "dup.yml")
	if err == nil {
		t.Fatal("two objects with the same absolute URI must be an error")
	}

	if !strings.Contains(err.Error(), "duplicate identifier") {
		t.Errorf("error = %v, want a duplicate identifier error", err)
	}
}

func TestErrorInImportedDocumentPointsAtIt(t *testing.T) {
	t.Parallel()

	docs := map[string]string{
		docMain:      "id: main\nbroken:\n  $import: broken.yml\n",
		"broken.yml": "# a comment\nid:\n  not: a string\n",
	}

	_, err := loadMem(t, schemaContext(t, identSchema), docs, docMain)
	if err == nil {
		t.Fatal("an identifier that is not a string must be an error")
	}

	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("error is %T, want *salad.Error", err)
	}

	leaf := se.Leaves()[0]
	if leaf.Loc.File != memPrefix+"broken.yml" {
		t.Errorf("error points at %s, want the imported document", leaf.Loc)
	}

	if leaf.Loc.Start.Line != 3 {
		t.Errorf("error is at line %d, want line 3 of the imported document", leaf.Loc.Start.Line)
	}
}

func TestImportMustBeTheOnlyField(t *testing.T) {
	t.Parallel()

	docs := map[string]string{
		docMain:  "id: main\nchild:\n  $import: child.yml\n  extra: nope\n",
		docChild: "id: kid\n",
	}

	_, err := loadMem(t, schemaContext(t, identSchema), docs, docMain)
	if err == nil || !strings.Contains(err.Error(), "only field") {
		t.Errorf("error = %v, want a complaint that $import must stand alone", err)
	}
}

func TestExplicitContextDirectives(t *testing.T) {
	t.Parallel()

	docs := map[string]string{
		"main.yml": "$base: http://example.com/base\n" +
			"$namespaces:\n  ex: http://example.com/ns#\n" +
			"$schemas:\n  - http://example.com/onto.xml\n" +
			"$unknown: ignored\n" +
			"$graph:\n  - id: thing\n",
	}

	doc, err := loadMem(t, schemaContext(t, identSchema), docs, docMain)
	if err != nil {
		t.Fatalf("Load(%s): %v", docMain, err)
	}

	if doc.Metadata == nil || !doc.Metadata.Has(dirBase) || !doc.Metadata.Has("$unknown") {
		t.Fatalf("metadata = %v, want the document's directives", doc.Metadata)
	}

	if doc.Metadata.Has(dirGraph) {
		t.Error("$graph must not appear in the document metadata")
	}

	thing := mustMap(t, mustSeq(t, doc.Root).At(0))
	if id, _ := AsString(mustGet(t, thing, "id")); id != "http://example.com/base#thing" {
		t.Errorf("id = %q, want it resolved against $base", id)
	}
}

func TestLoadNodeResolvesInMemoryDocuments(t *testing.T) {
	t.Parallel()

	loader := NewLoader(WithContext(schemaContext(t, identSchema)), WithSkipLinkCheck(true))
	node := mustParse(t, "inline.yml", "id: thing\nchild:\n  id: kid\n")

	doc, err := loader.LoadNode(node, "http://example.com/doc")
	if err != nil {
		t.Fatalf("LoadNode: %v", err)
	}

	child := mustMap(t, mustGet(t, mustMap(t, doc.Root), "child"))
	if id, _ := AsString(mustGet(t, child, "id")); id != "http://example.com/doc#thing/kid" {
		t.Errorf("nested id = %q, want it scoped under its parent", id)
	}
}

func TestLoaderIsConcurrencySafe(t *testing.T) {
	t.Parallel()

	docs := map[string]string{
		"root.yml": "id: root\nchild:\n  $import: child.yml\n",
		docChild:   "id: kid\n",
	}
	loader := NewLoader(
		WithFetcher(newMemFetcher(docs)),
		WithContext(schemaContext(t, identSchema)),
		WithSkipLinkCheck(true),
	)

	var wg sync.WaitGroup

	for range 8 {
		wg.Go(func() {
			_, err := loader.Load("root.yml")
			if err != nil {
				t.Errorf("concurrent Load failed: %v", err)
			}
		})
	}

	wg.Wait()
}

func TestUnimplementedMessage(t *testing.T) {
	t.Parallel()

	msg := unimplemented("salad loader stream", "Symbol", 1, "two")
	for _, want := range []string{"salad loader stream", "Symbol", "not implemented"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not mention %q", msg, want)
		}
	}
}

func TestDirectiveErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		docs    map[string]string
		wantMsg string
	}{
		{
			name:    "a missing root document",
			docs:    make(map[string]string),
			wantMsg: "cannot fetch",
		},
		{
			name:    "an import of a document that is not there",
			docs:    map[string]string{docMain: "id: main\nchild:\n  $import: gone.yml\n"},
			wantMsg: "cannot fetch",
		},
		{
			name: "an import naming a fragment that is not there",
			docs: map[string]string{
				docMain:   "id: main\nchild:\n  $import: lib.yml#gone\n",
				"lib.yml": "id: lib\n",
			},
			wantMsg: "no object with the identifier",
		},
		{
			name:    "an import target that is not a string",
			docs:    map[string]string{docMain: "id: main\nchild:\n  $import: [a]\n"},
			wantMsg: "$import must be a string",
		},
		{
			name:    "an include of a document that is not there",
			docs:    map[string]string{docMain: "id: main\ntext:\n  $include: gone.txt\n"},
			wantMsg: "cannot $include",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadMem(t, schemaContext(t, identSchema), tc.docs, docMain)
			if err == nil || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error = %v, want one mentioning %q", err, tc.wantMsg)
			}
		})
	}
}

func TestAnImportedDocumentIsResolvedOnce(t *testing.T) {
	t.Parallel()

	docs := map[string]string{
		docMain:      "id: main\na:\n  $import: shared.yml\nb:\n  $import: ./shared.yml\n",
		"shared.yml": "id: shared\n",
	}

	doc, err := loadMem(t, schemaContext(t, identSchema), docs, docMain)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	root := mustMap(t, doc.Root)
	if mustGet(t, root, "a") != mustGet(t, root, "b") {
		t.Error("two references to the same document must yield the same resolved node")
	}
}
