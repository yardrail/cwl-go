package cwlcore

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dop251/goja"
)

// evalCase is one expression and the value it must evaluate to.
type evalCase struct {
	want any
	name string
	expr string
}

// runEvalCases evaluates each case against testContext and compares.
func runEvalCases(t *testing.T, evaluator *Evaluator, cases []evalCase) {
	t.Helper()

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

func TestEvalJavaScriptExpressions(t *testing.T) {
	t.Parallel()

	cases := []evalCase{
		{name: "addition", expr: exprIncrement, want: int64(4)},
		{name: "string method", expr: "$(inputs.s.toUpperCase())", want: "A"},
		{name: "string length", expr: "$(inputs.s.length)", want: int64(1)},
		{
			name: "array method",
			expr: "$(inputs.arr.map(function (x) { return x * 2; }))",
			want: []any{int64(2), int64(4), int64(6)},
		},
		{name: "array join", expr: "$(inputs.arr.join('-'))", want: "1-2-3"},
		{name: "builtin", expr: "$(Math.max(1, 5, 3))", want: int64(5)},
		{
			name: "object literal",
			expr: "$({a: 1, b: [2, 3]})",
			want: map[string]any{"a": int64(1), "b": []any{int64(2), int64(3)}},
		},
		{name: "ternary", expr: "$(inputs.b ? 'yes' : 'no')", want: "yes"},
		{name: "a bare null", expr: refNull, want: nil},
		{name: "null comparison", expr: "$(inputs.nul === null)", want: true},
		{
			name: "json round trip",
			expr: "$(JSON.parse(JSON.stringify(inputs.rec)))",
			want: map[string]any{"a": int64(1), "b": int64(2)},
		},
		{name: "self is null", expr: "$(self === null)", want: true},
		{name: "runtime", expr: "$(runtime.outdir + '/x')", want: "/out/x"},
		{name: "float arithmetic", expr: "$(inputs.f * 2)", want: int64(5)},
		{name: "non integral float", expr: "$(inputs.f + 0.25)", want: 2.75},
		{name: "string with a paren", expr: "$('(' + inputs.s + ')')", want: "(a)"},
		{name: "nested quotes", expr: `$("it's " + inputs.s)`, want: "it's a"},
	}

	runEvalCases(t, NewEvaluator(WithJS(nil)), cases)
}

func TestEvalJavaScriptFunctionBodies(t *testing.T) {
	t.Parallel()

	cases := []evalCase{
		{name: "early return", expr: "${ if (inputs.b) { return 'yes'; } return 'no'; }", want: "yes"},
		{
			name: "multi statement",
			expr: "${ var t = 0; for (var i = 0; i < inputs.arr.length; i++) { t += inputs.arr[i]; } return t; }",
			want: int64(6),
		},
		{name: "returns an object", expr: "${ return {ok: true}; }", want: map[string]any{"ok": true}},
		{name: "returns a list", expr: "${ return [1, 2]; }", want: []any{int64(1), int64(2)}},
		{name: "returns null", expr: "${ return null; }", want: nil},
		{name: "brace inside a string", expr: exprBraceString, want: "}"},
		{name: "closure", expr: "${ var f = function () { return inputs.n; }; return f(); }", want: int64(3)},
		{name: "inside a larger string", expr: "n=${ return inputs.n; }!", want: "n=3!"},
	}

	runEvalCases(t, NewEvaluator(WithJS(nil)), cases)
}

func TestEvalExpressionLib(t *testing.T) {
	t.Parallel()

	lib := []string{
		"function double(x) { return x * 2; }",
		"var GREETING = '" + someText + "';",
		"function greet(who) { return GREETING + ' ' + who; }",
	}

	cases := []evalCase{
		{name: "declared function", expr: "$(double(inputs.n))", want: int64(6)},
		{name: "declared variable", expr: "$(GREETING)", want: someText},
		{name: "function using a variable", expr: "$(greet(inputs.s))", want: someText + " a"},
		{name: "from a function body", expr: "${ return double(21); }", want: int64(42)},
	}

	runEvalCases(t, NewEvaluator(WithJS(lib)), cases)
}

// TestEvalSandboxIsolation pins the spec's requirement that evaluation
// "permits no side effects to leak outside the context". A global planted by
// one expression must be invisible to the next.
func TestEvalSandboxIsolation(t *testing.T) {
	t.Parallel()

	evaluator := NewEvaluator(WithJS([]string{"var counter = 0;"}))
	ctx := testContext()

	_, err := evaluator.Eval("${ leaked = 'yes'; return 1; }", ctx)
	if err == nil {
		t.Fatal("assigning to an undeclared global succeeded, want a strict-mode error")
	}

	_, err = evaluator.Eval("${ counter = 99; return counter; }", ctx)
	if err != nil {
		t.Fatalf("Eval returned error: %v", err)
	}

	got, err := evaluator.Eval("$(counter)", ctx)
	if err != nil {
		t.Fatalf("Eval returned error: %v", err)
	}

	if got != int64(0) {
		t.Errorf("counter = %#v after a previous evaluation set it to 99, want 0", got)
	}

	kind, err := evaluator.Eval("$(typeof leaked)", ctx)
	if err != nil {
		t.Fatalf("Eval returned error: %v", err)
	}

	if kind != "undefined" {
		t.Errorf("typeof leaked = %#v, want %q", kind, "undefined")
	}
}

// TestEvalDoesNotMutateTheContext is the other half of isolation: the
// expression must not be able to reach the caller's Go data.
func TestEvalDoesNotMutateTheContext(t *testing.T) {
	t.Parallel()

	inputs := map[string]any{"rec": map[string]any{"a": int64(1)}}
	ctx := &EvalContext{Inputs: inputs}

	_, err := NewEvaluator(WithJS(nil)).Eval("${ inputs.rec.a = 99; return inputs.rec.a; }", ctx)
	if err != nil {
		t.Fatalf("Eval returned error: %v", err)
	}

	nested, ok := inputs["rec"].(map[string]any)
	if !ok {
		t.Fatalf("inputs.rec is %#v, want an object", inputs["rec"])
	}

	if nested["a"] != int64(1) {
		t.Errorf("inputs.rec.a = %#v after evaluation, want the original 1", nested["a"])
	}
}

// TestEvalStrictMode pins the spec's "Expressions also must be evaluated in
// Javascript strict mode".
func TestEvalStrictMode(t *testing.T) {
	t.Parallel()

	evaluator := NewEvaluator(WithJS(nil))

	rejected := []string{
		"${ undeclared = 1; return undeclared; }",
		"${ delete Object.prototype; return 1; }",
	}

	for _, expr := range rejected {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			_, err := evaluator.Eval(expr, testContext())
			if !errors.Is(err, ErrExpressionEval) {
				t.Errorf("Eval(%q) error = %v, want ErrExpressionEval", expr, err)
			}
		})
	}

	t.Run("this is undefined", func(t *testing.T) {
		t.Parallel()

		got, err := evaluator.Eval("$(function () { return this === undefined; }())", testContext())
		if err != nil {
			t.Fatalf("Eval returned error: %v", err)
		}

		if strict, ok := got.(bool); !ok || !strict {
			t.Errorf("this === undefined is %#v inside a strict function, want true", got)
		}
	})
}

