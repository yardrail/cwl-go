package cwlcore

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Expression strings and values that more than one table uses. They are
// constants because the project's lint configuration treats a literal repeated
// three times as a missing constant; the names mirror what they evaluate.
const (
	refNull      = "$(null)"
	refInputs    = "$(inputs)"
	refSelf      = "$(self)"
	refNumber    = "$(inputs.n)"
	refFloat     = "$(inputs.f)"
	refString    = "$(inputs.s)"
	refBool      = "$(inputs.b)"
	refNullField = "$(inputs.nul)"
	refArray     = "$(inputs.arr)"
	refRecord    = "$(inputs.rec)"
	refMissing   = "$(inputs.missing)"
	refOutdir    = "$(runtime.outdir)"
	refCores     = "$(runtime.cores)"

	exprIncrement   = "$(inputs.n + 1)"
	exprSample      = "$(inputs.x)"
	exprBody        = "${return 1;}"
	exprBraceString = `${ return "}"; }`

	outdirPath = "/out"
	deepValue  = "value"
	wantTrue   = "true"
	listKey    = "list"
	plainText  = "plain text"
)

// testInputs is the input object most of the expression tests evaluate
// against. It deliberately mixes every JSON shape, plus a Unicode key and keys
// that can only be reached through the bracket forms of the grammar.
func testInputs() map[string]any {
	return map[string]any{
		"n":      int64(3),
		"f":      2.5,
		"s":      "a",
		"b":      true,
		"nul":    nil,
		"arr":    []any{int64(1), int64(2), int64(3)},
		"empty":  make([]any, 0),
		"rec":    map[string]any{"b": int64(2), "a": int64(1)},
		"a b":    "spaced",
		`quo"te`: "double",
		"quo'te": "single",
		"length": "shadow",
		"файл":   "unicode",
		"nested": map[string]any{
			listKey: []any{
				map[string]any{"deep": deepValue},
			},
		},
	}
}

// testContext is testInputs with a populated runtime block.
func testContext() *EvalContext {
	cores := int64(4)
	ram := int64(1024)

	return &EvalContext{
		Inputs: testInputs(),
		Self:   nil,
		Runtime: RuntimeContext{
			Outdir: outdirPath,
			Tmpdir: "/tmp",
			Cores:  &cores,
			RAM:    &ram,
		},
	}
}

