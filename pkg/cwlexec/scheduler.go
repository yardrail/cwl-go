package cwlexec

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// ErrNoProcess reports a [NewRunner] call with no process to run.
var ErrNoProcess = errors.New("no process to run")

// OnError says what a run does when a step fails.
type OnError string

const (
	// OnErrorStop stops scheduling new work as soon as a step fails. It is the zero value, and so
	// the default: a workflow whose first step failed is rarely improved by running the rest of
	// it, and the steps downstream of the failure could not run in any case.
	//
	// Work already in flight is left alone. Interrupting it is what cancelling the context does,
	// and a handler that has already started a container should be told to stop once, by one
	// mechanism, not two.
	OnErrorStop OnError = ""

	// OnErrorContinue keeps running every branch that does not depend on the failed step. The run
	// still ends in failure; it just gets as far as it can first, which is what a caller
	// collecting results from independent branches wants.
	OnErrorContinue OnError = "continue"
)

// Config is what a run needs to know that the document does not say.
//
// It replaces the reference implementation's runtime-context object, which accumulated some fifty
// mutable fields covering everything from container images to provenance capture. The concerns that
// belong to a handler — staging, the argv, the environment a tool runs in — are deliberately absent:
// a handler owns its own execution environment, and a scheduler that tried to configure one would be
// configuring something it cannot see.
//
// Containers is the one field that looks like it belongs on the other side of that line, and it is
// here because it is not a description of how to run a tool. It is a standing instruction about what
// this engine may do at all, it has to reach every invocation of a run — a nested one included — and
// Config is the only thing that reaches all of them. It travels on to each [StepCall], which is
// where a handler reads it.
//
// The zero Config is usable. It runs with unbounded parallelism, no resource ceiling, the default
// resource selector, the default logger and OnErrorStop.
type Config struct {
	// Logger receives execution diagnostics and is passed on to each handler. Nil means
	// [slog.Default].
	Logger *slog.Logger

	// SelectResources resolves an invocation's resource request against the machine budget. Nil
	// means [DefaultSelectResources]. It is skipped entirely for a class registered with
	// [Unbudgeted].
	SelectResources func(request ResourceRequest, budget ResourceBudget) (Resources, error)

	// AllowRequirements names the extension requirement classes the caller vouches for, so that
	// they pass the fail-closed unknown-requirement gate. A nil map vouches for nothing, which is
	// the correct default: an engine that cannot honour a requirement must not run the process.
	AllowRequirements map[string]bool

	// OutDir is the directory each invocation's output directory is allocated under. Empty means
	// invocations are given no output directory.
	OutDir string

	// TmpDirPrefix is the directory each invocation's scratch directory is allocated under. Empty
	// means invocations are given no scratch directory.
	TmpDirPrefix string

	// OnError says what happens when a step fails; see [OnErrorStop].
	OnError OnError

	// Containers is the caller's software-container policy, passed on to each handler through
	// [StepCall.Containers]. The zero value asks for nothing; see [ContainerPolicy].
	Containers ContainerPolicy

	// Resources is the machine capacity resource selection clamps to. The zero value declares no
	// ceiling.
	Resources ResourceBudget

	// EvalTimeout bounds one expression evaluation. Zero means [cwlcore.DefaultEvalTimeout].
	//
	// It is worth setting before a large scatter. The default applies per evaluation and a `when`
	// is evaluated once per scatter element, so the worst case a thousand-element scatter can
	// reach is a thousand times the limit.
	EvalTimeout time.Duration

	// MaxParallel caps how many handler calls may be in flight at once. Zero or less is
	// unbounded.
	MaxParallel int

	// LenientRequirements is the specification's "unless overridden at user option" escape from
	// the unknown-requirement gate. It belongs behind a deliberate user choice, such as a
	// command-line flag, and is not a default.
	LenientRequirements bool
}

// ResumedStep supplies the outcome of one previously suspended invocation, addressed by the step
// identifier and scatter coordinates the matching [Suspension] carried.
//
// Nothing else identifies it. The Token and Payload a handler put in the Suspension are opaque to
// this package and are not consulted here either — a caller that wants to correlate them does so on
// its own side of the seam.
type ResumedStep struct {
	// Outputs is the output object the external event produced, used when Status is
	// StatusSuccess. It is validated against the step's declared output types before it is
	// injected; see [checkDeclaredOutputs] for why that check is not optional.
	Outputs map[string]any

	// StepID is the suspended invocation's step identifier.
	StepID string

	// Status is the outcome to inject: StatusSuccess, StatusSkipped, or one of the two failure
	// statuses. A failure short-circuits that branch, and everything downstream of it.
	Status Status

	// ScatterIndex is the suspended invocation's scatter coordinates, empty for an unscattered
	// step.
	ScatterIndex []int
}

