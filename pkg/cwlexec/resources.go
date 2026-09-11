package cwlexec

import (
	"errors"
	"fmt"
	"math"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

var (
	// ErrResourcesUnavailable reports a minimum request exceeding the machine budget.
	ErrResourcesUnavailable = errors.New("requested resources exceed the available budget")

	// ErrResourceExpression reports a ResourceRequirement expression that failed or returned non-number.
	ErrResourceExpression = errors.New("ResourceRequirement field did not resolve to a number")
)

// Default ResourceRequirement values per the CWL spec.
const (
	defaultCoresMin     = 1
	defaultRAMMinMiB    = 256
	defaultTmpDirMinMiB = 1024
	defaultOutDirMinMiB = 1024
)

// ResourceBudget is the machine capacity ceiling. Zero fields mean no ceiling.
type ResourceBudget struct {
	Cores     float64
	RAMMiB    int64
	TmpDirMiB int64
	OutDirMiB int64
}

// ResourceRequest is a resolved ResourceRequirement with evaluated expressions.
type ResourceRequest struct {
	CoresMin, CoresMax         float64
	RAMMinMiB, RAMMaxMiB       int64
	TmpDirMinMiB, TmpDirMaxMiB int64
	OutDirMinMiB, OutDirMaxMiB int64
}

// DefaultSelectResources clamps the request to the budget. Minimum exceeding budget is an error.
func DefaultSelectResources(request ResourceRequest, budget ResourceBudget) (Resources, error) {
	cores, err := clampFloat("cores", request.CoresMin, request.CoresMax, budget.Cores)
	if err != nil {
		return Resources{}, err
	}

	ram, err := clampInt("ram", request.RAMMinMiB, request.RAMMaxMiB, budget.RAMMiB)
	if err != nil {
		return Resources{}, err
	}

	tmpdir, err := clampInt("tmpdirSize", request.TmpDirMinMiB, request.TmpDirMinMiB, budget.TmpDirMiB)
	if err != nil {
		return Resources{}, err
	}

	outdir, err := clampInt("outdirSize", request.OutDirMinMiB, request.OutDirMinMiB, budget.OutDirMiB)
	if err != nil {
		return Resources{}, err
	}

	return Resources{Cores: cores, RAMMiB: ram, TmpDirMiB: tmpdir, OutDirMiB: outdir}, nil
}

// clampFloat resolves a fractional resource against bounds and budget.
func clampFloat(name string, minimum, maximum, budget float64) (float64, error) {
	if budget > 0 && minimum > budget {
		return 0, fmt.Errorf("%w: %s needs at least %g but only %g is available",
			ErrResourcesUnavailable, name, minimum, budget)
	}

	ceiling := maximum
	if budget > 0 && budget < ceiling {
		ceiling = budget
	}

	return math.Max(ceiling, minimum), nil
}

// clampInt resolves an integer resource against bounds and budget.
func clampInt(name string, minimum, maximum, budget int64) (int64, error) {
	if budget > 0 && minimum > budget {
		return 0, fmt.Errorf("%w: %s needs at least %d but only %d is available",
			ErrResourcesUnavailable, name, minimum, budget)
	}

	ceiling := maximum
	if budget > 0 && budget < ceiling {
		ceiling = budget
	}

	return max(ceiling, minimum), nil
}

// resourceResolver evaluates ResourceRequirement fields, short-circuiting on first error.
type resourceResolver struct {
	eval        *cwlcore.Evaluator
	evalContext *cwlcore.EvalContext
	err         error
}

// number resolves one declared value, returning fallback when the document declared none or when an
// earlier field has already failed.
func (r *resourceResolver) number(declared cwlcore.ResourceValue, name string, fallback float64) float64 {
	if r.err != nil || !declared.IsSet() {
		return fallback
	}

	if literal, isNumber := declared.Number(); isNumber {
		return literal
	}

	value, err := r.eval.Eval(string(declared.Expression()), r.evalContext)
	if err != nil {
		r.err = fmt.Errorf("%w: %s: %w", ErrResourceExpression, name, err)

		return fallback
	}

	resolved, numeric := asNumber(value)
	if !numeric {
		r.err = fmt.Errorf("%w: %s evaluated to %s", ErrResourceExpression, name, cwlcore.TypeName(value))

		return fallback
	}

	return resolved
}

// mebibytes resolves one declared value as a whole number of mebibytes, rounding a fractional
// result up so that a reservation is never smaller than what was asked for.
func (r *resourceResolver) mebibytes(declared cwlcore.ResourceValue, name string, fallback int64) int64 {
	return int64(math.Ceil(r.number(declared, name, float64(fallback))))
}

// resourceRequest resolves the ResourceRequirement in scope for one invocation, evaluating any
// expression field against that invocation's inputs.
//
// Every unstated field takes the specification's default, and an unstated maximum takes the
// corresponding minimum — "if ...Max is not specified, this defaults to ...Min" — so a selector
// never has to distinguish "no ceiling" from "the same as the floor".
func resourceRequest(step *plannedStep, call *StepCall) (ResourceRequest, error) {
	request := ResourceRequest{
		CoresMin:     defaultCoresMin,
		CoresMax:     0,
		RAMMinMiB:    defaultRAMMinMiB,
		RAMMaxMiB:    0,
		TmpDirMinMiB: defaultTmpDirMinMiB,
		TmpDirMaxMiB: 0,
		OutDirMinMiB: defaultOutDirMinMiB,
		OutDirMaxMiB: 0,
	}

	requirement, found, _ := step.scope.GetRequirement(cwlcore.ClassResourceRequirement)

	declared, typed := requirement.(*cwlcore.ResourceRequirement)
	if !found || !typed {
		applyMaxDefaults(&request)

		return request, nil
	}

	resolver := &resourceResolver{
		eval:        step.eval,
		evalContext: &cwlcore.EvalContext{Inputs: call.Inputs, Self: nil, Runtime: call.RuntimeContext()},
		err:         nil,
	}

	request.CoresMin = resolver.number(declared.CoresMin, "coresMin", request.CoresMin)
	request.CoresMax = resolver.number(declared.CoresMax, "coresMax", request.CoresMax)
	request.RAMMinMiB = resolver.mebibytes(declared.RAMMin, "ramMin", request.RAMMinMiB)
	request.RAMMaxMiB = resolver.mebibytes(declared.RAMMax, "ramMax", request.RAMMaxMiB)
	request.TmpDirMinMiB = resolver.mebibytes(declared.TmpdirMin, "tmpdirMin", request.TmpDirMinMiB)
	request.TmpDirMaxMiB = resolver.mebibytes(declared.TmpdirMax, "tmpdirMax", request.TmpDirMaxMiB)
	request.OutDirMinMiB = resolver.mebibytes(declared.OutdirMin, "outdirMin", request.OutDirMinMiB)
	request.OutDirMaxMiB = resolver.mebibytes(declared.OutdirMax, "outdirMax", request.OutDirMaxMiB)

	if resolver.err != nil {
		return ResourceRequest{}, resolver.err
	}

	applyMaxDefaults(&request)

	return request, nil
}

// applyMaxDefaults gives every unstated maximum the value of its minimum.
func applyMaxDefaults(request *ResourceRequest) {
	if request.CoresMax == 0 {
		request.CoresMax = request.CoresMin
	}

	if request.RAMMaxMiB == 0 {
		request.RAMMaxMiB = request.RAMMinMiB
	}

	if request.TmpDirMaxMiB == 0 {
		request.TmpDirMaxMiB = request.TmpDirMinMiB
	}

	if request.OutDirMaxMiB == 0 {
		request.OutDirMaxMiB = request.OutDirMinMiB
	}
}

// asNumber widens the numeric shapes an expression or a job order can produce into a float64.
//
// A [salad.Decimal] is one of them: a number a document wrote keeps its literal so that rendering
// can reproduce it, and every arithmetic use of it — a resource request, a range check, a type
// check — wants the float64 it rounds to.
func asNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	case salad.Decimal:
		return typed.Float64(), true
	default:
		return 0, false
	}
}
