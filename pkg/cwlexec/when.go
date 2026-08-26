package cwlexec

import (
	"errors"
	"fmt"
	"strings"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// ErrWhenNotBoolean reports a `when` expression that evaluated successfully but produced a value
// other than true or false.
//
// Spec, Conditional execution: `when` is "an expression that must be evaluated with `inputs` bound
// to the step input object (or individual scatter job), and returns a boolean value. It is an
// error if this expression returns a value other than `true` or `false`." There is deliberately no
// truthiness coercion, so the string "true", the numbers 1 and 0, null, an array and an object are
// each this error rather than a falsy or truthy value.
//
// This is distinct from a failure to evaluate at all, which surfaces as one of cwlcore's
// expression sentinels ([cwlcore.ErrJavaScript], [cwlcore.ErrNotParameterReference],
// [cwlcore.ErrExpressionSyntax], [cwlcore.ErrExpressionEval], [cwlcore.ErrExpressionTimeout])
// wrapped with the offending expression. Both are permanent failures for the step, but only this
// one means the workflow author wrote a well-behaved expression of the wrong type, so a scheduler
// that reports them differently can say so with a single [errors.Is].
var ErrWhenNotBoolean = errors.New("when expression did not return a boolean")

// EvalWhen evaluates a step's `when` expression against its resolved inputs and reports whether
// the step should run.
//
// The expression is evaluated with `inputs` bound to the step's input object, through the supplied
// evaluator — parameter references always, full JavaScript when the caller built the evaluator
// with [cwlcore.WithJS] because InlineJavascriptRequirement is in scope. The spec binds only
// `inputs` for a `when` expression, so `self` is null and the `runtime` resource fields are
// undefined: the condition is decided before any resources are reserved for the step. A nil eval
// is safe and supports parameter references only, which is what the spec requires of every
// conforming implementation regardless of any requirement being declared.
//
// A `when` that is absent, empty, or only whitespace returns true without evaluating anything: an
// unconditional step runs. Any other value is evaluated and must yield a boolean; see
// [ErrWhenNotBoolean] for why a non-boolean is an error rather than a truthiness test.
//
// EvalWhen holds no state between calls, so a scattered step evaluates its gate once per scatter
// sub-job simply by calling it once per [ScatterJob] with that job's Inputs. Spec: "The condition
// is evaluated after `scatter`, using the input object of each individual scatter job. This means
// over a set of scatter jobs, some may be executed and some may be skipped."
//
// A false result is not a failure. The step emits [SkippedOutputs] of its declared output ports
// instead of running.
func EvalWhen(when string, inputs map[string]any, eval *cwlcore.Evaluator) (bool, error) {
	expr := strings.TrimSpace(when)
	if expr == "" {
		return true, nil
	}

	// Deliberately Eval plus a type assertion rather than [cwlcore.Evaluator.EvalBool], which
	// does the same check: EvalBool reports a non-boolean as ErrExpressionEval, the very
	// sentinel a missing key or a thrown exception already uses, so delegating would fuse the
	// two failure modes the scheduler must tell apart. The rule is one line here and the
	// sentinel is the contract, so the duplication buys the classification.
	value, err := eval.Eval(expr, &cwlcore.EvalContext{Inputs: inputs})
	if err != nil {
		return false, fmt.Errorf("when %q: %w", when, err)
	}

	// A bool is the only accepted result. Both evaluation paths produce one for a JSON boolean:
	// a parameter reference hands back the input value as decoded, and a JavaScript result is
	// round-tripped through JSON, which narrows true and false to a Go bool (and numbers to
	// int64 or float64, neither of which is a condition).
	condition, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%w: when %q evaluated to %s, want true or false",
			ErrWhenNotBoolean, when, cwlcore.TypeName(value))
	}

	return condition, nil
}

// SkippedOutputs builds the output object a skipped step emits: one entry per declared output
// port, each null.
//
// A step whose `when` evaluated to false does not run, but it is not a dead end. Spec: "A skipped
// step produces `null` for all output parameters", and when a scattered step's results "are
// gathered, skipped steps must be `null` in the output arrays". Those nulls flow downstream
// exactly like real values, so a gated branch quietly produces nulls rather than deadlocking the
// steps that consume it — which is why every declared port must be present in the map rather than
// merely absent. The status that accompanies these outputs is StatusSkipped.
//
// The returned map is freshly allocated and shares nothing with declaredOut or with the result of
// any other call, so the caller may retain and mutate it. A nil or empty declaredOut yields an
// empty, non-nil map. Repeated ids collapse into the single entry they name.
func SkippedOutputs(declaredOut []string) map[string]any {
	outputs := make(map[string]any, len(declaredOut))

	for _, id := range declaredOut {
		outputs[id] = nil
	}

	return outputs
}
