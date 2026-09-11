package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// Decoders for each concrete process class and the shared ProcessBase.

// Keys for concrete process records.
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

// processBase decodes the shared Process fields.
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

// processID reads a process's identifier, assigning a blank node ID if none is declared.
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

// expressionTool decodes an ExpressionTool.
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
func (d *decoder) rawProcess(m *salad.MapNode, class string) *RawProcess {
	return &RawProcess{
		ProcessBase: d.processBase(m),
		Node:        m,
		ClassIRI:    class,
		Inputs:      decodeEach(d.parameterItems(m, keyInputs), d.operationInputParameter),
		Outputs:     decodeEach(d.parameterItems(m, keyOutputs), d.operationOutputParameter),
	}
}

// extensionWorkflow decodes an extension class that extends Workflow.
func (d *decoder) extensionWorkflow(m *salad.MapNode, class string) *ExtensionWorkflow {
	return &ExtensionWorkflow{
		ProcessBase: d.processBase(m),
		ClassIRI:    class,
		Node:        m,
		Steps:       decodeEach(d.listItems(m, keySteps, keyID, ""), d.workflowStep),
		Inputs:      decodeEach(d.parameterItems(m, keyInputs), d.workflowInputParameter),
		Outputs:     decodeEach(d.parameterItems(m, keyOutputs), d.workflowOutputParameter),
	}
}

const cwlWorkflowIRI = "https://w3id.org/cwl/cwl#Workflow"

// extendsWorkflow reports whether class transitively extends Workflow.
func (d *decoder) extendsWorkflow(class string) bool {
	if d.loaded == nil || d.loaded.Schema == nil {
		return false
	}

	iri := class
	if d.loaded.Context != nil {
		if expanded, ok := d.loaded.Context.Vocab()[class]; ok {
			iri = expanded
		}
	}

	t, ok := d.loaded.Schema.Type(iri)
	if !ok {
		return false
	}

	rec, ok := t.(*salad.RecordType)
	if !ok {
		return false
	}

	return d.extendsTransitively(rec, cwlWorkflowIRI)
}

// extendsTransitively walks the extends chain of rec looking for target.
func (d *decoder) extendsTransitively(rec *salad.RecordType, target string) bool {
	seen := make(map[string]bool, len(rec.Extends))
	queue := append(make([]string, 0, len(rec.Extends)), rec.Extends...)

	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]

		if seen[name] {
			continue
		}

		seen[name] = true

		if name == target {
			return true
		}

		if t, ok := d.loaded.Schema.Type(name); ok {
			if r, ok := t.(*salad.RecordType); ok {
				queue = append(queue, r.Extends...)
			}
		}
	}

	return false
}

// parameterItems returns items of an inputs/outputs field, expanding identifier-map form.
func (d *decoder) parameterItems(m *salad.MapNode, key string) []salad.Node {
	return d.listItems(m, key, keyID, keyType)
}
