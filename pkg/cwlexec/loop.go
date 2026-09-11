package cwlexec

import (
	"context"
	"fmt"
	"maps"
)

// runJob is the in-memory state of one invocation. Persisted state lives in [jobState].
type runJob struct {
	inputs     map[string]any
	index      []int
	err        error
	dispatched bool
}

// jobDone is an invocation's completion report to the event loop.
type jobDone struct {
	err    error
	step   *plannedStep
	job    *runJob
	result Result
	index  int
}

// runLoop owns all run state on a single goroutine. Handlers report back over done.
type runLoop struct {
	runner   *Runner
	state    *RunState
	jobs     map[string][]runJob
	errs     map[string]error
	done     chan jobDone
	finished chan struct{}
	running  int
	stopped  bool
}

// newLoop prepares the event loop for a run, taking ownership of state.
func (r *Runner) newLoop(state *RunState) *runLoop {
	return &runLoop{
		runner:   r,
		state:    state,
		jobs:     make(map[string][]runJob, len(r.plan.steps)),
		errs:     make(map[string]error, len(r.plan.steps)),
		done:     make(chan jobDone),
		finished: make(chan struct{}),
		running:  0,
		stopped:  false,
	}
}

// run drives the loop to completion or suspension and returns the result.
func (l *runLoop) run(ctx context.Context) (RunResult, error) {
	defer close(l.finished)

	err := l.execute(ctx)
	if err != nil {
		return RunResult{Outputs: nil, Status: StatusPermanentFail, Suspensions: nil, State: l.state.clone()}, err
	}

	return l.result()
}

// execute runs the dispatch/wait loop until nothing is running.
func (l *runLoop) execute(ctx context.Context) error {
	for {
		l.dispatch(ctx)

		if l.running == 0 {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case finished := <-l.done:
			l.running--
			l.record(finished)
		}
	}
}

// dispatch starts ready steps and launches pending invocations, repeating until no progress.
func (l *runLoop) dispatch(ctx context.Context) {
	for {
		started := l.startReadySteps()
		launched := l.launchPending(ctx)

		if !started && !launched {
			return
		}
	}
}

// startReadySteps starts steps whose dependencies are all satisfied. Returns true if any started.
func (l *runLoop) startReadySteps() bool {
	if l.stopped {
		return false
	}

	progressed := false

	for _, step := range l.runner.plan.steps {
		recorded := l.state.step(step.id)
		if recorded.Started || recorded.Status != "" || !l.ready(step) {
			continue
		}

		l.startStep(step, recorded)

		progressed = true
	}

	return progressed
}

// ready reports whether all dependencies have produced outputs.
func (l *runLoop) ready(step *plannedStep) bool {
	for _, dep := range step.deps {
		if !produced(l.state.steps[dep]) {
			return false
		}
	}

	return true
}

// produced reports whether a step finished with usable outputs (success or skipped).
func produced(recorded *stepState) bool {
	return recorded != nil && (recorded.Status == StatusSuccess || recorded.Status == StatusSkipped)
}

// startStep resolves inputs, expands scatter, applies valueFrom/when, and records invocations.
func (l *runLoop) startStep(step *plannedStep, recorded *stepState) {
	recorded.Started = true

	inputs, err := l.stepInputs(step)
	if err != nil {
		l.failStep(step, recorded, err)

		return
	}

	jobs, shape, err := expandJobs(step, inputs)
	if err != nil {
		l.failStep(step, recorded, err)

		return
	}

	l.adopt(step, recorded, jobs, shape)

	err = l.gateJobs(step, recorded)
	if err != nil {
		l.failStep(step, recorded, err)

		return
	}

	l.finishIfComplete(step, recorded)
}

// adopt installs expanded jobs on the step and records their scatter coordinates.
func (l *runLoop) adopt(step *plannedStep, recorded *stepState, jobs []runJob, shape []int) {
	l.jobs[step.id] = jobs
	recorded.Shape = shape
	recorded.Jobs = make([]jobState, len(jobs))

	for index := range jobs {
		recorded.Jobs[index].Index = jobs[index].index
	}
}

// gateJobs applies valueFrom and `when` to each pending invocation.
func (l *runLoop) gateJobs(step *plannedStep, recorded *stepState) error {
	jobs := l.jobs[step.id]

	for index := range jobs {
		if recorded.Jobs[index].Status != "" {
			continue
		}

		resolved, err := applyValueFrom(step, jobs[index].inputs)
		if err != nil {
			return err
		}

		jobs[index].inputs = resolved

		admitted, err := EvalWhen(step.when, resolved, step.eval)
		if err != nil {
			return fmt.Errorf("step %q: %w", step.id, err)
		}

		if admitted {
			continue
		}

		recorded.Jobs[index].Status = StatusSkipped
		recorded.Jobs[index].Outputs = SkippedOutputs(step.out)
	}

	return nil
}

