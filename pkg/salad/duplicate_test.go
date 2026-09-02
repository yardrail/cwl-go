package salad

import (
	"errors"
	"strings"
	"testing"
)

// scopedNameSchema models a schema that gives two sibling fields the same
// identifier scope: neither declares a subscope, so an object under one and an
// object under the other resolve to the same absolute URI when they share a
// short name.
//
// It is the generic form of the shape CWL v1.2 produces for a process whose
// input and output share a name, expressed without that vocabulary's terms.
const scopedNameSchema = `
$graph:
  - name: Port
    type: record
    fields:
      - name: id
        type: string
        jsonldPredicate: "@id"
  - name: Holder
    type: record
    documentRoot: true
    fields:
      - name: id
        type: string
        jsonldPredicate: "@id"
      - name: takes
        type: Port[]
        jsonldPredicate:
          _id: "http://example.com/v#takes"
          mapSubject: id
      - name: gives
        type: Port[]
        jsonldPredicate:
          _id: "http://example.com/v#gives"
          mapSubject: id
      - name: uses
        type: string
        jsonldPredicate:
          _id: "http://example.com/v#uses"
          _type: "@id"
          refScope: 0
`

// loadScopedNames resolves a document against scopedNameSchema.
func loadScopedNames(t *testing.T, body string) (*Document, error) {
	t.Helper()

	loader := NewLoader(
		WithFetcher(newMemFetcher(map[string]string{docSimple: "$base: " + wfBase + "\n" + body})),
		WithContext(schemaContext(t, scopedNameSchema)),
	)

	return loader.Load(docSimple)
}

func TestOneNameSharedByTwoFieldsIsNotADuplicate(t *testing.T) {
	t.Parallel()

	doc, err := loadScopedNames(t, "takes:\n  shared: {}\ngives:\n  shared: {}\n")
	if err != nil {
		t.Fatalf("two sibling fields sharing an identifier scope must not collide: %v", err)
	}

	takes := mustSeq(t, mustGet(t, mustMap(t, doc.Root), "takes"))

	id, _ := AsString(mustGet(t, mustMap(t, takes.At(0)), "id"))
	if id != wfBase+"#shared" {
		t.Errorf("id = %q, want the URI the schema resolves it to, unchanged", id)
	}
}

func TestASharedNameStillResolvesReferences(t *testing.T) {
	t.Parallel()

	doc, err := loadScopedNames(t, "takes:\n  shared: {}\ngives:\n  shared: {}\nuses: shared\n")
	if err != nil {
		if se, ok := errors.AsType[*Error](err); ok {
			t.Fatalf("Load:\n%s", se.Pretty())
		}

		t.Fatalf("Load: %v", err)
	}

	got, _ := AsString(mustGet(t, mustMap(t, doc.Root), "uses"))
	if got != wfBase+"#shared" {
		t.Errorf("uses = %q, want it to resolve to the shared URI", got)
	}
}

func TestTwoObjectsInOneFieldAreStillADuplicate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{name: "two entries of one list", body: "takes:\n  - id: same\n  - id: same\n"},
		{name: "two entries of the other list", body: "gives:\n  - id: same\n  - id: same\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadScopedNames(t, tc.body)
			if err == nil {
				t.Fatal("two objects in one field claiming one URI must still be an error")
			}

			if !strings.Contains(err.Error(), "duplicate identifier") {
				t.Errorf("error = %v, want a duplicate identifier error", err)
			}
		})
	}
}

func TestTwoTopLevelObjectsAreStillADuplicate(t *testing.T) {
	t.Parallel()

	docs := map[string]string{docSimple: "$graph:\n  - id: same\n  - id: same\n"}

	loader := NewLoader(
		WithFetcher(newMemFetcher(docs)),
		WithContext(schemaContext(t, scopedNameSchema)),
		WithSkipLinkCheck(true),
	)

	_, err := loader.Load(docSimple)
	if err == nil || !strings.Contains(err.Error(), "duplicate identifier") {
		t.Errorf("error = %v, want two $graph entries claiming one URI to be an error", err)
	}
}

func TestAnObjectSupersedesAnAssertedIdentifier(t *testing.T) {
	t.Parallel()

	doc, err := loadIdentityDoc(t, emitterDoc("[out, {id: out}]", "step_one/out"))
	if err != nil {
		t.Fatalf("an asserted identifier and an object may name one URI: %v", err)
	}

	target, ok := AsMap(resolverIndexEntry(t, doc, wfBase+"#step_one/out"))
	if !ok || target == nil {
		t.Error("the object form must supersede the bare assertion in the identifier index")
	}
}

// resolverIndexEntry re-resolves a document and returns what its identifier
// index holds for id, which is what an $import of that fragment would yield.
func resolverIndexEntry(t *testing.T, doc *Document, id string) Node {
	t.Helper()

	loader := NewLoader(
		WithFetcher(newMemFetcher(map[string]string{docSimple: "x"})),
		WithContext(schemaContext(t, identityLinkSchema)),
		WithSkipLinkCheck(true),
	)

	r := loader.newResolver()

	_, err := r.resolve(doc.Root, scope{ctx: loader.Context(), base: wfBase, fileBase: wfBase})
	if err != nil {
		t.Fatalf("re-resolving: %v", err)
	}

	return r.idx[id]
}
