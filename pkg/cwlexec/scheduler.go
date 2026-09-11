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
	// OnErrorStop stops scheduling new work on first failure. Zero value / default.
	OnErrorStop OnError = ""
	// OnErrorContinue runs independent branches despite failures.
	OnErrorContinue OnError = "continue"
)

// Config holds runtime settings not expressed in the CWL document.
// The zero value is usable: unbounded parallelism, no resource ceiling, OnErrorStop.
type Config struct {
	// Logger receives execution diagnostics. Nil means [slog.Default].
	Logger *slog.Logger
	// SelectResources resolves resource requests. Nil means [DefaultSelectResources].
	SelectResources func(request ResourceRequest, budget ResourceBudget) (Resources, error)
	// AllowRequirements names extension requirement classes to allow.
	AllowRequirements map[string]bool
	// OutDir is the base for invocation output directories.
	OutDir string
	// TmpDirPrefix is the base for invocation scratch directories.
	TmpDirPrefix        string
	OnError             OnError
	ContainerExecutor   ContainerExecutor // Nil means containers unsupported.
	OutputResolver      OutputResolver    // Nil means local filesystem.
	Containers          ContainerPolicy
	Resources           ResourceBudget // Machine capacity ceiling for resource selection.
	EvalTimeout         time.Duration  // Per-expression timeout; zero means default.
	MaxParallel         int            // Max concurrent handlers; 0 = unbounded.
	LenientRequirements bool           // Bypass unknown-requirement gate.
}

// ResumedStep supplies the outcome of a previously suspended invocation.
type ResumedStep struct {
	Outputs      map[string]any // Used when Status is StatusSuccess.
	StepID       string
	Status       Status
	ScatterIndex []int // Empty for unscattered steps.
}

// RunResult is the terminal or suspended outcome of a run.
type RunResult struct {
	Outputs     map[string]any // Populated only on StatusSuccess.
	Status      Status
	Suspensions []Suspension // Non-empty iff StatusSuspended.
	State       RunState     // Persist and hand back to [Runner.Resume].
}

// Runner executes a CWL process. Stateless between runs.
type Runner struct {
	plan     *plan
	registry *Registry
	cfg      *Config
}

// NewRunner creates a runner, eagerly analyzing the process. Fails fast on unresolvable
// references, missing handlers, or unsupported requirements. A nil cfg uses zero [Config].
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
		ContainerExecutor:   nil,
		OutputResolver:      nil,
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

// Run executes the process to completion, suspension, or failure.
// Cancelling ctx aborts; error is non-nil unless the run succeeded or suspended.
func (r *Runner) Run(ctx context.Context, inputs map[string]any) (RunResult, error) {
	return r.newLoop(newRunState(r.initialInputs(inputs))).run(ctx)
}

// Resume continues a suspended run with the supplied outcomes. May itself return suspended.
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

// bindHandlers resolves each step's handler eagerly, failing fast on missing handlers.
func (r *Runner) bindHandlers() error {
	for _, step := range r.plan.steps {
		handler, found := r.registry.Handler(step.class)
		if !found {
			if isStepContainer(step.run) {
				handler, found = r.registry.Handler(Class(cwlcore.ClassWorkflow))
			}
		}

		if !found {
			return fmt.Errorf("%w: step %q has class %q", ErrNoHandler, step.id, step.class)
		}

		step.handler = handler
	}

	return nil
}

// initialInputs merges the job order with declared defaults.
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
	OutDir string
	TmpDir string
}

// dirsFor returns deterministic output and scratch directory paths for an invocation.
func (c *Config) dirsFor(step string, index []int) stepDirs {
	name := invocationName(step, index)

	return stepDirs{OutDir: joinBase(c.OutDir, name), TmpDir: joinBase(c.TmpDirPrefix, name)}
}

// invocationName renders a directory name from step ID and scatter coordinates.
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

// sanitizePathSegment makes a step ID safe for use as a single path segment.
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
