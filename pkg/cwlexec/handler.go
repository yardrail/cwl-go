package cwlexec

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Errors reported by the step-handler contract and by the built-in handlers. They are wrapped with
// context, so callers should test them with [errors.Is].
var (
	// ErrNoHandler reports a process class with no handler registered. A class present in the
	// document and absent from the [Registry] is a fail-closed error at run time: guessing at
	// how to execute an unknown class is how an engine silently produces wrong data.
	ErrNoHandler = errors.New("no handler registered for process class")

	// ErrResultInvariant reports a handler that returned a [Result] violating the invariants
	// documented on [Result] — an unknown Status, a Suspension without StatusSuspended, or
	// StatusSuspended without a Suspension. [Outcome] converts it into a permanent failure.
	ErrResultInvariant = errors.New("handler returned an invalid Result")

	// ErrWrongProcessClass reports a handler that was dispatched a process whose decoded Go type
	// does not match the class it is registered for. It means the registry and the document
	// disagree, which is a scheduler bug rather than a document error.
	ErrWrongProcessClass = errors.New("handler received a process of the wrong class")

	// ErrNotImplemented reports a built-in placeholder handler that is not yet written. It exists
	// so that a run fails loudly, naming the class, rather than silently succeeding.
	ErrNotImplemented = errors.New("not yet implemented")
)

// Class is a CWL process class string — "CommandLineTool", "Workflow", "ExpressionTool",
// "Operation", or an extension class IRI carried by a [cwlcore.RawProcess].
//
// cwlexec dispatches purely on this value and never enumerates non-standard classes itself. That
// is the whole extension mechanism at execution time: a downstream engine decodes its own classes
// through cwlcore's RawProcess seam and registers a [StepHandler] for each, and this package needs
// to know nothing about them.
type Class string

// Status is the closed set of outcomes a step invocation can have.
//
// Four of the five mirror the specification's process status values; StatusSuspended is this
// engine's own addition, and has no analogue in the reference implementation.
type Status string

const (
	// StatusSuccess reports a step that ran and produced its outputs.
	StatusSuccess Status = "success"

	// StatusPermanentFail reports a step that failed in a way that retrying cannot fix.
	StatusPermanentFail Status = "permanentFail"

	// StatusTemporaryFail reports a step that failed in a way that a retry might fix.
	//
	// cwlexec never retries it. The specification only permits retrying — "an implementation may
	// choose to retry a step execution which resulted in temporaryFailure" — so whether, how
	// often and how fast to retry is caller policy, and a scheduler that quietly re-ran the step
	// would take that decision away from the caller. The status is surfaced instead.
	StatusTemporaryFail Status = "temporaryFail"

	// StatusSkipped reports a step whose `when` expression evaluated to false. It is produced by
	// the scheduler from [EvalWhen] and [SkippedOutputs]; a handler never returns it, because a
	// skipped step is never dispatched to one.
	StatusSkipped Status = "skipped"

	// StatusSuspended reports a step that has paused pending an external event. The accompanying
	// [Suspension] is what the caller persists and later resumes from; see [Result.Suspension].
	StatusSuspended Status = "suspended"
)

// Resources is the resolved per-invocation resource reservation a handler may rely on: the values
// the specification exposes to expressions as runtime.cores, runtime.ram, runtime.tmpdirSize and
// runtime.outdirSize.
//
// It is the *output* of resource selection, not a request. The request side — ResourceBudget,
// ResourceRequest and the pluggable Config.SelectResources hook — belongs to the scheduler, which
// derives this value from the process's ResourceRequirement and hands it to the handler. A class
// registered with [Unbudgeted] skips selection entirely and receives the zero value.
//
// A zero field means unresolved, not zero: see [StepCall.RuntimeContext] for why that distinction
// is load-bearing.
type Resources struct {
	// Cores is the number of CPU cores reserved. It is a float because ResourceRequirement's
	// coresMin and coresMax are `long | float | Expression`.
	Cores float64

	// RAMMiB is the RAM reservation in mebibytes.
	RAMMiB int64

	// TmpDirMiB is the temporary-directory reservation in mebibytes.
	TmpDirMiB int64

	// OutDirMiB is the output-directory reservation in mebibytes.
	OutDirMiB int64
}

