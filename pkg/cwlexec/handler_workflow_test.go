package cwlexec

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// The sentinels the nesting fixtures fail with, so that a failure crossing the boundary can be
// classified rather than matched by message.
var (
	errLeafRefused  = errors.New("the leaf refused")
	errNestedSaidSo = errors.New("the nested run said so")
)

// The identifiers and names the nested-workflow fixtures are built from. Each nested workflow gets a
// base of its own so that a workflow nested inside another never shares a resolved identifier with
// it, which is what a document with two files would give.
const (
	subURI   = "file:///sub.cwl#sub"
	midURI   = "file:///mid.cwl#mid"
	innerURI = "file:///inner.cwl#inner"

	// subIn is the input every nested fixture takes, and subStep the single step it runs.
	subIn   = "x"
	subStep = "inner"

	// stepNested is the name of the outer step whose run: is a nested workflow.
	stepNested = "nested"

	// valueOne is the value the fixtures thread through the nesting.
	valueOne = "one"
)

// subWorkflow builds a nested workflow at base: one input, one step running run, and one output
// wired to that step's port.
//
// It is deliberately the smallest workflow that still has an edge in it — an input flowing into a
// step and a step output flowing out — because what these tests exercise is the boundary between two
// runs, not the scheduling inside either of them.
func subWorkflow(base string, run cwlcore.Process) *cwlcore.Workflow {
	const port = portA

	if run == nil {
		run = newOperation(base+"/"+subStep+"/run", []string{subIn}, []string{port}, nil)
	}

	workflow := &cwlcore.Workflow{}
	workflow.ID = base

	input := cwlcore.WorkflowInputParameter{}
	input.IDField = base + "/" + subIn
	workflow.Inputs = []cwlcore.WorkflowInputParameter{input}

	workflow.Steps = []cwlcore.WorkflowStep{{
		ID:  base + "/" + subStep,
		Run: cwlcore.StepRun{Process: run},
		In: []cwlcore.WorkflowStepInput{{
			ID:     base + "/" + subStep + "/" + subIn,
			Source: []string{base + "/" + subIn},
		}},
		Out: []cwlcore.WorkflowStepOutput{{ID: base + "/" + subStep + "/" + port}},
	}}

	output := cwlcore.WorkflowOutputParameter{OutputSource: []string{base + "/" + subStep + "/" + port}}
	output.IDField = base + "/" + port
	workflow.Outputs = []cwlcore.WorkflowOutputParameter{output}

	return workflow
}

// outerSpec describes the outer workflow every nesting fixture shares: one input, one step whose
// run: is sub, and one output reading that step's port.
func outerSpec(sub cwlcore.Process, scatter []string) *wfSpec {
	return &wfSpec{
		inputs: []string{subIn},
		steps: []stepSpec{{
			name:    stepNested,
			run:     sub,
			in:      []inSpec{{name: subIn, sources: []string{subIn}}},
			out:     []string{portA},
			scatter: scatter,
		}},
		outputs: []outSpec{{name: portFinal, sources: []string{stepNested + "/" + portA}}},
	}
}

// nestedCtx returns the context a run with subworkflows in it is driven under: the one that carries
// the caller's own registry down into the nested runs. See [WithSubworkflows].
func nestedCtx(t *testing.T, registry *Registry) context.Context {
	t.Helper()

	return WithSubworkflows(t.Context(), registry, nil)
}

// runNested executes runner under [nestedCtx], failing the test on an error.
func runNested(t *testing.T, runner *Runner, registry *Registry, inputs map[string]any) RunResult {
	t.Helper()

	result, err := runner.Run(nestedCtx(t, registry), inputs)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}

	return result
}

// subCall builds a [StepCall] for a subworkflow by hand, the way a caller driving one process
// directly would, so that the handler's own gates can be tested without a run around them.
func subCall(process cwlcore.Process, scope *cwlcore.RequirementScope) *StepCall {
	return &StepCall{
		StepID:       stepNested,
		Process:      process,
		Class:        Class(cwlcore.ClassWorkflow),
		Inputs:       object(subIn, valueOne),
		Requirements: scope,
	}
}

