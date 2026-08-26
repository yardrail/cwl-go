package cwlexec

import (
	"errors"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// expressionToolBuiltIn returns the built-in registered for the ExpressionTool class, so the tests
// exercise it exactly as a run would reach it.
func expressionToolBuiltIn(t *testing.T) StepHandler {
	t.Helper()

	handler, found := NewRegistry().Handler(Class(cwlcore.ClassExpressionTool))
	if !found {
		t.Fatal("NewRegistry must carry an ExpressionTool handler")
	}

	return handler
}

// runExpressionTool executes one call through the built-in and normalizes the outcome the way the
// scheduler will.
func runExpressionTool(t *testing.T, call *StepCall) (Result, error) {
	t.Helper()

	return Outcome(expressionToolBuiltIn(t).Execute(t.Context(), call))
}

func TestExpressionToolSuccess(t *testing.T) {
	t.Parallel()

	cases := []struct {
		inputs map[string]any
		want   map[string]any
		scope  *cwlcore.RequirementScope
		name   string
		expr   string
		outs   []string
	}{
		{
			name:  "javascript function body",
			expr:  `${return {"out": "hello"};}`,
			outs:  []string{outID},
			scope: jsScope(nil),
			want:  map[string]any{outPort: "hello"},
		},
		{
			name:  "expressionLib is in scope",
			expr:  `${return {"out": greet()};}`,
			outs:  []string{outID},
			scope: jsScope([]string{`function greet() { return "hi"; }`}),
			want:  map[string]any{outPort: "hi"},
		},
		{
			// A whole-object parameter reference needs no InlineJavascriptRequirement: every
			// conforming implementation evaluates parameter references, so the built-in must
			// not demand JavaScript that this document never uses.
			name:   "parameter reference without javascript",
			expr:   "$(inputs.obj)",
			outs:   []string{outID, extraID},
			inputs: map[string]any{"obj": map[string]any{outPort: "a", extraPort: "b"}},
			want:   map[string]any{outPort: "a", extraPort: "b"},
		},
		{
			// A declared port the expression does not mention is null, not a failure: null is a
			// legal value for an optional output, and an ExpressionTool's outputs are exempt
			// from type validation, so there is nothing to reject it against.
			name:  "declared port the expression omits becomes null",
			expr:  `${return {};}`,
			outs:  []string{outID, extraID},
			scope: jsScope(nil),
			want:  map[string]any{outPort: nil, extraPort: nil},
		},
		{
			name:  "no declared ports",
			expr:  `${return {};}`,
			outs:  nil,
			scope: jsScope(nil),
			want:  make(map[string]any),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			call := &StepCall{
				StepID:       stepID,
				Process:      newExpressionTool(tc.expr, tc.outs...),
				Class:        Class(cwlcore.ClassExpressionTool),
				Inputs:       tc.inputs,
				Requirements: tc.scope,
			}

			result, err := runExpressionTool(t, call)
			if err != nil {
				t.Fatalf("Execute error = %v", err)
			}

			if result.Status != StatusSuccess {
				t.Fatalf("status = %q, want success", result.Status)
			}

			if !maps.Equal(result.Outputs, tc.want) {
				t.Fatalf("outputs = %#v, want %#v", result.Outputs, tc.want)
			}
		})
	}
}

func TestExpressionToolOutputsAreNotTypeValidated(t *testing.T) {
	t.Parallel()

	// The schema says an ExpressionToolOutputParameter's type "just acts as a hint, as the
	// outputs of an ExpressionTool process are always considered valid". So a port declared File
	// holding a number is not this handler's error to raise.
	tool := newExpressionTool(`${return {"out": 3};}`, outID)
	tool.Outputs[0].Type = cwlcore.NewPrimitiveType("File")

	call := &StepCall{StepID: stepID, Process: tool, Requirements: jsScope(nil)}

	result, err := runExpressionTool(t, call)
	if err != nil {
		t.Fatalf("Execute error = %v; an ExpressionTool's outputs must not be type-validated", err)
	}

	if result.Outputs[outPort] != int64(3) {
		t.Fatalf("outputs = %#v, want the value the expression produced", result.Outputs)
	}
}

