package cwlexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// The built-in Workflow handler: a subworkflow is a run of its own.
//
// A Workflow under a step's run: is not executed by some second, smaller engine — it re-enters the
// same scheduler through [NewRunner], with the same handler registry, the same requirement scoping
// and the same outcome normalization as the run above it. What lives in this file is only what the
// two levels have to agree about: which registry and configuration the inner run inherits, where
// its outputs are written, how a workflow that invokes itself is stopped, and how the inner run's
// outcome is mapped onto one step of the outer one.

// SubworkflowTag is the correlation identifier — the [Suspension.Token] — this handler sets on a
// suspension it is propagating out of a nested run. Its [Suspension.Payload] decodes with
// [DecodeSubworkflowSuspension].
//
// A handler's token is opaque to the rest of cwlexec, so nothing in this package reads this constant
// either. It is exported because a caller sorting persisted suspensions needs to be able to tell a
// nested run's from one its own handlers produced.
const SubworkflowTag = "cwlexec:subworkflow"

// ErrSubworkflowStatus reports a nested run that ended in a way the run contract does not describe:
// a status no run produces, or a failure with no error to explain it.
//
// It is a fail-closed backstop rather than a document error. A failed branch must always be able to
// say why it failed, and "the nested run said so and nothing more" is at least a true answer.
var ErrSubworkflowStatus = errors.New("subworkflow ended with no usable outcome")

// ErrSubworkflowCycle reports a workflow that invokes itself as a subworkflow, directly or through
// another workflow.
//
// The specification makes that a fatal error, and it has to be, since each level of the recursion
// enters the scheduler again: left alone it would run until the process ran out of stack or memory.
// Decoding rejects the same thing earlier for a document whose run: references form a loop — see the
// cycle check in pkg/cwlcore — so this is the backstop for a process graph that reached the engine
// some other way, and it fires at the first repetition rather than after unbounded nesting.
var ErrSubworkflowCycle = errors.New("a workflow invokes itself as a subworkflow")

// Compile-time proof that the handler satisfies the contract.
var _ StepHandler = workflowHandler{}

// subworkflowKey addresses a [subworkflowEnv] in a context. It is a private type, so no other
// package can collide with it.
type subworkflowKey struct{}

// subworkflowEnv is what a nested run inherits from the run above it: the registry its steps
// dispatch through, the configuration the outer run was given, and the chain of workflows currently
// being invoked above it.
//
// It travels in the context rather than in the [StepCall] because the step-handler contract does not
// carry either — a handler is deliberately told about its own invocation and nothing about the run
// around it. See [WithSubworkflows] for what a caller has to do about that.
type subworkflowEnv struct {
	registry *Registry
	cfg      *Config

	// ancestors are the workflows enclosing this invocation, outermost first. Identity is the
	// decoded process pointer, so the same workflow reached twice down one chain is the cycle it
	// is, while the same workflow run by two sibling steps is not.
	ancestors []*cwlcore.Workflow
}

// WithSubworkflows returns a context carrying the registry and configuration that a Workflow under a
// step's run: is executed with, and is how a caller makes its own handlers and settings reach a
// nested run.
//
// It exists because a [StepHandler] is handed one [StepCall] and nothing about the run around it,
// which is the right contract for every other handler and the one thing this one genuinely needs.
// Without it a nested run falls back to a fresh [NewRegistry] and the zero [Config], which is
// correct for a document using only the four core process classes — the whole of the specification —
// and wrong for a caller that registered an extension class, replaced a built-in handler, vouched
// for an extension requirement through Config.AllowRequirements, or set LenientRequirements. Pass
// the same registry and configuration given to [NewRunner]:
//
//	runner, err := cwlexec.NewRunner(ctx, process, registry, cfg)
//	result, err := runner.Run(cwlexec.WithSubworkflows(ctx, registry, cfg), inputs)
//
// A nil registry means [NewRegistry]; a nil cfg means the zero [Config]. Config.OutDir and
// Config.TmpDirPrefix are always overridden per nested run, with the suspended invocation's own
// StepCall.OutDir and StepCall.TmpDir, so what is passed here for those two is ignored.
func WithSubworkflows(ctx context.Context, registry *Registry, cfg *Config) context.Context {
	return context.WithValue(ctx, subworkflowKey{}, subworkflowEnv{registry: registry, cfg: cfg})
}

