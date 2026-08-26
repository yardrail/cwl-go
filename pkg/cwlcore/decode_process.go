package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// One decoder per concrete process class, plus the shared ProcessBase and the
// RawProcess fallback. Each is a single struct literal over the field-reading
// helpers, so that adding a field to the model is a one-line change here.

// Keys the concrete process records add to the shared Process fields. The three
// standard-stream names are shared with the type shortcuts in decode_type.go,
// which spell the same words.
const (
	keyStdin              = "stdin"
	keyStdout             = "stdout"
	keyStderr             = "stderr"
	keyBaseCommand        = "baseCommand"
	keyArguments          = "arguments"
	keySuccessCodes       = "successCodes"
	keyTemporaryFailCodes = "temporaryFailCodes"
	keyPermanentFailCodes = "permanentFailCodes"
)

// processBase decodes the fields the schema's abstract Process record gives
// every process.
func (d *decoder) processBase(m *salad.MapNode) ProcessBase {
	return ProcessBase{
		ID:           d.processID(m),
		Label:        d.text(m, keyLabel),
		CWLVersion:   shortName(d.text(m, keyCWLVersion)),
		Doc:          d.textList(m, keyDoc),
		Requirements: d.requirements(m, keyRequirements),
		Hints:        d.hints(m, keyHints),
		Intent:       d.textList(m, keyIntent),
	}
}

// processID reads a process's identifier, assigning a blank node identifier to a
// process that declares none.
func (d *decoder) processID(m *salad.MapNode) string {
	if id := d.text(m, keyID); id != "" {
		return id
	}

	return blankNodeID(m)
}

// commandLineTool decodes a CommandLineTool.
func (d *decoder) commandLineTool(m *salad.MapNode) *CommandLineTool {
	return &CommandLineTool{
		ProcessBase:        d.processBase(m),
		Stdin:              d.expression(m, keyStdin),
		Stdout:             d.expression(m, keyStdout),
		Stderr:             d.expression(m, keyStderr),
		Inputs:             decodeEach(d.parameterItems(m, keyInputs), d.commandInputParameter),
		Outputs:            decodeEach(d.parameterItems(m, keyOutputs), d.commandOutputParameter),
		BaseCommand:        d.textList(m, keyBaseCommand),
		Arguments:          decodeEach(d.listItems(m, keyArguments, "", ""), d.argument),
		SuccessCodes:       d.intList(m, keySuccessCodes),
		TemporaryFailCodes: d.intList(m, keyTemporaryFailCodes),
		PermanentFailCodes: d.intList(m, keyPermanentFailCodes),
	}
}

// workflow decodes a Workflow.
func (d *decoder) workflow(m *salad.MapNode) *Workflow {
	return &Workflow{
		ProcessBase: d.processBase(m),
		Inputs:      decodeEach(d.parameterItems(m, keyInputs), d.workflowInputParameter),
		Outputs:     decodeEach(d.parameterItems(m, keyOutputs), d.workflowOutputParameter),
		Steps:       decodeEach(d.listItems(m, keySteps, keyID, ""), d.workflowStep),
	}
}

// expressionTool decodes an ExpressionTool. Its inputs are WorkflowInputParameter
// because the schema specializes them to that record rather than one of their own.
func (d *decoder) expressionTool(m *salad.MapNode) *ExpressionTool {
	return &ExpressionTool{
		ProcessBase: d.processBase(m),
		Expression:  d.expression(m, keyExpression),
		Inputs:      decodeEach(d.parameterItems(m, keyInputs), d.workflowInputParameter),
		Outputs:     decodeEach(d.parameterItems(m, keyOutputs), d.expressionToolOutputParameter),
	}
}

// operation decodes an Operation.
func (d *decoder) operation(m *salad.MapNode) *Operation {
	return &Operation{
		ProcessBase: d.processBase(m),
		Inputs:      decodeEach(d.parameterItems(m, keyInputs), d.operationInputParameter),
		Outputs:     decodeEach(d.parameterItems(m, keyOutputs), d.operationOutputParameter),
	}
}

// rawProcess decodes a process whose class this package has no type for.
//
// It is not a degraded result: the shared process fields are decoded in full,
// and so are the inputs and outputs, using the generic Operation parameter shape
// — which is sound because an extension class that adds a process to CWL
// specializes the abstract parameter records exactly the way Operation does.
// That is enough to wire the process into a dependency graph without knowing
// anything about the class. Everything else is reachable through Node.
func (d *decoder) rawProcess(m *salad.MapNode, class string) *RawProcess {
	return &RawProcess{
		ProcessBase: d.processBase(m),
		Node:        m,
		ClassIRI:    class,
		Inputs:      decodeEach(d.parameterItems(m, keyInputs), d.operationInputParameter),
		Outputs:     decodeEach(d.parameterItems(m, keyOutputs), d.operationOutputParameter),
	}
}

// parameterItems returns the items of an inputs or outputs field, expanding the
// identifier-map form the schema's mapSubject/mapPredicate pair allows.
func (d *decoder) parameterItems(m *salad.MapNode, key string) []salad.Node {
	return d.listItems(m, key, keyID, keyType)
}