// subworkflowScope is the requirement scope a step running a subworkflow is handed by the plan.
func subworkflowScope(sub cwlcore.Process) *cwlcore.RequirementScope {
	workflow := buildWorkflow(&wfSpec{})

	return cwlcore.NewScope(workflow).Push(nil, nil).PushProcess(sub)
}

func TestSubworkflowRunsAsANestedRun(t *testing.T) {
	t.Parallel()

	sub := subWorkflow(subURI, nil)
	registry := testRegistry(constOutputs(object(portA, valueOne)))

	result := runNested(t, mustRunner(t, outerSpec(sub, nil), registry, nil), registry, object(subIn, valueOne))

	if result.Status != StatusSuccess {
		t.Fatalf("status = %q, want success", result.Status)
	}

	if got := result.Outputs[portFinal]; got != valueOne {
		t.Fatalf("outputs[%q] = %v, want %q", portFinal, got, valueOne)
	}
}

func TestSubworkflowNestsTwoLevels(t *testing.T) {
	t.Parallel()

	inner := subWorkflow(innerURI, nil)
	middle := subWorkflow(midURI, inner)
	registry := testRegistry(constOutputs(object(portA, valueOne)))

	result := runNested(t, mustRunner(t, outerSpec(middle, nil), registry, nil), registry, object(subIn, valueOne))

	if got := result.Outputs[portFinal]; got != valueOne {
		t.Fatalf("outputs[%q] = %v, want %q; a value must flow through two levels", portFinal, got, valueOne)
	}
}

func TestSubworkflowInheritsRequirements(t *testing.T) {
	t.Parallel()

	// The tool needs JavaScript, and only the outer workflow declares InlineJavascriptRequirement.
	tool := newExpressionTool("${return {a: inputs."+subIn+" + \"-js\"};}", subURI+"/"+subStep+"/run/"+portA)
	tool.Inputs = []cwlcore.WorkflowInputParameter{{}}
	tool.Inputs[0].IDField = subURI + "/" + subStep + "/run/" + subIn

	sub := subWorkflow(subURI, tool)

	registry := NewRegistry()

	result := runNested(t, mustRunner(t, outerSpec(sub, nil), registry, nil), registry, object(subIn, valueOne))

	if got := result.Outputs[portFinal]; got != valueOne+"-js" {
		t.Fatalf("outputs[%q] = %v, want %q; the nested run must inherit the outer requirements",
			portFinal, got, valueOne+"-js")
	}
}

func TestInheritRequirementsWithoutAScope(t *testing.T) {
	t.Parallel()

	sub := subWorkflow(subURI, nil)

	if got := inheritRequirements(sub, nil); got != sub {
		t.Fatal("a call with no requirement scope must plan over the workflow itself")
	}
}

func TestSubworkflowNeedsItsFeatureRequirement(t *testing.T) {
	t.Parallel()

	sub := subWorkflow(subURI, nil)

	// The plan refuses the step before the run starts, which is where a document author sees it.
	spec := outerSpec(sub, nil)
	spec.noFeatures = true

	_, err := NewRunner(t.Context(), buildWorkflow(spec), testRegistry(constOutputs(nil)), nil)
	if !errors.Is(err, ErrRequirementNotInScope) {
		t.Fatalf("NewRunner error = %v, want ErrRequirementNotInScope", err)
	}

	// And the handler refuses the call, for a scope resolved outside the plan.
	bare := &cwlcore.Workflow{}
	bare.ID = subURI

	others := []cwlcore.ProcessRequirement{&cwlcore.ScatterFeatureRequirement{}}

	for name, scope := range map[string]*cwlcore.RequirementScope{
		"no scope":              nil,
		"scope without it":      cwlcore.NewScope(bare),
		"scope with other reqs": cwlcore.NewScope(bare).Push(others, nil),
	} {
		result, execErr := workflowHandler{}.Execute(t.Context(), subCall(sub, scope))

		if !errors.Is(execErr, ErrRequirementNotInScope) {
			t.Fatalf("%s: error = %v, want ErrRequirementNotInScope", name, execErr)
		}

		if result.Status != StatusPermanentFail {
			t.Fatalf("%s: status = %q, want a permanent failure", name, result.Status)
		}

		assertNames(t, execErr, cwlcore.ClassSubworkflowFeatureRequirement, stepNested)
	}
}

