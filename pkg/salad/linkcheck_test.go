package salad

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// refFieldSchema declares the four kinds of reference field link validation
// distinguishes: a plain link, a scoped reference, an identity assertion, and a
// field traversal must stop at.
const refFieldSchema = `
$graph:
  - name: Thing
    type: record
    documentRoot: true
    fields:
      - name: id
        type: string
        jsonldPredicate: "@id"
      - name: link
        type: string
        jsonldPredicate:
          _type: "@id"
      - name: ref
        type: string
        jsonldPredicate:
          _type: "@id"
          refScope: 1
      - name: assertion
        type: string
        jsonldPredicate:
          _type: "@id"
          identity: true
      - name: opaque
        type: Any
        jsonldPredicate:
          noLinkCheck: true
`

// docHeader opens a document based at testBaseURI with an identified root.
const docHeader = "$base: " + testBaseURI + "\nid: root\n"

// loadChecked resolves a document with link validation enabled.
func loadChecked(t *testing.T, body string) (*Document, error) {
	t.Helper()

	loader := NewLoader(
		WithFetcher(newMemFetcher(map[string]string{docSimple: body})),
		WithContext(schemaContext(t, refFieldSchema)),
	)

	return loader.Load(docSimple)
}

func TestRefScopeSearchesParentScopesUpToTopLevel(t *testing.T) {
	t.Parallel()

	doc, err := loadChecked(t, `
$base: http://example.com/base
outer:
  id: foo
  inner:
    id: bar
    ref: target
extra:
  id: target
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	outer := mustMap(t, mustGet(t, mustMap(t, doc.Root), "outer"))
	inner := mustMap(t, mustGet(t, outer, "inner"))

	got, _ := AsString(mustGet(t, inner, "ref"))
	if got != testBaseURI+"#target" {
		t.Errorf("ref = %q, want the top-level scope to be the last one searched", got)
	}
}

func TestRefScopeReportsWhatItTried(t *testing.T) {
	t.Parallel()

	_, err := loadChecked(t, `
$base: http://example.com/base
outer:
  id: foo
  inner:
    id: bar
    ref: nowhere
`)
	if err == nil {
		t.Fatal("an unresolvable scoped reference must be an error")
	}

	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("error is %T, want *salad.Error", err)
	}

	for _, want := range []string{"#foo/nowhere", "#nowhere"} {
		if !strings.Contains(se.Pretty(), want) {
			t.Errorf("error =\n%s\nwant it to report trying %s", se.Pretty(), want)
		}
	}
}

func TestLinkValidationRules(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name:    "a link to an object in the document resolves",
			body:    docHeader + "link: \"#other\"\nextra:\n  id: \"#other\"\n",
			wantErr: false,
		},
		{
			name:    "a link to nothing is an error",
			body:    docHeader + "link: nowhere\n",
			wantErr: true,
		},
		{
			name:    "an identity assertion need not exist",
			body:    docHeader + "assertion: nowhere\n",
			wantErr: false,
		},
		{
			name:    "traversal stops at a noLinkCheck field",
			body:    docHeader + "opaque:\n  link: nowhere\n",
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadChecked(t, tc.body)
			if (err != nil) != tc.wantErr {
				t.Errorf("error = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestSkipLinkCheckOptsOut(t *testing.T) {
	t.Parallel()

	loader := NewLoader(
		WithFetcher(newMemFetcher(map[string]string{
			docSimple: "$base: " + testBaseURI + "\nid: root\nlink: nowhere\n",
		})),
		WithContext(schemaContext(t, refFieldSchema)),
		WithSkipLinkCheck(true),
	)

	_, err := loader.Load(docSimple)
	if err != nil {
		t.Errorf("WithSkipLinkCheck must suppress link validation, got %v", err)
	}
}

func TestLinkToAnExistingResourceResolves(t *testing.T) {
	t.Parallel()

	loader := NewLoader(
		WithFetcher(newMemFetcher(map[string]string{
			docSimple:   "id: root\nlink: other.yml\n",
			"other.yml": "id: other\n",
		})),
		WithContext(schemaContext(t, refFieldSchema)),
	)

	_, err := loader.Load(docSimple)
	if err != nil {
		t.Errorf("a link to a fetchable document must validate, got %v", err)
	}
}

func TestLinkListsAreCheckedItemByItem(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name:    "every item of a list of links must resolve",
			body:    docHeader + "link: [\"#a\", \"#b\"]\nextra:\n  - id: \"#a\"\n  - id: \"#b\"\n",
			wantErr: false,
		},
		{
			name:    "one dangling item in a list is enough",
			body:    docHeader + "link: [\"#a\", nowhere]\nextra:\n  - id: \"#a\"\n",
			wantErr: true,
		},
		{
			name:    "a non-string item is traversed rather than checked",
			body:    docHeader + "link:\n  - id: \"#a\"\n",
			wantErr: false,
		},
		{
			name:    "a link field holding an object is traversed",
			body:    docHeader + "link:\n  id: \"#a\"\n",
			wantErr: false,
		},
		{
			name:    "an empty reference is not a link",
			body:    docHeader + "link: \"\"\n",
			wantErr: false,
		},
		{
			name:    "a template expression is not a link",
			body:    docHeader + "link: $(inputs.other)\n",
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadChecked(t, tc.body)
			if (err != nil) != tc.wantErr {
				t.Errorf("error = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

// writeLinkDoc lays out a directory holding a document, a file and a
// subdirectory, and returns the path of the document.
func writeLinkDoc(t *testing.T, link string) string {
	t.Helper()

	dir := t.TempDir()

	mkErr := os.Mkdir(filepath.Join(dir, "subdir"), 0o750)
	if mkErr != nil {
		t.Fatalf("creating the subdirectory: %v", mkErr)
	}

	siblingErr := os.WriteFile(filepath.Join(dir, "sibling.txt"), []byte("x"), 0o600)
	if siblingErr != nil {
		t.Fatalf("writing the sibling file: %v", siblingErr)
	}

	path := filepath.Join(dir, docSimple)

	docErr := os.WriteFile(path, []byte("id: root\nlink: "+link+"\n"), 0o600)
	if docErr != nil {
		t.Fatalf("writing the document: %v", docErr)
	}

	return path
}

func TestLinkTargetsOnTheFileSystem(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		link    string
		wantErr bool
	}{
		{name: "a sibling file", link: "sibling.txt", wantErr: false},
		{name: "a directory", link: "subdir", wantErr: false},
		{name: "a directory named with a trailing slash", link: "subdir/", wantErr: false},
		{name: "nothing at all", link: "nosuchthing", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			loader := NewLoader(
				WithFetcher(NewDefaultFetcher(WithCacheDir(""))),
				WithContext(schemaContext(t, refFieldSchema)),
			)

			_, err := loader.Load(writeLinkDoc(t, tc.link))
			if (err != nil) != tc.wantErr {
				t.Errorf("error = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}
