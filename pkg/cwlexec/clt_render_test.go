package cwlexec

import (
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// cltTextCase is one row of the argText table: a value and either the text it renders as or the
// sentinel its failure must wrap.
type cltTextCase struct {
	value   any
	wantErr error
	name    string
	want    string
}

// run renders the row's value and checks it against the expectation.
func (c cltTextCase) run(t *testing.T) {
	t.Helper()

	got, err := argText(c.value)
	if c.wantErr != nil {
		if !errors.Is(err, c.wantErr) {
			t.Fatalf("argText(%v): error %v does not wrap %v", c.value, err, c.wantErr)
		}

		return
	}

	if err != nil {
		t.Fatalf("argText(%v): unexpected error: %v", c.value, err)
	}

	if got != c.want {
		t.Errorf("argText(%v) = %q, want %q", c.value, got, c.want)
	}
}

func TestArgText(t *testing.T) {
	t.Parallel()

	cases := []cltTextCase{
		{name: "plain string", value: cltText, want: cltText},
		{name: "empty string", value: "", want: ""},
		{name: "boolean true", value: true, want: "true"},
		{name: "boolean false", value: false, want: "false"},
		{name: "int64", value: int64(-7), want: "-7"},
		{name: "int", value: 12, want: "12"},
		{name: "uint8", value: uint8(200), want: "200"},
		{name: "float", value: 1.25, want: "1.25"},
		{name: "typed File", value: &cwlcore.File{Path: "/a"}, want: "/a"},
		{name: "typed Directory", value: &cwlcore.Directory{Path: "/d"}, want: "/d"},
		{
			name:  "mapped File",
			value: map[string]any{fileClassField: cwlcore.ClassFile, filePathField: "/m"},
			want:  "/m",
		},
		{
			name:  "mapped Directory",
			value: map[string]any{fileClassField: cwlcore.ClassDirectory, filePathField: "/md"},
			want:  "/md",
		},
		{
			name:    "typed File with no path",
			value:   &cwlcore.File{Location: "file:///a"},
			wantErr: ErrBindingValue,
		},
		{
			name:    "mapped File with no path",
			value:   map[string]any{fileClassField: cwlcore.ClassFile},
			wantErr: ErrBindingValue,
		},
		{
			name:    "mapped File whose path is not a string",
			value:   map[string]any{fileClassField: cwlcore.ClassFile, filePathField: 3},
			wantErr: ErrBindingValue,
		},
		{
			name:    "a plain object",
			value:   map[string]any{"a": 1},
			wantErr: ErrBindingValue,
		},
		{
			name:    "an object whose class is not a string",
			value:   map[string]any{fileClassField: 1},
			wantErr: ErrBindingValue,
		},
		{
			name:    "an object of some other class",
			value:   map[string]any{fileClassField: "Widget"},
			wantErr: ErrBindingValue,
		},
		{
			name:    "a list",
			value:   []any{"a"},
			wantErr: ErrBindingValue,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			testCase.run(t)
		})
	}
}

func TestFloatText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		want string
		in   float64
	}{
		{name: "whole number keeps a decimal point", in: 4, want: "4.0"},
		{name: "zero", in: 0, want: "0.0"},
		{name: "fraction", in: 3.5, want: "3.5"},
		{name: "negative", in: -0.125, want: "-0.125"},
		{name: "small switches to an exponent", in: 1e-5, want: "1e-05"},
		{name: "just below the digits threshold", in: 1e15, want: "1000000000000000.0"},
		{name: "at the digits threshold", in: 1e16, want: "10000000000000000"},
		{name: "the 43-digit integer", in: 1e42, want: "1" + strings.Repeat("0", 42)},
		{name: "negative at that magnitude", in: -1e42, want: "-1" + strings.Repeat("0", 42)},
		{name: "not a number", in: math.NaN(), want: "NaN"},
		{name: "positive infinity", in: math.Inf(1), want: "Infinity"},
		{name: "negative infinity", in: math.Inf(-1), want: "-Infinity"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := floatText(testCase.in); got != testCase.want {
				t.Errorf("floatText(%v) = %q, want %q", testCase.in, got, testCase.want)
			}

			// The whole point of the delegation: a command-line rendering and an
			// interpolated one are the same rule, so they can never drift apart again
			// without this failing.
			if got := cwlcore.EncodeJSON(testCase.in); got != testCase.want {
				t.Errorf("cwlcore.EncodeJSON(%v) = %q, want %q", testCase.in, got, testCase.want)
			}
		})
	}
}

