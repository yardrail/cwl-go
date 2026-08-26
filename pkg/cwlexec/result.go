package cwlexec

import (
	"errors"
	"fmt"
	"slices"
)

// Errors reported while resuming a run from a persisted snapshot.
var (
	// ErrNoSuspension reports a [ResumedStep] that addresses no suspended invocation: an unknown
	// step, coordinates that name no sub-job, or an invocation that is not waiting. Resuming
	// something that never suspended would inject an outcome over one the run already has, so it
	// is refused rather than guessed at.
	ErrNoSuspension = errors.New("no suspended invocation matches this resumed step")

	// ErrResumedStatus reports a [ResumedStep] whose status is not an outcome an invocation can
	// have — an empty status, or a second suspension.
	ErrResumedStatus = errors.New("resumed step has no usable status")

	// ErrStateMismatch reports a snapshot that no longer describes the document it was taken
	// against: a step whose scatter re-expands to a different number of sub-jobs, or to a
	// different nesting shape. Continuing would write outputs into the wrong slots.
	ErrStateMismatch = errors.New("run state does not match the workflow it is being resumed against")

	// ErrStepFailed reports a failure this engine only knows as the text a snapshot preserved.
	//
	// An error value does not survive being persisted, so a failure recorded in one run segment
	// and reported in the next arrives as a message rather than as the sentinel it was. This
	// wraps it, so that such a failure is still an error a caller can classify as coming from a
	// step rather than from the engine.
	ErrStepFailed = errors.New("step failed")
)

// result renders the loop's terminal or suspended outcome.
//
// The three cases are ordered by what a caller must act on first. A failure ends the run whatever
// else happened, so it wins; suspensions are next, because a run that is merely waiting has no
// outputs yet; only a run that neither failed nor is waiting has an output object to produce.
func (l *runLoop) result() (RunResult, error) {
	snapshot := l.state.clone()

	status, failure := l.failure()
	if failure != nil {
		return RunResult{Status: status, State: snapshot}, failure
	}

	suspensions := l.suspensions()
	if len(suspensions) > 0 {
		return RunResult{Status: StatusSuspended, Suspensions: suspensions, State: snapshot}, nil
	}

	outputs, err := l.runOutputs()
	if err != nil {
		return RunResult{Status: StatusPermanentFail, State: snapshot}, err
	}

	return RunResult{Status: StatusSuccess, Outputs: outputs, State: snapshot}, nil
}

// failure reports the run's failure status and the error behind it, or a nil error when no step
// failed.
//
// Steps are scanned in document order rather than in the order they happened to fail, so that the
// same workflow run twice reports the same failure even though its steps completed concurrently. A
// permanent failure outranks a temporary one, for the same reason it does within a step.
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

// stepError recovers the error behind a failed step, preferring the live error value so that
// [errors.Is] still classifies it, and falling back to the text a rehydrated snapshot preserved.
func (l *runLoop) stepError(id string, recorded *stepState) error {
	live, found := l.errs[id]
	if found {
		return live
	}

	return fmt.Errorf("%w: step %q: %s", ErrStepFailed, id, recorded.Error)
}

// suspensions collects every invocation that is waiting, in document order by step and by scatter
// coordinate within a step, so that a caller persisting them sees a stable order.
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
		// A failed branch must always be able to say why it failed, and a ResumedStep carries
		// no message of its own, so the status it reported becomes the explanation.
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

// rehydrateSteps rebuilds the run-time state of every step a snapshot recorded as started, so that
// the loop can carry on from where it left off.
//
// A step whose invocations are now all terminal is simply finished — that is the ordinary path
// after a suspension is resumed. A step with work still pending has its inputs re-derived from the
// outputs the snapshot holds, which is what lets a snapshot stay small enough to persist: the input
// object of every sub-job is a function of its upstream outputs, so it is recomputed rather than
// stored.
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

// restart re-derives the input objects of a started step's invocations, unless every one of them is
// already terminal and there is nothing left to run.
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
