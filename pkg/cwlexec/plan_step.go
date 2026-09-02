package cwlexec

import (
	"context"
	"fmt"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// planStep analyses one workflow step: resolves the process it runs, builds its requirement scope
// and evaluator, and checks the feature requirements the specification attaches to the document
// features it uses.
//
// Everything here is fixed for the whole run, so it happens once at [NewRunner] time. That is also
// what makes an unrunnable document fail before any step has run, rather than half way through.
func planStep(
	ctx context.Context, workflow *cwlcore.Workflow, step *cwlcore.WorkflowStep, cfg *Config,
) (*plannedStep, error) {
	// Test the resolved process, not IsRef: cwlcore keeps Run.Ref populated after resolution so a
	// diagnostic can still say what the step pointed at, and fills Run.Process alongside it. A step
	// is unresolved only when nothing was decoded for it.
	if step.Run.Process == nil {
		return nil, fmt.Errorf("%w: step %q runs %q", ErrUnresolvedRun, ShortName(step.ID), step.Run.Ref)
	}

	run := step.Run.Process

	// Two scopes, and the difference is load-bearing.
	//
	// stepScope is what governs the *step*: the enclosing workflow's declarations plus the
	// step's own, with nothing pushed for the process under run:. It is what
	// [checkStepFeatures] queries, because the four workflow-feature requirements describe
	// things a step does — scatter, several sources, valueFrom, a Workflow under run: — and a
	// step that does them is entitled to have the requirement declared on the workflow around
	// it. Pushing the run process first would defeat that: a CommandLineTool does not inherit
	// those four classes, so [cwlcore.RequirementScope.PushProcess] filters them out of every
	// enclosing frame, and the check would then conclude the workflow never declared them.
	//
	// scope is what governs the *process*, and is the one every other consumer gets: the
	// unknown-requirement gate below, the expression evaluator, resource resolution
	// ([resourceRequest]), SchemaDef type resolution, and the [StepCall] handed to the handler.
	// Each of those asks "what is in effect for the thing being run", which is exactly the
	// question the inheritance filter exists to answer.
	stepScope := cwlcore.NewScope(workflow).Push(step.Requirements, step.Hints)
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
	planned.pending = newPendingValues(ctx, workflow, planned, decls)

	return planned, checkStepFeatures(planned, stepScope)
}

// checkStepFeatures rejects a step that uses a document feature without the requirement the
// specification demands be in scope for it.
//
// scope must be the *step's* scope — the workflow's declarations plus the step's own, without the
// process under run: pushed onto it. See the two scopes built in [planStep].
//
// The gate is fail-closed on purpose. Each of these features changes what the step's inputs mean —
// scatter turns an array into a series of jobs, several sources turn a value into a list, valueFrom
// replaces the value outright — so running the step regardless would not be a lenient reading of
// the document, it would be a different document.
func checkStepFeatures(planned *plannedStep, scope *cwlcore.RequirementScope) error {
	if len(planned.scatter) > 0 && !inScope(scope, cwlcore.ClassScatterFeatureRequirement) {
		return featureError(planned.id, "scatter", cwlcore.ClassScatterFeatureRequirement)
	}

	if planned.class == Class(cwlcore.ClassWorkflow) && !inScope(scope, cwlcore.ClassSubworkflowFeatureRequirement) {
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

// stepOutPorts lists the short names of the outputs the step publishes.
//
// These are the step's own out ids, not the output parameters of the process under run:. The two
// differ whenever a step declares a subset of what its tool produces, and it is the step's list
// that names the ports the rest of the workflow can read, that a skipped step fills with nulls, and
// that a scattered step gathers into arrays.
func stepOutPorts(step *cwlcore.WorkflowStep) []string {
	ports := make([]string, 0, len(step.Out))
	for _, out := range step.Out {
		ports = append(ports, ShortName(out.ID))
	}

	return ports
}

// shortNames reduces resolved identifiers to the short names an input object is keyed by.
func shortNames(ids []string) []string {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		names = append(names, ShortName(id))
	}

	return names
}

// inputDecls lists the declared input parameters of any process kind, in document order.
func inputDecls(process cwlcore.Process) []portDecl {
	switch typed := process.(type) {
	case *cwlcore.CommandLineTool:
		return commandInputDecls(typed.Inputs)
	case *cwlcore.Workflow:
		return workflowInputDecls(typed.Inputs)
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

// outputDecls lists the declared output parameters of any process kind, in document order.
func outputDecls(process cwlcore.Process) []portDecl {
	switch typed := process.(type) {
	case *cwlcore.CommandLineTool:
		return baseDecls(typed.Outputs, func(p *cwlcore.CommandOutputParameter) *cwlcore.ParameterBase {
			return &p.ParameterBase
		})
	case *cwlcore.Workflow:
		return baseDecls(typed.Outputs, func(p *cwlcore.WorkflowOutputParameter) *cwlcore.ParameterBase {
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

// commandInputDecls renders a CommandLineTool's inputs, whose defaults live on the concrete type.
func commandInputDecls(params []cwlcore.CommandInputParameter) []portDecl {
	decls := make([]portDecl, 0, len(params))
	for index := range params {
		decls = append(decls, inputDecl(&params[index].ParameterBase, params[index].Default))
	}

	return decls
}

// workflowInputDecls renders the inputs of a Workflow or an ExpressionTool, which the schema gives
// the same parameter type.
func workflowInputDecls(params []cwlcore.WorkflowInputParameter) []portDecl {
	decls := make([]portDecl, 0, len(params))
	for index := range params {
		decls = append(decls, inputDecl(&params[index].ParameterBase, params[index].Default))
	}

	return decls
}

// operationInputDecls renders the inputs of an Operation, and of the RawProcess that stands in for
// an extension class.
func operationInputDecls(params []cwlcore.OperationInputParameter) []portDecl {
	decls := make([]portDecl, 0, len(params))
	for index := range params {
		decls = append(decls, inputDecl(&params[index].ParameterBase, params[index].Default))
	}

	return decls
}

// inputDecl renders one input parameter from the base every parameter type shares and the default
// its concrete type carries.
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

// baseDecls renders parameters that carry no default, reading each one's shared base through base.
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

// defaultValue materializes a declared default into the plain Go value an input object holds. A
// parameter that declared none yields nil, which is exactly how an absent value reads.
func defaultValue(node salad.Node) any {
	return salad.ToAny(node)
}