func TestNeedsParsing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want bool
	}{
		{name: "nothing at all", in: "", want: false},
		{name: "prose", in: "hello world", want: false},
		{name: "lone dollar", in: "cost: $5", want: false},
		{name: "call syntax", in: "f(x)", want: false},
		{name: "braces alone", in: "{a}", want: false},
		{name: "parameter reference", in: exprSample, want: true},
		{name: "a body expression", in: exprBody, want: true},
		{name: "embedded", in: "prefix-" + exprSample + "-suffix", want: true},
		{name: "escaped still needs parsing", in: `\` + exprSample, want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := NeedsParsing(tc.in); got != tc.want {
				t.Errorf("NeedsParsing(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestEvalPreservesTypeForWholeStringReference(t *testing.T) {
	t.Parallel()

	cases := []struct {
		want any
		name string
		expr string
	}{
		{name: "number", expr: refNumber, want: int64(3)},
		{name: "a float", expr: refFloat, want: 2.5},
		{name: "string", expr: refString, want: "a"},
		{name: "a boolean value", expr: refBool, want: true},
		{name: "null field", expr: refNullField, want: nil},
		{name: "a null literal", expr: refNull, want: nil},
		{name: "a list value", expr: refArray, want: []any{int64(1), int64(2), int64(3)}},
		{name: "object", expr: refRecord, want: map[string]any{"a": int64(1), "b": int64(2)}},
		{name: "whole inputs", expr: refInputs, want: testInputs()},
		{name: "leading whitespace", expr: "  " + refNumber, want: int64(3)},
		{name: "trailing whitespace", expr: refNumber + "\t\n", want: int64(3)},
		{name: "surrounding whitespace", expr: "  " + refNumber + "  ", want: int64(3)},
		{name: "runtime string", expr: refOutdir, want: outdirPath},
		{name: "runtime number", expr: refCores, want: int64(4)},
		{name: "no expression", expr: plainText, want: plainText},
	}

	evaluator := NewEvaluator()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := evaluator.Eval(tc.expr, testContext())
			if err != nil {
				t.Fatalf("Eval(%q) returned error: %v", tc.expr, err)
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Eval(%q) = %#v, want %#v", tc.expr, got, tc.want)
			}
		})
	}
}

func TestEvalInterpolatesEmbeddedReferences(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		expr string
		want string
	}{
		{name: "string keeps no quotes", expr: "x" + refString + "y", want: "xay"},
		{name: "number", expr: "n=" + refNumber, want: "n=3"},
		{name: "float keeps its fraction", expr: "f=" + refFloat, want: "f=2.5"},
		{name: "boolean", expr: "b=" + refBool, want: "b=true"},
		{name: "a null field", expr: "v=" + refNullField, want: "v=null"},
		{name: "list", expr: "a=" + refArray, want: "a=[1, 2, 3]"},
		{name: "object sorted by key", expr: "r=" + refRecord, want: `r={"a": 1, "b": 2}`},
		{name: "two fragments", expr: refString + "-" + refNumber, want: "a-3"},
		{name: "adjacent fragments", expr: refString + refString, want: "aa"},
		{name: "trailing literal only", expr: refNumber + "!", want: "3!"},
		{name: "leading literal only", expr: "!" + refNumber, want: "!3"},
		{name: "three in a row", expr: refNumber + refNumber + refNumber, want: "333"},
	}

	evaluator := NewEvaluator()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := evaluator.Eval(tc.expr, testContext())
			if err != nil {
				t.Fatalf("Eval(%q) returned error: %v", tc.expr, err)
			}

			if got != tc.want {
				t.Errorf("Eval(%q) = %#v, want %q", tc.expr, got, tc.want)
			}
		})
	}
}

// TestEvalObjectInterpolationIsDeterministic guards the one place Go's
// randomised map iteration could leak into output.
func TestEvalObjectInterpolationIsDeterministic(t *testing.T) {
	t.Parallel()

	ctx := &EvalContext{Inputs: map[string]any{
		"m": map[string]any{"z": 1, "m": 2, "a": 3, "B": 4, "0": 5},
	}}
	evaluator := NewEvaluator()

	const want = `x{"0": 5, "B": 4, "a": 3, "m": 2, "z": 1}`

	for range 50 {
		got, err := evaluator.Eval("x$(inputs.m)", ctx)
		if err != nil {
			t.Fatalf("Eval returned error: %v", err)
		}

		if got != want {
			t.Fatalf("Eval = %#v, want %q", got, want)
		}
	}
}

func TestEvalEscaping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		expr string
		want string
	}{
		{name: "escaped reference", expr: `\` + refNumber, want: refNumber},
		{name: "escaped function body", expr: `\` + exprBody, want: exprBody},
		{name: "escaped backslash then a reference", expr: `\\` + refNumber, want: `\3`},
		{name: "escape inside text", expr: `a\$(x)b` + refNumber, want: "a$(x)b3"},
		{name: "unknown escape is left alone", expr: `\n` + refNumber, want: `\n3`},
		{name: "escaped dollar not opening", expr: `\$x` + refNumber, want: `\$x3`},
		{name: "double backslash", expr: `\\ ` + refNumber, want: `\ 3`},
	}

	evaluator := NewEvaluator()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := evaluator.Eval(tc.expr, testContext())
			if err != nil {
				t.Fatalf("Eval(%q) returned error: %v", tc.expr, err)
			}

			if got != tc.want {
				t.Errorf("Eval(%q) = %#v, want %q", tc.expr, got, tc.want)
			}
		})
	}
}

func TestEvalString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		expr string
		want string
	}{
		{name: "string passes through", expr: refString, want: "a"},
		{name: "number becomes text", expr: refNumber, want: "3"},
		{name: "float keeps its fraction", expr: refFloat, want: "2.5"},
		{name: "boolean", expr: refBool, want: wantTrue},
		{name: "null becomes empty", expr: refNullField, want: ""},
		{name: "object", expr: refRecord, want: `{"a": 1, "b": 2}`},
		{name: "already a string", expr: "literal", want: "literal"},
		{name: "an interpolated number", expr: "n=" + refNumber, want: "n=3"},
	}

	evaluator := NewEvaluator()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := evaluator.EvalString(tc.expr, testContext())
			if err != nil {
				t.Fatalf("EvalString(%q) returned error: %v", tc.expr, err)
			}

			if got != tc.want {
				t.Errorf("EvalString(%q) = %q, want %q", tc.expr, got, tc.want)
			}
		})
	}
}

func TestEvalStringPropagatesErrors(t *testing.T) {
	t.Parallel()

	_, err := NewEvaluator().EvalString(refMissing, testContext())
	if !errors.Is(err, ErrExpressionEval) {
		t.Fatalf("EvalString error = %v, want ErrExpressionEval", err)
	}
}

// TestEvalWithoutJavaScript pins the spec rule that absent
// InlineJavascriptRequirement "the workflow platform must not perform
// expression interpolation" of anything beyond a parameter reference.
func TestEvalWithoutJavaScript(t *testing.T) {
	t.Parallel()

	cases := []struct {
		want error
		name string
		expr string
	}{
		{name: "function body", expr: exprBody, want: ErrJavaScript},
		{name: "function call", expr: "$(Math.max(1, 2))", want: ErrJavaScript},
		{name: "arithmetic needs javascript", expr: exprIncrement, want: ErrJavaScript},
		{name: "unknown root symbol", expr: "$(Math.PI)", want: ErrNotParameterReference},
		{name: "member on a string", expr: "$(inputs.s.length)", want: ErrExpressionEval},
		{name: "missing key", expr: refMissing, want: ErrExpressionEval},
		{name: "index out of range", expr: "$(inputs.arr[9])", want: ErrExpressionEval},
	}

	evaluator := NewEvaluator()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := evaluator.Eval(tc.expr, testContext())
			if !errors.Is(err, tc.want) {
				t.Errorf("Eval(%q) error = %v, want %v", tc.expr, err, tc.want)
			}
		})
	}
}

// TestEvalWithoutJavaScriptAcceptsParameterReferences is the other half of the
// rule: a plain parameter reference must work with no requirement declared.
func TestEvalWithoutJavaScriptAcceptsParameterReferences(t *testing.T) {
	t.Parallel()

	evaluator := NewEvaluator()

	for _, expr := range referenceCorpus() {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			_, err := evaluator.Eval(expr, testContext())
			if err != nil {
				t.Errorf("Eval(%q) returned error: %v", expr, err)
			}
		})
	}
}

func TestEvalNilContext(t *testing.T) {
	t.Parallel()

	evaluator := NewEvaluator()

	got, err := evaluator.Eval(refInputs, nil)
	if err != nil {
		t.Fatalf("Eval returned error: %v", err)
	}

	object, ok := got.(map[string]any)
	if !ok || len(object) != 0 {
		t.Errorf("inputs = %#v, want an empty object", got)
	}

	_, err = evaluator.Eval("$(inputs.anything)", nil)
	if !errors.Is(err, ErrExpressionEval) {
		t.Errorf("Eval error = %v, want ErrExpressionEval", err)
	}
}

func TestEvalUnterminatedExpression(t *testing.T) {
	t.Parallel()

	cases := []string{
		"$(inputs.n",
		"${ return 1;",
		"a $( b",
		`$("unclosed string)`,
	}

	evaluator := NewEvaluator(WithJS(nil))

	for _, expr := range cases {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			_, err := evaluator.Eval(expr, testContext())
			if !errors.Is(err, ErrExpressionSyntax) {
				t.Errorf("Eval(%q) error = %v, want ErrExpressionSyntax", expr, err)
			}
		})
	}
}

