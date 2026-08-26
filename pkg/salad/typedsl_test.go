package salad

import "testing"

// dslSchema declares one type-DSL field and one secondary-files-DSL field.
const dslSchema = `
$graph:
  - name: PrimitiveType
    type: enum
    symbols: ["null", "int", "string"]
  - name: Thing
    type: record
    documentRoot: true
    fields:
      - name: extype
        type: string
        jsonldPredicate:
          _type: "@vocab"
          typeDSL: true
      - name: secondaryFiles
        type: string
        jsonldPredicate:
          _type: "@vocab"
          secondaryFilesDSL: true
`

// resolveDSL resolves a one-field document and returns the field's value.
func resolveDSL(t *testing.T, field, value string) Node {
	t.Helper()

	loader := NewLoader(WithContext(schemaContext(t, dslSchema)), WithSkipLinkCheck(true))

	doc, err := loader.LoadNode(mustParse(t, testFile, field+": "+value+"\n"), "http://example.com/doc")
	if err != nil {
		t.Fatalf("LoadNode: %v", err)
	}

	return mustGet(t, mustMap(t, doc.Root), field)
}

func TestTypeDSLExpansion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		value string
		want  string
	}{
		{name: "a plain type is unchanged", value: "string", want: `"string"`},
		{name: "a trailing question mark makes a union with null", value: `"string?"`, want: `["null", "string"]`},
		{name: "trailing brackets make an array", value: `"string[]"`, want: `{type: array, items: string}`},
		{
			name:  "brackets and a question mark make an optional array",
			value: `"string[]?"`,
			want:  `["null", {type: array, items: string}]`,
		},
		{
			name:  "a list is expanded item by item and flattened",
			value: `["int?", "string"]`,
			want:  `["null", "int", "string"]`,
		},
		{name: "repeated members are dropped", value: `["int?", "null"]`, want: `["null", "int"]`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := resolveDSL(t, "extype", tc.value)
			if !nodeEqual(got, mustParse(t, testFile, tc.want)) {
				t.Errorf("extype = %s, want %s", canonicalKey(got), tc.want)
			}
		})
	}
}

func TestTypeDSLRejectsBracketsInTheMiddle(t *testing.T) {
	t.Parallel()

	loader := NewLoader(WithContext(schemaContext(t, dslSchema)), WithSkipLinkCheck(true))

	_, err := loader.LoadNode(mustParse(t, testFile, `extype: "string[]x"`), "http://example.com/doc")
	if err == nil {
		t.Fatal("[] anywhere but at the end must be an error")
	}
}

func TestTypeDSLRejectsAMalformedMemberInAList(t *testing.T) {
	t.Parallel()

	loader := NewLoader(WithContext(schemaContext(t, dslSchema)), WithSkipLinkCheck(true))

	_, err := loader.LoadNode(mustParse(t, testFile, `extype: ["foo[bar", string]`), "http://example.com/doc")
	if err == nil {
		t.Fatal("a malformed member inside a type-DSL list must be an error")
	}
}

func TestExpandTypeNameBareShorthands(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, in, want string }{
		{name: "a bare question mark", in: "?", want: `"?"`},
		{name: "bare brackets", in: "[]", want: `"[]"`},
		{name: "bare brackets and a question mark", in: "[]?", want: `"[]?"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := expandTypeName(tc.in, SourceLine{})
			if err != nil {
				t.Fatalf("expandTypeName(%q): %v", tc.in, err)
			}

			if !nodeEqual(got, mustParse(t, testFile, tc.want)) {
				t.Errorf("expandTypeName(%q) = %s, want %s", tc.in, canonicalKey(got), tc.want)
			}
		})
	}
}

func TestSecondaryFilesDSLExpansion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "a string becomes a pattern with no requirement",
			value: `".bai"`,
			want:  `{pattern: ".bai", required: null}`,
		},
		{
			name:  "a trailing question mark makes it optional",
			value: `".bai?"`,
			want:  `{pattern: ".bai", required: false}`,
		},
		{
			name:  "an object is left alone, question mark and all",
			value: `{pattern: ".bai?"}`,
			want:  `{pattern: ".bai?"}`,
		},
		{
			name:  "a list is expanded item by item",
			value: `[".bai", {pattern: ".crai", required: true}]`,
			want:  `[{pattern: ".bai", required: null}, {pattern: ".crai", required: true}]`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := resolveDSL(t, "secondaryFiles", tc.value)
			if !nodeEqual(got, mustParse(t, testFile, tc.want)) {
				t.Errorf("secondaryFiles = %s, want %s", canonicalKey(got), tc.want)
			}
		})
	}
}
