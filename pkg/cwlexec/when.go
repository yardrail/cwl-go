package cwlexec

import (
	"errors"
	"fmt"
	"strings"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// ErrWhenNotBoolean reports a `when` expression that returned a non-boolean value.
var ErrWhenNotBoolean = errors.New("when expression did not return a boolean")

// EvalWhen evaluates a step's `when` expression and reports whether the step should run.
// Empty or whitespace-only expressions return true. Non-boolean results are [ErrWhenNotBoolean].
func EvalWhen(when string, inputs map[string]any, eval *cwlcore.Evaluator) (bool, error) {
	expr := strings.TrimSpace(when)
	if expr == "" {
		return true, nil
	}

	// Eval, not EvalBool: we need ErrWhenNotBoolean distinct from ErrExpressionEval.
	value, err := eval.Eval(
		expr,
		&cwlcore.EvalContext{
			Inputs: inputs,
			Self:   nil,
			Runtime: cwlcore.RuntimeContext{
				Cores:      nil,
				RAM:        nil,
				OutdirSize: nil,
				TmpdirSize: nil,
				ExitCode:   nil,
				Outdir:     "",
				Tmpdir:     "",
			},
		},
	)
	if err != nil {
		return false, fmt.Errorf("when %q: %w", when, err)
	}

	condition, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%w: when %q evaluated to %s, want true or false",
			ErrWhenNotBoolean, when, cwlcore.TypeName(value))
	}

	return condition, nil
}

// SkippedOutputs returns a map with null for each declared output port.
func SkippedOutputs(declaredOut []string) map[string]any {
	outputs := make(map[string]any, len(declaredOut))

	for _, id := range declaredOut {
		outputs[id] = nil
	}

	return outputs
}