// launchPending dispatches pending invocations up to the parallelism cap.
func (l *runLoop) launchPending(ctx context.Context) bool {
	if l.stopped {
		return false
	}

	launched := false

	for _, step := range l.runner.plan.steps {
		recorded := l.state.step(step.id)
		if !recorded.Started || recorded.Status != "" {
			continue
		}

		if !l.launchStep(ctx, step, recorded, &launched) {
			return launched
		}
	}

	return launched
}

// launchStep launches one step's pending invocations. Returns false when the cap is full.
func (l *runLoop) launchStep(ctx context.Context, step *plannedStep, recorded *stepState, launched *bool) bool {
	jobs := l.jobs[step.id]

	for index := range jobs {
		if !l.hasCapacity() || recorded.Status != "" {
			return false
		}

		if jobs[index].dispatched || recorded.Jobs[index].Status != "" {
			continue
		}

		l.launch(ctx, jobs, step, index)

		*launched = true
	}

	return true
}

// hasCapacity reports whether another handler may be started.
func (l *runLoop) hasCapacity() bool {
	return l.runner.cfg.MaxParallel <= 0 || l.running < l.runner.cfg.MaxParallel
}

// launch builds a [StepCall] and runs the handler on its own goroutine.
func (l *runLoop) launch(ctx context.Context, jobs []runJob, step *plannedStep, index int) {
	job := &jobs[index]
	job.dispatched = true

	call, err := l.newCall(job, step)
	if err != nil {
		l.record(
			jobDone{
				err:    err,
				step:   step,
				job:    job,
				result: Result{Status: "", Outputs: nil, Suspension: nil},
				index:  index,
			},
		)

		return
	}

	l.running++

	go func() {
		result, executeErr := step.handler.Execute(ctx, call)

		select {
		case l.done <- jobDone{step: step, job: job, index: index, result: result, err: executeErr}:
		case <-l.finished:
		}
	}()
}

// record folds an invocation's result into run state via [Outcome].
func (l *runLoop) record(finished jobDone) {
	step := finished.step
	recorded := l.state.step(step.id)
	job := &recorded.Jobs[finished.index]

	result, err := Outcome(finished.result, finished.err)

	job.Status = result.Status
	job.Suspension = wireSuspension(result.Suspension)

	if err != nil {
		job.Error = err.Error()
		finished.job.err = err
	}

	if result.Status == StatusSuccess {
		job.Outputs = projectOutputs(step.out, result.Outputs)
	}

	l.finishIfComplete(step, recorded)
}

// finishIfComplete completes the step once all its invocations have terminal outcomes.
func (l *runLoop) finishIfComplete(step *plannedStep, recorded *stepState) {
	for index := range recorded.Jobs {
		if !recorded.Jobs[index].terminal() {
			return
		}
	}

	status, failed := l.jobsOutcome(step, recorded)
	if failed != nil {
		l.failStepWith(step, recorded, status, failed)

		return
	}

	outputs, err := gatherStep(step, recorded)
	if err != nil {
		l.failStep(step, recorded, err)

		return
	}

	recorded.Status = status
	recorded.Outputs = outputs
}

// jobsOutcome reduces all invocation outcomes to the step's overall status.
func (l *runLoop) jobsOutcome(step *plannedStep, recorded *stepState) (Status, error) {
	worst := StatusSuccess

	var failure error

	for index := range recorded.Jobs {
		status := recorded.Jobs[index].Status
		if status != StatusPermanentFail && status != StatusTemporaryFail {
			continue
		}

		if failure == nil || (worst == StatusTemporaryFail && status == StatusPermanentFail) {
			worst, failure = status, l.jobError(step, recorded, index)
		}
	}

	if failure != nil {
		return worst, failure
	}

	if recorded.Shape == nil && recorded.Jobs[0].Status == StatusSkipped {
		return StatusSkipped, nil
	}

	return StatusSuccess, nil
}

// jobError returns the error for a failed invocation, preferring the live value over serialized text.
func (l *runLoop) jobError(step *plannedStep, recorded *stepState, index int) error {
	jobs := l.jobs[step.id]
	if index < len(jobs) && jobs[index].err != nil {
		return jobs[index].err
	}

	return fmt.Errorf("%w: %s", ErrStepFailed, recorded.Jobs[index].Error)
}

// failStep records a permanent failure for the step.
func (l *runLoop) failStep(step *plannedStep, recorded *stepState, err error) {
	l.failStepWith(step, recorded, StatusPermanentFail, err)
}

