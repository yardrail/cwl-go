package cwlexec

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Errors reported by the built-in ExpressionTool handler.
var (
	// ErrExpressionToolResult reports an ExpressionTool whose expression evaluated successfully
	// but produced something other than an object. The schema requires that "the expression must
	// return a plain Javascript object which matches the output parameters of the
	// ExpressionTool".
	ErrExpressionToolResult = errors.New("ExpressionTool expression did not return an object")

	// ErrUndeclaredOutput reports an expression result naming a field that is not one of the
	// process's declared output parameters.
	ErrUndeclaredOutput = errors.New("expression result names an undeclared output parameter")
)

// expressionToolHandler is the built-in handler for the ExpressionTool class.
//
// An ExpressionTool "executes a pure Javascript expression that has access to the same input
// parameters as a workflow", so this handler runs no program, stages no files and starts no
// container: the specification is explicit that for an ExpressionTool "no Docker software container
// is required or allowed". It deliberately does not reject a DockerRequirement it finds in scope —
// one declared on an enclosing workflow is inherited by every step including this one, and failing
// on it would make a perfectly ordinary document unrunnable. Nothing is done with it, which is what
// "not required" means in practice.
type expressionToolHandler struct{}

// Execute evaluates the tool's expression, binds the result to the tool's declared output ports, and
// writes down whatever file and directory literals the result carries.
//
// ctx bounds only that last step. The expression evaluation itself is bounded by cwlcore's own
// timeout rather than by a context, so the only cancellable work here is the filesystem work — see
// [materializeExpressionOutputs].
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

	// The outputs are returned unvalidated, on purpose. The schema says of an
	// ExpressionToolOutputParameter's type that it "just acts as a hint, as the outputs of an
	// ExpressionTool process are always considered valid". A declared type of File on a port
	// holding the number 3 is therefore not this handler's error to raise.
	//
	// Materializing the literals is not validation and is not optional: a File the expression
	// invented has no bytes anywhere until this runs, and neither a downstream step nor the
	// caller can do anything with one that has none. It is also where the filesystem values are
	// typed, so that this handler's output object has the same shape a CommandLineTool's does.
	written, err := materializeExpressionOutputs(ctx, call, outputs)
	if err != nil {
		return PermanentFail(fmt.Errorf("%s: %w", describe(call), err))
	}

	return Success(written)
}

// bindExpressionOutputs maps the object an ExpressionTool's expression returned onto the tool's
// declared output ports, keyed by output parameter short name.
//
// The two sides may disagree in either direction, and the two directions are not symmetric:
//
//   - A declared port the object does not mention becomes null. That is not an error, because a
//     null is a legal value for an optional output and the specification exempts an
//     ExpressionTool's outputs from type validation, so there is nothing here to reject it
//     against. Every declared port is present in the result either way, so a downstream step
//     wired to one never blocks waiting for a key that will not arrive.
//   - A field the object names that is not a declared port is an error. Silently dropping it
//     would discard a result the workflow author believed they were producing, and the usual
//     cause is a typo in exactly one of the two spellings — the loud failure is the useful one.
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

// annotateJavascript adds the one piece of context a bare [cwlcore.ErrJavaScript] is missing: that
// the fix is a declaration in the document.
//
// It is only added when the requirement really is absent from scope. If it is present and the
// evaluation still says JavaScript is unavailable, the evaluator was built without it — a
// scheduler bug — and telling the author to declare what they already declared would send them
// looking in the wrong place.
func annotateJavascript(err error, scope *cwlcore.RequirementScope) error {
	if _, enabled := inlineJavascript(scope); enabled || !errors.Is(err, cwlcore.ErrJavaScript) {
		return err
	}

	return fmt.Errorf("%w (declare an InlineJavascriptRequirement to enable JavaScript expressions)", err)
}
