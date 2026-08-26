package cwlexec

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

const (
	// flagKey is the input id the boolean fixtures hang off.
	flagKey = "flag"
	// refFlag reads the boolean input the fixtures set.
	refFlag = "$(inputs.flag)"
	// refValue reads the input the non-boolean fixtures park their odd value in.
	refValue = "$(inputs.v)"
	// literalTrue is the word "true" written with no expression around it, which is a plain
	// string and so not a condition.
	literalTrue = "true"
	// refMisspelled is the most common CWL authoring mistake in a gate: the parameter-context
	// root spelled "input" rather than "inputs".
	refMisspelled = "$(input.flag)"
)

// jsLib is an expressionLib fragment, so that the WithJS cases can prove the library is in scope
// for a `when` expression and not merely for the evaluator's own tests.
var jsLib = []string{"function isBig(n) { return n > 10; }"}

// noJS is an evaluator supporting parameter references only, the configuration of a step with no
// InlineJavascriptRequirement in scope.
func noJS() *cwlcore.Evaluator { return cwlcore.NewEvaluator() }

// withJS is an evaluator with InlineJavascriptRequirement in scope, carrying jsLib.
func withJS() *cwlcore.Evaluator { return cwlcore.NewEvaluator(cwlcore.WithJS(jsLib)) }

// whenCase is one row of the EvalWhen tables: an expression, the inputs it sees, the evaluator
// configuration it runs under, and either the boolean it must produce or the sentinel its failure
// must wrap.
type whenCase struct {
	inputs  map[string]any
	eval    func() *cwlcore.Evaluator
	wantErr error
	name    string
	when    string
	want    bool
}

// classifiers are every sentinel a caller may key on to decide what to tell the user about a
// failed gate. A failure must match exactly one of them, which is the whole point of the split
// between "the gate is the wrong type" and the several ways a gate can blow up.
var classifiers = []error{
	ErrWhenNotBoolean,
	cwlcore.ErrJavaScript,
	cwlcore.ErrNotParameterReference,
	cwlcore.ErrExpressionSyntax,
	cwlcore.ErrExpressionEval,
	cwlcore.ErrExpressionTimeout,
}

// run evaluates one row and asserts the outcome.
func (c whenCase) run(t *testing.T) {
	t.Helper()

	if c.wantErr == nil {
		c.assertRuns(t)

		return
	}

	c.assertFails(t)
}

// evaluator builds the row's evaluator. A row with none is run against a nil *Evaluator, which is
// a supported configuration and not merely an omission.
func (c whenCase) evaluator() *cwlcore.Evaluator {
	if c.eval == nil {
		return nil
	}

	return c.eval()
}

// assertRuns checks a row that must produce a verdict.
func (c whenCase) assertRuns(t *testing.T) {
	t.Helper()

	got, err := EvalWhen(c.when, c.inputs, c.evaluator())
	if err != nil {
		t.Fatalf("EvalWhen(%q) = _, %v; want %v, <nil>", c.when, err, c.want)
	}

	if got != c.want {
		t.Fatalf("EvalWhen(%q) = %v; want %v", c.when, got, c.want)
	}
}

// assertFails checks a row that must fail, and pins its classification in both directions: the
// expected sentinel matches and every other classifier does not.
func (c whenCase) assertFails(t *testing.T) {
	t.Helper()

	got, err := EvalWhen(c.when, c.inputs, c.evaluator())
	if err == nil {
		t.Fatalf("EvalWhen(%q) = %v, <nil>; want error wrapping %v", c.when, got, c.wantErr)
	}

	if got {
		t.Fatalf("EvalWhen(%q) = true alongside error %v; a failed gate must not admit the step", c.when, err)
	}

	for _, sentinel := range classifiers {
		want := errors.Is(c.wantErr, sentinel)
		if errors.Is(err, sentinel) != want {
			t.Errorf("EvalWhen(%q) errors.Is(err, %v) = %v; want %v (err = %v)",
				c.when, sentinel, !want, want, err)
		}
	}
}

