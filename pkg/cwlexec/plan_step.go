package cwlexec

import (
	"context"
	"fmt"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// planStep analyses one workflow step into a [plannedStep].
func planStep(
	ctx context.Context, sc cwlcore.StepContainer, step *cwlcore.WorkflowStep, cfg *Config,
) (*plannedStep, error) {
	if step.Run.Process == nil {
		return nil, fmt.Errorf("%w: step %q runs %q", ErrUnresolvedRun, ShortName(step.ID), step.Run.Ref)
	}

	run := step.Run.Process

	// stepScope: step-level (for feature checks). scope: process-level (for execution).
	stepScope := cwlcore.NewScope(sc).Push(step.Requirements, step.Hints)
	scope := stepScope.PushProcess(run)

	err := scope.CheckKnown(cfg.AllowRequirements, cfg.checkOptions()...)
	if err != nil {
		return nil, err
	}

	planned := &plannedStep{
		step:       step,
		run:        run,
		scope:      scope,
		eval:       EvaluatorFor(scope, cfg.evalOptions()...),
		handler:    nil,
		outTypes:   nil,
		defaults:   nil,
		declaredIn: nil,
		pending:    nil,
		id:         ShortName(step.ID),
		class:      Class(run.Class()),
		when:       string(step.When),
		method:     ScatterMethod(step.ScatterMethod),
		out:        stepOutPorts(step),
		scatter:    shortNames(step.Scatter),
		deps:       nil,
		implicit:   false,
	}
	decls := inputDecls(run)
	planned.outTypes = declaredTypes(outputDecls(run))
	planned.declaredIn = declaredInputs(decls)
	planned.defaults = declaredDefaults(decls)
	planned.pending = newPendingValues(ctx, sc, planned, decls, cfg.OutputResolver)

	return planned, checkStepFeatures(planned, stepScope)
}

// checkStepFeatures rejects steps using features without the required requirement in scope.
func checkStepFeatures(planned *plannedStep, scope *cwlcore.RequirementScope) error {
	if len(planned.scatter) > 0 && !inScope(scope, cwlcore.ClassScatterFeatureRequirement) {
		return featureError(planned.id, "scatter", cwlcore.ClassScatterFeatureRequirement)
	}

	if isStepContainer(planned.run) && !inScope(scope, cwlcore.ClassSubworkflowFeatureRequirement) {
		return featureError(planned.id, "a Workflow under run:", cwlcore.ClassSubworkflowFeatureRequirement)
	}

	for index := range planned.step.In {
		in := &planned.step.In[index]

		if len(in.Source) > 1 && !inScope(scope, cwlcore.ClassMultipleInputFeatureRequirement) {
			return featureError(planned.id, "several sources on input "+ShortName(in.ID),
				cwlcore.ClassMultipleInputFeatureRequirement)
		}

		if in.ValueFrom != "" && !inScope(scope, cwlcore.ClassStepInputExpressionRequirement) {
			return featureError(planned.id, "valueFrom on input "+ShortName(in.ID),
				cwlcore.ClassStepInputExpressionRequirement)
		}
	}

	return nil
}

// featureError renders one missing-requirement finding.
func featureError(step, feature, class string) error {
	return fmt.Errorf("%w: step %q uses %s but %s is not in scope", ErrRequirementNotInScope, step, feature, class)
}

// isStepContainer reports whether a process contains workflow steps.
func isStepContainer(p cwlcore.Process) bool {
	_, ok := p.(cwlcore.StepContainer)

	return ok
}

// stepOutPorts returns the short names of a step's declared output ports.
func stepOutPorts(step *cwlcore.WorkflowStep) []string {
	ports := make([]string, 0, len(step.Out))
	for _, out := range step.Out {
		ports = append(ports, ShortName(out.ID))
	}

	return ports
}

// shortNames extracts short names from resolved identifiers.
func shortNames(ids []string) []string {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		names = append(names, ShortName(id))
	}

	return names
}