// RunResult is the terminal or suspended outcome of a run.
type RunResult struct {
	// Outputs is the run's output object, populated only when Status is StatusSuccess.
	Outputs map[string]any

	// Status is the run's outcome: StatusSuccess, StatusSuspended, or one of the two failure
	// statuses.
	Status Status

	// Suspensions holds one entry per invocation waiting on an external event, and is non-empty
	// if and only if Status is StatusSuspended. A run that failed abandons whatever was waiting;
	// the suspensions are still in State, but they are history rather than work to resume.
	//
	// The order is stable: steps in document order, and scatter sub-jobs in coordinate order.
	Suspensions []Suspension

	// State is the snapshot to persist and hand back to [Runner.Resume]. It is meaningful for
	// every outcome, including a cancelled run's, where it records how far the run had got.
	State RunState
}

// Runner executes one process — a Workflow, or any other process class as a single implicit step.
//
// A Runner holds no state between runs, so one may drive several runs as long as they are
// serialized. Everything about a run lives in the [RunState] the run itself carries.
type Runner struct {
	plan     *plan
	registry *Registry
	cfg      *Config
}

// NewRunner binds a process, a handler registry and a configuration into a runner, and analyses the
// process before returning.
//
// The analysis is deliberately eager and fail-closed. An unresolved run: reference, a source that
// names nothing, a cycle, a feature used without the requirement it needs, an unrecognized
// requirement class, a step whose class has no registered handler — each of them fails here, before
// any step has run, rather than half way through a run that has already written files.
//
// The process need not be a Workflow. Any other class is executed as a single implicit step through
// the same registry, the same requirement scoping and the same outcome normalization, which is what
// lets a bare CommandLineTool — the shape most CWL documents take — be run at all.
//
// ctx bounds the analysis, which reads from disk: a parameter's or a step input's `default` that
// names a File is resolved against the document it was written in and measured from the filesystem
// before the plan is complete, exactly as a job order's value is. A cancelled context stops a
// document whose defaults name a great many files rather than measuring all of them. Nothing is
// executed here, so cancelling costs nothing but the analysis.
//
// A nil cfg is the zero [Config]. registry must not be nil; build one with [NewRegistry].
func NewRunner(ctx context.Context, process cwlcore.Process, registry *Registry, cfg *Config) (*Runner, error) {
	if process == nil {
		return nil, ErrNoProcess
	}

	settings := Config{
		Logger:              nil,
		SelectResources:     nil,
		AllowRequirements:   nil,
		OutDir:              "",
		TmpDirPrefix:        "",
		OnError:             "",
		Containers:          ContainerPolicy{Disabled: false, NoMatchUser: false, NoReadOnly: false, Keep: false},
		Resources:           ResourceBudget{Cores: 0, RAMMiB: 0, TmpDirMiB: 0, OutDirMiB: 0},
		EvalTimeout:         0,
		MaxParallel:         0,
		LenientRequirements: false,
	}
	if cfg != nil {
		settings = *cfg
	}

	analysed, err := newPlan(ctx, process, &settings)
	if err != nil {
		return nil, err
	}

	runner := &Runner{plan: analysed, registry: registry, cfg: &settings}

	err = runner.bindHandlers()
	if err != nil {
		return nil, err
	}

	return runner, nil
}

// Run executes the process to completion, suspension or failure.
//
// It drives a single event-loop goroutine that owns all run state: steps whose inputs are satisfied
// are started, their invocations are handed to handlers on goroutines of their own, and every
// completion triggers one pass that re-scans for newly ready work. Handlers never touch run state,
// so there is no lock anywhere in the loop.
//
// Cancelling ctx aborts the run: in-flight handlers see the cancelled context, and Run returns
// ctx.Err() alongside a best-effort [RunState] recording how far it had got. Suspensions already
// recorded are inert data and are unaffected — resuming from a persisted snapshot is the only way
// back into a suspended branch, and a fresh Run always starts from the beginning.
//
// The error is non-nil exactly when the run did not succeed and is not merely waiting: a failed
// step's error, or the context's.
func (r *Runner) Run(ctx context.Context, inputs map[string]any) (RunResult, error) {
	return r.newLoop(newRunState(r.initialInputs(inputs))).run(ctx)
}

