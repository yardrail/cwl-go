package cwlexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// SubworkflowTag is the [Suspension.Token] for suspensions propagated from nested runs.
const SubworkflowTag = "cwlexec:subworkflow"

// ErrSubworkflowStatus reports a nested run with an unexplainable outcome.
var ErrSubworkflowStatus = errors.New("subworkflow ended with no usable outcome")

// ErrSubworkflowCycle reports a workflow that invokes itself, directly or transitively.
var ErrSubworkflowCycle = errors.New("a workflow invokes itself as a subworkflow")

var _ StepHandler = workflowHandler{}

// subworkflowKey is the context key for [subworkflowEnv].
type subworkflowKey struct{}

// subworkflowEnv carries the registry, config, and ancestor chain for nested runs.
type subworkflowEnv struct {
	registry *Registry
	cfg      *Config

	// ancestors tracks enclosing workflows for cycle detection.
	ancestors []cwlcore.StepContainer
}

// WithSubworkflows makes the registry and config available to nested Workflow runs.
// Nil registry means [NewRegistry]; nil cfg means zero [Config].
func WithSubworkflows(ctx context.Context, registry *Registry, cfg *Config) context.Context {
	return context.WithValue(ctx, subworkflowKey{}, subworkflowEnv{registry: registry, cfg: cfg, ancestors: nil})
}

// subworkflowsFrom reads the nested-run environment from ctx.
func subworkflowsFrom(ctx context.Context) subworkflowEnv {
	env, carried := ctx.Value(subworkflowKey{}).(subworkflowEnv)

	if !carried || env.registry == nil {
		env.registry = NewRegistry()
	}

	return env
}

// descend returns the environment one nesting level deeper, with workflow added to ancestors.
func (e subworkflowEnv) descend(sc cwlcore.StepContainer, cfg *Config) subworkflowEnv {
	ancestors := make([]cwlcore.StepContainer, 0, len(e.ancestors)+1)
	ancestors = append(ancestors, e.ancestors...)

	return subworkflowEnv{registry: e.registry, cfg: cfg, ancestors: append(ancestors, sc)}
}

// childConfig derives config for a nested run, overriding OutDir, TmpDirPrefix, Logger, and Containers
// from the call.
func (e subworkflowEnv) childConfig(call *StepCall) *Config {
	cfg := Config{
		Logger:              nil,
		SelectResources:     nil,
		AllowRequirements:   nil,
		OutDir:              "",
		TmpDirPrefix:        "",
		OnError:             "",
		ContainerExecutor:   nil,
		Containers:          ContainerPolicy{Disabled: false, NoMatchUser: false, NoReadOnly: false, Keep: false},
		Resources:           ResourceBudget{Cores: 0, RAMMiB: 0, TmpDirMiB: 0, OutDirMiB: 0},
		EvalTimeout:         0,
		MaxParallel:         0,
		LenientRequirements: false,
	}
	if e.cfg != nil {
		cfg = *e.cfg
	}

	cfg.Logger = call.Logger
	cfg.ContainerExecutor = call.ContainerExecutor
	cfg.Containers = call.Containers
	cfg.OutDir = call.OutDir
	cfg.TmpDirPrefix = call.TmpDir

	return &cfg
}

// workflowHandler is the built-in Workflow handler.
type workflowHandler struct{}

// Execute runs a subworkflow as a nested run.
func (workflowHandler) Execute(ctx context.Context, call *StepCall) (Result, error) {
	return Outcome(runSubworkflow(ctx, call))
}

// runSubworkflow validates and executes a nested workflow.
func runSubworkflow(ctx context.Context, call *StepCall) (Result, error) {
	sc, ok := call.Process.(cwlcore.StepContainer)
	if !ok {
		return PermanentFail(fmt.Errorf("%w: %s is not a Workflow", ErrWrongProcessClass, describe(call)))
	}

	if !subworkflowsEnabled(call.Requirements) {
		return PermanentFail(fmt.Errorf("%w: %s runs a Workflow but %s is not in scope",
			ErrRequirementNotInScope, describe(call), cwlcore.ClassSubworkflowFeatureRequirement))
	}

	env := subworkflowsFrom(ctx)
	if slices.Contains(env.ancestors, sc) {
		return PermanentFail(fmt.Errorf("%w: %s", ErrSubworkflowCycle, describe(call)))
	}

	cfg := env.childConfig(call)

	child, err := NewRunner(ctx, inheritRequirements(sc, call.Requirements), env.registry, cfg)
	if err != nil {
		return PermanentFail(fmt.Errorf("%s: %w", describe(call), err))
	}

	call.Log().Debug("running subworkflow", "step", call.StepID, "workflow", sc.Base().ID, "dir", cfg.OutDir)

	nested := context.WithValue(ctx, subworkflowKey{}, env.descend(sc, cfg))

	run, err := child.Run(nested, call.Inputs)

	return subResult(call, run, err)
}