func TestSubworkflowRejectsAnotherClass(t *testing.T) {
	t.Parallel()

	tool := newExpressionTool("$(inputs.x)")

	result, err := workflowHandler{}.Execute(t.Context(), subCall(tool, subworkflowScope(tool)))

	if !errors.Is(err, ErrWrongProcessClass) {
		t.Fatalf("error = %v, want ErrWrongProcessClass", err)
	}

	if result.Status != StatusPermanentFail {
		t.Fatalf("status = %q, want a permanent failure", result.Status)
	}
}

func TestSubworkflowSelfInvocationIsFatal(t *testing.T) {
	t.Parallel()

	direct := subWorkflow(subURI, nil)
	direct.Steps[0].Run.Process = direct

	first := subWorkflow(subURI, nil)
	second := subWorkflow(midURI, first)
	first.Steps[0].Run.Process = second

	for name, workflow := range map[string]*cwlcore.Workflow{
		"direct":   direct,
		"indirect": first,
	} {
		spec := outerSpec(workflow, nil)
		registry := testRegistry(constOutputs(object(portA, valueOne)))

		_, err := mustRunner(t, spec, registry, nil).Run(nestedCtx(t, registry), object(subIn, valueOne))

		if !errors.Is(err, ErrSubworkflowCycle) {
			t.Fatalf("%s: error = %v, want ErrSubworkflowCycle", name, err)
		}
	}
}

func TestSubworkflowFailurePropagates(t *testing.T) {
	t.Parallel()

	sentinel := errLeafRefused

	cases := map[string]struct {
		fail func(error) (Result, error)
		want Status
	}{
		"permanent": {fail: PermanentFail, want: StatusPermanentFail},
		"temporary": {fail: TemporaryFail, want: StatusTemporaryFail},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			sub := subWorkflow(subURI, nil)
			registry := testRegistry(func(_ context.Context, _ *StepCall) (Result, error) {
				return tc.fail(sentinel)
			})

			result, err := mustRunner(t, outerSpec(sub, nil), registry, nil).
				Run(nestedCtx(t, registry), object(subIn, valueOne))

			if !errors.Is(err, sentinel) {
				t.Fatalf("error = %v, want the leaf's own error", err)
			}

			if result.Status != tc.want {
				t.Fatalf("status = %q, want %q; the nested verdict must survive the boundary",
					result.Status, tc.want)
			}
		})
	}
}

func TestSubworkflowRejectsAnUnplannableNestedRun(t *testing.T) {
	t.Parallel()

	// The nested workflow's output reads a port no step publishes, which only its own plan can
	// discover — the outer plan never looks inside a subworkflow.
	sub := subWorkflow(subURI, nil)
	sub.Outputs[0].OutputSource = []string{subURI + "/" + subStep + "/" + portB}

	registry := testRegistry(constOutputs(object(portA, valueOne)))

	_, err := mustRunner(t, outerSpec(sub, nil), registry, nil).Run(nestedCtx(t, registry), object(subIn, valueOne))

	if !errors.Is(err, ErrUnknownSource) {
		t.Fatalf("error = %v, want ErrUnknownSource", err)
	}
}

func TestSubworkflowUnderScatter(t *testing.T) {
	t.Parallel()

	sub := subWorkflow(subURI, nil)

	var (
		mutex sync.Mutex
		dirs  []string
	)

	registry := testRegistry(func(_ context.Context, call *StepCall) (Result, error) {
		mutex.Lock()

		dirs = append(dirs, call.OutDir)

		mutex.Unlock()

		return Success(object(portA, call.Inputs[subIn]))
	})

	cfg := &Config{OutDir: "/runs/out"}
	runner := mustRunner(t, outerSpec(sub, []string{subIn}), registry, cfg)

	result := runNested(t, runner, registry, object(subIn, []any{valueOne, valueTwo}))

	gathered, ok := result.Outputs[portFinal].([]any)
	if !ok || len(gathered) != 2 || gathered[0] != valueOne || gathered[1] != valueTwo {
		t.Fatalf("outputs[%q] = %#v, want the two elements gathered in order", portFinal, result.Outputs[portFinal])
	}

	// Each sub-job's nested run must work inside its own invocation directory, or two elements of
	// one scatter would write over each other.
	mutex.Lock()
	defer mutex.Unlock()

	if len(dirs) != 2 || dirs[0] == dirs[1] {
		t.Fatalf("nested output directories = %q, want two distinct paths under the step's own", dirs)
	}

	for _, dir := range dirs {
		if !strings.HasPrefix(dir, "/runs/out/"+stepNested+"_") {
			t.Fatalf("nested output directory %q is not inside the scattered step's own directory", dir)
		}
	}
}