func BenchmarkEvalParameterReference(b *testing.B) {
	evaluator := NewEvaluator()
	ctx := testContext()

	for b.Loop() {
		_, err := evaluator.Eval("$(inputs.nested.list[0].deep)", ctx)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEvalJavaScript(b *testing.B) {
	evaluator := NewEvaluator(WithJS(nil))
	ctx := testContext()

	for b.Loop() {
		_, err := evaluator.Eval(exprIncrement, ctx)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// TestEvalMisspelledRootSymbolIsNotAJavaScriptError is the regression guard for
// the diagnosis a typo used to receive. `when: $(input.flag)` — one letter
// short of inputs — reported that InlineJavascriptRequirement was missing,
// which is advice that cannot possibly help, and which a caller keying on the
// sentinel would have repeated to the user.
func TestEvalMisspelledRootSymbolIsNotAJavaScriptError(t *testing.T) {
	t.Parallel()

	cases := []string{
		"$(input.flag)",
		"$(Input.flag)",
		"$(inputss)",
		"$(selff.path)",
		"$(runtim.outdir)",
		"$(inputs2['a'])",
	}

	evaluator := NewEvaluator()

	for _, expr := range cases {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			_, err := evaluator.Eval(expr, testContext())
			if !errors.Is(err, ErrNotParameterReference) {
				t.Fatalf("Eval(%q) error = %v, want ErrNotParameterReference", expr, err)
			}

			if errors.Is(err, ErrJavaScript) {
				t.Errorf("Eval(%q) error = %v, want it not to blame InlineJavascriptRequirement", expr, err)
			}

			if !strings.Contains(err.Error(), expr) {
				t.Errorf("Eval(%q) error = %v, want it to quote the offending fragment", expr, err)
			}
		})
	}
}

// TestEvalJavaScriptOnlySyntaxReportsErrJavaScript is the other side of the
// split: a fragment the parameter-reference grammar cannot parse at all is
// JavaScript by elimination, so the engine really is the missing capability.
func TestEvalJavaScriptOnlySyntaxReportsErrJavaScript(t *testing.T) {
	t.Parallel()

	cases := []string{
		exprBody,
		exprIncrement,
		"$(Math.max(1, 2))",
		"$(inputs.arr[0] + 1)",
		"$(1 + 1)",
		"$(inputs.arr.map(function (x) { return x; }))",
		"${ return inputs.n; }",
	}

	evaluator := NewEvaluator()

	for _, expr := range cases {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			_, err := evaluator.Eval(expr, testContext())
			if !errors.Is(err, ErrJavaScript) {
				t.Fatalf("Eval(%q) error = %v, want ErrJavaScript", expr, err)
			}

			if errors.Is(err, ErrNotParameterReference) {
				t.Errorf("Eval(%q) error = %v, want only the javascript sentinel", expr, err)
			}

			if !strings.Contains(err.Error(), expr) {
				t.Errorf("Eval(%q) error = %v, want it to quote the offending fragment", expr, err)
			}
		})
	}
}