// inputDecls lists the declared input parameters of any process kind.
func inputDecls(process cwlcore.Process) []portDecl {
	switch typed := process.(type) {
	case *cwlcore.CommandLineTool:
		return commandInputDecls(typed.Inputs)
	case cwlcore.StepContainer:
		return workflowInputDecls(typed.WorkflowInputs())
	case *cwlcore.ExpressionTool:
		return workflowInputDecls(typed.Inputs)
	case *cwlcore.Operation:
		return operationInputDecls(typed.Inputs)
	case *cwlcore.RawProcess:
		return operationInputDecls(typed.Inputs)
	default:
		return make([]portDecl, 0)
	}
}

// outputDecls lists the declared output parameters of any process kind.
func outputDecls(process cwlcore.Process) []portDecl {
	switch typed := process.(type) {
	case *cwlcore.CommandLineTool:
		return baseDecls(typed.Outputs, func(p *cwlcore.CommandOutputParameter) *cwlcore.ParameterBase {
			return &p.ParameterBase
		})
	case cwlcore.StepContainer:
		return baseDecls(typed.WorkflowOutputs(), func(p *cwlcore.WorkflowOutputParameter) *cwlcore.ParameterBase {
			return &p.ParameterBase
		})
	case *cwlcore.ExpressionTool:
		return baseDecls(typed.Outputs, func(p *cwlcore.ExpressionToolOutputParameter) *cwlcore.ParameterBase {
			return &p.ParameterBase
		})
	case *cwlcore.Operation:
		return baseDecls(typed.Outputs, func(p *cwlcore.OperationOutputParameter) *cwlcore.ParameterBase {
			return &p.ParameterBase
		})
	case *cwlcore.RawProcess:
		return baseDecls(typed.Outputs, func(p *cwlcore.OperationOutputParameter) *cwlcore.ParameterBase {
			return &p.ParameterBase
		})
	default:
		return make([]portDecl, 0)
	}
}

// commandInputDecls converts CommandLineTool input parameters to portDecl.
func commandInputDecls(params []cwlcore.CommandInputParameter) []portDecl {
	decls := make([]portDecl, 0, len(params))
	for index := range params {
		decls = append(decls, inputDecl(&params[index].ParameterBase, params[index].Default))
	}

	return decls
}

// workflowInputDecls converts Workflow/ExpressionTool input parameters to portDecl.
func workflowInputDecls(params []cwlcore.WorkflowInputParameter) []portDecl {
	decls := make([]portDecl, 0, len(params))
	for index := range params {
		decls = append(decls, inputDecl(&params[index].ParameterBase, params[index].Default))
	}

	return decls
}

// operationInputDecls converts Operation/RawProcess input parameters to portDecl.
func operationInputDecls(params []cwlcore.OperationInputParameter) []portDecl {
	decls := make([]portDecl, 0, len(params))
	for index := range params {
		decls = append(decls, inputDecl(&params[index].ParameterBase, params[index].Default))
	}

	return decls
}

// inputDecl builds a portDecl from a parameter base and its default.
func inputDecl(base *cwlcore.ParameterBase, def salad.Node) portDecl {
	return portDecl{
		Default:      defaultValue(def),
		DefaultNode:  def,
		Name:         ShortName(base.IDField),
		Type:         base.Type,
		LoadListing:  base.LoadListing,
		LoadContents: base.LoadContents,
	}
}

// baseDecls converts parameters without defaults to portDecl.
func baseDecls[T any](params []T, base func(*T) *cwlcore.ParameterBase) []portDecl {
	decls := make([]portDecl, 0, len(params))
	for index := range params {
		shared := base(&params[index])
		decls = append(
			decls,
			portDecl{
				Default:      nil,
				DefaultNode:  nil,
				Name:         ShortName(shared.IDField),
				Type:         shared.Type,
				LoadListing:  "",
				LoadContents: false,
			},
		)
	}

	return decls
}

// defaultValue materializes a salad node into a plain Go value, or nil.
func defaultValue(node salad.Node) any {
	return salad.ToAny(node)
}
