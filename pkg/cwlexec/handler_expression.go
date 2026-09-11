package cwlexec

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

var (
	// ErrExpressionToolResult reports an expression that returned a non-object value.
	ErrExpressionToolResult = errors.New("ExpressionTool expression did not return an object")
	// ErrUndeclaredOutput reports a result field not matching any declared output.
	ErrUndeclaredOutput = errors.New("expression result names an undeclared output parameter")
)

// expressionToolHandler is the built-in ExpressionTool handler. No container, no staging.
type expressionToolHandler struct{}

// Execute evaluates the tool's expression and binds results to output ports.
func (expressionToolHandler) Execute(ctx context.Context, call *StepCall) (Result, error) {
	tool, ok := call.Process.(*cwlcore.ExpressionTool)
	if !ok {
		return PermanentFail(fmt.Errorf("%w: %s is not an ExpressionTool", ErrWrongProcessClass, describe(call)))
	}

	evalContext := &cwlcore.EvalContext{Inputs: call.Inputs, Self: nil, Runtime: call.RuntimeContext()}

	value, err := call.Evaluator().Eval(string(tool.Expression), evalContext)
	if err != nil {
		return PermanentFail(fmt.Errorf("%s: %w", describe(call), annotateJavascript(err, call.Requirements)))
	}

	object, ok := value.(map[string]any)
	if !ok {
		return PermanentFail(fmt.Errorf("%w: %s returned %s",
			ErrExpressionToolResult, describe(call), cwlcore.TypeName(value)))
	}

	outputs, err := bindExpressionOutputs(tool.Outputs, object)
	if err != nil {
		return PermanentFail(fmt.Errorf("%s: %w", describe(call), err))
	}

	written, err := materializeExpressionOutputs(ctx, call, outputs)
	if err != nil {
		return PermanentFail(fmt.Errorf("%s: %w", describe(call), err))
	}

	return Success(written)
}

// bindExpressionOutputs maps expression results to declared output ports.
// Missing ports become null; undeclared fields are an error.
func bindExpressionOutputs(
	params []cwlcore.ExpressionToolOutputParameter,
	object map[string]any,
) (map[string]any, error) {
	outputs := make(map[string]any, len(params))

	for index := range params {
		key := ShortName(params[index].ID())
		outputs[key] = object[key]
	}

	undeclared := make([]string, 0, len(object))

	for key := range object {
		if _, declared := outputs[key]; !declared {
			undeclared = append(undeclared, key)
		}
	}

	if len(undeclared) > 0 {
		slices.Sort(undeclared)

		return nil, fmt.Errorf("%w: %q", ErrUndeclaredOutput, undeclared)
	}

	return outputs, nil
}

// annotateJavascript adds a "declare InlineJavascriptRequirement" hint when appropriate.
func annotateJavascript(err error, scope *cwlcore.RequirementScope) error {
	if _, enabled := inlineJavascript(scope); enabled || !errors.Is(err, cwlcore.ErrJavaScript) {
		return err
	}

	return fmt.Errorf("%w (declare an InlineJavascriptRequirement to enable JavaScript expressions)", err)
}