func TestIntegerValue(t *testing.T) {
	t.Parallel()

	cases := []struct {
		value any
		name  string
		want  int64
		ok    bool
	}{
		{name: "int", value: 3, want: 3, ok: true},
		{name: "int64", value: int64(-4), want: -4, ok: true},
		{name: "uint", value: uint(5), want: 5, ok: true},
		{name: "uint64 beyond int64", value: uint64(math.MaxUint64), ok: false},
		{name: "whole float", value: 6.0, want: 6, ok: true},
		{name: "fractional float", value: 6.5, ok: false},
		{name: "float beyond exact integers", value: 1e18, ok: false},
		{name: "infinity", value: math.Inf(1), ok: false},
		{name: "a string", value: "3", ok: false},
		{name: "bool", value: true, ok: false},
		{name: "a nil value", value: nil, ok: false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, ok := integerValue(testCase.value)
			if ok != testCase.ok {
				t.Fatalf("integerValue(%v) ok = %v, want %v", testCase.value, ok, testCase.ok)
			}

			if ok && got != testCase.want {
				t.Errorf("integerValue(%v) = %d, want %d", testCase.value, got, testCase.want)
			}
		})
	}
}

func TestNumberText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		value any
		name  string
		want  string
		ok    bool
	}{
		{name: "int32", value: int32(-9), want: "-9", ok: true},
		{name: "uint16", value: uint16(9), want: "9", ok: true},
		{name: "float32", value: float32(0.5), want: "0.5", ok: true},
		{name: "a numeric-looking string is not a number", value: "9", ok: false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, ok := numberText(testCase.value)
			if ok != testCase.ok {
				t.Fatalf("numberText(%v) ok = %v, want %v", testCase.value, ok, testCase.ok)
			}

			if ok && got != testCase.want {
				t.Errorf("numberText(%v) = %q, want %q", testCase.value, got, testCase.want)
			}
		})
	}
}

func TestAsList(t *testing.T) {
	t.Parallel()

	if list, ok := valueList([]any{1, 2}); !ok || len(list) != 2 {
		t.Errorf("valueList([]any) = %v, %v; want a two-element list", list, ok)
	}

	// A caller that already has a typed slice should not have to convert it first.
	list, ok := valueList([]string{"a", "b", "c"})
	if !ok || len(list) != 3 || list[2] != "c" {
		t.Errorf("valueList([]string) = %v, %v; want a three-element list", list, ok)
	}

	if _, ok := valueList("abc"); ok {
		t.Error("valueList(string) reported a list")
	}

	if _, ok := valueList(nil); ok {
		t.Error("valueList(nil) reported a list")
	}
}

func TestIsRecordValue(t *testing.T) {
	t.Parallel()

	cases := []struct {
		value any
		name  string
		want  bool
	}{
		{name: "plain object", value: map[string]any{"a": 1}, want: true},
		{name: "File map", value: map[string]any{fileClassField: cwlcore.ClassFile, filePathField: "/a"}, want: false},
		{name: "typed File", value: &cwlcore.File{Path: "/a"}, want: false},
		{name: "a bare string", value: "s", want: false},
		{name: "a nil value", value: nil, want: false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := isRecordValue(testCase.value); got != testCase.want {
				t.Errorf("isRecordValue(%v) = %v, want %v", testCase.value, got, testCase.want)
			}
		})
	}
}

// TestRenderArgEmitShapes covers the prefix/separate combinations directly, so that the argv
// tables above do not have to enumerate them for every value type.
// cltShapeCase is one row of the emit-shape table: a binding, a value, and the argv fragment they
// render to.
type cltShapeCase struct {
	binding *cwlcore.CommandLineBinding
	value   any
	name    string
	want    []string
}

// run renders the row's binding and compares the fragment it produced.
func (c cltShapeCase) run(t *testing.T) {
	t.Helper()

	args, err := renderArg(&boundArg{binding: c.binding, value: c.value})
	if err != nil {
		t.Fatalf("renderArg: unexpected error: %v", err)
	}

	if got := (&CommandLine{Args: args}).Argv(); !slices.Equal(got, c.want) {
		t.Errorf("renderArg = %q, want %q", got, c.want)
	}
}

func TestRenderArgEmitShapes(t *testing.T) {
	t.Parallel()

	cases := []cltShapeCase{
		{
			name:    "value alone",
			binding: &cwlcore.CommandLineBinding{},
			value:   "v",
			want:    []string{"v"},
		},
		{
			name:    "prefix and value",
			binding: &cwlcore.CommandLineBinding{Prefix: "-p"},
			value:   "v",
			want:    []string{"-p", "v"},
		},
		{
			name:    "prefix concatenated",
			binding: &cwlcore.CommandLineBinding{Prefix: "-p", Separate: cwlcore.NewOptBool(false)},
			value:   "v",
			want:    []string{"-pv"},
		},
		{
			name:    "record with no prefix adds nothing",
			binding: &cwlcore.CommandLineBinding{},
			value:   map[string]any{"a": 1},
			want:    nil,
		},
		{
			name:    "list with no prefix adds nothing",
			binding: &cwlcore.CommandLineBinding{},
			value:   []any{"a"},
			want:    nil,
		},
		{
			name:    "false boolean with no prefix is not an error",
			binding: &cwlcore.CommandLineBinding{},
			value:   false,
			want:    nil,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			testCase.run(t)
		})
	}
}
