package salad

import (
	"strings"
	"testing"
)

// idmapSchema declares one identifier-map field with a mapPredicate and one
// without, so that both halves of the rule can be exercised.
const idmapSchema = `
$graph:
  - name: Thing
    type: record
    documentRoot: true
    fields:
      - name: inputs
        type: string
        jsonldPredicate:
          mapSubject: id
          mapPredicate: type
      - name: steps
        type: string
        jsonldPredicate:
          mapSubject: id
`

// resolveIdmap resolves a document against the identifier-map schema.
func resolveIdmap(t *testing.T, body string) (*Document, error) {
	t.Helper()

	loader := NewLoader(WithContext(schemaContext(t, idmapSchema)), WithSkipLinkCheck(true))

	return loader.LoadNode(mustParse(t, testFile, body), "http://example.com/doc")
}

func TestMustEntryOnAMissingKey(t *testing.T) {
	t.Parallel()

	m := NewMapNode(SourceLine{}, entries("a", "1"))

	got := mustEntry(m, "missing")
	if !IsNull(got) {
		t.Errorf("mustEntry on a missing key = %v, want the null scalar", got)
	}
}

func TestIdentifierMapExpansion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "object values keep their fields and gain the mapSubject",
			body: "inputs:\n  b:\n    type: int\n  a:\n    type: string\n",
			want: "inputs:\n  - {type: string, id: a}\n  - {type: int, id: b}\n",
		},
		{
			name: "a non-object value is assigned to the mapPredicate",
			body: "inputs:\n  a: string\n",
			want: "inputs:\n  - {type: string, id: a}\n",
		},
		{
			name: "a list value is assigned to the mapPredicate too",
			body: "inputs:\n  a: [null, string]\n",
			want: "inputs:\n  - {type: [null, string], id: a}\n",
		},
		{
			name: "a value that is already a list is left alone",
			body: "inputs:\n  - {id: a, type: string}\n",
			want: "inputs:\n  - {id: a, type: string}\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			doc, err := resolveIdmap(t, tc.body)
			if err != nil {
				t.Fatalf("LoadNode: %v", err)
			}

			if !nodeEqual(doc.Root, mustParse(t, testFile, tc.want)) {
				t.Errorf("resolved = %s, want %s", canonicalKey(doc.Root), tc.want)
			}
		})
	}
}

func TestIdentifierMapKeysAreVisitedInSortedOrder(t *testing.T) {
	t.Parallel()

	doc, err := resolveIdmap(t, "inputs:\n  zeta: int\n  alpha: int\n  mid: int\n")
	if err != nil {
		t.Fatalf("LoadNode: %v", err)
	}

	seq := mustSeq(t, mustGet(t, mustMap(t, doc.Root), fieldInputs))

	for i, want := range []string{"alpha", "mid", "zeta"} {
		got, _ := AsString(mustGet(t, mustMap(t, seq.At(i)), "id"))
		if got != want {
			t.Errorf("item %d id = %q, want %q", i, got, want)
		}
	}
}

func TestIdentifierMapErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{
			name:    "a non-object value with no mapPredicate",
			body:    "steps:\n  a: oops\n",
			wantMsg: "declares no mapPredicate",
		},
		{
			name:    "a value that already carries the mapSubject",
			body:    "inputs:\n  a:\n    id: b\n",
			wantMsg: "already has a \"id\" field",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := resolveIdmap(t, tc.body)
			if err == nil || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error = %v, want one mentioning %q", err, tc.wantMsg)
			}
		})
	}
}