// Resume re-enters a run that suspended, supplying the outcome each waiting invocation has since
// received.
//
// The snapshot's version is checked first and a mismatch is refused outright: rehydrating a state
// whose meaning has moved would resume a run that never existed. Each resumed outcome is then
// matched to the invocation it addresses and — for a successful one — validated against that step's
// declared output types before it enters the graph, because resumed outputs come from outside the
// engine and are the least-trusted data in it.
//
// Injected outcomes behave exactly as though their handlers had just returned them, and the same
// loop then carries on. A Resume may therefore itself return a suspended result, when a gate
// downstream of the one just satisfied is reached, so a caller loops persist-and-resume until the
// status is terminal. Cancellation behaves as it does in [Runner.Run].
func (r *Runner) Resume(ctx context.Context, state RunState, resumed []ResumedStep) (RunResult, error) {
	restored, err := state.rehydrate()
	if err != nil {
		return RunResult{Status: StatusPermanentFail, Outputs: nil, Suspensions: nil, State: state}, err
	}

	loop := r.newLoop(restored)

	err = loop.inject(resumed)
	if err == nil {
		err = loop.rehydrateSteps()
	}

	if err != nil {
		return RunResult{Status: StatusPermanentFail, Outputs: nil, Suspensions: nil, State: loop.state.clone()}, err
	}

	return loop.run(ctx)
}

// bindHandlers resolves each step's handler once, so that a class nothing can execute is reported
// before the run starts rather than when the step that needs it becomes ready.
func (r *Runner) bindHandlers() error {
	for _, step := range r.plan.steps {
		handler, found := r.registry.Handler(step.class)
		if !found {
			return fmt.Errorf("%w: step %q has class %q", ErrNoHandler, step.id, step.class)
		}

		step.handler = handler
	}

	return nil
}

// initialInputs builds the input object a run starts from: the caller's job order, with the
// declared default filling in every input it left out.
func (r *Runner) initialInputs(inputs map[string]any) map[string]any {
	object := make(map[string]any, len(inputs)+len(r.plan.inputs))

	maps.Copy(object, inputs)

	for index := range r.plan.inputs {
		decl := &r.plan.inputs[index]
		if _, supplied := object[decl.Name]; !supplied && decl.Default != nil {
			object[decl.Name] = decl.Default
		}
	}

	return object
}

// selectResources applies the configured selector, or the default one.
func (c *Config) selectResources(request ResourceRequest) (Resources, error) {
	if c.SelectResources == nil {
		return DefaultSelectResources(request, c.Resources)
	}

	return c.SelectResources(request, c.Resources)
}

// evalOptions renders the configuration's expression settings as evaluator options.
func (c *Config) evalOptions() []cwlcore.EvalOption {
	if c.EvalTimeout <= 0 {
		return nil
	}

	return []cwlcore.EvalOption{cwlcore.WithTimeout(c.EvalTimeout)}
}

// checkOptions renders the configuration's unknown-requirement policy as check options.
func (c *Config) checkOptions() []cwlcore.CheckOption {
	if !c.LenientRequirements {
		return nil
	}

	return []cwlcore.CheckOption{cwlcore.WithLenient()}
}

// stepDirs is the pair of directories allocated to one invocation.
type stepDirs struct {
	// OutDir is where the invocation writes its outputs.
	OutDir string

	// TmpDir is the scratch space the invocation may use.
	TmpDir string
}

// dirsFor allocates the output and scratch directories of one invocation.
//
// The paths are derived from the configured bases and are therefore stable: the same invocation of
// the same run gets the same directory when the run is resumed, which a handler that has already
// written half its output depends on. Two runs that must not collide are given different bases —
// that is the caller's decision, not something a scheduler can infer.
//
// The directories are not created here. Whether one is needed at all, and what has to be staged
// into it, is knowledge the handler has and the scheduler does not.
func (c *Config) dirsFor(step string, index []int) stepDirs {
	name := invocationName(step, index)

	return stepDirs{OutDir: joinBase(c.OutDir, name), TmpDir: joinBase(c.TmpDirPrefix, name)}
}

// invocationName renders one invocation's directory name: the step, plus its scatter coordinates
// when it has any.
func invocationName(step string, index []int) string {
	parts := make([]string, 0, len(index)+1)
	parts = append(parts, step)

	for _, coordinate := range index {
		parts = append(parts, strconv.Itoa(coordinate))
	}

	return strings.Join(parts, "_")
}

// joinBase appends name to a configured base directory, leaving an unset base unset.
func joinBase(base, name string) string {
	if base == "" {
		return ""
	}

	return filepath.Join(base, sanitizePathSegment(name))
}

// sanitizePathSegment reduces a step identifier to something safe to use as one path segment. A
// step identifier is document-supplied, so it is not trusted to stay inside the base directory.
func sanitizePathSegment(name string) string {
	replaced := strings.Map(func(r rune) rune {
		if r == filepath.Separator || r == '/' || r == '\\' || r == 0 {
			return '_'
		}

		return r
	}, name)

	if replaced == "" || replaced == "." || replaced == ".." {
		return "_"
	}

	return replaced
}