// TestEvalBooleanLiteralIsNotAParameterReference confirms the spec reading:
// the parameter-reference grammar resolves a leading symbol against the
// parameter context, and only inputs, self and runtime are in it. `true` is a
// symbol like any other there, so $(true) needs JavaScript — unlike $(null),
// which every implementation treats as the literal null.
func TestEvalBooleanLiteralIsNotAParameterReference(t *testing.T) {
	t.Parallel()

	cases := []struct {
		want any
		expr string
	}{
		{expr: "$(true)", want: true},
		{expr: "$(false)", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			checkNeedsJavaScript(t, tc.expr, tc.want)
		})
	}

	got, err := NewEvaluator().Eval(refNull, nil)
	if err != nil || got != nil {
		t.Errorf("Eval(%q) = %#v, %v, want the null value", refNull, got, err)
	}
}

// checkNeedsJavaScript asserts that expr is rejected as a parameter reference
// and yields want once the engine is available.
func checkNeedsJavaScript(t *testing.T, expr string, want any) {
	t.Helper()

	_, err := NewEvaluator().Eval(expr, nil)
	if !errors.Is(err, ErrNotParameterReference) {
		t.Fatalf("Eval(%q) without javascript error = %v, want ErrNotParameterReference", expr, err)
	}

	got, err := NewEvaluator(WithJS(nil)).Eval(expr, nil)
	if err != nil {
		t.Fatalf("Eval(%q) with javascript returned error: %v", expr, err)
	}

	if got != want {
		t.Errorf("Eval(%q) with javascript = %#v, want %v", expr, got, want)
	}
}