func TestEvalJavaScriptInvalidResults(t *testing.T) {
	t.Parallel()

	cases := []string{
		"$(undefined)",
		refMissing,
		"$(void 0)",
		"$(function () { return 1; })",
		"$(0/0)",
		"$(1/0)",
		"$(-1/0)",
		"${ var x = 1; }",
		"${ throw new Error('boom'); }",
		"$((function () { throw 'plain'; })())",
		"$(nosuchvariable)",
		"$(inputs.n.nope.deeper)",
		"${ var a = {}; a.self = a; return a; }",
		// A toJSON that itself returns undefined: JSON.stringify then
		// returns undefined for a non-function value, which is jsKind's
		// "result" fallback rather than its "function" case.
		"${ return {toJSON: function(){ return undefined; }}; }",
	}

	evaluator := NewEvaluator(WithJS(nil))

	for _, expr := range cases {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			value, err := evaluator.Eval(expr, testContext())
			if !errors.Is(err, ErrExpressionEval) {
				t.Errorf("Eval(%q) = %#v, err = %v, want ErrExpressionEval", expr, value, err)
			}
		})
	}
}

// TestEvalJavaScriptDateBecomesAString records a decision the spec leaves
// implicit: a Date is not a JSON type, but JSON.stringify gives it one through
// toJSON, and that is the value the reference implementation observes. Going
// through stringify rather than exporting the engine's own value is what keeps
// a Go [time.Time] from reaching the caller.
func TestEvalJavaScriptDateBecomesAString(t *testing.T) {
	t.Parallel()

	got, err := NewEvaluator(WithJS(nil)).Eval("$(new Date(0))", testContext())
	if err != nil {
		t.Fatalf("Eval returned error: %v", err)
	}

	text, ok := got.(string)
	if !ok || !strings.HasPrefix(text, "1970-01-01") {
		t.Errorf("new Date(0) = %#v, want its ISO string", got)
	}
}