// subworkflowsEnabled checks for SubworkflowFeatureRequirement in scope.
func subworkflowsEnabled(scope *cwlcore.RequirementScope) bool {
	if scope == nil {
		return false
	}

	return inScope(scope, cwlcore.ClassSubworkflowFeatureRequirement)
}

// inheritRequirements copies the workflow with the effective requirements/hints from scope.
func inheritRequirements(sc cwlcore.StepContainer, scope *cwlcore.RequirementScope) cwlcore.StepContainer {
	if scope == nil {
		return sc
	}

	reqs := scope.EffectiveRequirements()
	hints := scope.EffectiveHints()

	switch w := sc.(type) {
	case *cwlcore.Workflow:
		view := *w
		view.Requirements = reqs
		view.Hints = hints

		return &view
	case *cwlcore.ExtensionWorkflow:
		view := *w
		view.Requirements = reqs
		view.Hints = hints

		return &view
	default:
		return sc
	}
}

// subResult maps a nested run's outcome onto the step's Result.
func subResult(call *StepCall, run RunResult, err error) (Result, error) {
	switch run.Status {
	case StatusSuccess:
		return Success(run.Outputs)
	case StatusSuspended:
		return suspendNested(call, run)
	case StatusTemporaryFail:
		return TemporaryFail(nestedError(call, run.Status, err))
	default:
		return PermanentFail(nestedError(call, run.Status, err))
	}
}

// nestedError wraps a nested failure, falling back to naming the status if err is nil.
func nestedError(call *StepCall, status Status, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", describe(call), err)
	}

	return fmt.Errorf("%w: %s reported %q", ErrSubworkflowStatus, describe(call), status)
}

// suspendNested packs nested suspensions into the step's payload and suspends the outer step.
func suspendNested(call *StepCall, run RunResult) (Result, error) {
	payload, err := json.Marshal(subworkflowPayload{
		State:       &run.State,
		Suspensions: wireSuspensions(run.Suspensions),
	})
	if err != nil {
		return PermanentFail(fmt.Errorf("%s: recording a nested suspension: %w", describe(call), err))
	}

	return call.Suspend(SubworkflowTag, payload)
}

// subworkflowPayload is the wire shape of a nested suspension's payload.
type subworkflowPayload struct {
	State       *RunState        `json:"state"`
	Suspensions []suspensionJSON `json:"suspensions,omitempty"`
}

// wireSuspensions converts suspensions to their JSON-serializable form.
func wireSuspensions(suspensions []Suspension) []suspensionJSON {
	wire := make([]suspensionJSON, 0, len(suspensions))

	for index := range suspensions {
		waiting := &suspensions[index]
		wire = append(wire, suspensionJSON{
			StepID:       waiting.StepID,
			Token:        waiting.Token,
			Payload:      waiting.Payload,
			ScatterIndex: waiting.ScatterIndex,
		})
	}

	return wire
}

// SubworkflowSuspension is a decoded nested-run suspension payload.
// The caller drives resume; the engine does not re-enter the nested run automatically.
type SubworkflowSuspension struct {
	State       RunState
	Suspensions []Suspension
}

// DecodeSubworkflowSuspension decodes a [SubworkflowTag] suspension payload.
func DecodeSubworkflowSuspension(payload []byte) (SubworkflowSuspension, error) {
	var wire subworkflowPayload

	err := json.Unmarshal(payload, &wire)
	if err != nil {
		return SubworkflowSuspension{}, fmt.Errorf("cwlexec: decoding a subworkflow suspension: %w", err)
	}

	decoded := SubworkflowSuspension{
		State:       *wire.State,
		Suspensions: make([]Suspension, 0, len(wire.Suspensions)),
	}

	for index := range wire.Suspensions {
		decoded.Suspensions = append(decoded.Suspensions, wire.Suspensions[index].asSuspension())
	}

	return decoded, nil
}
