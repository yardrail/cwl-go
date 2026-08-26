package cwlcore

import (
	"errors"
	"reflect"
	"testing"
)

// optionalRuntimeFields are the runtime.* fields that exist only when the
// caller supplies them.
func optionalRuntimeFields() []string {
	return []string{runtimeCores, runtimeRAM, runtimeOutdirSize, runtimeTmpdirSize, runtimeExitCode}
}

func TestEvalParamRefGrammar(t *testing.T) {
	t.Parallel()

	cases := []struct {
		want any
		name string
		expr string
	}{
		{name: "the null literal", expr: refNull, want: nil},
		{name: "bare root", expr: refInputs, want: testInputs()},
		{name: "self when nil", expr: refSelf, want: nil},

		{name: "dotted field", expr: refString, want: "a"},
		{name: "single quoted field", expr: "$(inputs['s'])", want: "a"},
		{name: "double quoted field", expr: `$(inputs["s"])`, want: "a"},
		{name: "field with a space", expr: "$(inputs['a b'])", want: "spaced"},
		{name: "field with an escaped double quote", expr: `$(inputs["quo\"te"])`, want: "double"},
		{name: "field with an escaped single quote", expr: `$(inputs['quo\'te'])`, want: "single"},
		{name: "unicode field", expr: "$(inputs.файл)", want: "unicode"},

		{name: "index", expr: "$(inputs.arr[0])", want: int64(1)},
		{name: "last index", expr: "$(inputs.arr[2])", want: int64(3)},

		{name: "length of a list", expr: "$(inputs.arr.length)", want: int64(3)},
		{name: "length of an empty list", expr: "$(inputs.empty.length)", want: int64(0)},
		{name: "length as an ordinary field", expr: "$(inputs.length)", want: "shadow"},

		{name: "deep chain", expr: "$(inputs.nested.list[0].deep)", want: deepValue},
		{name: "mixed segment forms", expr: `$(inputs['nested']["list"][0].deep)`, want: deepValue},

		{name: "runtime string", expr: refOutdir, want: outdirPath},
		{name: "runtime number", expr: refCores, want: int64(4)},
		{name: "runtime bracket", expr: "$(runtime['tmpdir'])", want: "/tmp"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := evalParamRef(tc.expr[len("$"):], testContext())
			if err != nil {
				t.Fatalf("evalParamRef(%q) returned error: %v", tc.expr, err)
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("evalParamRef(%q) = %#v, want %#v", tc.expr, got, tc.want)
			}
		})
	}
}

// TestEvalParamRefSelfAndRuntime covers the parts of the parameter context the
// shared fixture leaves empty.
func TestEvalParamRefSelfAndRuntime(t *testing.T) {
	t.Parallel()

	exit := 7
	ctx := &EvalContext{
		Self:    map[string]any{keyPath: "/f", keySize: int64(12)},
		Runtime: RuntimeContext{Outdir: "/o", ExitCode: &exit},
	}

	cases := []struct {
		want any
		name string
		expr string
	}{
		{name: "self object", expr: refSelf, want: map[string]any{keyPath: "/f", keySize: int64(12)}},
		{name: "self field", expr: "$(self.path)", want: "/f"},
		{name: "exit code", expr: "$(runtime.exitCode)", want: int64(7)},
		{name: "empty tmpdir is still defined", expr: "$(runtime.tmpdir)", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := evalParamRef(tc.expr[len("$"):], ctx)
			if err != nil {
				t.Fatalf("evalParamRef(%q) returned error: %v", tc.expr, err)
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("evalParamRef(%q) = %#v, want %#v", tc.expr, got, tc.want)
			}
		})
	}
}

// TestEvalParamRefSelfAsList pins that self may be a list, which is the shape
// an outputEval or a secondaryFiles expression sees.
func TestEvalParamRefSelfAsList(t *testing.T) {
	t.Parallel()

	ctx := &EvalContext{Self: []any{map[string]any{"basename": "x.txt"}}}

	got, err := evalParamRef("(self[0].basename)", ctx)
	if err != nil {
		t.Fatalf("evalParamRef returned error: %v", err)
	}

	if got != "x.txt" {
		t.Errorf("self[0].basename = %#v, want %q", got, "x.txt")
	}

	length, err := evalParamRef("(self.length)", ctx)
	if err != nil {
		t.Fatalf("evalParamRef returned error: %v", err)
	}

	if length != int64(1) {
		t.Errorf("self.length = %#v, want 1", length)
	}
}

func TestEvalParamRefErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		want error
		name string
		expr string
	}{
		{name: "missing field", expr: refMissing, want: ErrExpressionEval},
		{name: "missing bracketed field", expr: "$(inputs['missing'])", want: ErrExpressionEval},
		{name: "missing nested field", expr: "$(inputs.rec.missing)", want: ErrExpressionEval},
		{name: "index out of range", expr: "$(inputs.arr[3])", want: ErrExpressionEval},
		{name: "index zero out of range", expr: "$(inputs.empty[0])", want: ErrExpressionEval},
		{name: "index into an object", expr: "$(inputs.rec[0])", want: ErrExpressionEval},
		{name: "field on a string", expr: "$(inputs.s.nope)", want: ErrExpressionEval},
		{name: "field on a number", expr: "$(inputs.n.nope)", want: ErrExpressionEval},
		{name: "field on null", expr: "$(inputs.nul.nope)", want: ErrExpressionEval},
		{name: "index into a string", expr: "$(inputs.s[0])", want: ErrExpressionEval},
		{name: "length mid chain", expr: "$(inputs.arr.length.nope)", want: ErrExpressionEval},
		{name: "unset runtime field", expr: refCores, want: ErrExpressionEval},

		{name: "unknown root symbol", expr: "$(Math.PI)", want: ErrNotParameterReference},
		{name: "arithmetic is not a reference", expr: exprIncrement, want: ErrNotParameterReference},
		{name: "call", expr: "$(f())", want: ErrNotParameterReference},
		{name: "empty reference", expr: "$()", want: ErrNotParameterReference},
		{name: "leading dot", expr: "$(.inputs)", want: ErrNotParameterReference},
		{name: "trailing dot", expr: "$(inputs.)", want: ErrNotParameterReference},
		{name: "negative index", expr: "$(inputs.arr[-1])", want: ErrNotParameterReference},
		{name: "unterminated bracket", expr: "$(inputs['a)", want: ErrNotParameterReference},
		{name: "oversized index", expr: "$(inputs.arr[999999999999999999999999])", want: ErrNotParameterReference},
	}

	// A context with no runtime resources set, so runtime.cores is undefined.
	ctx := &EvalContext{Inputs: testInputs()}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			value, err := evalParamRef(tc.expr[len("$"):], ctx)
			if !errors.Is(err, tc.want) {
				t.Errorf("evalParamRef(%q) = %#v, err = %v, want %v", tc.expr, value, err, tc.want)
			}
		})
	}
}

// TestEvalParamRefAcceptsForeignCollections pins the reflective fallback: a
// caller that hands us a []string or a map[string]string still gets an answer
// rather than a confusing "is a string" error.
func TestEvalParamRefAcceptsForeignCollections(t *testing.T) {
	t.Parallel()

	ctx := &EvalContext{Inputs: map[string]any{
		"names": []string{"a", "b"},
		"env":   map[string]string{"HOME": "/root"},
	}}

	cases := []struct {
		want any
		expr string
	}{
		{expr: "$(inputs.names[1])", want: "b"},
		{expr: "$(inputs.names.length)", want: int64(2)},
		{expr: "$(inputs.env.HOME)", want: "/root"},
	}

	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()

			got, err := evalParamRef(tc.expr[len("$"):], ctx)
			if err != nil {
				t.Fatalf("evalParamRef(%q) returned error: %v", tc.expr, err)
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("evalParamRef(%q) = %#v, want %#v", tc.expr, got, tc.want)
			}
		})
	}
}

func TestRuntimeContextOmitsUnsetFields(t *testing.T) {
	t.Parallel()

	sparse := RuntimeContext{Outdir: "/o", Tmpdir: "/t"}.asMap()

	for _, key := range optionalRuntimeFields() {
		if _, ok := sparse[key]; ok {
			t.Errorf("runtime.%s is defined, want it omitted", key)
		}
	}

	one := int64(1)
	exit := 0
	populated := RuntimeContext{
		Cores: &one, RAM: &one, OutdirSize: &one, TmpdirSize: &one, ExitCode: &exit,
	}.asMap()

	for _, key := range append(optionalRuntimeFields(), runtimeOutdir, runtimeTmpdir) {
		if _, ok := populated[key]; !ok {
			t.Errorf("runtime.%s is missing, want it defined", key)
		}
	}
}

func TestTypeName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		value any
		name  string
		want  string
	}{
		{name: "nothing", value: nil, want: nullSymbol},
		{name: "a bool", value: true, want: typeNameBoolean},
		{name: "some text", value: "x", want: typeNameString},
		{name: "an integer", value: int64(3), want: typeNameNumber},
		{name: "a fraction", value: 2.5, want: typeNameNumber},
		{name: "a named integer", value: namedInt(1), want: typeNameNumber},
		{name: "a slice of any", value: []any{int64(1)}, want: typeNameList},
		{name: "a typed slice", value: []string{"a"}, want: typeNameList},
		{name: "a map", value: map[string]any{"a": int64(1)}, want: typeNameObject},
		{name: "something else entirely", value: func() {}, want: "a func()"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := TypeName(tc.value); got != tc.want {
				t.Errorf("TypeName(%#v) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}
