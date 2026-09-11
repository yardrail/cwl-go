package cwlexec

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

var (
	// ErrUnresolvedRun reports a step whose run: is still an unresolved reference.
	ErrUnresolvedRun = errors.New("workflow step run: is an unresolved reference")

	// ErrUnknownSource reports a source naming no known workflow input or step output.
	ErrUnknownSource = errors.New("source names no workflow input or step output")

	// ErrDuplicateStep reports two steps with the same short name.
	ErrDuplicateStep = errors.New("duplicate workflow step identifier")

	// ErrCycle reports a cycle in the step dependency graph.
	ErrCycle = errors.New("workflow steps form a cycle")

	// ErrRequirementNotInScope reports a feature used without its required requirement.
	ErrRequirementNotInScope = errors.New("feature used without the requirement it needs")
)

// implicitStepID is the default step ID for a bare (non-Workflow) process.
const implicitStepID = "main"

// portDecl is a declared input or output parameter.
type portDecl struct {
	Default      any                     // materialized default value, or nil
	DefaultNode  salad.Node              // raw salad node of the default, or nil
	Name         string                  // short name
	Type         cwlcore.TypeRef         // declared type
	LoadListing  cwlcore.LoadListingEnum // directory listing depth (inputs only)
	LoadContents bool                    // read file contents (inputs only)
}

// sourceRef identifies a value source: a step output port, or a run input (Step == "").
type sourceRef struct {
	Step string
	Port string
}

// plannedStep is one step of the execution plan, resolved once at plan time.
type plannedStep struct {
	step       *cwlcore.WorkflowStep
	run        cwlcore.Process
	scope      *cwlcore.RequirementScope
	eval       *cwlcore.Evaluator
	handler    StepHandler
	outTypes   map[string]cwlcore.TypeRef
	defaults   map[string]any
	declaredIn map[string]bool
	pending    *pendingValues
	id         string
	class      Class
	when       string
	method     ScatterMethod
	out        []string
	scatter    []string
	deps       []string
	implicit   bool
}

// plan is the static analysis of a process. Built once by [NewRunner], immutable thereafter.
type plan struct {
	byID    map[string]*plannedStep
	sources map[string]sourceRef
	inputs  []portDecl
	outputs []sink
	steps   []*plannedStep
}

// newPlan analyses a process into an execution plan.
func newPlan(ctx context.Context, process cwlcore.Process, cfg *Config) (*plan, error) {
	sc, isWorkflow := process.(cwlcore.StepContainer)
	if !isWorkflow {
		return bareProcessPlan(ctx, process, cfg)
	}

	steps := sc.WorkflowSteps()
	inputs := sc.WorkflowInputs()
	outputs := sc.WorkflowOutputs()

	built := &plan{
		byID:    make(map[string]*plannedStep, len(steps)),
		sources: make(map[string]sourceRef, len(steps)),
		inputs:  inputDecls(process),
		outputs: make([]sink, 0, len(outputs)),
		steps:   make([]*plannedStep, 0, len(steps)),
	}

	for index := range inputs {
		id := inputs[index].IDField
		built.sources[id] = sourceRef{Step: "", Port: ShortName(id)}
	}

	err := built.addSteps(ctx, sc, cfg)
	if err != nil {
		return nil, err
	}

	err = built.addWorkflowOutputs(sc)
	if err != nil {
		return nil, err
	}

	err = built.resolveEdges()
	if err != nil {
		return nil, err
	}

	return built, nil
}

// bareProcessPlan builds a single-step plan for a non-Workflow process.
func bareProcessPlan(ctx context.Context, process cwlcore.Process, cfg *Config) (*plan, error) {
	scope := cwlcore.NewScope(process)

	err := scope.CheckKnown(cfg.AllowRequirements, cfg.checkOptions()...)
	if err != nil {
		return nil, err
	}

	outs := outputDecls(process)
	ins := inputDecls(process)

	step := &plannedStep{
		step:       nil,
		run:        process,
		scope:      scope,
		eval:       EvaluatorFor(scope, cfg.evalOptions()...),
		handler:    nil,
		outTypes:   declaredTypes(outs),
		defaults:   declaredDefaults(ins),
		declaredIn: declaredInputs(ins),
		pending:    newProcessValues(ctx, process, scope, ins),
		id:         processStepID(process),
		class:      Class(process.Class()),
		when:       "",
		method:     "",
		out:        declaredNames(outs),
		scatter:    nil,
		deps:       nil,
		implicit:   true,
	}

	built := &plan{
		byID:    map[string]*plannedStep{step.id: step},
		sources: make(map[string]sourceRef, len(outs)),
		inputs:  ins,
		outputs: make([]sink, 0, len(outs)),
		steps:   []*plannedStep{step},
	}

	for _, port := range step.out {
		id := step.id + "/" + port
		built.sources[id] = sourceRef{Step: step.id, Port: port}
		built.outputs = append(
			built.outputs,
			sink{
				Name:      port,
				LinkMerge: "",
				PickValue: "",
				Sources:   []string{id},
				Type:      cwlcore.TypeRef{},
				StepInput: false,
			},
		)
	}

	return built, nil
}