// failureCase is one row of the ExpressionTool failure table: a tool that cannot produce outputs,
// the sentinel its failure must wrap, and any text the message must carry so that the author can
// act on it.
type failureCase struct {
	inputs   map[string]any
	scope    *cwlcore.RequirementScope
	wantErr  error
	name     string
	expr     string
	wantText string
	outs     []string
}

// run executes the row and asserts that it failed the way the row says.
func (c *failureCase) run(t *testing.T) {
	t.Helper()

	call := &StepCall{
		StepID:       stepID,
		Process:      newExpressionTool(c.expr, c.outs...),
		Inputs:       c.inputs,
		Requirements: c.scope,
	}

	result, err := runExpressionTool(t, call)
	c.assertFailed(t, result, err)
	c.assertExplained(t, err)
}

// assertFailed checks the outcome: a permanent failure wrapping the row's sentinel, carrying no
// outputs at all.
func (c *failureCase) assertFailed(t *testing.T, result Result, err error) {
	t.Helper()

	if !errors.Is(err, c.wantErr) {
		t.Fatalf("error = %v, want %v", err, c.wantErr)
	}

	if result.Status != StatusPermanentFail {
		t.Fatalf("status = %q, want a permanent failure", result.Status)
	}

	if result.Outputs != nil {
		t.Fatalf("a failed ExpressionTool must produce no outputs, got %#v", result.Outputs)
	}
}

// assertExplained checks that the message names the step and whatever else the row requires.
func (c *failureCase) assertExplained(t *testing.T, err error) {
	t.Helper()

	if c.wantText != "" && !strings.Contains(err.Error(), c.wantText) {
		t.Fatalf("error %q does not mention %q", err, c.wantText)
	}

	if !strings.Contains(err.Error(), stepID) {
		t.Fatalf("error %q does not name the step", err)
	}
}

func TestExpressionToolFailures(t *testing.T) {
	t.Parallel()

	cases := []failureCase{
		{
			name:     "function body without InlineJavascriptRequirement",
			expr:     `${return {"out": 1};}`,
			outs:     []string{outID},
			wantErr:  cwlcore.ErrJavaScript,
			wantText: "declare an InlineJavascriptRequirement",
		},
		{
			name:    "expression that fails to evaluate",
			expr:    "$(inputs.missing.deeper)",
			outs:    []string{outID},
			wantErr: cwlcore.ErrExpressionEval,
		},
		{
			name:    "result is a scalar",
			expr:    "$(inputs.n)",
			outs:    []string{outID},
			inputs:  map[string]any{"n": int64(3)},
			wantErr: ErrExpressionToolResult,
		},
		{
			name:    "result is an array",
			expr:    `${return [1, 2];}`,
			outs:    []string{outID},
			scope:   jsScope(nil),
			wantErr: ErrExpressionToolResult,
		},
		{
			name:    "result is null",
			expr:    `${return null;}`,
			outs:    []string{outID},
			scope:   jsScope(nil),
			wantErr: ErrExpressionToolResult,
		},
		{
			name:    "expression is a plain literal",
			expr:    "not an expression at all",
			outs:    []string{outID},
			wantErr: ErrExpressionToolResult,
		},
		{
			name:     "result names an undeclared output port",
			expr:     `${return {"out": 1, "typo": 2};}`,
			outs:     []string{outID},
			scope:    jsScope(nil),
			wantErr:  ErrUndeclaredOutput,
			wantText: "typo",
		},
		{
			name:     "result names only undeclared ports",
			expr:     `${return {"b": 1, "a": 2};}`,
			outs:     nil,
			scope:    jsScope(nil),
			wantErr:  ErrUndeclaredOutput,
			wantText: `["a" "b"]`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t)
		})
	}
}

func TestExpressionToolWrongProcessClass(t *testing.T) {
	t.Parallel()

	call := &StepCall{StepID: stepID, Process: &cwlcore.CommandLineTool{}}

	_, err := runExpressionTool(t, call)
	if !errors.Is(err, ErrWrongProcessClass) {
		t.Fatalf("error = %v, want ErrWrongProcessClass", err)
	}
}