func TestSubworkflowHonoursCancellation(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	sub := subWorkflow(subURI, nil)

	registry := testRegistry(func(inner context.Context, _ *StepCall) (Result, error) {
		close(started)
		<-inner.Done()

		return PermanentFail(inner.Err())
	})

	ctx, cancel := context.WithCancel(nestedCtx(t, registry))
	defer cancel()

	runner := mustRunner(t, outerSpec(sub, nil), registry, nil)

	go func() {
		<-started
		cancel()
	}()

	_, err := runner.Run(ctx, object(subIn, valueOne))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled to reach the leaf of the nesting", err)
	}
}

// The token and payload the nested leaf handler suspends with, which must reach the caller untouched
// through the outer suspension's payload.
const (
	leafToken   = "waiting-on-a-human"
	leafPayload = "opaque"
)

// suspendingNesting builds a run whose subworkflow's only step suspends, and drives it to the
// suspension. It returns the outer runner, the registry it dispatches through, and the outer result.
func suspendingNesting(t *testing.T) (*Runner, *Registry, RunResult) {
	t.Helper()

	registry := testRegistry(func(_ context.Context, call *StepCall) (Result, error) {
		return call.Suspend(leafToken, []byte(leafPayload))
	})

	outer := mustRunner(t, outerSpec(subWorkflow(subURI, nil), nil), registry, nil)

	return outer, registry, runNested(t, outer, registry, object(subIn, valueOne))
}

func TestSubworkflowSuspensionPropagates(t *testing.T) {
	t.Parallel()

	_, _, result := suspendingNesting(t)

	if result.Status != StatusSuspended || len(result.Suspensions) != 1 {
		t.Fatalf("status = %q with %d suspensions, want one suspension", result.Status, len(result.Suspensions))
	}

	// The outer run suspends under the outer step's own address: the nested run's step names mean
	// nothing to it.
	waiting := result.Suspensions[0]
	if waiting.StepID != stepNested || waiting.Token != SubworkflowTag {
		t.Fatalf("suspension = %+v, want the outer step's own address and %q", waiting, SubworkflowTag)
	}

	nested, err := DecodeSubworkflowSuspension(waiting.Payload)
	if err != nil {
		t.Fatalf("DecodeSubworkflowSuspension: %v", err)
	}

	if len(nested.Suspensions) != 1 || nested.Suspensions[0].StepID != subStep {
		t.Fatalf("nested suspensions = %+v, want the nested run's own step", nested.Suspensions)
	}

	if nested.Suspensions[0].Token != leafToken || string(nested.Suspensions[0].Payload) != leafPayload {
		t.Fatalf("nested suspension = %+v, want the leaf handler's own token and payload", nested.Suspensions[0])
	}
}

func TestSubworkflowSuspensionResumesThroughTheCaller(t *testing.T) {
	t.Parallel()

	outer, registry, result := suspendingNesting(t)

	nested, err := DecodeSubworkflowSuspension(result.Suspensions[0].Payload)
	if err != nil {
		t.Fatalf("DecodeSubworkflowSuspension: %v", err)
	}

	// Step two of the documented recipe: the caller drives the nested run to completion itself,
	// because a resumed invocation is never dispatched to its handler again.
	inner, err := NewRunner(t.Context(), subWorkflow(subURI, nil), registry, nil)
	if err != nil {
		t.Fatalf("NewRunner over the subworkflow: %v", err)
	}

	resumedInner, err := inner.Resume(nestedCtx(t, registry), nested.State, []ResumedStep{{
		StepID:  subStep,
		Status:  StatusSuccess,
		Outputs: object(portA, valueOne),
	}})
	if err != nil {
		t.Fatalf("Resume of the nested run: %v", err)
	}

	if resumedInner.Status != StatusSuccess || resumedInner.Outputs[portA] != valueOne {
		t.Fatalf("nested resume = %+v, want the nested run's outputs", resumedInner)
	}

	// Step three: the nested outputs are injected into the outer run as that step's outcome.
	resumedOuter, err := outer.Resume(nestedCtx(t, registry), result.State, []ResumedStep{{
		StepID:  stepNested,
		Status:  StatusSuccess,
		Outputs: resumedInner.Outputs,
	}})
	if err != nil {
		t.Fatalf("Resume of the outer run: %v", err)
	}

	if resumedOuter.Status != StatusSuccess || resumedOuter.Outputs[portFinal] != valueOne {
		t.Fatalf("outer resume = %+v, want the nested outputs carried through", resumedOuter)
	}
}