// processStepID returns the step ID from the process, or [implicitStepID] as fallback.
func processStepID(process cwlcore.Process) string {
	id := ShortName(process.Base().ID)
	if id == "" || id[0] == '_' {
		return implicitStepID
	}

	return id
}

// addSteps plans every workflow step and indexes their outputs.
func (p *plan) addSteps(ctx context.Context, sc cwlcore.StepContainer, cfg *Config) error {
	steps := sc.WorkflowSteps()
	for index := range steps {
		step := &steps[index]

		planned, err := planStep(ctx, sc, step, cfg)
		if err != nil {
			return err
		}

		if _, clash := p.byID[planned.id]; clash {
			return fmt.Errorf("%w: %q", ErrDuplicateStep, planned.id)
		}

		p.byID[planned.id] = planned
		p.steps = append(p.steps, planned)

		for _, out := range step.Out {
			p.sources[out.ID] = sourceRef{Step: planned.id, Port: ShortName(out.ID)}
		}
	}

	return nil
}

// addWorkflowOutputs records the wiring of the workflow's output parameters.
func (p *plan) addWorkflowOutputs(sc cwlcore.StepContainer) error {
	outputs := sc.WorkflowOutputs()
	for index := range outputs {
		param := &outputs[index]

		if len(param.OutputSource) > 1 &&
			!inScope(cwlcore.NewScope(sc), cwlcore.ClassMultipleInputFeatureRequirement) {
			return fmt.Errorf(
				"%w: workflow output %q draws on %d sources but MultipleInputFeatureRequirement is not in scope",
				ErrRequirementNotInScope,
				ShortName(param.IDField),
				len(param.OutputSource),
			)
		}

		p.outputs = append(p.outputs, sink{
			Name:      ShortName(param.IDField),
			LinkMerge: param.LinkMerge,
			PickValue: param.PickValue,
			Sources:   param.OutputSource,
			Type:      param.Type,
			StepInput: false,
		})
	}

	return nil
}

// resolveEdges validates source references and checks for cycles.
func (p *plan) resolveEdges() error {
	for _, step := range p.steps {
		err := p.resolveStepEdges(step)
		if err != nil {
			return err
		}
	}

	for index := range p.outputs {
		wiring := &p.outputs[index]

		for _, source := range wiring.Sources {
			if _, known := p.sources[source]; !known {
				return fmt.Errorf("%w: output %q reads %q", ErrUnknownSource, wiring.Name, source)
			}
		}
	}

	return p.checkAcyclic()
}

// resolveStepEdges records the dependency edges for one step.
func (p *plan) resolveStepEdges(step *plannedStep) error {
	deps := make([]string, 0, len(step.step.In))

	for index := range step.step.In {
		in := &step.step.In[index]

		for _, source := range in.Source {
			ref, known := p.sources[source]
			if !known {
				return fmt.Errorf("%w: step %q input %q reads %q",
					ErrUnknownSource, step.id, ShortName(in.ID), source)
			}

			if ref.Step != "" && !slices.Contains(deps, ref.Step) {
				deps = append(deps, ref.Step)
			}
		}
	}

	step.deps = deps

	return nil
}

// checkAcyclic reports a cycle in the dependency graph using DFS.
func (p *plan) checkAcyclic() error {
	const (
		open = 1
		shut = 2
	)

	mark := make(map[string]int, len(p.steps))

	var visit func(*plannedStep) error

	visit = func(step *plannedStep) error {
		switch mark[step.id] {
		case shut:
			return nil
		case open:
			return fmt.Errorf("%w: step %q depends on itself", ErrCycle, step.id)
		}

		mark[step.id] = open

		for _, dep := range step.deps {
			err := visit(p.byID[dep])
			if err != nil {
				return err
			}
		}

		mark[step.id] = shut

		return nil
	}

	for _, step := range p.steps {
		err := visit(step)
		if err != nil {
			return err
		}
	}

	return nil
}

// inScope reports whether a requirement of the named class is in scope.
func inScope(scope *cwlcore.RequirementScope, class string) bool {
	_, found, _ := scope.GetRequirement(class)

	return found
}

// declaredNames extracts short names from port declarations.
func declaredNames(decls []portDecl) []string {
	names := make([]string, 0, len(decls))
	for index := range decls {
		names = append(names, decls[index].Name)
	}

	return names
}

// declaredInputs indexes declared input parameter names.
func declaredInputs(decls []portDecl) map[string]bool {
	names := make(map[string]bool, len(decls))
	for index := range decls {
		names[decls[index].Name] = true
	}

	return names
}

// declaredTypes maps port short names to their declared types.
func declaredTypes(decls []portDecl) map[string]cwlcore.TypeRef {
	types := make(map[string]cwlcore.TypeRef, len(decls))
	for index := range decls {
		types[decls[index].Name] = decls[index].Type
	}

	return types
}

// declaredDefaults maps port short names to their non-nil defaults.
func declaredDefaults(decls []portDecl) map[string]any {
	defaults := make(map[string]any, len(decls))

	for index := range decls {
		decl := &decls[index]
		if decl.Default != nil {
			defaults[decl.Name] = decl.Default
		}
	}

	return defaults
}
