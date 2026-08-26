package cwlexec

import "github.com/yardrail/cwl-go/pkg/cwlcore"

// EvaluatorFor builds the expression evaluator a requirement scope calls for: parameter references
// always, plus full JavaScript — and the scope's expressionLib — when an InlineJavascriptRequirement
// is in effect.
//
// Every expression this package evaluates goes through an evaluator built this way: a step's
// `when`, a step input's valueFrom, an ExpressionTool's expression, a tool's output eval. Building
// it in one place is what keeps them consistent about which expression syntax is legal where.
//
// A declaration in hints counts, not only one in requirements. The requirement carries no
// obligation an implementation can fail to meet — it enables a syntax — so honouring an advisory
// declaration can only make more documents work, and it matches the reference implementation.
//
// A nil scope is valid and yields a parameter-references-only evaluator, which is what the
// specification requires of every conforming implementation whether or not anything is declared.
// Extra options are applied after the requirement-derived ones, so a caller can override the
// evaluation timeout — worth doing before a large scatter, where the default applies per
// evaluation and `when` is evaluated once per sub-job.
func EvaluatorFor(scope *cwlcore.RequirementScope, opts ...cwlcore.EvalOption) *cwlcore.Evaluator {
	lib, enabled := inlineJavascript(scope)
	if !enabled {
		return cwlcore.NewEvaluator(opts...)
	}

	derived := make([]cwlcore.EvalOption, 0, len(opts)+1)
	derived = append(derived, cwlcore.WithJS(lib))

	return cwlcore.NewEvaluator(append(derived, opts...)...)
}

// inlineJavascript reports the expressionLib of the InlineJavascriptRequirement in effect for
// scope, and whether one is in effect at all. A nil scope has none.
func inlineJavascript(scope *cwlcore.RequirementScope) ([]string, bool) {
	if scope == nil {
		return nil, false
	}

	requirement, found, _ := scope.GetRequirement(cwlcore.ClassInlineJavascriptRequirement)
	if !found {
		return nil, false
	}

	// A RawRequirement can never carry this class — decoding models it — but the assertion is
	// checked anyway, because an unchecked one here would turn a decode bug into a panic inside
	// a handler goroutine.
	inline, ok := requirement.(*cwlcore.InlineJavascriptRequirement)
	if !ok {
		return nil, false
	}

	return inline.ExpressionLib, true
}