// TestNilEvaluator pins the nil tolerance the godoc promises, so that it stays
// symmetric with the nil *EvalContext Eval already accepts.
func TestNilEvaluator(t *testing.T) {
	t.Parallel()

	for _, evaluator := range []*Evaluator{nil, {}} {
		checkReferencesOnly(t, evaluator)
	}
}

// checkReferencesOnly asserts that evaluator resolves parameter references and
// nothing more.
func checkReferencesOnly(t *testing.T, evaluator *Evaluator) {
	t.Helper()

	got, err := evaluator.Eval(refNumber, testContext())
	if err != nil || got != int64(3) {
		t.Errorf("Eval = %#v, %v, want 3", got, err)
	}

	text, err := evaluator.EvalString(refNumber, testContext())
	if err != nil || text != "3" {
		t.Errorf("EvalString = %q, %v, want the digit three", text, err)
	}

	flag, err := evaluator.EvalBool(refBool, testContext())
	if err != nil || !flag {
		t.Errorf("EvalBool = %v, %v, want true", flag, err)
	}

	_, err = evaluator.Eval(exprBody, testContext())
	if !errors.Is(err, ErrJavaScript) {
		t.Errorf("Eval(%q) error = %v, want ErrJavaScript", exprBody, err)
	}
}

func TestEvalBool(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		expr string
		want bool
	}{
		{name: "a true reference", expr: refBool, want: true},
		{name: "a false reference", expr: "$(inputs.off)", want: false},
		{name: "a comparison", expr: "$(inputs.n > 2)", want: true},
		{name: "a negation", expr: "$(!inputs.b)", want: false},
		{name: "a function body", expr: "${ return inputs.n === 3; }", want: true},
	}

	ctx := testContext()
	ctx.Inputs["off"] = false
	evaluator := NewEvaluator(WithJS(nil))

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := evaluator.EvalBool(tc.expr, ctx)
			if err != nil {
				t.Fatalf("EvalBool(%q) returned error: %v", tc.expr, err)
			}

			if got != tc.want {
				t.Errorf("EvalBool(%q) = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

// evalSentinels is the closed set of classifiers an expression failure may
// carry. Every failure must match exactly one.
func evalSentinels() []error {
	return []error{
		ErrJavaScript,
		ErrNotParameterReference,
		ErrExpressionSyntax,
		ErrExpressionEval,
		ErrExpressionTimeout,
		ErrNotBoolean,
	}
}

// checkExactlyOneSentinel asserts that err matches want and no other
// classifier. The one-of-six invariant is what lets a scheduler dispatch on
// the sentinel alone, so fusing two would be a silent contract break rather
// than a visible failure.
func checkExactlyOneSentinel(t *testing.T, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}

	for _, sentinel := range evalSentinels() {
		if errors.Is(sentinel, want) {
			continue
		}

		if errors.Is(err, sentinel) {
			t.Errorf("error = %v matches both %v and %v, want exactly one classifier", err, want, sentinel)
		}
	}
}

// TestEvalBoolRejectsNonBooleans pins the spec's "It is an error if the
// expression evaluates to any other value": nothing is coerced, the error
// names the type that turned up instead, and it is ErrNotBoolean rather than
// an evaluation failure — the expression ran fine, the document is wrong.
func TestEvalBoolRejectsNonBooleans(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		expr string
		kind string
	}{
		{name: "a number", expr: refNumber, kind: typeNameNumber},
		{name: "one", expr: "$(1)", kind: typeNameNumber},
		{name: "zero", expr: "$(0)", kind: typeNameNumber},
		{name: "the string true", expr: `$("true")`, kind: typeNameString},
		{name: "an unparsed literal", expr: wantTrue, kind: typeNameString},
		{name: "a null", expr: refNullField, kind: nullSymbol},
		{name: "a null literal", expr: refNull, kind: nullSymbol},
		{name: "a list", expr: refArray, kind: typeNameList},
		{name: "an empty list", expr: "$(inputs.empty)", kind: typeNameList},
		{name: "an object", expr: refRecord, kind: typeNameObject},
	}

	evaluator := NewEvaluator(WithJS(nil))

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := evaluator.EvalBool(tc.expr, testContext())
			if got {
				t.Errorf("EvalBool(%q) = true alongside an error", tc.expr)
			}

			checkExactlyOneSentinel(t, err, ErrNotBoolean)

			if !strings.Contains(err.Error(), tc.kind) {
				t.Errorf("EvalBool(%q) error = %v, want it to name %q", tc.expr, err, tc.kind)
			}
		})
	}
}