func TestEvalWhenAbsent(t *testing.T) {
	t.Parallel()

	// A nil evaluator is deliberate: nothing may be evaluated for an absent condition, and an
	// evaluator that did run would report these strings as non-boolean results.
	for _, tc := range []whenCase{
		{name: "empty", when: "", want: true},
		{name: "spaces", when: "   ", want: true},
		{name: "tab and newline", when: "\t\n", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t)
		})
	}
}

func TestEvalWhenParameterReference(t *testing.T) {
	t.Parallel()

	truthy := map[string]any{flagKey: true}
	falsy := map[string]any{flagKey: false}

	cases := []whenCase{
		{name: "true, no js", when: refFlag, inputs: truthy, eval: noJS, want: true},
		{name: "false, no js", when: refFlag, inputs: falsy, eval: noJS, want: false},
		{name: "true, js", when: refFlag, inputs: truthy, eval: withJS, want: true},
		{name: "false, js", when: refFlag, inputs: falsy, eval: withJS, want: false},
		{name: "nil evaluator falls back to parameter references", when: refFlag, inputs: truthy, want: true},
		{
			name: "bracket segment", when: `$(inputs["flag"])`, inputs: truthy, eval: noJS, want: true,
		},
		{
			name:   "nested field",
			when:   "$(inputs.opts.enabled)",
			inputs: map[string]any{"opts": map[string]any{"enabled": false}},
			eval:   noJS,
			want:   false,
		},
		{
			name:   "indexed element",
			when:   "$(inputs.flags[1])",
			inputs: map[string]any{"flags": []any{false, true}},
			eval:   noJS,
			want:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t)
		})
	}
}

func TestEvalWhenJavaScript(t *testing.T) {
	t.Parallel()

	cases := []whenCase{
		{name: "literal true", when: "$(true)", eval: withJS, want: true},
		{name: "literal false", when: "$(false)", eval: withJS, want: false},
		{name: "comparison", when: "$(inputs.n > 2)", inputs: map[string]any{"n": int64(3)}, eval: withJS, want: true},
		{name: "negation", when: "$(!inputs.flag)", inputs: map[string]any{flagKey: true}, eval: withJS, want: false},
		{
			name:   "function body",
			when:   "${ return inputs.name.length > 0; }",
			inputs: map[string]any{"name": "x"},
			eval:   withJS,
			want:   true,
		},
		{
			name:   "expressionLib function",
			when:   "$(isBig(inputs.n))",
			inputs: map[string]any{"n": int64(11)},
			eval:   withJS,
			want:   true,
		},
		{
			name:   "expressionLib function, false",
			when:   "${ return isBig(inputs.n); }",
			inputs: map[string]any{"n": int64(9)},
			eval:   withJS,
			want:   false,
		},
		{
			// Strict equality against a JS null is how a real workflow gates on an upstream
			// step having been skipped.
			name:   "null check",
			when:   "$(inputs.upstream !== null)",
			inputs: map[string]any{"upstream": nil},
			eval:   withJS,
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t)
		})
	}
}

func TestEvalWhenWithoutJavaScript(t *testing.T) {
	t.Parallel()

	cases := []whenCase{
		{
			name:    "function body needs js",
			when:    "${ return true; }",
			eval:    noJS,
			wantErr: cwlcore.ErrJavaScript,
		},
		{
			name:    "expression that is not a parameter reference needs js",
			when:    "$(inputs.n > 2)",
			inputs:  map[string]any{"n": int64(3)},
			eval:    noJS,
			wantErr: cwlcore.ErrJavaScript,
		},
		{
			name:    "function body needs js, nil evaluator",
			when:    "${ return true; }",
			wantErr: cwlcore.ErrJavaScript,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t)
		})
	}
}