func TestEvalJavaScriptSyntaxError(t *testing.T) {
	t.Parallel()

	cases := []string{
		"$(1 +)",
		"${ return ; ; ) }",
		"$(var x = 1)",
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

func TestEvalTimeout(t *testing.T) {
	t.Parallel()

	const limit = 50 * time.Millisecond

	evaluator := NewEvaluator(WithJS(nil), WithTimeout(limit))

	cases := []string{
		"$(function () { while (true) {} }())",
		"${ while (true) {} }",
		"${ var i = 0; while (true) { i = i + 1; } }",
	}

	for _, expr := range cases {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			start := time.Now()

			_, err := evaluator.Eval(expr, testContext())
			if !errors.Is(err, ErrExpressionTimeout) {
				t.Fatalf("Eval(%q) error = %v, want ErrExpressionTimeout", expr, err)
			}

			if elapsed := time.Since(start); elapsed > 10*time.Second {
				t.Errorf("Eval(%q) took %s to be interrupted", expr, elapsed)
			}
		})
	}
}

// TestEvalTimeoutDoesNotAffectLaterEvaluations pins that an interrupted
// evaluation leaves nothing behind — the interrupt is cleared with the sandbox
// it belonged to.
func TestEvalTimeoutDoesNotAffectLaterEvaluations(t *testing.T) {
	t.Parallel()

	evaluator := NewEvaluator(WithJS(nil), WithTimeout(50*time.Millisecond))

	_, err := evaluator.Eval("${ while (true) {} }", testContext())
	if !errors.Is(err, ErrExpressionTimeout) {
		t.Fatalf("Eval error = %v, want ErrExpressionTimeout", err)
	}

	got, err := evaluator.Eval(exprIncrement, testContext())
	if err != nil {
		t.Fatalf("Eval after a timeout returned error: %v", err)
	}

	if got != int64(4) {
		t.Errorf("Eval after a timeout = %#v, want 4", got)
	}
}

func TestWithTimeoutRejectsNonPositive(t *testing.T) {
	t.Parallel()

	for _, d := range []time.Duration{0, -time.Second} {
		evaluator := NewEvaluator(WithTimeout(d))
		if got := evaluator.jsTimeout(); got != DefaultEvalTimeout {
			t.Errorf("jsTimeout() with WithTimeout(%s) = %s, want %s", d, got, DefaultEvalTimeout)
		}
	}

	if got := NewEvaluator().jsTimeout(); got != DefaultEvalTimeout {
		t.Errorf("default jsTimeout() = %s, want %s", got, DefaultEvalTimeout)
	}
}

// TestEvaluatorIsConcurrencySafe exercises the compiled-program cache, which is
// the only mutable state an Evaluator has.
func TestEvaluatorIsConcurrencySafe(t *testing.T) {
	t.Parallel()

	evaluator := NewEvaluator(WithJS([]string{"function triple(x) { return x * 3; }"}))
	exprs := []string{
		refNumber,
		"$(triple(inputs.n))",
		"${ return inputs.arr.length; }",
		"x" + refString + "y",
	}

	var group sync.WaitGroup

	for range 8 {
		for _, expr := range exprs {
			group.Go(func() {
				_, err := evaluator.Eval(expr, testContext())
				if err != nil {
					t.Errorf("Eval(%q) returned error: %v", expr, err)
				}
			})
		}
	}

	group.Wait()
}