// TestEvalBoolPropagatesEvaluationErrors keeps the wrapper transparent: a
// failure to evaluate at all must arrive with its own classifier and must not
// pick up ErrNotBoolean on the way out.
func TestEvalBoolPropagatesEvaluationErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		want error
		name string
		expr string
	}{
		{name: "a missing key", expr: refMissing, want: ErrExpressionEval},
		{name: "an out-of-range index", expr: "$(inputs.arr[9])", want: ErrExpressionEval},
		{name: "a thrown exception", expr: "${ throw new Error('boom'); }", want: ErrExpressionEval},
		{name: "a reference error", expr: "$(nosuchvariable)", want: ErrExpressionEval},
		{name: "an undefined result", expr: "$(undefined)", want: ErrExpressionEval},
		{name: "unparseable javascript", expr: "$(1 +)", want: ErrExpressionSyntax},
		{name: "an unterminated fragment", expr: "$(inputs.b", want: ErrExpressionSyntax},
	}

	evaluator := NewEvaluator(WithJS(nil))

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := evaluator.EvalBool(tc.expr, testContext())
			checkExactlyOneSentinel(t, err, tc.want)
		})
	}
}

// TestEvalBoolWithoutJavaScriptKeepsItsClassifier covers the sentinels that
// only arise with no engine available.
func TestEvalBoolWithoutJavaScriptKeepsItsClassifier(t *testing.T) {
	t.Parallel()

	cases := []struct {
		want error
		name string
		expr string
	}{
		{name: "a misspelled root symbol", expr: "$(input.flag)", want: ErrNotParameterReference},
		{name: "a boolean literal", expr: "$(true)", want: ErrNotParameterReference},
		{name: "a function body", expr: exprBody, want: ErrJavaScript},
		{name: "a comparison", expr: "$(inputs.n > 2)", want: ErrJavaScript},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewEvaluator().EvalBool(tc.expr, testContext())
			checkExactlyOneSentinel(t, err, tc.want)
		})
	}
}

// TestEvalBoolTimeoutKeepsItsClassifier is the last of the six.
func TestEvalBoolTimeoutKeepsItsClassifier(t *testing.T) {
	t.Parallel()

	evaluator := NewEvaluator(WithJS(nil), WithTimeout(50*time.Millisecond))

	_, err := evaluator.EvalBool("${ while (true) {} }", testContext())
	checkExactlyOneSentinel(t, err, ErrExpressionTimeout)
}
