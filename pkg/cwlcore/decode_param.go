package cwlcore

import "github.com/yardrail/cwl-go/pkg/salad"

// The seven concrete parameter types, the shared ParameterBase, and the small
// binding records the parameters hang off.

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
//
// A record field carries the same field set under a "name" key rather than an
// "id" one, but it is a different Go type with two binding fields of its own, so
// decode_type.go decodes it separately instead of sharing this.
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

// workflowInputParameter decodes an input parameter of a Workflow or an
// ExpressionTool.
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

// operationInputParameter decodes an input parameter of an Operation, and the
// generic input shape a RawProcess uses.
func (d *decoder) operationInputParameter(node salad.Node) OperationInputParameter {
	m := d.mapping(node, whatInputParameter)

	return OperationInputParameter{
		ParameterBase: d.parameterBase(node, m),
		Default:       fieldNode(m, keyDefault),
	}
}

// operationOutputParameter decodes an output parameter of an Operation, and the
// generic output shape a RawProcess uses.
func (d *decoder) operationOutputParameter(node salad.Node) OperationOutputParameter {
	m := d.mapping(node, whatOutputParameter)

	return OperationOutputParameter{ParameterBase: d.parameterBase(node, m)}
}

// expressionToolOutputParameter decodes an output parameter of an ExpressionTool.
func (d *decoder) expressionToolOutputParameter(node salad.Node) ExpressionToolOutputParameter {
	m := d.mapping(node, whatOutputParameter)

	return ExpressionToolOutputParameter{ParameterBase: d.parameterBase(node, m)}
}

// inputBinding decodes the workflow-level InputBinding, whose only field is
// loadContents.
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
//
// separate and shellQuote are read as OptBool rather than bool because their
// schema default is true: reading an absent field as false would silently invert
// the default everywhere neither is written out.
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

// secondaryFiles decodes a secondaryFiles field, whose schema type is one
// SecondaryFileSchema or an array of them.
func (d *decoder) secondaryFiles(m *salad.MapNode) []SecondaryFileSchema {
	return decodeEach(d.oneOrMany(m, keySecondaryFiles), d.secondaryFile)
}

// secondaryFile decodes one secondary-file pattern. A bare string is the
// pattern itself, which is what the schema's secondaryFilesDSL expands to.
func (d *decoder) secondaryFile(node salad.Node) SecondaryFileSchema {
	if pattern, ok := salad.AsString(node); ok {
		return SecondaryFileSchema{Pattern: Expression(pattern)}
	}

	m := d.mapping(node, "a secondary file pattern")

	return SecondaryFileSchema{
		Pattern:  d.expression(m, keyPattern),
		Required: d.exprBool(m, keyRequired),
	}
}