// StepCall is the fully-resolved unit of work handed to a [StepHandler]: one process, the input
// object it is to be run with, and everything about its context that the handler cannot work out
// for itself.
//
// It replaces the (process, job order, runtime context) triple the reference implementation threads
// through its job methods. One StepCall describes exactly one invocation: a scattered step produces
// one per sub-job, each with its own Inputs and ScatterIndex.
//
// A handler must treat a StepCall as read-only. Inputs in particular is shared with the scheduler
// and, for a scattered step, with sibling sub-jobs.
type StepCall struct {
	// StepID is the workflow-local step identifier, used for logging and for re-addressing a
	// resumed call. For a bare top-level process run as a single implicit step, it is that
	// process's identifier.
	StepID string

	// Process is the decoded `run:` target. It may be a [cwlcore.RawProcess] standing in for an
	// extension class, which is what a caller-registered handler will normally type-assert to.
	Process cwlcore.Process

	// Class is Process.Class(), precomputed because it is the dispatch key.
	Class Class

	// Inputs is the resolved input object for this invocation: sources followed, valueFrom
	// applied, defaults filled in, and — for a scattered step — the scatter keys replaced by
	// this sub-job's elements. Keys are parameter short names; see [ShortName].
	Inputs map[string]any

	// ScatterIndex is this sub-job's coordinates within its step's scatter, as
	// [ScatterJob.Index] reports them. It is empty for an unscattered step, and together with
	// StepID it uniquely addresses the invocation across a whole run.
	ScatterIndex []int

	// Requirements is the requirement and hint view in effect for this process, already scoped
	// and precedence-resolved: workflow, then step, then the process itself. Look a class up
	// with [cwlcore.RequirementScope.GetRequirement] rather than walking the process's own
	// declarations, which see only the innermost frame.
	//
	// It may be nil, which reads as an empty scope in which nothing is declared.
	Requirements *cwlcore.RequirementScope

	// Resources is the reservation resolved for this invocation, or the zero value for a class
	// registered with [Unbudgeted].
	Resources Resources

	// Containers is the container policy the caller configured on [Config.Containers], carried
	// here because a handler is told about its own invocation and nothing about the run around
	// it — the same route [StepCall.Logger] travels, and for the same reason.
	//
	// The zero value asks for nothing, which is what an engine given no instruction does. A
	// handler that runs tools in containers is expected to honour it; one that does not may
	// ignore it, exactly as it ignores Resources it has no use for.
	Containers ContainerPolicy

	// OutDir is the directory this invocation must write its output files to, allocated by the
	// scheduler. It is runtime.outdir.
	OutDir string

	// TmpDir is the scratch directory this invocation may use, allocated by the scheduler and
	// not preserved after the step finishes. It is runtime.tmpdir.
	TmpDir string

	// Eval is the expression evaluator configured for this call's requirement scope, with
	// JavaScript enabled when an InlineJavascriptRequirement is in scope.
	//
	// The scheduler supplies it so that one evaluator — and so one compiled-program cache — is
	// shared by every invocation of a step. It may be nil, in which case [StepCall.Evaluator]
	// derives one from Requirements per call; that is correct but throws the cache away, which
	// matters on a large scatter.
	Eval *cwlcore.Evaluator

	// Logger is where the handler should write execution diagnostics. It may be nil; reach it
	// through [StepCall.Log], which substitutes the default logger.
	Logger *slog.Logger
}

// StepHandler executes one ready invocation of a process class.
//
// The registry holds one handler per class, so an implementation may assume its own class but must
// not assume anything about the surrounding run. Handlers are called concurrently, once per ready
// step and once per scatter sub-job, so an implementation must be safe for concurrent use.
type StepHandler interface {
	// Execute runs one already-scattered, when-passing invocation.
	//
	// The contract for the two results is that Status is authoritative and error explains it:
	//
	//   - StatusSuccess, StatusSuspended and StatusSkipped must come with a nil error.
	//   - StatusPermanentFail and StatusTemporaryFail should come with a non-nil error saying
	//     why; that error is what the caller sees.
	//   - A non-nil error with the zero Status is read as StatusPermanentFail, so the ordinary
	//     Go idiom `return Result{}, err` is safe.
	//
	// The scheduler passes every return through [Outcome], which applies those rules and rejects
	// a Result that violates the invariants documented on [Result].
	//
	// Execute must not block the scheduler waiting on an external event. To wait for one it
	// returns a suspended Result — see [StepCall.Suspend] — which costs nothing to hold. It
	// should, however, honour ctx cancellation for work it does perform.
	Execute(ctx context.Context, call *StepCall) (Result, error)
}

// HandlerFunc adapts an ordinary function to [StepHandler].
type HandlerFunc func(ctx context.Context, call *StepCall) (Result, error)

// Execute calls f, satisfying [StepHandler].
func (f HandlerFunc) Execute(ctx context.Context, call *StepCall) (Result, error) {
	return f(ctx, call)
}