func TestSuspendNestedRejectsAnUnrecordablePayload(t *testing.T) {
	t.Parallel()

	// A run state holding a value JSON cannot represent cannot be handed to a caller to persist,
	// and pretending the step merely failed to suspend would lose the wait.
	run := RunResult{Status: StatusSuspended, State: *newRunState(object(subIn, make(chan int)))}

	result, err := suspendNested(subCall(nil, nil), run)

	if err == nil || result.Status != StatusPermanentFail {
		t.Fatalf("result = %+v, err = %v; want a permanent failure", result, err)
	}

	assertNames(t, err, "recording a nested suspension")
}

func TestSubResultMapsEveryRunStatus(t *testing.T) {
	t.Parallel()

	sentinel := errNestedSaidSo

	cases := []struct {
		err    error
		name   string
		status Status
		want   Status
	}{
		{name: "permanent", status: StatusPermanentFail, err: sentinel, want: StatusPermanentFail},
		{name: "temporary", status: StatusTemporaryFail, err: sentinel, want: StatusTemporaryFail},
		{name: "skipped", status: StatusSkipped, want: StatusPermanentFail},
		{name: "unknown", status: "wat", want: StatusPermanentFail},
		{name: "failed without an error", status: StatusPermanentFail, want: StatusPermanentFail},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			run := RunResult{Status: tc.status}

			result, err := subResult(subCall(nil, nil), run, tc.err)

			if result.Status != tc.want {
				t.Fatalf("status = %q, want %q", result.Status, tc.want)
			}

			if err == nil {
				t.Fatal("a failed nested run must always say why")
			}

			if tc.err == nil {
				assertNames(t, err, string(tc.status))
			}
		})
	}
}

func TestDecodeSubworkflowSuspensionRejectsRubbish(t *testing.T) {
	t.Parallel()

	_, err := DecodeSubworkflowSuspension([]byte("not json"))
	if err == nil {
		t.Fatal("a payload that is not this handler's must not decode")
	}

	// An empty payload decodes to an empty snapshot rather than failing, so a caller reading a
	// suspension recorded before anything waited is not told the payload is corrupt.
	decoded, err := DecodeSubworkflowSuspension([]byte(`{"state":{"version":1}}`))
	if err != nil {
		t.Fatalf("DecodeSubworkflowSuspension: %v", err)
	}

	if len(decoded.Suspensions) != 0 {
		t.Fatalf("suspensions = %+v, want none", decoded.Suspensions)
	}
}

func TestWithSubworkflowsCarriesTheRegistry(t *testing.T) {
	t.Parallel()

	// The nested workflow runs a class only the caller's registry knows about.
	extension := &cwlcore.RawProcess{ClassIRI: string(extensionClass)}
	extension.ID = subURI + "/" + subStep + "/run"
	extension.Inputs = []cwlcore.OperationInputParameter{{}}
	extension.Inputs[0].IDField = extension.ID + "/" + subIn
	extension.Outputs = []cwlcore.OperationOutputParameter{{}}
	extension.Outputs[0].IDField = extension.ID + "/" + portA

	sub := subWorkflow(subURI, extension)

	registry := NewRegistry()
	registry.Register(extensionClass, HandlerFunc(constOutputs(object(portA, valueOne))))

	runner := mustRunner(t, outerSpec(sub, nil), registry, nil)

	// Without the context, the nested run falls back to the built-in registry and cannot dispatch.
	_, err := runner.Run(t.Context(), object(subIn, valueOne))
	if !errors.Is(err, ErrNoHandler) {
		t.Fatalf("error = %v, want ErrNoHandler without WithSubworkflows", err)
	}

	ctx := WithSubworkflows(t.Context(), registry, &Config{})

	result, err := runner.Run(ctx, object(subIn, valueOne))
	if err != nil {
		t.Fatalf("Run with WithSubworkflows: %v", err)
	}

	if got := result.Outputs[portFinal]; got != valueOne {
		t.Fatalf("outputs[%q] = %v, want the caller's own handler to have run", portFinal, got)
	}
}

