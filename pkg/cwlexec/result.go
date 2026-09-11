package cwlexec

import (
	"errors"
	"fmt"
	"slices"
)

var (
	// ErrNoSuspension reports a [ResumedStep] that addresses no suspended invocation.
	ErrNoSuspension = errors.New("no suspended invocation matches this resumed step")
	// ErrResumedStatus reports a [ResumedStep] with an invalid status.
	ErrResumedStatus = errors.New("resumed step has no usable status")
	// ErrStateMismatch reports a snapshot inconsistent with the current workflow.
	ErrStateMismatch = errors.New("run state does not match the workflow it is being resumed against")
	// ErrStepFailed wraps a persisted failure message from a prior run segment.
	ErrStepFailed = errors.New("step failed")
)

// result returns the run's outcome. Priority: failure > suspension > success.
func (l *runLoop) result() (RunResult, error) {
	snapshot := l.state.clone()

	status, failure := l.failure()
	if failure != nil {
		return RunResult{Status: status, Outputs: nil, Suspensions: nil, State: snapshot}, failure
	}

	suspensions := l.suspensions()
	if len(suspensions) > 0 {
		return RunResult{Status: StatusSuspended, Outputs: nil, Suspensions: suspensions, State: snapshot}, nil
	}

	outputs, err := l.runOutputs()
	if err != nil {
		return RunResult{Status: StatusPermanentFail, Outputs: nil, Suspensions: nil, State: snapshot}, err
	}

	return RunResult{Status: StatusSuccess, Outputs: outputs, Suspensions: nil, State: snapshot}, nil
}

// failure returns the worst failure across all steps, in document order. Permanent outranks temporary.
func (l *runLoop) failure() (Status, error) {
	worst := StatusSuccess

	var failure error

	for _, step := range l.runner.plan.steps {
		recorded := l.state.step(step.id)

		status := recorded.Status
		if status != StatusPermanentFail && status != StatusTemporaryFail {
			continue
		}

		if failure == nil || (worst == StatusTemporaryFail && status == StatusPermanentFail) {
			worst, failure = status, l.stepError(step.id, recorded)
		}
	}

	return worst, failure
}

// stepError returns the live error if available, else wraps the persisted error text.
func (l *runLoop) stepError(id string, recorded *stepState) error {
	live, found := l.errs[id]
	if found {
		return live
	}

	return fmt.Errorf("%w: step %q: %s", ErrStepFailed, id, recorded.Error)
}

// suspensions collects all waiting invocations in document order.
func (l *runLoop) suspensions() []Suspension {
	waiting := make([]Suspension, 0)

	for _, step := range l.runner.plan.steps {
		recorded := l.state.step(step.id)

		for index := range recorded.Jobs {
			job := &recorded.Jobs[index]
			if job.Status == StatusSuspended && job.Suspension != nil {
				waiting = append(waiting, job.Suspension.asSuspension())
			}
		}
	}

	return waiting
}

// runOutputs resolves the run's own output object from the outputs its steps produced.
func (l *runLoop) runOutputs() (map[string]any, error) {
	outputs := make(map[string]any, len(l.runner.plan.outputs))

	for index := range l.runner.plan.outputs {
		wiring := &l.runner.plan.outputs[index]
		if !wiring.wired() {
			outputs[wiring.Name] = nil

			continue
		}

		value, err := wiring.value(l.sourceValue)
		if err != nil {
			return nil, err
		}

		outputs[wiring.Name] = value
	}

	return outputs, nil
}

// inject applies each resumed outcome to the invocation it addresses, as though that invocation's
// handler had just returned it.
func (l *runLoop) inject(resumed []ResumedStep) error {
	for index := range resumed {
		err := l.injectOne(&resumed[index])
		if err != nil {
			return err
		}
	}

	return nil
}

// injectOne applies one resumed outcome.
func (l *runLoop) injectOne(resumed *ResumedStep) error {
	step, job, err := l.suspended(resumed)
	if err != nil {
		return err
	}

	switch resumed.Status {
	case StatusSuccess:
		checked, checkErr := checkDeclaredOutputs(step, resumed.Outputs)
		if checkErr != nil {
			return checkErr
		}

		job.Outputs = checked
	case StatusSkipped:
		job.Outputs = SkippedOutputs(step.out)
	case StatusPermanentFail, StatusTemporaryFail:
		job.Error = fmt.Sprintf("step %q was resumed with status %q", step.id, resumed.Status)
	default:
		return fmt.Errorf("%w: step %q reports %q", ErrResumedStatus, resumed.StepID, resumed.Status)
	}

	job.Status = resumed.Status
	job.Suspension = nil

	return nil
}

// suspended finds the suspended invocation a resumed step addresses.
func (l *runLoop) suspended(resumed *ResumedStep) (*plannedStep, *jobState, error) {
	step, known := l.runner.plan.byID[resumed.StepID]
	if !known {
		return nil, nil, fmt.Errorf("%w: no step %q in this workflow", ErrNoSuspension, resumed.StepID)
	}

	recorded := l.state.steps[resumed.StepID]
	if recorded != nil {
		for index := range recorded.Jobs {
			job := &recorded.Jobs[index]
			if job.Status == StatusSuspended && slices.Equal(job.Index, resumed.ScatterIndex) {
				return step, job, nil
			}
		}
	}

	return nil, nil, fmt.Errorf("%w: step %q at %v", ErrNoSuspension, resumed.StepID, resumed.ScatterIndex)
}

// rehydrateSteps rebuilds run-time state from a snapshot so the loop can resume.
func (l *runLoop) rehydrateSteps() error {
	for _, step := range l.runner.plan.steps {
		recorded := l.state.steps[step.id]
		if recorded == nil || !recorded.Started || recorded.Status != "" {
			continue
		}

		err := l.restart(step, recorded)
		if err != nil {
			return err
		}

		l.finishIfComplete(step, recorded)
	}

	return nil
}

// restart re-derives inputs for a started step unless all its invocations are terminal.
func (l *runLoop) restart(step *plannedStep, recorded *stepState) error {
	if !slices.ContainsFunc(recorded.Jobs, func(job jobState) bool { return !job.terminal() }) {
		return nil
	}

	inputs, err := l.stepInputs(step)
	if err != nil {
		return err
	}

	jobs, shape, err := expandJobs(step, inputs)
	if err != nil {
		return err
	}

	if len(jobs) != len(recorded.Jobs) || !slices.Equal(shape, recorded.Shape) {
		return fmt.Errorf("%w: step %q recorded %d sub-jobs of shape %v but now expands to %d of shape %v",
			ErrStateMismatch, step.id, len(recorded.Jobs), recorded.Shape, len(jobs), shape)
	}

	l.jobs[step.id] = jobs

	return l.gateJobs(step, recorded)
}