// Result is a handler's outcome. Exactly one of Outputs and Suspension is meaningful, selected by
// Status:
//
//   - StatusSuccess: Outputs holds the produced output object; Suspension is nil.
//   - StatusSkipped: Outputs holds the all-null object from [SkippedOutputs]; Suspension is nil.
//   - StatusPermanentFail, StatusTemporaryFail: Suspension is nil, and any Outputs are advisory —
//     a failed invocation's partial outputs are not wired downstream.
//   - StatusSuspended: Suspension is set and Outputs is nil.
//
// [Outcome] enforces those invariants at the point a handler's return crosses back into cwlexec.
type Result struct {
	// Status is the outcome of the invocation.
	Status Status

	// Outputs is the output object, keyed by output parameter short name; see [ShortName].
	// A process with no outputs may leave it nil.
	Outputs map[string]any

	// Suspension is set if and only if Status is StatusSuspended.
	//
	// The scheduler records it against this invocation, stops scheduling this branch — and only
	// this branch, siblings and independent branches keep running — and surfaces it to the
	// caller once no other work remains. The caller persists it and later drives a resume.
	Suspension *Suspension
}

// Suspension is the caller-owned handle for a paused invocation: everything needed to re-address
// the invocation when it resumes, plus whatever the handler needs to remember about the wait.
//
// Token and Payload are opaque to cwlexec. Nothing in this package reads, parses, compares or
// interprets them; they are carried to the caller and handed back untouched on resume. That is the
// seam that keeps durability out of this layer — a handler can pack a queue name, a signal
// identifier, a deadline or a serialized state machine into them without cwlexec growing any
// knowledge of the mechanism. Only StepID and ScatterIndex are used internally, and only to match
// the resumed outputs back to the invocation that asked for them.
type Suspension struct {
	// StepID is the suspended invocation's step identifier, copied from its [StepCall].
	StepID string

	// ScatterIndex is the suspended invocation's scatter coordinates, copied from its
	// [StepCall]. Together with StepID it addresses exactly one invocation of the run.
	ScatterIndex []int

	// Token is a handler-chosen correlation identifier. Opaque to cwlexec.
	Token string

	// Payload is handler-serialized wait state. Opaque to cwlexec, and never inspected even to
	// check that it is valid JSON or valid UTF-8.
	Payload []byte
}

// Success returns a successful Result carrying outputs, in the two-result shape a handler returns.
// A handler writes `return cwlexec.Success(out)`.
func Success(outputs map[string]any) (Result, error) {
	return Result{Status: StatusSuccess, Outputs: outputs}, nil
}

// PermanentFail returns a permanently failed Result carrying err, in the two-result shape a handler
// returns. Use it for a failure that retrying cannot fix: a malformed document, a tool that exited
// with a permanent failure code, an expression that will fail the same way every time.
func PermanentFail(err error) (Result, error) {
	return Result{Status: StatusPermanentFail}, err
}

// TemporaryFail returns a temporarily failed Result carrying err, in the two-result shape a handler
// returns. cwlexec never retries it — see [StatusTemporaryFail] — so this reports to the caller
// that a retry is permitted, not that one has happened.
func TemporaryFail(err error) (Result, error) {
	return Result{Status: StatusTemporaryFail}, err
}

// Suspend returns a suspended Result addressed to this invocation, in the two-result shape a
// handler returns. The handler supplies only the opaque token and payload; the addressing fields
// are filled in from the call, so a handler cannot get them wrong.
//
// ScatterIndex is copied, so the returned Suspension is safe to retain after the call returns.
func (c *StepCall) Suspend(token string, payload []byte) (Result, error) {
	suspension := &Suspension{
		StepID:       c.StepID,
		ScatterIndex: slices.Clone(c.ScatterIndex),
		Token:        token,
		Payload:      payload,
	}

	return Result{Status: StatusSuspended, Suspension: suspension}, nil
}

// Evaluator returns the expression evaluator for this call: [StepCall.Eval] when the scheduler
// supplied one, and otherwise one derived from Requirements by [EvaluatorFor].
//
// The fallback exists so that a handler is usable with a hand-built StepCall — in a test, or when
// a caller drives a single process directly — without every handler repeating the requirement
// lookup. It is not the fast path: a derived evaluator has an empty compiled-program cache.
func (c *StepCall) Evaluator() *cwlcore.Evaluator {
	if c.Eval != nil {
		return c.Eval
	}

	return EvaluatorFor(c.Requirements)
}

