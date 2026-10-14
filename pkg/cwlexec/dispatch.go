package cwlexec

import "context"

// StepInvocation is a ready-to-run step handler call. The caller decides how to
// run it (goroutine, coroutine, synchronously) and feeds the result back via
// [DispatchLoop.Complete].
type StepInvocation struct {
	id       int
	fn       func(context.Context) (Result, error)
	complete func(Result, error)
}

// ID returns the invocation's opaque identifier, passed back to Complete.
func (inv StepInvocation) ID() int { return inv.id }

// Run executes the step handler with the given context and returns its result.
// The context is passed through to the handler's Execute method, allowing the
// caller to control cancellation and carry values (e.g. a Temporal coroutine
// context).
func (inv StepInvocation) Run(ctx context.Context) (Result, error) { return inv.fn(ctx) }

// DispatchLoop exposes the runner's state machine to a caller-provided
// execution loop. The caller calls Pending to get dispatchable work, runs
// each StepInvocation however it likes, and feeds results back via Complete.
type DispatchLoop struct {
	l          *runLoop
	dispatched []StepInvocation
	nextID     int
}

// Pending returns invocations that are ready to dispatch, respecting
// MaxParallel. Returns nil when nothing is ready. The caller must dispatch
// each returned invocation exactly once.
func (d *DispatchLoop) Pending() []StepInvocation {
	d.l.startReadySteps()

	var batch []StepInvocation

	for _, step := range d.l.runner.plan.steps {
		if !d.l.hasCapacity() {
			break
		}

		d.collectStep(step, &batch)
	}

	return batch
}

// Complete feeds a finished invocation's result back into the state machine.
// Must be called exactly once per dispatched invocation.
func (d *DispatchLoop) Complete(id int, result Result, err error) {
	d.dispatched[id].complete(result, err)
}

// InFlight returns how many invocations have been dispatched but not yet
// completed.
func (d *DispatchLoop) InFlight() int { return d.l.running }

func (d *DispatchLoop) collectStep(step *plannedStep, batch *[]StepInvocation) {
	recorded := d.l.state.step(step.id)
	if !recorded.Started || recorded.Status != "" {
		return
	}

	jobs := d.l.jobs[step.id]

	for index := range jobs {
		if !d.l.hasCapacity() || recorded.Status != "" {
			return
		}

		if jobs[index].dispatched || recorded.Jobs[index].Status != "" {
			continue
		}

		inv, ok := d.buildInvocation(step, &jobs[index], index)
		if !ok {
			continue
		}

		*batch = append(*batch, inv)
	}
}

func (d *DispatchLoop) buildInvocation(
	step *plannedStep, job *runJob, index int,
) (StepInvocation, bool) {
	call, err := d.l.newCall(job, step)
	if err != nil {
		d.l.record(jobDone{
			step: step, job: job, index: index,
			result: Result{Status: "", Outputs: nil, Suspension: nil}, err: err,
		})

		return StepInvocation{id: 0, fn: nil, complete: nil}, false
	}

	job.dispatched = true
	d.l.running++

	inv := StepInvocation{
		id: d.nextID,
		fn: func(ctx context.Context) (Result, error) {
			return step.handler.Execute(ctx, call)
		},
		complete: func(result Result, err error) {
			d.l.running--
			d.l.record(jobDone{step: step, job: job, index: index, result: result, err: err})
		},
	}

	d.nextID++
	d.dispatched = append(d.dispatched, inv)

	return inv, true
}
