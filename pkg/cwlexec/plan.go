package cwlexec

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// Errors reported while analysing a process into the static plan a [Runner] executes. They are
// wrapped with context, so callers should test them with [errors.Is].
var (
	// ErrUnresolvedRun reports a workflow step whose run: field is still a reference to a process
	// defined elsewhere. Decoding leaves such a reference unresolved, and a scheduler cannot
	// invent the process it names, so the run fails before it starts rather than at the moment
	// that step becomes ready.
	ErrUnresolvedRun = errors.New("workflow step run: is an unresolved reference")

	// ErrUnknownSource reports a source — a step input's, or a workflow output's — that names
	// neither a workflow input nor an output the producing step lists in its out.
	ErrUnknownSource = errors.New("source names no workflow input or step output")

	// ErrDuplicateStep reports two workflow steps whose identifiers reduce to the same short name.
	// The short name is what addresses a step in a [Suspension] and in a [RunState], so two steps
	// sharing one would make a resumed run ambiguous.
	ErrDuplicateStep = errors.New("duplicate workflow step identifier")

	// ErrCycle reports steps whose source wiring forms a cycle. A workflow is a directed acyclic
	// graph; a cycle would leave the ready-queue loop with work it can never start, which is
	// indistinguishable at run time from a deadlock.
	ErrCycle = errors.New("workflow steps form a cycle")

	// ErrRequirementNotInScope reports a document feature used without the requirement the
	// specification demands for it — scatter without ScatterFeatureRequirement, several sources
	// without MultipleInputFeatureRequirement, valueFrom without StepInputExpressionRequirement,
	// a Workflow under run: without SubworkflowFeatureRequirement.
	ErrRequirementNotInScope = errors.New("feature used without the requirement it needs")
)

// implicitStepID names the single synthetic step a bare, non-Workflow process is executed as, when
// that process carries no identifier of its own.
const implicitStepID = "main"

// portDecl is one declared parameter of a process, reduced to what the scheduler needs: the short
// name that keys an input or output object, the declared type, and — on the input side — the
// default the document supplied.
type portDecl struct {
	// Default is the parameter's declared default, materialized into plain Go values, or nil.
	Default any

	// DefaultNode is the parameter's declared default as the validated salad node it was
	// decoded from, or nil. It is what a File or Directory default has to be normalized from,
	// which is why the materialized form alone will not do.
	DefaultNode salad.Node

	// Name is the parameter short name; see [ShortName].
	Name string

	// Type is the parameter's declared type, unset when the process kind carries none.
	Type cwlcore.TypeRef

	// LoadListing is how deeply a Directory value bound to this parameter has its listing read
	// before expressions run. Only input parameters carry it.
	LoadListing cwlcore.LoadListingEnum

	// LoadContents requests that a File value bound to this parameter have its first 64 KiB
	// read into its contents field. Only input parameters carry it.
	LoadContents bool
}

// sourceRef says where the value behind a resolved source identifier comes from: an output port of
// a step, or — when Step is empty — an input of the run itself.
type sourceRef struct {
	Step string
	Port string
}

// plannedStep is one node of the plan: a step, the process it runs, and everything about it that is
// fixed for the whole run and so is worked out once rather than per invocation.
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

// plan is the static analysis of the process a [Runner] executes: its steps in document order, the
// index that resolves a source identifier to the port producing it, and the run's own inputs and
// outputs. It is built once by [NewRunner] and never mutated afterwards, so one Runner can drive
// several serialized runs.
type plan struct {
	byID    map[string]*plannedStep
	sources map[string]sourceRef
	inputs  []portDecl
	outputs []sink
	steps   []*plannedStep
}

// newPlan analyses process into the static plan a run executes.
//
// A Workflow becomes one planned step per workflow step. Any other process becomes a single
// implicit step running that process directly, which is how a bare CommandLineTool — the shape most
// of the conformance suite takes — reaches the same registry, the same requirement scoping and the
// same outcome normalization as a step of a workflow.
func newPlan(ctx context.Context, process cwlcore.Process, cfg *Config) (*plan, error) {
	workflow, isWorkflow := process.(*cwlcore.Workflow)
	if !isWorkflow {
		return bareProcessPlan(ctx, process, cfg)
	}

	built := &plan{
		byID:    make(map[string]*plannedStep, len(workflow.Steps)),
		sources: make(map[string]sourceRef, len(workflow.Steps)),
		inputs:  inputDecls(process),
		outputs: make([]sink, 0, len(workflow.Outputs)),
		steps:   make([]*plannedStep, 0, len(workflow.Steps)),
	}

	for index := range workflow.Inputs {
		id := workflow.Inputs[index].IDField
		built.sources[id] = sourceRef{Port: ShortName(id)}
	}

	err := built.addSteps(ctx, workflow, cfg)
	if err != nil {
		return nil, err
	}

	err = built.addWorkflowOutputs(workflow)
	if err != nil {
		return nil, err
	}

	err = built.resolveEdges()
	if err != nil {
		return nil, err
	}

	return built, nil
}

