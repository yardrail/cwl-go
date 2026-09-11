package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// Decoding parameter types, ParameterBase, and binding records.

// Keys the parameters, bindings and step sinks add to the shared field set.
const (
	keyPrefix        = "prefix"
	keyItemSeparator = "itemSeparator"
	keyValueFrom     = "valueFrom"
	keyPosition      = "position"
	keySeparate      = "separate"
	keyShellQuote    = "shellQuote"
	keyGlob          = "glob"
	keyOutputEval    = "outputEval"
	keyOutputSource  = "outputSource"
	keyLinkMerge     = "linkMerge"
	keyPickValue     = "pickValue"
)

// Names used when a parameter or binding turns out not to be a mapping.
const (
	whatInputParameter  = "an input parameter"
	whatOutputParameter = "an output parameter"
)

// parameterBase decodes the fields every input and output parameter shares.
func (d *decoder) parameterBase(node salad.Node, m *salad.MapNode) ParameterBase {
	return ParameterBase{
		Node:           node,
		IDField:        d.text(m, keyID),
		Label:          d.text(m, keyLabel),
		Doc:            d.textList(m, keyDoc),
		Type:           d.typeRef(fieldNode(m, keyType)),
		SecondaryFiles: d.secondaryFiles(m),
		Format:         d.expressionList(m, keyFormat),
		LoadContents:   d.flag(m, keyLoadContents),
		LoadListing:    LoadListingEnum(d.text(m, keyLoadListing)),
		Streamable:     d.flag(m, keyStreamable),
	}
}

// commandInputParameter decodes an input parameter of a CommandLineTool.
func (d *decoder) commandInputParameter(node salad.Node) CommandInputParameter {
	m := d.mapping(node, whatInputParameter)

	return CommandInputParameter{
		ParameterBase: d.parameterBase(node, m),
		InputBinding:  d.commandLineBinding(fieldNode(m, keyInputBinding)),
		Default:       fieldNode(m, keyDefault),
	}
}

// commandOutputParameter decodes an output parameter of a CommandLineTool.
func (d *decoder) commandOutputParameter(node salad.Node) CommandOutputParameter {
	m := d.mapping(node, whatOutputParameter)

	return CommandOutputParameter{
		ParameterBase: d.parameterBase(node, m),
		OutputBinding: d.commandOutputBinding(fieldNode(m, keyOutputBinding)),
	}
}

// workflowInputParameter decodes a Workflow or ExpressionTool input parameter.
func (d *decoder) workflowInputParameter(node salad.Node) WorkflowInputParameter {
	m := d.mapping(node, whatInputParameter)

	return WorkflowInputParameter{
		ParameterBase: d.parameterBase(node, m),
		InputBinding:  d.inputBinding(fieldNode(m, keyInputBinding)),
		Default:       fieldNode(m, keyDefault),
	}
}

// workflowOutputParameter decodes an output parameter of a Workflow.
func (d *decoder) workflowOutputParameter(node salad.Node) WorkflowOutputParameter {
	m := d.mapping(node, whatOutputParameter)

	return WorkflowOutputParameter{
		ParameterBase: d.parameterBase(node, m),
		OutputSource:  d.textList(m, keyOutputSource),
		LinkMerge:     LinkMergeMethod(shortName(d.text(m, keyLinkMerge))),
		PickValue:     PickValueMethod(shortName(d.text(m, keyPickValue))),
	}
}

// operationInputParameter decodes an Operation input parameter (also used by RawProcess).
func (d *decoder) operationInputParameter(node salad.Node) OperationInputParameter {
	m := d.mapping(node, whatInputParameter)

	return OperationInputParameter{
		ParameterBase: d.parameterBase(node, m),
		Default:       fieldNode(m, keyDefault),
	}
}

// operationOutputParameter decodes an Operation output parameter (also used by RawProcess).
func (d *decoder) operationOutputParameter(node salad.Node) OperationOutputParameter {
	m := d.mapping(node, whatOutputParameter)

	return OperationOutputParameter{ParameterBase: d.parameterBase(node, m)}
}

// expressionToolOutputParameter decodes an output parameter of an ExpressionTool.
func (d *decoder) expressionToolOutputParameter(node salad.Node) ExpressionToolOutputParameter {
	m := d.mapping(node, whatOutputParameter)

	return ExpressionToolOutputParameter{ParameterBase: d.parameterBase(node, m)}
}

// inputBinding decodes a workflow-level InputBinding.
func (d *decoder) inputBinding(node salad.Node) *InputBinding {
	if node == nil {
		return nil
	}

	m := d.mapping(node, "an input binding")
	if m == nil {
		return nil
	}

	return &InputBinding{LoadContents: d.flag(m, keyLoadContents)}
}

// commandLineBinding decodes a CommandLineBinding.
func (d *decoder) commandLineBinding(node salad.Node) *CommandLineBinding {
	if node == nil {
		return nil
	}

	m := d.mapping(node, "a command line binding")
	if m == nil {
		return nil
	}

	return &CommandLineBinding{
		Prefix:        d.text(m, keyPrefix),
		ItemSeparator: d.text(m, keyItemSeparator),
		ValueFrom:     d.expression(m, keyValueFrom),
		Position:      d.exprLong(m, keyPosition),
		Separate:      d.optBool(m, keySeparate),
		ShellQuote:    d.optBool(m, keyShellQuote),
		LoadContents:  d.flag(m, keyLoadContents),
	}
}

// commandOutputBinding decodes a CommandOutputBinding.
func (d *decoder) commandOutputBinding(node salad.Node) *CommandOutputBinding {
	if node == nil {
		return nil
	}

	m := d.mapping(node, "an output binding")
	if m == nil {
		return nil
	}

	return &CommandOutputBinding{
		OutputEval:   d.expression(m, keyOutputEval),
		LoadListing:  LoadListingEnum(d.text(m, keyLoadListing)),
		Glob:         d.expressionList(m, keyGlob),
		LoadContents: d.flag(m, keyLoadContents),
	}
}

// secondaryFiles decodes a secondaryFiles field.
func (d *decoder) secondaryFiles(m *salad.MapNode) []SecondaryFileSchema {
	return decodeEach(d.oneOrMany(m, keySecondaryFiles), d.secondaryFile)
}

// secondaryFile decodes one secondary-file pattern.
func (d *decoder) secondaryFile(node salad.Node) SecondaryFileSchema {
	if pattern, ok := salad.AsString(node); ok {
		return SecondaryFileSchema{Pattern: Expression(pattern), Required: ExprBool{expr: "", kind: 0, value: false}}
	}

	m := d.mapping(node, "a secondary file pattern")

	return SecondaryFileSchema{
		Pattern:  d.expression(m, keyPattern),
		Required: d.exprBool(m, keyRequired),
	}
}