// failStepWith records a step failure. Stops the loop unless OnErrorContinue is set.
func (l *runLoop) failStepWith(step *plannedStep, recorded *stepState, status Status, err error) {
	recorded.Started = true
	recorded.Status = status
	recorded.Error = err.Error()
	l.errs[step.id] = err

	if l.runner.cfg.OnError != OnErrorContinue {
		l.stopped = true
	}
}

// stepInputs resolves the input object for a step. Implicit steps use the run's inputs directly.
func (l *runLoop) stepInputs(step *plannedStep) (map[string]any, error) {
	if !step.implicit {
		return resolveInputs(step, l.sourceValue)
	}

	object := make(map[string]any, len(l.state.inputs)+len(step.defaults))
	maps.Copy(object, l.state.inputs)
	applyProcessDefaults(step.defaults, object)

	return object, nil
}

// sourceValue reads the value behind a resolved source identifier.
func (l *runLoop) sourceValue(id string) (any, bool) {
	ref, known := l.runner.plan.sources[id]
	if !known {
		return nil, false
	}

	if ref.Step == "" {
		return l.state.inputs[ref.Port], true
	}

	recorded := l.state.steps[ref.Step]
	if recorded == nil || !produced(recorded) {
		return nil, false
	}

	return recorded.Outputs[ref.Port], true
}

// expandJobs enumerates a step's invocations: one if unscattered, or one per scatter element.
func expandJobs(step *plannedStep, inputs map[string]any) ([]runJob, []int, error) {
	if len(step.scatter) == 0 {
		return []runJob{{inputs: inputs, index: nil, err: nil, dispatched: false}}, nil, nil
	}

	method := step.method
	if method == "" {
		method = Dotproduct
	}

	expanded, err := ExpandScatter(inputs, step.scatter, method)
	if err != nil {
		return nil, nil, fmt.Errorf("step %q: %w", step.id, err)
	}

	jobs := make([]runJob, 0, len(expanded.Jobs))
	for _, job := range expanded.Jobs {
		jobs = append(jobs, runJob{inputs: job.Inputs, index: job.Index, err: nil, dispatched: false})
	}

	return jobs, expanded.OutShape.Dims, nil
}

// gatherStep assembles a step's output object from its invocations.
func gatherStep(step *plannedStep, recorded *stepState) (map[string]any, error) {
	if recorded.Shape == nil {
		return recorded.Jobs[0].Outputs, nil
	}

	expanded := ScatterPlan{
		Method:   "",
		Keys:     nil,
		Jobs:     make([]ScatterJob, 0, len(recorded.Jobs)),
		OutShape: OutShape{Dims: recorded.Shape},
	}

	outputs := make(map[int]map[string]any, len(recorded.Jobs))

	for index := range recorded.Jobs {
		expanded.Jobs = append(expanded.Jobs, ScatterJob{Inputs: nil, Index: recorded.Jobs[index].Index})
		outputs[index] = recorded.Jobs[index].Outputs
	}

	gathered, err := expanded.Gather(outputs, step.out)
	if err != nil {
		return nil, fmt.Errorf("step %q: %w", step.id, err)
	}

	return gathered, nil
}

// projectOutputs reduces outputs to only the ports declared in the step's out list.
func projectOutputs(ports []string, outputs map[string]any) map[string]any {
	projected := make(map[string]any, len(ports))
	for _, port := range ports {
		projected[port] = outputs[port]
	}

	return projected
}

// newCall builds the [StepCall] for one invocation, resolving resources and directories.
func (l *runLoop) newCall(job *runJob, step *plannedStep) (*StepCall, error) {
	dirs := l.runner.cfg.dirsFor(step.id, job.index)

	call := &StepCall{
		StepID:            step.id,
		Process:           step.run,
		Class:             step.class,
		Inputs:            projectDeclaredInputs(step, job.inputs),
		ScatterIndex:      job.index,
		Requirements:      step.scope,
		Resources:         Resources{Cores: 0, RAMMiB: 0, TmpDirMiB: 0, OutDirMiB: 0},
		ContainerExecutor: l.runner.cfg.ContainerExecutor,
		Containers:        l.runner.cfg.Containers,
		OutDir:            dirs.OutDir,
		TmpDir:            dirs.TmpDir,
		Eval:              step.eval,
		Logger:            l.runner.cfg.Logger,
	}

	if l.runner.registry.IsUnbudgeted(step.class) {
		return call, nil
	}

	request, err := resourceRequest(step, call)
	if err != nil {
		return nil, fmt.Errorf("step %q: %w", step.id, err)
	}

	resources, err := l.runner.cfg.selectResources(request)
	if err != nil {
		return nil, fmt.Errorf("step %q: %w", step.id, err)
	}

	call.Resources = resources

	return call, nil
}