// TestEvalWhenUnknownRootSymbol covers the fragments that parse as parameter references but name a
// symbol outside the parameter context. They are reported as [cwlcore.ErrNotParameterReference],
// never as [cwlcore.ErrJavaScript], because a misspelled root and a genuine JavaScript global are
// lexically identical here and "declare InlineJavascriptRequirement" is actively wrong advice for
// the misspelling — which is the more common mistake by far.
func TestEvalWhenUnknownRootSymbol(t *testing.T) {
	t.Parallel()

	cases := []whenCase{
		{
			name:    "misspelled inputs root, no js",
			when:    refMisspelled,
			inputs:  map[string]any{flagKey: true},
			eval:    noJS,
			wantErr: cwlcore.ErrNotParameterReference,
		},
		{
			name:    "misspelled inputs root, nil evaluator",
			when:    refMisspelled,
			inputs:  map[string]any{flagKey: true},
			wantErr: cwlcore.ErrNotParameterReference,
		},
		{
			name:    "javascript literal, no js",
			when:    "$(true)",
			eval:    noJS,
			wantErr: cwlcore.ErrNotParameterReference,
		},
		{
			name:    "javascript global, no js",
			when:    "$(Math.PI)",
			eval:    noJS,
			wantErr: cwlcore.ErrNotParameterReference,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t)
		})
	}
}

// TestEvalWhenNotBoolean pins the spec's "It is an error if this expression returns a value other
// than true or false" across every JSON type, through both evaluation paths. No truthiness
// coercion: "true", 1 and 0 are errors, not conditions.
func TestEvalWhenNotBoolean(t *testing.T) {
	t.Parallel()

	cases := []whenCase{
		{name: "bare literal string", when: literalTrue, eval: noJS, wantErr: ErrWhenNotBoolean},
		{name: "bare literal string false", when: "false", eval: noJS, wantErr: ErrWhenNotBoolean},
		{
			name:    `the string "true"`,
			when:    refValue,
			inputs:  map[string]any{"v": literalTrue},
			eval:    noJS,
			wantErr: ErrWhenNotBoolean,
		},
		{
			name:    "an empty string",
			when:    refValue,
			inputs:  map[string]any{"v": ""},
			eval:    noJS,
			wantErr: ErrWhenNotBoolean,
		},
		{
			name:    "the number 1",
			when:    refValue,
			inputs:  map[string]any{"v": int64(1)},
			eval:    noJS,
			wantErr: ErrWhenNotBoolean,
		},
		{
			name:    "the number 0",
			when:    refValue,
			inputs:  map[string]any{"v": int64(0)},
			eval:    noJS,
			wantErr: ErrWhenNotBoolean,
		},
		{
			name:    "a float",
			when:    refValue,
			inputs:  map[string]any{"v": 1.5},
			eval:    noJS,
			wantErr: ErrWhenNotBoolean,
		},
		{
			name:    "an explicit null",
			when:    "$(null)",
			eval:    noJS,
			wantErr: ErrWhenNotBoolean,
		},
		{
			name:    "a null input",
			when:    refValue,
			inputs:  map[string]any{"v": nil},
			eval:    noJS,
			wantErr: ErrWhenNotBoolean,
		},
		{
			name:    "an array",
			when:    refValue,
			inputs:  map[string]any{"v": []any{true}},
			eval:    noJS,
			wantErr: ErrWhenNotBoolean,
		},
		{
			name:    "an empty array",
			when:    refValue,
			inputs:  map[string]any{"v": make([]any, 0)},
			eval:    noJS,
			wantErr: ErrWhenNotBoolean,
		},
		{
			name:    "an object",
			when:    refValue,
			inputs:  map[string]any{"v": map[string]any{"a": true}},
			eval:    noJS,
			wantErr: ErrWhenNotBoolean,
		},
		{
			// A caller may hand in inputs that are not in the canonical decoded shape; those
			// are still not booleans, and the error must name what did arrive.
			name:    "a value of some other Go type",
			when:    refValue,
			inputs:  map[string]any{"v": []string{"a"}},
			eval:    noJS,
			wantErr: ErrWhenNotBoolean,
		},
		{
			name:    "an interpolated string of two references",
			when:    "$(inputs.flag)$(inputs.flag)",
			inputs:  map[string]any{flagKey: true},
			eval:    noJS,
			wantErr: ErrWhenNotBoolean,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t)
		})
	}
}