// subworkflowsFrom reads the environment a nested run inherits, substituting the built-in registry
// when the context carries none.
func subworkflowsFrom(ctx context.Context) subworkflowEnv {
	env, carried := ctx.Value(subworkflowKey{}).(subworkflowEnv)

	if !carried || env.registry == nil {
		env.registry = NewRegistry()
	}

	return env
}

// descend returns the environment one level further in: the same registry, the configuration the
// nested run is about to be given, and workflow appended to the ancestor chain.
//
// Handing the child configuration down rather than the parent's is what makes a doubly-nested run
// inherit the settings that were resolved for its own parent, and it costs nothing, because the two
// directory fields are re-derived at every level anyway.
func (e subworkflowEnv) descend(workflow *cwlcore.Workflow, cfg *Config) subworkflowEnv {
	ancestors := make([]*cwlcore.Workflow, 0, len(e.ancestors)+1)
	ancestors = append(ancestors, e.ancestors...)

	return subworkflowEnv{registry: e.registry, cfg: cfg, ancestors: append(ancestors, workflow)}
}

// childConfig derives the configuration one nested run executes under.
//
// Everything that describes how the caller wants work done — the resource selector and budget, the
// parallelism cap, the failure policy, the expression timeout, the requirement policy — is inherited
// unchanged: a nested step is still work this caller asked for, and a subworkflow that quietly ran
// under different rules than its parent would be the wrong answer, not a smaller one.
//
// Three fields must differ, and all three for the same reason — they address this invocation rather
// than the run:
//
//   - OutDir becomes the invocation's own output directory, so the nested run allocates its steps'
//     directories inside it. The nesting is then visible in the paths, two invocations of a
//     scattered subworkflow step cannot collide, and — because [Config.dirsFor] derives the path
//     rather than inventing one — the same nested step gets the same directory when the run is
//     resumed.
//   - TmpDirPrefix becomes the invocation's scratch directory, for the same reason.
//   - Logger becomes the call's, which is the logger the outer run was configured with; it reaches
//     here through the [StepCall] rather than through the context.
//
// Containers is taken from the call for the same reason Logger is, and it is the one that has to be:
// a --no-container that stopped applying one level down would run a container the caller told this
// engine not to run. Inheriting e.cfg would already carry it when a caller wired up
// [WithSubworkflows]; reading it off the [StepCall] carries it whether or not they did.
//
// An unset OutDir or TmpDir stays unset, which reads as "this run allocates no directories" exactly
// as it does at the top level.
func (e subworkflowEnv) childConfig(call *StepCall) *Config {
	cfg := Config{}
	if e.cfg != nil {
		cfg = *e.cfg
	}

	cfg.Logger = call.Logger
	cfg.Containers = call.Containers
	cfg.OutDir = call.OutDir
	cfg.TmpDirPrefix = call.TmpDir

	return &cfg
}

// workflowHandler is the built-in handler for the Workflow class.
type workflowHandler struct{}

// Execute runs one invocation of a subworkflow as a nested run.
//
// It cannot deadlock the run above it. The outer scheduler calls a handler on a goroutine of its own
// and then parks in a select on its own completion channel and the context, so the outer event loop
// is never the goroutine sitting in here; and the nested run shares no channel, no lock and no state
// with it, communicating only through this call's return. The one resource the two levels do share
// is the caller's machine: Config.MaxParallel bounds each run separately, so n levels of nesting
// admit up to the product of their caps in flight at once.
func (workflowHandler) Execute(ctx context.Context, call *StepCall) (Result, error) {
	return Outcome(runSubworkflow(ctx, call))
}

