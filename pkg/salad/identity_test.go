package salad

import (
	"strings"
	"testing"
)

// identityLinkSchema models the shape that makes an identity link a
// *declaration* rather than a reference: a field whose jsonldPredicate is
// _type: "@id" with identity: true, whose values may be written either as bare
// strings or as objects carrying their own identifier.
//
// It is the generic form of the pattern a workflow vocabulary uses for a step's
// named outputs, expressed without any of that vocabulary's terms.
const identityLinkSchema = `
$namespaces:
  ex: http://example.com/ex#
$graph:
  - name: Emitter
    type: record
    documentRoot: true
    fields:
      - name: id
        type: string
        jsonldPredicate: "@id"
      - name: emits
        type: string
        jsonldPredicate:
          _id: "http://example.com/v#emits"
          _type: "@id"
          identity: true
      - name: consumes
        type: string
        jsonldPredicate:
          _id: "http://example.com/v#consumes"
          _type: "@id"
          refScope: 0
      - name: kind
        type: string
        jsonldPredicate:
          _id: "http://example.com/v#kind"
          _type: "@vocab"
      - name: nodes
        type: string
        jsonldPredicate:
          _id: "http://example.com/v#nodes"
          mapSubject: id
`

// wfBase is the base URI the identity-link documents resolve against.
const wfBase = "http://example.com/wf"

// loadIdentityDoc resolves a document against identityLinkSchema with link
// validation on, which is what makes a missing declaration observable.
func loadIdentityDoc(t *testing.T, body string) (*Document, error) {
	t.Helper()

	loader := NewLoader(
		WithFetcher(newMemFetcher(map[string]string{docSimple: "$base: " + wfBase + "\n" + body})),
		WithContext(schemaContext(t, identityLinkSchema)),
	)

	return loader.Load(docSimple)
}

// emitterDoc builds a two-node document whose sink refers to a node's output.
func emitterDoc(emits, consumes string) string {
	return "nodes:\n" +
		"  step_one:\n    emits: " + emits + "\n" +
		"  step_two:\n    emits: [out]\n" +
		"sinks:\n  - id: final\n    consumes: " + consumes + "\n"
}

func TestBareIdentityLinkDeclaresAnIdentifier(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		emits string
	}{
		{name: "bare string form", emits: "[out]"},
		{name: "object form", emits: "[{id: out}]"},
		{name: "mixed forms in one list", emits: "[out, {id: other}]"},
		{name: "a lone bare string", emits: "out"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			doc, err := loadIdentityDoc(t, emitterDoc(tc.emits, "step_one/out"))
			if err != nil {
				t.Fatalf("resolving %s: %v", tc.emits, err)
			}

			sink := mustMap(t, mustSeq(t, mustGet(t, mustMap(t, doc.Root), "sinks")).At(0))

			got, _ := AsString(mustGet(t, sink, "consumes"))
			if got != wfBase+"#step_one/out" {
				t.Errorf("consumes = %q, want the declared identifier %s#step_one/out", got, wfBase)
			}
		})
	}
}

func TestBareIdentityLinkIsExpandedInPlace(t *testing.T) {
	t.Parallel()

	doc, err := loadIdentityDoc(t, emitterDoc("[out, {id: other}]", "step_one/other"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	nodes := mustSeq(t, mustGet(t, mustMap(t, doc.Root), "nodes"))
	emits := mustSeq(t, mustGet(t, mustMap(t, nodes.At(0)), "emits"))

	bare, _ := AsString(emits.At(0))
	if bare != wfBase+"#step_one/out" {
		t.Errorf("the bare form resolved to %q, want it scoped to its node", bare)
	}

	object := mustMap(t, emits.At(1))
	if id, _ := AsString(mustGet(t, object, "id")); id != wfBase+"#step_one/other" {
		t.Errorf("the object form resolved to %q, want it scoped to its node", id)
	}
}

func TestIdentityLinksAreScopedToTheirObject(t *testing.T) {
	t.Parallel()

	doc, err := loadIdentityDoc(t, emitterDoc("[out]", "step_two/out"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	sink := mustMap(t, mustSeq(t, mustGet(t, mustMap(t, doc.Root), "sinks")).At(0))

	got, _ := AsString(mustGet(t, sink, "consumes"))
	if got != wfBase+"#step_two/out" {
		t.Errorf("consumes = %q, want the second node's own out, not the first's", got)
	}

	nodes := mustSeq(t, mustGet(t, mustMap(t, doc.Root), "nodes"))

	first, _ := AsString(mustSeq(t, mustGet(t, mustMap(t, nodes.At(0)), "emits")).At(0))
	second, _ := AsString(mustSeq(t, mustGet(t, mustMap(t, nodes.At(1)), "emits")).At(0))

	if first == second {
		t.Errorf("both nodes declared %q; two objects declaring the same name must not collide", first)
	}
}

func TestDanglingReferenceStillFails(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{name: "an output no node declares", body: emitterDoc("[out]", "step_one/nonexistent")},
		{name: "a node that does not exist", body: emitterDoc("[out]", "step_three/out")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadIdentityDoc(t, tc.body)
			if err == nil {
				t.Fatal("a reference to an identifier nothing declares must still be an error")
			}

			if !strings.Contains(err.Error(), "unresolved links") {
				t.Errorf("error = %v, want a link error", err)
			}
		})
	}
}

func TestDeclaredNamespaceIRIPassesLinkChecking(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name:    "a prefix the document declares",
			body:    "$namespaces:\n  own: http://example.com/own#\nid: root\nkind: own:Magic\n",
			wantErr: false,
		},
		{
			name:    "a prefix the schema declares",
			body:    "id: root\nkind: ex:Magic\n",
			wantErr: false,
		},
		{
			name:    "the same IRI spelled out in full",
			body:    "id: root\nkind: http://example.com/ex#Magic\n",
			wantErr: false,
		},
		{
			name:    "a prefix nobody declares",
			body:    "id: root\nkind: nope:Magic\n",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadIdentityDoc(t, tc.body)
			if (err != nil) != tc.wantErr {
				t.Errorf("error = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestDeclaredNamespaceInsideAGraphIsRemembered(t *testing.T) {
	t.Parallel()

	body := "$namespaces:\n  own: http://example.com/own#\n$graph:\n  - id: root\n    kind: own:Magic\n"

	_, err := loadIdentityDoc(t, body)
	if err != nil {
		t.Errorf("a namespace declared beside a $graph must still be honoured, got %v", err)
	}
}
