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

// Step-handler errors. Test with [errors.Is].
var (
	// ErrNoHandler reports a process class with no registered handler.
	ErrNoHandler = errors.New("no handler registered for process class")

	// ErrResultInvariant reports a [Result] violating its documented invariants.
	ErrResultInvariant = errors.New("handler returned an invalid Result")

	// ErrWrongProcessClass reports a class/handler type mismatch (scheduler bug).
	ErrWrongProcessClass = errors.New("handler received a process of the wrong class")

	// ErrNotImplemented reports a placeholder handler that is not yet written.
	ErrNotImplemented = errors.New("not yet implemented")
)

// Class is a CWL process class string (e.g. "CommandLineTool", "Workflow").
type Class string

// Status is the outcome of a step invocation.
type Status string

const (
	// StatusSuccess reports a step that ran and produced outputs.
	StatusSuccess Status = "success"
	// StatusPermanentFail reports an unrecoverable step failure.
	StatusPermanentFail Status = "permanentFail"
	// StatusTemporaryFail signals a retryable failure. cwlexec never retries; retry is caller policy.
	StatusTemporaryFail Status = "temporaryFail"
	// StatusSkipped is set by the scheduler when `when` evaluates to false; never returned by a handler.
	StatusSkipped Status = "skipped"
	// StatusSuspended indicates a pause pending an external event; see [Result.Suspension].
	StatusSuspended Status = "suspended"
)

// Resources is the resolved resource reservation for an invocation (runtime.cores, etc.).
// Zero fields mean unresolved, not zero.
type Resources struct {
	// Cores is a float because coresMin/coresMax may be fractional.
	Cores     float64
	RAMMiB    int64
	TmpDirMiB int64
	OutDirMiB int64
}

// StepCall is the fully-resolved unit of work handed to a [StepHandler].
// One per invocation; treat as read-only.
type StepCall struct {
	StepID  string
	Process cwlcore.Process
	Class   Class
	// Inputs keys are parameter short names; see [ShortName].
	Inputs map[string]any
	// ScatterIndex is empty for unscattered steps.
	ScatterIndex []int
	// Requirements may be nil (reads as empty scope).
	Requirements      *cwlcore.RequirementScope
	Resources         Resources
	ContainerExecutor ContainerExecutor
	OutputResolver    OutputResolver
	Containers        ContainerPolicy
	// OutDir is runtime.outdir.
	OutDir string
	// TmpDir is runtime.tmpdir; not preserved after the step finishes.
	TmpDir string
	// Eval may be nil; [StepCall.Evaluator] derives one from Requirements if so.
	Eval *cwlcore.Evaluator
	// Logger may be nil; use [StepCall.Log] for a nil-safe accessor.
	Logger *slog.Logger
}

// StepHandler executes one invocation of a process class. Must be safe for concurrent use.
type StepHandler interface {
	// Execute runs one invocation. `return Result{}, err` is safe (treated as PermanentFail).
	// Must not block on external events; use [StepCall.Suspend] instead.
	Execute(ctx context.Context, call *StepCall) (Result, error)
}

// HandlerFunc adapts a function to [StepHandler].
type HandlerFunc func(ctx context.Context, call *StepCall) (Result, error)

// Execute calls f, satisfying [StepHandler].
func (f HandlerFunc) Execute(ctx context.Context, call *StepCall) (Result, error) {
	return f(ctx, call)
}

// Result is a handler's outcome. [Outcome] enforces invariants at the scheduler boundary.
type Result struct {
	Status Status
	// Outputs is keyed by output parameter short name; see [ShortName].
	Outputs map[string]any
	// Suspension is set iff Status is StatusSuspended.
	Suspension *Suspension
}

// Suspension is a handle for a paused invocation. Token and Payload are opaque to cwlexec.
type Suspension struct {
	StepID       string
	ScatterIndex []int
	Token        string
	Payload      []byte
}

// Success returns a successful Result carrying outputs.
func Success(outputs map[string]any) (Result, error) {
	return Result{Status: StatusSuccess, Outputs: outputs, Suspension: nil}, nil
}

// PermanentFail returns a permanently failed Result.
func PermanentFail(err error) (Result, error) {
	return Result{Status: StatusPermanentFail, Outputs: nil, Suspension: nil}, err
}

// TemporaryFail returns a temporarily failed Result. cwlexec does not retry.
func TemporaryFail(err error) (Result, error) {
	return Result{Status: StatusTemporaryFail, Outputs: nil, Suspension: nil}, err
}

// Suspend returns a suspended Result. Addressing fields are filled from the call.
func (c *StepCall) Suspend(token string, payload []byte) (Result, error) {
	suspension := &Suspension{
		StepID:       c.StepID,
		ScatterIndex: slices.Clone(c.ScatterIndex),
		Token:        token,
		Payload:      payload,
	}

	return Result{Status: StatusSuspended, Outputs: nil, Suspension: suspension}, nil
}

// Evaluator returns Eval if set, otherwise derives one from Requirements via [EvaluatorFor].
func (c *StepCall) Evaluator() *cwlcore.Evaluator {
	if c.Eval != nil {
		return c.Eval
	}

	return EvaluatorFor(c.Requirements)
}

// Log returns Logger, or [slog.Default] if nil.
func (c *StepCall) Log() *slog.Logger {
	if c.Logger == nil {
		return slog.Default()
	}

	return c.Logger
}

// RuntimeContext builds the runtime.* parameter context for expressions.
// Zero resource fields are left undefined (not zero). Cores is ceil'd to an integer.
func (c *StepCall) RuntimeContext() cwlcore.RuntimeContext {
	runtime := cwlcore.RuntimeContext{
		Cores:      nil,
		RAM:        nil,
		OutdirSize: nil,
		TmpdirSize: nil,
		ExitCode:   nil,
		Outdir:     c.OutDir,
		Tmpdir:     c.TmpDir,
	}

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

// Outcome normalizes a handler return into what the scheduler acts on.
// Non-nil error with non-failure status becomes PermanentFail.
func Outcome(result Result, err error) (Result, error) {
	if err != nil {
		if result.Status == StatusPermanentFail || result.Status == StatusTemporaryFail {
			return Result{Status: result.Status, Outputs: nil, Suspension: nil}, err
		}

		return Result{Status: StatusPermanentFail, Outputs: nil, Suspension: nil}, err
	}

	invalid := result.validate()
	if invalid != nil {
		return Result{Status: StatusPermanentFail, Outputs: nil, Suspension: nil}, invalid
	}

	return result, nil
}

// validate checks [Result] invariants.
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

// ShortName extracts the short name from a resolved identifier (e.g. "file:///w.cwl#tool/out" → "out").
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
