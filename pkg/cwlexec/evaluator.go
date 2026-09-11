package cwlexec

import "github.com/yardrail/cwl-go/pkg/cwlcore"

// EvaluatorFor builds an evaluator from scope. JS is enabled when InlineJavascriptRequirement
// is in scope. Nil scope yields parameter-references only.
func EvaluatorFor(scope *cwlcore.RequirementScope, opts ...cwlcore.EvalOption) *cwlcore.Evaluator {
	lib, enabled := inlineJavascript(scope)
	if !enabled {
		return cwlcore.NewEvaluator(opts...)
	}

	derived := make([]cwlcore.EvalOption, 0, len(opts)+1)
	derived = append(derived, cwlcore.WithJS(lib))

	return cwlcore.NewEvaluator(append(derived, opts...)...)
}

// inlineJavascript returns the expressionLib and whether InlineJavascriptRequirement is in scope.
func inlineJavascript(scope *cwlcore.RequirementScope) ([]string, bool) {
	if scope == nil {
		return nil, false
	}

	requirement, found, _ := scope.GetRequirement(cwlcore.ClassInlineJavascriptRequirement)
	if !found {
		return nil, false
	}

	inline, ok := requirement.(*cwlcore.InlineJavascriptRequirement)
	if !ok {
		return nil, false
	}

	return inline.ExpressionLib, true
}