// runSubworkflow checks that the invocation is one that may run at all, and then runs it.
func runSubworkflow(ctx context.Context, call *StepCall) (Result, error) {
	workflow, ok := call.Process.(*cwlcore.Workflow)
	if !ok {
		return PermanentFail(fmt.Errorf("%w: %s is not a Workflow", ErrWrongProcessClass, describe(call)))
	}

	if !subworkflowsEnabled(call.Requirements) {
		return PermanentFail(fmt.Errorf("%w: %s runs a Workflow but %s is not in scope",
			ErrRequirementNotInScope, describe(call), cwlcore.ClassSubworkflowFeatureRequirement))
	}

	env := subworkflowsFrom(ctx)
	if slices.Contains(env.ancestors, workflow) {
		return PermanentFail(fmt.Errorf("%w: %s", ErrSubworkflowCycle, describe(call)))
	}

	cfg := env.childConfig(call)

	child, err := NewRunner(ctx, inheritRequirements(workflow, call.Requirements), env.registry, cfg)
	if err != nil {
		return PermanentFail(fmt.Errorf("%s: %w", describe(call), err))
	}

	call.Log().Debug("running subworkflow", "step", call.StepID, "workflow", workflow.ID, "dir", cfg.OutDir)

	nested := context.WithValue(ctx, subworkflowKey{}, env.descend(workflow, cfg))

	run, err := child.Run(nested, call.Inputs)

	return subResult(call, run, err)
}

// subworkflowsEnabled reports whether a SubworkflowFeatureRequirement is in scope for the call.
//
// The specification attaches the feature to the document construct rather than to the engine: a
// Workflow under run: is only legal where the requirement is in scope, so the gate is fail-closed
// here as it is in the plan. [NewRunner] already refuses such a step before the run starts, which is
// where a document author sees it; this is the same check for a [StepCall] built by hand, and for a
// scope the plan resolved differently from the one the invocation is handed.
func subworkflowsEnabled(scope *cwlcore.RequirementScope) bool {
	if scope == nil {
		return false
	}

	return inScope(scope, cwlcore.ClassSubworkflowFeatureRequirement)
}

// inheritRequirements returns the view of the subworkflow the nested run is planned over: the same
// workflow, carrying the requirements and hints that are in effect for it.
//
// Requirements are inherited by everything below the process that declares them, and a subworkflow
// boundary does not stop that — an InlineJavascriptRequirement on the outer workflow is in scope for
// a tool three levels down. The nested run builds its own scopes from the process it is given, and
// so would see only what the subworkflow declared itself; resolving the effective set into a copy of
// the workflow closes the gap without the nested plan needing to know it is nested.
//
// Reading it out of the scope rather than concatenating the two lists is what gets the precedence
// right for free: [cwlcore.RequirementScope.EffectiveRequirements] has already resolved one winner
// per class with the inner-most declaration winning, so the subworkflow's own declarations survive
// and an outer one only fills a gap. Hints are deliberately not collapsed the same way, which is
// that method's own documented asymmetry, and within a single frame the last declaration still wins
// — so the inner-most hint still beats an outer one.
//
// The copy is shallow, and never mutates the decoded document: a scattered subworkflow step's
// sub-jobs plan concurrently over the same steps.
func inheritRequirements(workflow *cwlcore.Workflow, scope *cwlcore.RequirementScope) *cwlcore.Workflow {
	if scope == nil {
		return workflow
	}

	view := *workflow
	view.Requirements = scope.EffectiveRequirements()
	view.Hints = scope.EffectiveHints()

	return &view
}

// subResult maps a nested run's outcome onto the outcome of the step that ran it.
//
// The mapping is the identity everywhere it can be: the nested run's outputs are the step's outputs,
// and its failure is the step's failure, keeping the permanent/temporary distinction so that a
// caller's retry policy sees the verdict the failing leaf reached rather than one invented at the
// boundary. Everything else is a permanent failure of the step: a run cannot report StatusSkipped —
// a skip is a step-level outcome and there is no step above the nested run to gate it — so that,
// and any status a future engine might add, land in the same arm as an ordinary permanent failure.
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

// nestedError explains a nested run's failure, falling back to naming the status when the run
// reported one without an error. A failed branch must always be able to say why it failed.
func nestedError(call *StepCall, status Status, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", describe(call), err)
	}

	return fmt.Errorf("%w: %s reported %q", ErrSubworkflowStatus, describe(call), status)
}