// TestEvalJavaScriptRejectsAnUnencodableContext covers runJS's e.prepare error
// branch and, inside it, setJSGlobals' jsonParse-error branch: NaN has no JSON
// spelling, so EncodeJSON renders it as the literal token NaN, which the
// sandbox's own JSON.parse then rejects while installing the parameter context.
func TestEvalJavaScriptRejectsAnUnencodableContext(t *testing.T) {
	t.Parallel()

	_, err := NewEvaluator(WithJS(nil)).Eval("$(1)", &EvalContext{Self: math.NaN()})
	if !errors.Is(err, ErrExpressionEval) {
		t.Fatalf("Eval with an unencodable context error = %v, want ErrExpressionEval", err)
	}
}

// TestEvalJavaScriptRejectsAnUnparsableExpressionLib covers prepare's
// expressionLib-compile-error branch.
func TestEvalJavaScriptRejectsAnUnparsableExpressionLib(t *testing.T) {
	t.Parallel()

	_, err := NewEvaluator(WithJS([]string{"function( {"})).Eval("$(1)", testContext())
	if !errors.Is(err, ErrExpressionSyntax) {
		t.Fatalf("Eval with an unparsable expressionLib error = %v, want ErrExpressionSyntax", err)
	}
}

// TestRecoverPanicConvertsAPanicToAnError covers recoverPanic directly, since
// there is no stable way to make goja itself panic on demand.
func TestRecoverPanicConvertsAPanicToAnError(t *testing.T) {
	t.Parallel()

	_, err := recoverPanic(func() (goja.Value, error) {
		panic("boom")
	})
	if !errors.Is(err, ErrExpressionEval) {
		t.Fatalf("recoverPanic error = %v, want ErrExpressionEval", err)
	}

	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error %q does not mention the panic value", err)
	}
}

// TestRecoverPanicPassesThroughANormalResult is recoverPanic's non-panicking
// path, pinned alongside the panic case so the two are read together.
func TestRecoverPanicPassesThroughANormalResult(t *testing.T) {
	t.Parallel()

	want := goja.New().ToValue(1)

	value, err := recoverPanic(func() (goja.Value, error) {
		return want, nil
	})
	if err != nil || value != want {
		t.Errorf("recoverPanic = %v, %v, want %v, nil", value, err, want)
	}
}

// TestJsTimeoutDefaultsWhenUnset covers jsTimeout's own fallback, which
// NewEvaluator and WithTimeout never leave reachable: both already resolve a
// non-positive duration to DefaultEvalTimeout before it is stored.
func TestJsTimeoutDefaultsWhenUnset(t *testing.T) {
	t.Parallel()

	e := &Evaluator{jsEnabled: true}
	if got := e.jsTimeout(); got != DefaultEvalTimeout {
		t.Errorf("jsTimeout() on a zero-timeout Evaluator = %s, want %s", got, DefaultEvalTimeout)
	}
}

// TestConvertJSErrorFallsBackForAPlainError covers convertJSError's catch-all,
// reached by an error that is neither a *goja.InterruptedError nor a
// *goja.Exception.
func TestConvertJSErrorFallsBackForAPlainError(t *testing.T) {
	t.Parallel()

	err := convertJSError(errSynthetic, time.Second)
	if !errors.Is(err, ErrExpressionEval) {
		t.Errorf("convertJSError = %v, want it to wrap ErrExpressionEval", err)
	}

	if !strings.Contains(err.Error(), errSynthetic.Error()) {
		t.Errorf("error %q does not mention the original error", err)
	}
}

// TestSetJSGlobalsBranches drives setJSGlobals directly against a hand-built
// sandbox, covering the three failure branches ordinary evaluation cannot
// reach: an unencodable context, a JSON.parse replaced with something that
// does not return an object, and a global that cannot be assigned.
func TestSetJSGlobalsBranches(t *testing.T) {
	t.Parallel()

	t.Run("an unencodable context", func(t *testing.T) {
		t.Parallel()

		err := setJSGlobals(goja.New(), &EvalContext{Self: math.NaN()})
		if !errors.Is(err, ErrExpressionEval) {
			t.Errorf("setJSGlobals = %v, want ErrExpressionEval", err)
		}
	})

	t.Run("JSON.parse returns something that is not an object", func(t *testing.T) {
		t.Parallel()

		sandbox := goja.New()

		_, err := sandbox.RunString(`JSON.parse = function () { return 42; };`)
		if err != nil {
			t.Fatalf("RunString: %v", err)
		}

		err = setJSGlobals(sandbox, &EvalContext{})
		if !errors.Is(err, ErrExpressionEval) {
			t.Errorf("setJSGlobals = %v, want ErrExpressionEval", err)
		}
	})

	t.Run("a global cannot be assigned", func(t *testing.T) {
		t.Parallel()

		sandbox := goja.New()

		_, err := sandbox.RunString(
			`Object.defineProperty(this, "inputs", {value: 1, writable: false, configurable: false});`,
		)
		if err != nil {
			t.Fatalf("RunString: %v", err)
		}

		err = setJSGlobals(sandbox, &EvalContext{})
		if !errors.Is(err, ErrExpressionEval) {
			t.Errorf("setJSGlobals = %v, want ErrExpressionEval", err)
		}
	})
}

