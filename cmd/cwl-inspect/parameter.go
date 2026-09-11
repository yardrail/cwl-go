package main

import (
	"github.com/yardrail/cwl-go/cmd/internal/cwlcli"
	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// parameterObjects dumps a slice of parameters, extracting the shared base via base.
func parameterObjects[T any](params []T, base func(*T) *cwlcore.ParameterBase) []*cwlcli.Object {
	out := make([]*cwlcli.Object, 0, len(params))
	for i := range params {
		out = append(out, parameterObject(base(&params[i])))
	}

	return out
}

// parameterObject dumps the fields every parameter shares.
func parameterObject(p *cwlcore.ParameterBase) *cwlcli.Object {
	o := cwlcli.NewObject()
	o.SetString("id", p.IDField)
	o.Set("type", p.Type.String())
	o.SetString("label", p.Label)
	o.SetSlice("doc", stringItems(p.Doc))
	o.SetSlice("format", expressionItems(p.Format))
	o.SetSlice("secondaryFiles", secondaryFileItems(p.SecondaryFiles))

	if p.LoadContents {
		o.Set("loadContents", true)
	}

	o.SetString("loadListing", string(p.LoadListing))

	if p.Streamable {
		o.Set("streamable", true)
	}

	return o
}

// commandInputItems dumps a CommandLineTool's inputs.
func commandInputItems(params []cwlcore.CommandInputParameter) []any {
	out := parameterObjects(params, func(p *cwlcore.CommandInputParameter) *cwlcore.ParameterBase {
		return &p.ParameterBase
	})

	for i := range params {
		addBinding(out[i], params[i].InputBinding)
		addDefault(out[i], params[i].Default)
	}

	return objectItems(out)
}

// commandOutputItems dumps a CommandLineTool's outputs.
func commandOutputItems(params []cwlcore.CommandOutputParameter) []any {
	out := parameterObjects(params, func(p *cwlcore.CommandOutputParameter) *cwlcore.ParameterBase {
		return &p.ParameterBase
	})

	for i := range params {
		addOutputBinding(out[i], params[i].OutputBinding)
	}

	return objectItems(out)
}

// workflowInputItems dumps a Workflow's or an ExpressionTool's inputs.
func workflowInputItems(params []cwlcore.WorkflowInputParameter) []any {
	out := parameterObjects(params, func(p *cwlcore.WorkflowInputParameter) *cwlcore.ParameterBase {
		return &p.ParameterBase
	})

	for i := range params {
		addDefault(out[i], params[i].Default)
	}

	return objectItems(out)
}

// workflowOutputItems dumps a Workflow's outputs with their sources.
func workflowOutputItems(params []cwlcore.WorkflowOutputParameter) []any {
	out := parameterObjects(params, func(p *cwlcore.WorkflowOutputParameter) *cwlcore.ParameterBase {
		return &p.ParameterBase
	})

	for i := range params {
		out[i].SetSlice("outputSource", stringItems(params[i].OutputSource))
		out[i].SetString("linkMerge", string(params[i].LinkMerge))
		out[i].SetString("pickValue", string(params[i].PickValue))
	}

	return objectItems(out)
}

// expressionToolOutputItems dumps an ExpressionTool's outputs.
func expressionToolOutputItems(params []cwlcore.ExpressionToolOutputParameter) []any {
	return objectItems(parameterObjects(params, func(p *cwlcore.ExpressionToolOutputParameter) *cwlcore.ParameterBase {
		return &p.ParameterBase
	}))
}

// operationInputItems dumps Operation/RawProcess inputs.
func operationInputItems(params []cwlcore.OperationInputParameter) []any {
	out := parameterObjects(params, func(p *cwlcore.OperationInputParameter) *cwlcore.ParameterBase {
		return &p.ParameterBase
	})

	for i := range params {
		addDefault(out[i], params[i].Default)
	}

	return objectItems(out)
}

// operationOutputItems dumps Operation/RawProcess outputs.
func operationOutputItems(params []cwlcore.OperationOutputParameter) []any {
	return objectItems(parameterObjects(params, func(p *cwlcore.OperationOutputParameter) *cwlcore.ParameterBase {
		return &p.ParameterBase
	}))
}

// addBinding adds a parameter's command-line binding, if there is one.
func addBinding(o *cwlcli.Object, binding *cwlcore.CommandLineBinding) {
	if binding == nil {
		return
	}

	o.Set("inputBinding", bindingObject(binding))
}

// bindingObject dumps a command-line binding.
func bindingObject(binding *cwlcore.CommandLineBinding) *cwlcli.Object {
	b := cwlcli.NewObject()

	if binding.Position.IsSet() {
		b.Set("position", binding.Position.String())
	}

	b.SetString("prefix", binding.Prefix)
	b.Set("separate", binding.Separate.Or(true))
	b.Set("shellQuote", binding.ShellQuote.Or(true))
	b.SetString("itemSeparator", binding.ItemSeparator)
	b.SetString("valueFrom", string(binding.ValueFrom))

	if binding.LoadContents {
		b.Set("loadContents", true)
	}

	return b
}

// argumentItems dumps a CommandLineTool's arguments in document order.
func argumentItems(args []cwlcore.CommandLineArgument) []any {
	out := make([]any, 0, len(args))

	for _, arg := range args {
		binding := arg.Binding()
		if binding == nil {
			out = append(out, arg.String())

			continue
		}

		out = append(out, bindingObject(binding))
	}

	return out
}

// addOutputBinding adds an output binding, if there is one.
func addOutputBinding(o *cwlcli.Object, binding *cwlcore.CommandOutputBinding) {
	if binding == nil {
		return
	}

	b := cwlcli.NewObject()
	b.SetSlice("glob", expressionItems(binding.Glob))
	b.SetString("outputEval", string(binding.OutputEval))

	if binding.LoadContents {
		b.Set("loadContents", true)
	}

	b.SetString("loadListing", string(binding.LoadListing))

	o.Set("outputBinding", b)
}

// addDefault adds a parameter's default value, if set.
func addDefault(o *cwlcli.Object, node salad.Node) {
	if node == nil {
		return
	}

	o.Set("default", cwlcli.Plain(salad.ToAny(node)))
}

// secondaryFileItems dumps the secondary-file patterns a parameter declares.
func secondaryFileItems(schemas []cwlcore.SecondaryFileSchema) []any {
	out := make([]any, 0, len(schemas))

	for _, schema := range schemas {
		o := cwlcli.NewObject()
		o.SetString("pattern", string(schema.Pattern))

		if schema.Required.IsSet() {
			o.Set("required", schema.Required.String())
		}

		out = append(out, o)
	}

	return out
}