// suspendNested propagates a nested run's suspensions as a suspension of the step that ran it.
//
// A [Suspension] addresses one invocation of one run — its StepID and ScatterIndex are meaningful
// only within the run that produced them — so a nested run's suspensions cannot be handed up as they
// stand: the outer run has no step by those names. They are packed into the payload instead, and the
// step is suspended at the outer level under its own address, which is the only address the outer
// run can resume. See [SubworkflowSuspension] for what a caller does with the payload, and for what
// this does not do.
func suspendNested(call *StepCall, run RunResult) (Result, error) {
	payload, err := json.Marshal(subworkflowPayload{
		State:       run.State,
		Suspensions: wireSuspensions(run.Suspensions),
	})
	if err != nil {
		return PermanentFail(fmt.Errorf("%s: recording a nested suspension: %w", describe(call), err))
	}

	return call.Suspend(SubworkflowTag, payload)
}

// subworkflowPayload is the wire shape of a nested suspension's payload. Every field is named
// explicitly, because what a caller persists is part of this package's contract.
type subworkflowPayload struct {
	State       RunState         `json:"state"`
	Suspensions []suspensionJSON `json:"suspensions,omitempty"`
}

// wireSuspensions renders a nested run's suspensions in the shape the payload records them as.
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

// SubworkflowSuspension is the decoded payload of a suspension the built-in Workflow handler
// propagated out of a nested run: the nested run's own snapshot, and the invocations inside it that
// are waiting.
//
// # What is supported
//
// A nested run that suspends pauses exactly the step that ran it, at the outer level, addressed by
// that step's own StepID and ScatterIndex. Sibling steps of the outer run keep going, and the outer
// run finishes as StatusSuspended with that step among its suspensions, exactly as for any other
// suspended handler.
//
// Resuming it is possible, and the caller drives it, in three moves:
//
//  1. Decode the payload with [DecodeSubworkflowSuspension]. Suspensions holds the waiting
//     invocations addressed within the nested run, which is the addressing a caller satisfying them
//     needs.
//  2. Build a [Runner] over the same subworkflow — the process under that step's run: — with the
//     same [Registry], and a [Config] equal to the outer one but with OutDir set to the suspended
//     invocation's own StepCall.OutDir and TmpDirPrefix to its StepCall.TmpDir. Call [Runner.Resume]
//     on it with State and the outcomes the nested invocations have since received. That may itself
//     suspend again, and the same loop applies.
//  3. When the nested run reaches StatusSuccess, resume the outer run with a [ResumedStep]
//     addressing the outer step — the StepID and ScatterIndex on the outer [Suspension] — carrying
//     the nested run's Outputs. The outer scheduler validates them against that step's declared
//     output types and carries on.
//
// # What is not supported
//
// The engine will not do any of that for the caller. A resumed invocation is never dispatched to its
// handler a second time — [Runner.Resume] injects the outcome a caller supplies and moves on — so
// this handler is never re-entered for a suspension it produced, and cannot re-enter the nested run
// on the caller's behalf. There is therefore no transparent nested resume: a caller that persists an
// outer suspension and hands it back gets the outer step resumed with whatever outputs it supplies,
// and it is the caller's job to have obtained them by driving the nested run as above.
//
// Nor is the nested [Config] recorded in the payload. Only the two directory fields are derived by
// this handler, from the suspended invocation's own OutDir and TmpDir; everything else is what the
// caller already configured, so the payload would only be repeating what the caller knows.
type SubworkflowSuspension struct {
	// State is the nested run's snapshot, in the shape [Runner.Resume] takes.
	State RunState

	// Suspensions are the invocations waiting inside the nested run, addressed within it. The
	// order is the one [RunResult.Suspensions] produced: steps in document order, sub-jobs in
	// coordinate order.
	Suspensions []Suspension
}

// DecodeSubworkflowSuspension decodes the payload of a suspension whose Token is [SubworkflowTag].
// See [SubworkflowSuspension] for what to do with the result.
func DecodeSubworkflowSuspension(payload []byte) (SubworkflowSuspension, error) {
	var wire subworkflowPayload

	err := json.Unmarshal(payload, &wire)
	if err != nil {
		return SubworkflowSuspension{}, fmt.Errorf("cwlexec: decoding a subworkflow suspension: %w", err)
	}

	decoded := SubworkflowSuspension{
		State:       wire.State,
		Suspensions: make([]Suspension, 0, len(wire.Suspensions)),
	}

	for index := range wire.Suspensions {
		decoded.Suspensions = append(decoded.Suspensions, wire.Suspensions[index].asSuspension())
	}

	return decoded, nil
}
