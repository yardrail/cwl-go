package cwlexec

import (
	"context"
	"fmt"
	"maps"
)

// runJob is the run-time half of one invocation: the input object it is to be executed with, and
// whether it has been handed to a handler yet.
//
// The persisted half lives in [jobState]. The two are kept apart because the input object of an
// invocation is derivable from the run's recorded outputs, and a snapshot that carried a copy of
// every sub-job's inputs would grow with the square of a large scatter for nothing.
type runJob struct {
	inputs     map[string]any
	index      []int
	err        error
	dispatched bool
}

// jobDone is one invocation reporting back to the event loop.
//
// The step is carried as the planned step itself rather than as its identifier. That is not a
// convenience: it is what makes the report unable to name a step this run does not have, which a
// string would leave the receiving end to look up and then wonder about.
type jobDone struct {
	err    error
	step   *plannedStep
	job    *runJob
	result Result
	index  int
}

// runLoop is the single goroutine that owns a run.
//
// Every mutation of run state happens on this one goroutine. Handlers execute on goroutines of
// their own and report back over done, which is the only channel of communication; nothing is
// shared, so there is no lock, no condition variable and no per-step wake-up. The reference
// implementation instead funnels every state change through one global condition variable, which is
// the design this deliberately does not port.
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

// newLoop prepares the event loop for a run over state, which it takes ownership of.
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

// run drives the loop to a terminal or suspended state and renders the result.
//
// Closing finished on the way out releases any handler goroutine still in flight from its report:
// a cancelled run does not wait for a handler that is ignoring its context, and does not leak the
// goroutine either, since the send it is parked on can no longer block.
func (l *runLoop) run(ctx context.Context) (RunResult, error) {
	defer close(l.finished)

	err := l.execute(ctx)
	if err != nil {
		return RunResult{Outputs: nil, Status: StatusPermanentFail, Suspensions: nil, State: l.state.clone()}, err
	}

	return l.result()
}

// execute is the loop proper: dispatch everything that is ready, then block until something
// completes, and repeat until nothing is left running.
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

// dispatch starts every step whose inputs are satisfied and launches every invocation the
// parallelism cap has room for, repeating until neither makes progress.
//
// The repetition is what makes a step that completes without a handler — a zero-cardinality
// scatter, a `when` that gated every sub-job out — unblock the steps downstream of it within the
// same pass, rather than stalling the run until some unrelated invocation happens to finish.
func (l *runLoop) dispatch(ctx context.Context) {
	for {
		started := l.startReadySteps()
		launched := l.launchPending(ctx)

		if !started && !launched {
			return
		}
	}
}

// startReadySteps starts every step whose dependencies have all produced their outputs, and reports
// whether any did.
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

// ready reports whether every step this one draws on has finished with usable outputs. A dependency
// that failed is never ready, which is what keeps a failed branch from being propagated as data.
func (l *runLoop) ready(step *plannedStep) bool {
	for _, dep := range step.deps {
		if !produced(l.state.steps[dep]) {
			return false
		}
	}

	return true
}

// produced reports whether a step has finished in a way that gives its output ports values.
func produced(recorded *stepState) bool {
	return recorded != nil && (recorded.Status == StatusSuccess || recorded.Status == StatusSkipped)
}

// startStep resolves a step's inputs, expands its scatter, applies its per-sub-job valueFrom and
// `when`, and records the resulting invocations as pending.
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

// adopt installs a freshly expanded job list on the step, recording each invocation's coordinates.
func (l *runLoop) adopt(step *plannedStep, recorded *stepState, jobs []runJob, shape []int) {
	l.jobs[step.id] = jobs
	recorded.Shape = shape
	recorded.Jobs = make([]jobState, len(jobs))

	for index := range jobs {
		recorded.Jobs[index].Index = jobs[index].index
	}
}

// gateJobs applies valueFrom and then `when` to each invocation that has no outcome yet.
//
// Both run once per sub-job, because both are defined over the input object of the individual
// scatter job: valueFrom sees that job's element as `self`, and a `when` may therefore admit some
// elements of a scatter and skip others.
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

// launchPending hands pending invocations to their handlers, up to the parallelism cap, and reports
// whether any were launched.
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

// launchStep hands one step's pending invocations to their handlers, reporting whether it got
// through all of them: a false result means the parallelism cap is full, or the step finished
// during the pass, and there is nothing more to launch this time round.
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