// Log returns [StepCall.Logger], or [slog.Default] when it is nil, so a handler can log without a
// nil check.
func (c *StepCall) Log() *slog.Logger {
	if c.Logger == nil {
		return slog.Default()
	}

	return c.Logger
}

// RuntimeContext renders this call's resources and directories as the runtime.* parameter context
// that expressions evaluated for the invocation see.
//
// A resource field that is zero is left *undefined* rather than set to zero. cwlcore renders an
// undefined field as absent, so an expression referring to it fails loudly instead of reading an
// unresolved reservation as "zero cores" — which would silently produce a wrong command line. This
// is what a handler for an [Unbudgeted] class hands to expressions: outdir and tmpdir, nothing
// else.
//
// Cores is rounded up to a whole core, because the specification's runtime.cores is an integer
// while ResourceRequirement's coresMin may be fractional.
func (c *StepCall) RuntimeContext() cwlcore.RuntimeContext {
	runtime := cwlcore.RuntimeContext{Outdir: c.OutDir, Tmpdir: c.TmpDir}

	if c.Resources.Cores > 0 {
		cores := int64(math.Ceil(c.Resources.Cores))
		runtime.Cores = &cores
	}

	if c.Resources.RAMMiB > 0 {
		ram := c.Resources.RAMMiB
		runtime.RAM = &ram
	}

	if c.Resources.OutDirMiB > 0 {
		outdirSize := c.Resources.OutDirMiB
		runtime.OutdirSize = &outdirSize
	}

	if c.Resources.TmpDirMiB > 0 {
		tmpdirSize := c.Resources.TmpDirMiB
		runtime.TmpdirSize = &tmpdirSize
	}

	return runtime
}

// Outcome normalizes what a handler returned into what the scheduler acts on, applying the two
// rules of the [StepHandler.Execute] contract and rejecting anything else.
//
// A handler is the one place in this engine where third-party code plugs in, so its return is
// checked rather than trusted:
//
//   - A non-nil error with a failure status is passed through unchanged, preserving the
//     permanent/temporary distinction the handler chose.
//   - A non-nil error with any other status — including the zero Status, which is the plain
//     `return Result{}, err` idiom — becomes StatusPermanentFail. Outputs and Suspension are
//     dropped: a call that reported an error does not also get to report data.
//   - A nil error with a Result that violates the invariants documented on [Result] becomes
//     StatusPermanentFail wrapping [ErrResultInvariant]. A handler that contradicts itself has
//     failed, whatever it says.
//
// Everything else is returned unchanged.
func Outcome(result Result, err error) (Result, error) {
	if err != nil {
		if result.Status == StatusPermanentFail || result.Status == StatusTemporaryFail {
			return Result{Status: result.Status}, err
		}

		return Result{Status: StatusPermanentFail}, err
	}

	invalid := result.validate()
	if invalid != nil {
		return Result{Status: StatusPermanentFail}, invalid
	}

	return result, nil
}

// validate reports how result violates the invariants documented on [Result], or nil.
func (r Result) validate() error {
	switch r.Status {
	case StatusSuccess, StatusSkipped, StatusPermanentFail, StatusTemporaryFail:
		if r.Suspension != nil {
			return fmt.Errorf("%w: status %q carries a Suspension", ErrResultInvariant, r.Status)
		}

		return nil
	case StatusSuspended:
		if r.Suspension == nil {
			return fmt.Errorf("%w: status %q without a Suspension", ErrResultInvariant, r.Status)
		}

		if r.Outputs != nil {
			return fmt.Errorf("%w: status %q carries Outputs", ErrResultInvariant, r.Status)
		}

		return nil
	default:
		return fmt.Errorf("%w: unknown status %q", ErrResultInvariant, r.Status)
	}
}

// ShortName reduces a resolved parameter or step identifier to the name the document author wrote,
// which is the key an input or output object uses.
//
// Decoding resolves every identifier to an absolute one — "file:///w.cwl#tool/out" — while an
// expression, a job order and a CWL output object all speak in short names — "out". Every handler
// and the scheduler must agree on that mapping, so it lives here rather than being re-derived in
// each of them.
//
// The rule matches the reference implementation's shortname(): take the fragment if there is one,
// otherwise the path, and keep the segment after the last "/". An identifier that is already short
// is returned unchanged.
func ShortName(id string) string {
	name, fragment, _ := strings.Cut(id, "#")
	if fragment != "" {
		name = fragment
	}

	if index := strings.LastIndex(name, "/"); index >= 0 {
		return name[index+1:]
	}

	return name
}
