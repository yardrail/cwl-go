package cwlexec

import (
	"context"
	"errors"
	"fmt"
)

// ErrOperationNotExecutable reports an attempt to execute a bare Operation.
//
// An Operation is a process with declared inputs and outputs and no implementation: the
// specification says it "does not provide enough information to be executed", and that "an
// implementation may execute the operation using an implementation-defined mechanism". There is no
// such mechanism in a spec-generic engine, so the built-in handler fails permanently rather than
// returning null outputs — a step that reports success has produced data, and fabricated data is
// worse than a stopped run. An engine that does know what its operations mean registers a handler
// over the built-in.
var ErrOperationNotExecutable = errors.New("cannot execute an Operation: it has no implementation")

// Compile-time proof that each built-in satisfies the handler contract.
var (
	_ StepHandler = operationHandler{}
	_ StepHandler = expressionToolHandler{}
)

// operationHandler is the built-in handler for the Operation class. See [ErrOperationNotExecutable].
type operationHandler struct{}

// Execute fails permanently, naming the operation that could not be run.
func (operationHandler) Execute(_ context.Context, call *StepCall) (Result, error) {
	return PermanentFail(fmt.Errorf("%w: %s", ErrOperationNotExecutable, describe(call)))
}

// commandLineToolPlaceholder returns the handler registered for CommandLineTool.
//
// It is no longer a placeholder — see [commandLineToolHandler] — but it keeps the name because
// [NewRegistry] is the sole caller and the two files are owned by different streams.
func commandLineToolPlaceholder() StepHandler {
	return commandLineToolHandler{}
}

// workflowPlaceholder returns the handler registered for Workflow.
//
// It is no longer a placeholder — see [workflowHandler] — but it keeps the name because
// [NewRegistry] is the sole caller and the two files are owned by different streams.
func workflowPlaceholder() StepHandler {
	return workflowHandler{}
}

// describe renders the call for an error message: the step, and the process it runs when that adds
// anything. It tolerates a zero StepCall so that an error path is never the thing that panics.
func describe(call *StepCall) string {
	if call == nil {
		return "no step"
	}

	id := ""
	if call.Process != nil {
		id = call.Process.Base().ID
	}

	if id == "" || id == call.StepID {
		return fmt.Sprintf("step %q", call.StepID)
	}

	return fmt.Sprintf("step %q running %q", call.StepID, id)
}