// hasCapacity reports whether another handler call may be started. A MaxParallel of zero or less is
// unbounded.
func (l *runLoop) hasCapacity() bool {
	return l.runner.cfg.MaxParallel <= 0 || l.running < l.runner.cfg.MaxParallel
}

// launch builds the call for one invocation and runs its handler on a goroutine of its own.
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

// record folds one invocation's report back into the run state.
//
// Every handler return passes through [Outcome] first. A handler is the only third-party code in
// this engine, so what it returns is normalized and checked rather than trusted: a Result that
// contradicts itself becomes a permanent failure, whatever it claimed.
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

// finishIfComplete gives the step its outcome once every one of its invocations has one.
//
// A suspended invocation is not one, which is exactly how a suspension pauses a single scatter slot
// without touching its siblings: they keep running, and the gather waits for the slot to be
// resumed.
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

// jobsOutcome reduces the invocations' outcomes to the step's own.
//
// A permanent failure outranks a temporary one, so a step that failed both ways reports the
// verdict that forecloses a retry. A step is only skipped when it was not scattered: a scattered
// step whose every sub-job was gated out still succeeds, producing arrays of nulls, because that is
// what its downstream consumers read.
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

// jobError recovers the error behind a failed invocation, preferring the live error value — so that
// [errors.Is] still works — and falling back to the text a rehydrated snapshot preserved.
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

// failStepWith records a failure for the step and, unless the caller asked to keep going, stops the
// loop from starting anything new. Work already in flight is left to finish: cancelling it is what
// the context is for, and a handler that has already started a container should be told once, by
// one mechanism.
func (l *runLoop) failStepWith(step *plannedStep, recorded *stepState, status Status, err error) {
	recorded.Started = true
	recorded.Status = status
	recorded.Error = err.Error()
	l.errs[step.id] = err

	if l.runner.cfg.OnError != OnErrorContinue {
		l.stopped = true
	}
}

// stepInputs resolves the input object a step starts from.
//
// A bare process run as the single implicit step takes the run's own input object directly: it has
// no `in` wiring to follow, because there is no workflow around it to wire it to.
func (l *runLoop) stepInputs(step *plannedStep) (map[string]any, error) {
	if !step.implicit {
		return resolveInputs(step, l.sourceValue)
	}

	object := make(map[string]any, len(l.state.inputs)+len(step.defaults))
	maps.Copy(object, l.state.inputs)
	applyProcessDefaults(step.defaults, object)

	return object, nil
}

// sourceValue reads the value behind a resolved source identifier, reporting whether the port that
// produces it has finished.
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

// expandJobs enumerates a step's invocations: one, or one per element of its scatter.
//
// A single scatter key with no declared scatterMethod is a dotproduct over that one array, which is
// the only reading available — the schema only requires scatterMethod when more than one input is
// scattered, and every method agrees on the one-key case.
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

// gatherStep assembles a finished step's output object from its invocations' outputs.
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

// projectOutputs reduces a handler's output object to the ports the step declares in its out list.
//
// The two differ whenever a step consumes a subset of what its tool produces, and it is the step's
// list that names what the rest of the workflow can read. Projecting here — rather than passing the
// handler's object through — is what keeps a phantom port from appearing downstream, and what
// guarantees every declared port is present even if the handler omitted it.
func projectOutputs(ports []string, outputs map[string]any) map[string]any {
	projected := make(map[string]any, len(ports))
	for _, port := range ports {
		projected[port] = outputs[port]
	}

	return projected
}

// newCall builds the [StepCall] for one invocation, resolving its resource reservation and the
// directories it is to work in.
//
// The input object is projected onto the parameters the process declares on the way past, which is
// the last thing to happen to it: valueFrom and `when` have both run by now, and both are entitled
// to read a step input the process does not declare. See [projectDeclaredInputs].
func (l *runLoop) newCall(job *runJob, step *plannedStep) (*StepCall, error) {
	dirs := l.runner.cfg.dirsFor(step.id, job.index)

	call := &StepCall{
		StepID:       step.id,
		Process:      step.run,
		Class:        step.class,
		Inputs:       projectDeclaredInputs(step, job.inputs),
		ScatterIndex: job.index,
		Requirements: step.scope,
		Resources:    Resources{Cores: 0, RAMMiB: 0, TmpDirMiB: 0, OutDirMiB: 0},
		Containers:   l.runner.cfg.Containers,
		OutDir:       dirs.OutDir,
		TmpDir:       dirs.TmpDir,
		Eval:         step.eval,
		Logger:       l.runner.cfg.Logger,
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