// bareProcessPlan builds the single-implicit-step plan for a process that is not a Workflow.
func bareProcessPlan(ctx context.Context, process cwlcore.Process, cfg *Config) (*plan, error) {
	scope := cwlcore.NewScope(process)

	err := scope.CheckKnown(cfg.AllowRequirements, cfg.checkOptions()...)
	if err != nil {
		return nil, err
	}

	outs := outputDecls(process)
	ins := inputDecls(process)

	step := &plannedStep{
		run:        process,
		scope:      scope,
		eval:       EvaluatorFor(scope, cfg.evalOptions()...),
		outTypes:   declaredTypes(outs),
		defaults:   declaredDefaults(ins),
		declaredIn: declaredInputs(ins),
		pending:    newProcessValues(ctx, process, scope, ins),
		id:         processStepID(process),
		class:      Class(process.Class()),
		out:        declaredNames(outs),
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
		built.outputs = append(built.outputs, sink{Name: port, Sources: []string{id}})
	}

	return built, nil
}

// processStepID names the implicit step after the process it runs, falling back to [implicitStepID]
// for a process decoding gave a blank-node identifier or none at all.
func processStepID(process cwlcore.Process) string {
	id := ShortName(process.Base().ID)
	if id == "" || id[0] == '_' {
		return implicitStepID
	}

	return id
}

// addSteps plans every step of workflow, in document order, and indexes the outputs they publish.
func (p *plan) addSteps(ctx context.Context, workflow *cwlcore.Workflow, cfg *Config) error {
	for index := range workflow.Steps {
		step := &workflow.Steps[index]

		planned, err := planStep(ctx, workflow, step, cfg)
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

// addWorkflowOutputs records the wiring of each of the workflow's own output parameters.
func (p *plan) addWorkflowOutputs(workflow *cwlcore.Workflow) error {
	for index := range workflow.Outputs {
		param := &workflow.Outputs[index]

		if len(param.OutputSource) > 1 &&
			!inScope(cwlcore.NewScope(workflow), cwlcore.ClassMultipleInputFeatureRequirement) {
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
		})
	}

	return nil
}

// resolveEdges turns every source identifier into a dependency edge, rejecting one that names no
// known port, and then rejects a cyclic graph.
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

// resolveStepEdges records the steps one step depends on, in the order its sources name them.
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

// checkAcyclic reports a cycle in the dependency graph, naming a step that is part of one.
//
// The walk is an ordinary three-colour depth-first search over the steps in document order, so the
// step it names is deterministic for a given document.
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

// inScope reports whether a requirement of the named class is declared anywhere in scope, in
// requirements or in hints.
func inScope(scope *cwlcore.RequirementScope, class string) bool {
	_, found, _ := scope.GetRequirement(class)

	return found
}

// declaredNames reduces port declarations to their short names, in declaration order.
func declaredNames(decls []portDecl) []string {
	names := make([]string, 0, len(decls))
	for index := range decls {
		names = append(names, decls[index].Name)
	}

	return names
}

// declaredInputs indexes the short names of a process's declared input parameters. It is the set a
// step's input object is projected onto before the process runs; see [projectDeclaredInputs].
func declaredInputs(decls []portDecl) map[string]bool {
	names := make(map[string]bool, len(decls))
	for index := range decls {
		names[decls[index].Name] = true
	}

	return names
}

// declaredTypes indexes port declarations by short name.
func declaredTypes(decls []portDecl) map[string]cwlcore.TypeRef {
	types := make(map[string]cwlcore.TypeRef, len(decls))
	for index := range decls {
		types[decls[index].Name] = decls[index].Type
	}

	return types
}

// declaredDefaults indexes the non-nil defaults of port declarations by short name. A parameter
// that declared no default is left out, so that "has a default" and "has a null default" stay
// distinguishable — the first fills an absent input, the second is already the absent value.
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