// TestEvalWhenNotBooleanUnderJavaScript repeats the type check through the JavaScript path, where
// truthiness coercion would be the natural thing for the engine to do and must not happen.
func TestEvalWhenNotBooleanUnderJavaScript(t *testing.T) {
	t.Parallel()

	cases := []whenCase{
		{name: "string literal", when: `$("true")`, eval: withJS, wantErr: ErrWhenNotBoolean},
		{name: "number one", when: "$(1)", eval: withJS, wantErr: ErrWhenNotBoolean},
		{name: "number zero", when: "$(0)", eval: withJS, wantErr: ErrWhenNotBoolean},
		{name: "null", when: "$(null)", eval: withJS, wantErr: ErrWhenNotBoolean},
		{name: "array literal", when: "$([1, 2])", eval: withJS, wantErr: ErrWhenNotBoolean},
		{name: "object literal", when: "$({a: 1})", eval: withJS, wantErr: ErrWhenNotBoolean},
		{
			name:    "function body returning a truthy string",
			when:    `${ return "yes"; }`,
			eval:    withJS,
			wantErr: ErrWhenNotBoolean,
		},
		{
			name:    "logical or yielding a value rather than a boolean",
			when:    "$(inputs.v || 0)",
			inputs:  map[string]any{"v": nil},
			eval:    withJS,
			wantErr: ErrWhenNotBoolean,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t)
		})
	}
}

// TestEvalWhenEvaluationFailure covers expressions that never produce a value at all. Each must
// wrap a cwlcore sentinel and must not be mistaken for a non-boolean result.
func TestEvalWhenEvaluationFailure(t *testing.T) {
	t.Parallel()

	cases := []whenCase{
		{
			name:    "missing input, no js",
			when:    "$(inputs.absent)",
			inputs:  map[string]any{flagKey: true},
			eval:    noJS,
			wantErr: cwlcore.ErrExpressionEval,
		},
		{
			name:    "missing input, js",
			when:    "$(inputs.absent.deeper)",
			inputs:  map[string]any{flagKey: true},
			eval:    withJS,
			wantErr: cwlcore.ErrExpressionEval,
		},
		{
			name:    "index out of range",
			when:    "$(inputs.flags[3])",
			inputs:  map[string]any{"flags": []any{true}},
			eval:    noJS,
			wantErr: cwlcore.ErrExpressionEval,
		},
		{
			name:    "thrown exception",
			when:    `${ throw new Error("boom"); }`,
			eval:    withJS,
			wantErr: cwlcore.ErrExpressionEval,
		},
		{
			// The same misspelling as TestEvalWhenUnknownRootSymbol, but with JavaScript in
			// scope the fragment reaches the engine and dies as an undefined reference.
			name:    "misspelled inputs root, js",
			when:    refMisspelled,
			inputs:  map[string]any{flagKey: true},
			eval:    withJS,
			wantErr: cwlcore.ErrExpressionEval,
		},
		{
			name:    "unterminated fragment",
			when:    "$(inputs.flag",
			inputs:  map[string]any{flagKey: true},
			eval:    noJS,
			wantErr: cwlcore.ErrExpressionSyntax,
		},
		{
			name:    "javascript syntax error",
			when:    "${ return ; ; ) }",
			eval:    withJS,
			wantErr: cwlcore.ErrExpressionSyntax,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t)
		})
	}
}

// TestEvalWhenErrorMessage checks that a failure names the offending expression, since a workflow
// may have dozens of gated steps and the sentinel alone does not say which one failed, and that a
// non-boolean names the type it did produce in cwlcore's shared vocabulary.
func TestEvalWhenErrorMessage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		value    any
		when     string
		contains string
	}{
		{when: refValue, value: literalTrue, contains: cwlcore.TypeName("")},
		{when: refValue, value: int64(1), contains: cwlcore.TypeName(int64(0))},
		{when: refValue, value: nil, contains: cwlcore.TypeName(nil)},
		{when: "$(inputs.absent)", value: literalTrue, contains: "has no field"},
	}

	for _, tc := range cases {
		_, err := EvalWhen(tc.when, map[string]any{"v": tc.value}, nil)
		if err == nil {
			t.Fatalf("EvalWhen(%q) with v = %v succeeded; want an error", tc.when, tc.value)
		}

		if !strings.Contains(err.Error(), tc.when) {
			t.Errorf("EvalWhen(%q) error = %q; want it to quote the expression", tc.when, err)
		}

		if !strings.Contains(err.Error(), tc.contains) {
			t.Errorf("EvalWhen(%q) with v = %v error = %q; want it to mention %q",
				tc.when, tc.value, err, tc.contains)
		}
	}
}