func TestSubworkflowsFromDefaultsToTheBuiltIns(t *testing.T) {
	t.Parallel()

	plain := subworkflowsFrom(t.Context())
	if plain.registry == nil || plain.cfg != nil || plain.ancestors != nil {
		t.Fatalf("env = %+v, want the built-in registry and nothing else", plain)
	}

	if _, found := plain.registry.Handler(Class(cwlcore.ClassWorkflow)); !found {
		t.Fatal("the fallback registry must carry the built-ins")
	}

	// A nil registry in the context is the same fallback, not a nil dereference later.
	if subworkflowsFrom(WithSubworkflows(t.Context(), nil, nil)).registry == nil {
		t.Fatal("WithSubworkflows(nil) must still yield a usable registry")
	}
}

func TestChildConfigDerivesTheNestedRunsDirectories(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	parent := &Config{
		Logger:              slog.Default(),
		AllowRequirements:   map[string]bool{"https://example.com/ext#Thing": true},
		OutDir:              "/runs/out",
		TmpDirPrefix:        "/runs/tmp",
		OnError:             OnErrorContinue,
		Resources:           ResourceBudget{Cores: 4},
		EvalTimeout:         time.Second,
		MaxParallel:         3,
		LenientRequirements: true,
	}

	env := subworkflowEnv{cfg: parent}
	call := &StepCall{OutDir: "/runs/out/sub", TmpDir: "/runs/tmp/sub", Logger: logger}

	child := env.childConfig(call)

	if child.OutDir != call.OutDir || child.TmpDirPrefix != call.TmpDir || child.Logger != logger {
		t.Fatalf("child = %+v, want the invocation's own directories and logger", child)
	}

	inherited := *child
	inherited.Logger, inherited.OutDir, inherited.TmpDirPrefix = parent.Logger, parent.OutDir, parent.TmpDirPrefix

	if !reflect.DeepEqual(inherited, *parent) {
		t.Fatalf("child = %+v, want every policy field inherited unchanged from %+v", inherited, parent)
	}

	// And with nothing configured, a nested run still gets the invocation's directories.
	bare := subworkflowEnv{}.childConfig(call)
	if bare.OutDir != call.OutDir || bare.TmpDirPrefix != call.TmpDir {
		t.Fatalf("bare child = %+v, want the invocation's own directories", bare)
	}
}

func TestSubworkflowEnvDescendKeepsTheChainImmutable(t *testing.T) {
	t.Parallel()

	first := subWorkflow(subURI, nil)
	second := subWorkflow(midURI, nil)

	root := subworkflowEnv{registry: NewRegistry()}
	one := root.descend(first, &Config{})
	two := one.descend(second, &Config{})

	if len(root.ancestors) != 0 || len(one.ancestors) != 1 || len(two.ancestors) != 2 {
		t.Fatalf("chains = %d, %d, %d; want each level to add exactly one",
			len(root.ancestors), len(one.ancestors), len(two.ancestors))
	}

	if one.ancestors[0] != first || two.ancestors[0] != first || two.ancestors[1] != second {
		t.Fatal("descend must append to the chain without rewriting it")
	}

	if two.registry != root.registry {
		t.Fatal("a nested run must dispatch through the same registry")
	}
}

func TestSubworkflowPayloadIsAStableWireShape(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(subworkflowPayload{
		State:       *newRunState(object(subIn, valueOne)),
		Suspensions: wireSuspensions([]Suspension{{StepID: subStep, Token: "t", ScatterIndex: []int{1}}}),
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	for _, key := range []string{`"state"`, `"suspensions"`, `"stepId"`, `"scatterIndex"`} {
		if !strings.Contains(string(encoded), key) {
			t.Fatalf("payload %s does not carry %s", encoded, key)
		}
	}
}
