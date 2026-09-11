package cwlexec

import (
	"context"
	"errors"
	"fmt"
)

// ErrOperationNotExecutable reports an attempt to execute a bare Operation.
var ErrOperationNotExecutable = errors.New("cannot execute an Operation: it has no implementation")

var (
	_ StepHandler = operationHandler{}
	_ StepHandler = expressionToolHandler{}
)

// operationHandler fails permanently for bare Operations.
type operationHandler struct{}

func (operationHandler) Execute(_ context.Context, call *StepCall) (Result, error) {
	return PermanentFail(fmt.Errorf("%w: %s", ErrOperationNotExecutable, describe(call)))
}

// commandLineToolPlaceholder returns the handler registered for CommandLineTool.
func commandLineToolPlaceholder() StepHandler {
	return commandLineToolHandler{}
}

// workflowPlaceholder returns the handler registered for Workflow.
func workflowPlaceholder() StepHandler {
	return workflowHandler{}
}

// describe renders the call for error messages. Tolerates nil.
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