// TestEvalWhenPerScatterJob exercises the property the scheduler depends on: one shared evaluator,
// one gate expression, a different verdict per scatter sub-job.
func TestEvalWhenPerScatterJob(t *testing.T) {
	t.Parallel()

	plan, err := ExpandScatter(
		map[string]any{"n": []any{int64(1), int64(20), int64(5), int64(30)}},
		[]string{"n"},
		Dotproduct,
	)
	if err != nil {
		t.Fatalf("ExpandScatter: %v", err)
	}

	eval := withJS()
	want := []bool{false, true, false, true}
	got := make([]bool, 0, len(plan.Jobs))

	for _, job := range plan.Jobs {
		run, evalErr := EvalWhen("$(isBig(inputs.n))", job.Inputs, eval)
		if evalErr != nil {
			t.Fatalf("EvalWhen for job %v: %v", job.Index, evalErr)
		}

		got = append(got, run)
	}

	if !slices.Equal(got, want) {
		t.Errorf("per-sub-job gates = %v; want %v", got, want)
	}
}

// TestEvalWhenConcurrent runs one evaluator from many goroutines, as a scattered step's gate does,
// so that -race covers the shared compiled-program cache behind it.
func TestEvalWhenConcurrent(t *testing.T) {
	t.Parallel()

	const workers = 16

	eval := withJS()

	var wg sync.WaitGroup

	for i := range workers {
		wg.Go(func() {
			inputs := map[string]any{"n": int64(i)}

			run, err := EvalWhen("$(isBig(inputs.n))", inputs, eval)
			if err != nil {
				t.Errorf("EvalWhen(n=%d): %v", i, err)

				return
			}

			if run != (i > 10) {
				t.Errorf("EvalWhen(n=%d) = %v; want %v", i, run, i > 10)
			}
		})
	}

	wg.Wait()
}

func TestSkippedOutputs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		declared []string
		want     []string
	}{
		{name: "nil", declared: nil, want: make([]string, 0)},
		{name: "empty", declared: make([]string, 0), want: make([]string, 0)},
		{name: "single", declared: []string{outPort}, want: []string{outPort}},
		{
			name:     "many",
			declared: []string{numPort, namePort, outPort, "extra", "final"},
			want:     []string{"extra", "final", namePort, numPort, outPort},
		},
		{
			name:     "repeated ids collapse",
			declared: []string{outPort, outPort},
			want:     []string{outPort},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertSkipped(t, tc.declared, tc.want)
		})
	}
}

// assertSkipped checks that a skipped step's output object has exactly the declared ports, each
// null, and is a real map rather than nil.
func assertSkipped(t *testing.T, declared, want []string) {
	t.Helper()

	outputs := SkippedOutputs(declared)
	if outputs == nil {
		t.Fatal("SkippedOutputs returned a nil map; a skipped step must emit an object")
	}

	keys := slices.Sorted(maps.Keys(outputs))
	if !slices.Equal(keys, want) {
		t.Fatalf("SkippedOutputs(%v) keys = %v; want %v", declared, keys, want)
	}

	for _, key := range keys {
		if outputs[key] != nil {
			t.Errorf("SkippedOutputs(%v)[%q] = %v; want nil", declared, key, outputs[key])
		}
	}
}

// TestSkippedOutputsIndependent pins that the caller owns the result outright: two calls share no
// state, and neither writes back into the declared-port slice.
func TestSkippedOutputsIndependent(t *testing.T) {
	t.Parallel()

	declared := []string{numPort, namePort}

	first := SkippedOutputs(declared)
	second := SkippedOutputs(declared)

	first[numPort] = 42
	delete(first, namePort)

	if got, ok := second[numPort]; !ok || got != nil {
		t.Errorf("second[%q] = %v, present %v; want nil, present true", numPort, got, ok)
	}

	if _, ok := second[namePort]; !ok {
		t.Errorf("second lost %q after the first map was mutated", namePort)
	}

	if !slices.Equal(declared, []string{numPort, namePort}) {
		t.Errorf("declared ports mutated to %v", declared)
	}
}
