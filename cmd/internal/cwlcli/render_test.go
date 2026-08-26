package cwlcli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONKeepsInsertionOrder(t *testing.T) {
	t.Parallel()

	o := NewObject().Set("zebra", 1).Set("apple", 2)

	got := renderJSON(t, o)
	want := "{\n  \"zebra\": 1,\n  \"apple\": 2\n}"

	if got != want {
		t.Errorf("JSON =\n%s\nwant\n%s", got, want)
	}
}

func TestJSONDoesNotEscapeHTML(t *testing.T) {
	t.Parallel()

	o := NewObject().Set("expr", "$(a < b && c > d)")

	if got := renderJSON(t, o); !strings.Contains(got, "$(a < b && c > d)") {
		t.Errorf("JSON = %s, want the expression unescaped", got)
	}
}

func TestJSONIsValidAndDeterministic(t *testing.T) {
	t.Parallel()

	value := nestedFixture()

	first := renderJSON(t, value)
	second := renderJSON(t, value)

	if first != second {
		t.Errorf("two renders differ:\n%s\n%s", first, second)
	}

	var decoded any

	err := json.Unmarshal([]byte(first), &decoded)
	if err != nil {
		t.Errorf("output is not valid JSON: %v\n%s", err, first)
	}
}

func TestJSONSortsGoMapKeys(t *testing.T) {
	t.Parallel()

	// A Go map is the one shape whose order the standard encoder decides.
	// It sorts, which is what keeps a dump containing one reproducible.
	o := NewObject().Set("m", map[string]int{"c": 3, "a": 1, "b": 2})

	got := renderJSON(t, o)
	if !strings.Contains(got, `"a": 1`) {
		t.Fatalf("JSON = %s, want the map rendered", got)
	}

	if strings.Index(got, `"a"`) > strings.Index(got, `"c"`) {
		t.Errorf("JSON = %s, want map keys sorted", got)
	}
}

func TestObjectMarshalJSONNested(t *testing.T) {
	t.Parallel()

	// Marshalling through the standard encoder must still preserve order,
	// because an Object can appear inside a value someone else encodes.
	encoded, err := json.Marshal([]any{NewObject().Set("z", 1).Set("a", 2)})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if string(encoded) != `[{"z":1,"a":2}]` {
		t.Errorf("Marshal = %s, want [{\"z\":1,\"a\":2}]", encoded)
	}
}

func TestText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value any
		name  string
		want  string
	}{
		{name: "scalar string", value: "hello", want: "hello"},
		{name: "nil renders as null", value: nil, want: "null"},
		{name: "empty string is quoted", value: NewObject().Set("k", ""), want: `k: ""`},
		{name: "empty object", value: NewObject(), want: "{}"},
		{name: "empty slice inlines", value: NewObject().Set("k", make([]any, 0)), want: "k: []"},
		{name: "one item list still nests", value: NewObject().Set("k", []any{"a"}), want: "k:\n  - a"},
		{
			name:  "nested object indents",
			value: NewObject().Set("outer", NewObject().Set("inner", 1)),
			want:  "outer:\n  inner: 1",
		},
		{
			name:  "list of objects aligns continuations",
			value: []any{NewObject().Set("a", 1).Set("b", 2)},
			want:  "- a: 1\n  b: 2",
		},
		{
			name:  "multiline string is quoted",
			value: NewObject().Set("k", "one\ntwo"),
			want:  `k: "one\ntwo"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := Text(tc.value); got != tc.want {
				t.Errorf("Text = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    Format
		wantErr bool
	}{
		{name: "json", input: "json", want: FormatJSON, wantErr: false},
		{name: "text", input: "text", want: FormatText, wantErr: false},
		{name: "unknown", input: "yaml", want: "", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var f Format

			err := f.Set(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Set(%q) error = %v, wantErr %v", tc.input, err, tc.wantErr)
			}

			if tc.wantErr {
				return
			}

			if f != tc.want {
				t.Errorf("Set(%q) = %q, want %q", tc.input, f, tc.want)
			}
		})
	}
}

func TestFormatZeroValueDefaultsToJSON(t *testing.T) {
	t.Parallel()

	var f Format

	if f.String() != string(FormatJSON) {
		t.Errorf("String() = %q, want %q", f.String(), FormatJSON)
	}

	var buf bytes.Buffer

	err := f.Render(&buf, NewObject().Set("a", 1))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if buf.String() != "{\n  \"a\": 1\n}\n" {
		t.Errorf("Render = %q", buf.String())
	}
}

func TestFormatRenderText(t *testing.T) {
	t.Parallel()

	f := FormatText

	var buf bytes.Buffer

	err := f.Render(&buf, NewObject().Set("a", 1))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if buf.String() != "a: 1\n" {
		t.Errorf("Render = %q, want %q", buf.String(), "a: 1\n")
	}
}

func TestFormats(t *testing.T) {
	t.Parallel()

	if got := Formats(); got != "json|text" {
		t.Errorf("Formats() = %q, want json|text", got)
	}
}

// renderJSON renders v and fails the test if it cannot be rendered.
func renderJSON(t *testing.T, v any) string {
	t.Helper()

	encoded, err := JSON(v)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}

	return string(encoded)
}

// nestedFixture builds a value exercising every shape a renderer handles.
func nestedFixture() *Object {
	return NewObject().
		Set("string", "s").
		Set("int", 1).
		Set("float", 1.5).
		Set("bool", true).
		Set("null", nil).
		Set("emptyObject", NewObject()).
		Set("list", []any{1, "two", NewObject().Set("three", 3)}).
		Set("strings", []string{"a", "b"}).
		Set("map", map[string]any{"b": 2, "a": 1})
}