// TestJsonTextPropagatesAJsonMethodError covers jsonText's own error branch,
// reached when JSON.stringify is unavailable.
func TestJsonTextPropagatesAJsonMethodError(t *testing.T) {
	t.Parallel()

	sandbox := goja.New()

	_, err := sandbox.RunString(`JSON.stringify = undefined;`)
	if err != nil {
		t.Fatalf("RunString: %v", err)
	}

	_, err = jsonText(sandbox, sandbox.ToValue(1))
	if !errors.Is(err, ErrExpressionEval) {
		t.Errorf("jsonText = %v, want ErrExpressionEval", err)
	}
}

// TestJsonMethodReportsAnUnavailableGlobal and
// TestJsonMethodReportsAnUnavailableMethod cover jsonMethod's two failure
// branches directly.
func TestJsonMethodReportsAnUnavailableGlobal(t *testing.T) {
	t.Parallel()

	sandbox := goja.New()

	err := sandbox.Set("JSON", goja.Undefined())
	if err != nil {
		t.Fatalf("Set: %v", err)
	}

	_, err = jsonMethod(sandbox, "parse")
	if !errors.Is(err, ErrExpressionEval) {
		t.Errorf("jsonMethod = %v, want ErrExpressionEval", err)
	}
}

func TestJsonMethodReportsAnUnavailableMethod(t *testing.T) {
	t.Parallel()

	sandbox := goja.New()

	_, err := sandbox.RunString(`JSON.parse = undefined;`)
	if err != nil {
		t.Fatalf("RunString: %v", err)
	}

	_, err = jsonMethod(sandbox, "parse")
	if !errors.Is(err, ErrExpressionEval) {
		t.Errorf("jsonMethod = %v, want ErrExpressionEval", err)
	}
}

// TestJsonParsePropagatesAJsonMethodError and TestJsonParseReportsMalformedText
// cover jsonParse's two failure branches.
func TestJsonParsePropagatesAJsonMethodError(t *testing.T) {
	t.Parallel()

	sandbox := goja.New()

	_, err := sandbox.RunString(`JSON.parse = undefined;`)
	if err != nil {
		t.Fatalf("RunString: %v", err)
	}

	_, err = jsonParse(sandbox, "{}")
	if !errors.Is(err, ErrExpressionEval) {
		t.Errorf("jsonParse = %v, want ErrExpressionEval", err)
	}
}

func TestJsonParseReportsMalformedText(t *testing.T) {
	t.Parallel()

	_, err := jsonParse(goja.New(), "not valid json{")
	if !errors.Is(err, ErrExpressionEval) {
		t.Errorf("jsonParse = %v, want ErrExpressionEval", err)
	}
}

// TestDecodeJSONTextReportsMalformedResult covers decodeJSONText's own error
// branch directly.
func TestDecodeJSONTextReportsMalformedResult(t *testing.T) {
	t.Parallel()

	_, err := decodeJSONText("not json")
	if !errors.Is(err, ErrExpressionEval) {
		t.Errorf("decodeJSONText = %v, want ErrExpressionEval", err)
	}
}

// TestNumberValueFallsBackToStringForUnparsableNumbers covers numberValue's
// final fallback, reached when a [json.Number] parses as neither an int64 nor a
// float64.
func TestNumberValueFallsBackToStringForUnparsableNumbers(t *testing.T) {
	t.Parallel()

	const text = "not-a-number"

	if got := numberValue(json.Number(text)); got != text {
		t.Errorf("numberValue(%q) = %#v, want the original text", text, got)
	}
}