func TestExpressionToolDoesNotBlameTheDocumentForASchedulerBug(t *testing.T) {
	t.Parallel()

	// InlineJavascriptRequirement is declared, but the evaluator the scheduler supplied does not
	// have JavaScript enabled. Telling the author to declare what they already declared would
	// send them looking in the wrong place.
	call := &StepCall{
		StepID:       stepID,
		Process:      newExpressionTool(`${return {"out": 1};}`, outID),
		Requirements: jsScope(nil),
		Eval:         cwlcore.NewEvaluator(),
	}

	_, err := runExpressionTool(t, call)
	if !errors.Is(err, cwlcore.ErrJavaScript) {
		t.Fatalf("error = %v, want ErrJavaScript", err)
	}

	if strings.Contains(err.Error(), "declare an InlineJavascriptRequirement") {
		t.Fatalf("error %q blames the document for a scheduler bug", err)
	}
}

func TestEvaluatorForNilScope(t *testing.T) {
	t.Parallel()

	value, err := EvaluatorFor(nil).Eval("$(inputs.x)", &cwlcore.EvalContext{Inputs: map[string]any{"x": "v"}})
	if err != nil {
		t.Fatalf("Eval error = %v", err)
	}

	if value != "v" {
		t.Fatalf("value = %#v, want \"v\"", value)
	}

	_, err = EvaluatorFor(nil).Eval("${return 1;}", nil)
	if !errors.Is(err, cwlcore.ErrJavaScript) {
		t.Fatalf("error = %v, want ErrJavaScript", err)
	}
}

func TestEvaluatorForWithoutTheRequirement(t *testing.T) {
	t.Parallel()

	scope := cwlcore.NewScope(&cwlcore.ExpressionTool{})

	_, err := EvaluatorFor(scope).Eval("${return 1;}", nil)
	if !errors.Is(err, cwlcore.ErrJavaScript) {
		t.Fatalf("error = %v, want ErrJavaScript", err)
	}
}

func TestEvaluatorForHintEnablesJavascript(t *testing.T) {
	t.Parallel()

	proc := &cwlcore.ExpressionTool{}
	proc.Hints = []cwlcore.Hint{&cwlcore.InlineJavascriptRequirement{ExpressionLib: []string{"var k = 7;"}}}

	value, err := EvaluatorFor(cwlcore.NewScope(proc)).Eval("${return k;}", nil)
	if err != nil {
		t.Fatalf("Eval error = %v", err)
	}

	if value != int64(7) {
		t.Fatalf("value = %#v, want 7", value)
	}
}

func TestEvaluatorForForeignHintOfTheSameClass(t *testing.T) {
	t.Parallel()

	// Hint is deliberately unsealed in cwlcore, so a downstream package can supply a hint of any
	// class — including this one. It is not an InlineJavascriptRequirement, and treating it as one
	// would run JavaScript the document never asked for.
	proc := &cwlcore.ExpressionTool{}
	proc.Hints = []cwlcore.Hint{foreignHint{}}

	_, err := EvaluatorFor(cwlcore.NewScope(proc)).Eval("${return 1;}", nil)
	if !errors.Is(err, cwlcore.ErrJavaScript) {
		t.Fatalf("error = %v, want ErrJavaScript", err)
	}
}

func TestEvaluatorForCallerOptionsWinLast(t *testing.T) {
	t.Parallel()

	eval := EvaluatorFor(jsScope(nil), cwlcore.WithTimeout(50*time.Millisecond))

	_, err := eval.Eval("${while (true) {}}", nil)
	if !errors.Is(err, cwlcore.ErrExpressionTimeout) {
		t.Fatalf("error = %v, want ErrExpressionTimeout", err)
	}
}

// foreignHint is a hint whose class collides with InlineJavascriptRequirement without being one.
type foreignHint struct{}

// Class returns the colliding class name.
func (foreignHint) Class() string {
	return cwlcore.ClassInlineJavascriptRequirement
}
