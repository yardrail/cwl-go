package salad

import (
	"os"
	"path/filepath"
	"testing"
)

// exampleDir holds the seven preprocessing examples from the Schema Salad
// specification, vendored verbatim.
const exampleDir = "testdata/examples"

// examplePath names a file in the vendored example directory.
func examplePath(name string) string {
	return filepath.Join(exampleDir, name)
}

// exampleContext loads <name>_schema.yml and derives the context that instance
// documents of that schema resolve against.
func exampleContext(t *testing.T, name string) *Context {
	t.Helper()

	loader := NewLoader(WithContext(saladBootstrapContext()), WithSkipLinkCheck(true))

	doc, err := loader.Load(examplePath(name + "_schema.yml"))
	if err != nil {
		t.Fatalf("loading %s_schema.yml: %v", name, err)
	}

	ctx, err := BuildContext(doc.Root, doc.Metadata)
	if err != nil {
		t.Fatalf("building the context for %s: %v", name, err)
	}

	return ctx
}

// readFixture parses a fixture without resolving it, which is how the expected
// output of an example is read.
func readFixture(t *testing.T, path string) Node {
	t.Helper()

	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	node, err := Parse(path, src)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	return node
}

// TestSpecExamples runs the specification's preprocessing examples: load the
// example's schema, resolve its source document, and compare the result with the
// document the specification says preprocessing must produce.
func TestSpecExamples(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		expected string
	}{
		{name: "field_name", expected: examplePath("field_name_proc.yml")},
		{name: "ident_res", expected: examplePath("ident_res_proc.yml")},
		{name: "link_res", expected: examplePath("link_res_proc.yml")},
		{name: "vocab_res", expected: examplePath("vocab_res_proc.yml")},
		{name: "map_res", expected: examplePath("map_res_proc.yml")},
		{name: "typedsl_res", expected: examplePath("typedsl_res_proc.yml")},
		// The vendored sfdsl_res_proc.yml is not well-formed: every one of its
		// four objects is missing a closing brace. Upstream never parses it —
		// its secondary-file DSL test is written with inline literals and the
		// fixture only feeds the specification's prose — so the expectation is
		// kept here as a repaired copy.
		{name: "sfdsl_res", expected: "testdata/sfdsl_res_proc.yml"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := exampleContext(t, tc.name)
			loader := NewLoader(WithContext(ctx), WithSkipLinkCheck(true))

			doc, err := loader.Load(examplePath(tc.name + "_src.yml"))
			if err != nil {
				t.Fatalf("resolving %s_src.yml: %v", tc.name, err)
			}

			want := readFixture(t, tc.expected)
			if !nodeEqual(doc.Root, want) {
				t.Errorf("resolved document does not match %s\n got: %s\nwant: %s",
					tc.expected, canonicalKey(doc.Root), canonicalKey(want))
			}
		})
	}
}
