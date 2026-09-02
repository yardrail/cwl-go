package cwlexec

import (
	"errors"
	"fmt"
	"math"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// Errors reported while resolving and selecting an invocation's resource reservation.
var (
	// ErrResourcesUnavailable reports a step whose minimum request exceeds the machine budget the
	// caller declared. It is a permanent failure for that step: no amount of waiting makes a
	// four-core machine able to reserve eight.
	ErrResourcesUnavailable = errors.New("requested resources exceed the available budget")

	// ErrResourceExpression reports a ResourceRequirement field whose expression failed to
	// evaluate, or evaluated to something that is not a number.
	ErrResourceExpression = errors.New("ResourceRequirement field did not resolve to a number")
)

// The ResourceRequirement defaults the specification states for fields a document leaves out. They
// describe the request, not the machine: a tool that says nothing about resources still asks for
// one core, 256 MiB of RAM, and a gibibyte each of scratch and output space.
const (
	defaultCoresMin     = 1
	defaultRAMMinMiB    = 256
	defaultTmpDirMinMiB = 1024
	defaultOutDirMinMiB = 1024
)

// ResourceBudget is the machine capacity resource selection may draw on: the ceiling every
// invocation's reservation is clamped to.
//
// A zero field means "no ceiling declared", not "none available". A caller that knows nothing about
// the machine leaves the whole struct zero, and every invocation then gets what it asked for.
type ResourceBudget struct {
	// Cores is the number of CPU cores available, or zero for no ceiling.
	Cores float64

	// RAMMiB is the RAM available in mebibytes, or zero for no ceiling.
	RAMMiB int64

	// TmpDirMiB is the scratch space available in mebibytes, or zero for no ceiling.
	TmpDirMiB int64

	// OutDirMiB is the output space available in mebibytes, or zero for no ceiling.
	OutDirMiB int64
}

// ResourceRequest is one invocation's resolved ResourceRequirement: the minima and maxima the
// document asked for, with every expression already evaluated against that invocation's inputs and
// every unstated field filled in with the specification's default.
//
// It is the input to resource selection; [Resources] is the output.
type ResourceRequest struct {
	// CoresMin and CoresMax bound the CPU reservation. They are floats because the schema types
	// coresMin and coresMax as `long | float | Expression`.
	CoresMin, CoresMax float64

	// RAMMinMiB and RAMMaxMiB bound the RAM reservation, in mebibytes.
	RAMMinMiB, RAMMaxMiB int64

	// TmpDirMinMiB and TmpDirMaxMiB bound the scratch-space reservation, in mebibytes.
	TmpDirMinMiB, TmpDirMaxMiB int64

	// OutDirMinMiB and OutDirMaxMiB bound the output-space reservation, in mebibytes.
	OutDirMinMiB, OutDirMaxMiB int64
}

// DefaultSelectResources is the resource selector a [Config] with no SelectResources hook uses.
//
// It reserves as much as the budget allows without exceeding what was asked for: the minimum is a
// floor, the maximum and the budget are both ceilings, and the tighter ceiling wins. A minimum the
// budget cannot meet is [ErrResourcesUnavailable] rather than a silent under-reservation — a tool
// that declared it needs eight cores has said it will not work with four.
//
// Scratch and output space are reserved at their minimum, which is what the reference
// implementation does: those fields describe space the tool needs, not space it can usefully be
// given more of.
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

// clampFloat resolves one fractional resource against its request bounds and the machine budget.
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

// clampInt resolves one whole-unit resource against its request bounds and the machine budget.
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

// resourceResolver evaluates ResourceRequirement fields, remembering the first failure so that a
// whole requirement can be read in one straight-line pass rather than eight error checks.
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
