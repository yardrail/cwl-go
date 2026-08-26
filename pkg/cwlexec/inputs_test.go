package cwlexec

import (
	"context"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// The names the wiring fixtures share: the input port a sink step resolves into, the sink step
// itself, and the workflow output port that reports what reached it.
const (
	sinkPort = "v"
	sinkStep = "sink"
	gotPort  = "got"

	// valueSupplied is a value the fixtures push along an edge to beat a default with.
	valueSupplied = "supplied"
)

// wiringSpec describes one step-input wiring to exercise.
type wiringSpec struct {
	def       any
	valueFrom string
	linkMerge cwlcore.LinkMergeMethod
	pickValue cwlcore.PickValueMethod
	sources   []string
}

// wiringWorkflow is two producer steps feeding one sink whose wiring is under test.
func wiringWorkflow(wiring *wiringSpec) *wfSpec {
	return &wfSpec{
		inputs: []string{"x"},
		steps: []stepSpec{
			{name: "p1", in: []inSpec{{name: portIn, sources: []string{"x"}}}, out: []string{portA}},
			{name: "p2", in: []inSpec{{name: portIn, sources: []string{"x"}}}, out: []string{portA}},
			{name: sinkStep, out: []string{portA}, in: []inSpec{{
				name:      sinkPort,
				def:       wiring.def,
				valueFrom: wiring.valueFrom,
				linkMerge: wiring.linkMerge,
				pickValue: wiring.pickValue,
				sources:   wiring.sources,
			}}},
		},
		outputs: []outSpec{{name: gotPort, sources: []string{sinkStep + "/" + portA}}},
	}
}

// producerRegistry answers each producer step with its configured value and the sink step with
// whatever arrived on its wired input.
func producerRegistry(values map[string]any) *Registry {
	return testRegistry(func(_ context.Context, call *StepCall) (Result, error) {
		if call.StepID == sinkStep {
			return Success(object(portA, call.Inputs[sinkPort]))
		}

		return Success(object(portA, values[call.StepID]))
	})
}

// bothSources names the outputs of both producer steps, in order.
func bothSources() []string {
	return []string{"p1/" + portA, "p2/" + portA}
}

func TestResolveInputsLinkMerge(t *testing.T) {
	t.Parallel()

	cases := []struct {
		values map[string]any
		want   any
		name   string
		wiring wiringSpec
	}{
		{
			name:   "one source passes through unwrapped",
			wiring: wiringSpec{sources: []string{"p1/" + portA}},
			values: object("p1", "one"),
			want:   "one",
		},
		{
			name:   "one source with an explicit merge_nested is wrapped",
			wiring: wiringSpec{sources: []string{"p1/" + portA}, linkMerge: cwlcore.LinkMergeNested},
			values: object("p1", "one"),
			want:   list("one"),
		},
		{
			name:   "several sources default to merge_nested",
			wiring: wiringSpec{sources: bothSources()},
			values: object("p1", "one", "p2", valueTwo),
			want:   list("one", valueTwo),
		},
		{
			name:   "merge_flattened concatenates arrays and appends scalars",
			wiring: wiringSpec{sources: bothSources(), linkMerge: cwlcore.LinkMergeFlattened},
			values: object("p1", list(1, 2), "p2", 3),
			want:   list(1, 2, 3),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runner := mustRunner(t, wiringWorkflow(&tc.wiring), producerRegistry(tc.values), nil)
			assertDeepEqual(t, "resolved", mustRun(t, runner, object("x", 0)).Outputs[gotPort], tc.want)
		})
	}
}

func TestResolveInputsPickValue(t *testing.T) {
	t.Parallel()

	cases := []struct {
		values map[string]any
		want   any
		name   string
		pick   cwlcore.PickValueMethod
	}{
		{
			name: "first_non_null skips leading nulls", pick: cwlcore.PickFirstNonNull,
			values: object("p1", nil, "p2", valueTwo), want: valueTwo,
		},
		{
			name: "the_only_non_null takes the single value", pick: cwlcore.PickTheOnlyNonNull,
			values: object("p1", nil, "p2", valueTwo), want: valueTwo,
		},
		{
			name: "all_non_null filters", pick: cwlcore.PickAllNonNull,
			values: object("p1", nil, "p2", valueTwo), want: list(valueTwo),
		},
		{
			name: "all_non_null may keep nothing", pick: cwlcore.PickAllNonNull,
			values: object("p1", nil, "p2", nil), want: make([]any, 0),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			wiring := wiringSpec{sources: bothSources(), pickValue: tc.pick}
			runner := mustRunner(t, wiringWorkflow(&wiring), producerRegistry(tc.values), nil)

			assertDeepEqual(t, "resolved", mustRun(t, runner, object("x", 0)).Outputs[gotPort], tc.want)
		})
	}
}

func TestResolveInputsRejectsUnusableWiring(t *testing.T) {
	t.Parallel()

	cases := []struct {
		values map[string]any
		name   string
		wiring wiringSpec
		want   error
	}{
		{
			name:   "first_non_null with every source null",
			wiring: wiringSpec{sources: bothSources(), pickValue: cwlcore.PickFirstNonNull},
			values: object("p1", nil, "p2", nil),
			want:   ErrPickValue,
		},
		{
			name:   "the_only_non_null with two candidates",
			wiring: wiringSpec{sources: bothSources(), pickValue: cwlcore.PickTheOnlyNonNull},
			values: object("p1", 1, "p2", 2),
			want:   ErrPickValue,
		},
		{
			name:   "unknown linkMerge",
			wiring: wiringSpec{sources: bothSources(), linkMerge: "merge_sideways"},
			values: object("p1", 1, "p2", 2),
			want:   ErrUnknownLinkMerge,
		},
		{
			name:   "unknown pickValue",
			wiring: wiringSpec{sources: bothSources(), pickValue: "pick_something"},
			values: object("p1", 1, "p2", 2),
			want:   ErrUnknownPickValue,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runner := mustRunner(t, wiringWorkflow(&tc.wiring), producerRegistry(tc.values), nil)

			_, err := runner.Run(t.Context(), object("x", 0))
			assertErrorIs(t, "Run", err, tc.want)
		})
	}
}

func TestResolveInputsDefaults(t *testing.T) {
	t.Parallel()

	cases := []struct {
		values map[string]any
		want   any
		name   string
		wiring wiringSpec
	}{
		{
			name:   "a step default covers a null source",
			wiring: wiringSpec{sources: []string{"p1/" + portA}, def: valueFallback},
			values: object("p1", nil),
			want:   valueFallback,
		},
		{
			name:   "a step default covers no source at all",
			wiring: wiringSpec{def: valueFallback},
			values: make(map[string]any),
			want:   valueFallback,
		},
		{
			name:   "a supplied value beats the default",
			wiring: wiringSpec{sources: []string{"p1/" + portA}, def: valueFallback},
			values: object("p1", valueSupplied),
			want:   valueSupplied,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runner := mustRunner(t, wiringWorkflow(&tc.wiring), producerRegistry(tc.values), nil)
			assertDeepEqual(t, "resolved", mustRun(t, runner, object("x", 0)).Outputs[gotPort], tc.want)
		})
	}
}

func TestResolveInputsValueFrom(t *testing.T) {
	t.Parallel()

	wiring := wiringSpec{sources: []string{"p1/" + portA}, valueFrom: "seen $(self) with $(inputs.v)"}
	runner := mustRunner(t, wiringWorkflow(&wiring), producerRegistry(object("p1", "raw")), nil)

	assertDeepEqual(t, "resolved", mustRun(t, runner, object("x", 0)).Outputs[gotPort], "seen raw with raw")
}

func TestResolveInputsValueFromFailureFailsTheStep(t *testing.T) {
	t.Parallel()

	wiring := wiringSpec{sources: []string{"p1/" + portA}, valueFrom: "$(inputs.missing.deeper)"}
	runner := mustRunner(t, wiringWorkflow(&wiring), producerRegistry(object("p1", "raw")), nil)

	_, err := runner.Run(t.Context(), object("x", 0))
	assertErrorIs(t, "Run", err, ErrValueFrom)
}

func TestResolveInputsValueFromIsPerScatterElement(t *testing.T) {
	t.Parallel()

	spec := scatterOverInput()
	spec.steps[0].in[0].valueFrom = "<$(self)>"

	runner := mustRunner(t, spec, testRegistry(constOutputsFromInput()), nil)

	result := mustRun(t, runner, object("xs", list("a", "b")))
	assertDeepEqual(t, "gathered", result.Outputs[portFinal], list("<a>", "<b>"))
}

// constOutputsFromInput publishes port a holding whatever arrived on port n.
func constOutputsFromInput() func(context.Context, *StepCall) (Result, error) {
	return func(_ context.Context, call *StepCall) (Result, error) {
		return Success(object(portA, call.Inputs[portIn]))
	}
}

func TestResolveInputsAppliesProcessDefaultForAnUnwiredParameter(t *testing.T) {
	t.Parallel()

	run := newOperation(wfID("s1")+"/run", make([]string, 0), []string{portA}, nil)
	extra := cwlcore.OperationInputParameter{Default: mustNode("from the tool")}
	extra.IDField = run.ID + "/" + sinkPort
	run.Inputs = append(run.Inputs, extra)

	spec := wfSpec{
		steps:   []stepSpec{{name: "s1", run: run, out: []string{portA}}},
		outputs: []outSpec{{name: gotPort, sources: []string{"s1/" + portA}}},
	}

	registry := testRegistry(func(_ context.Context, call *StepCall) (Result, error) {
		return Success(object(portA, call.Inputs[sinkPort]))
	})

	runner := mustRunner(t, &spec, registry, nil)
	assertDeepEqual(t, "resolved", mustRun(t, runner, nil).Outputs[gotPort], "from the tool")
}

// TestApplyProcessDefaultsFillsEveryUnsuppliedParameter covers the helper the implicit single-step
// plan uses. Process.yml makes "missing from the input object" and "present but null" one case, so
// an explicit null takes the default exactly as an absent key does, and only a real value is kept.
func TestApplyProcessDefaultsFillsEveryUnsuppliedParameter(t *testing.T) {
	t.Parallel()

	const (
		kept     = "kept"
		explicit = "explicit"
		fallback = "the default"
	)

	object := map[string]any{kept: valueSupplied, explicit: nil}
	applyProcessDefaults(map[string]any{kept: fallback, explicit: fallback, "added": fallback}, object)

	want := map[string]any{kept: valueSupplied, explicit: fallback, "added": fallback}
	assertDeepEqual(t, "filled", object, want)
}

// undeclaredIn is a step input the process under run: does not declare — legal to wire, and legal
// precisely because it never reaches the process.
const undeclaredIn = "e"

// undeclaredWorkflow is one step wiring two inputs at a process that declares only the first, which
// is the shape tests/pass-unconnected.cwl and tests/fail-unconnected.cwl share.
func undeclaredWorkflow(when, valueFrom string) *wfSpec {
	return &wfSpec{
		inputs: []string{"x", "y"},
		steps: []stepSpec{{
			name: sinkStep,
			run:  newOperation(wfID(sinkStep)+"/run", []string{portIn}, []string{portA}, nil),
			when: when,
			in: []inSpec{
				{name: portIn, sources: []string{"x"}, valueFrom: valueFrom},
				{name: undeclaredIn, sources: []string{"y"}},
			},
			out: []string{portA},
		}},
		outputs: []outSpec{{name: gotPort, sources: []string{sinkStep + "/" + portA}}},
	}
}

// reportInputs answers a step with the value that arrived on the declared port and whether the
// undeclared one arrived at all, which is the whole of what the projection decides.
func reportInputs() func(context.Context, *StepCall) (Result, error) {
	return func(_ context.Context, call *StepCall) (Result, error) {
		_, leaked := call.Inputs[undeclaredIn]

		return Success(object(portA, list(call.Inputs[portIn], leaked)))
	}
}

// TestProjectDeclaredInputs pins the four conformance tests that pull in opposite directions, which
// together are the whole specification of this behaviour:
//
//   - wf_step_connect_undeclared_param (required) — wiring an input the process does not declare is
//     not an error, and the declared inputs still arrive.
//   - wf_step_access_undeclared_param (required, should_fail) — the same wiring must fail once the
//     process reads the undeclared parameter, which it can only do if the parameter is absent.
//   - valuefrom_wf_step_other — a valueFrom may read an undeclared step input, so the projection
//     cannot happen before valueFrom runs.
//   - cond-wf-003.1 — a `when` may gate on one, so it cannot happen before `when` runs either.
//     That leaves exactly one place for it: the call itself.
func TestProjectDeclaredInputs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		want      any
		name      string
		when      string
		valueFrom string
		inputs    map[string]any
	}{
		{
			name:   "wf_step_connect_undeclared_param: wired, delivered, and the extra dropped",
			inputs: object("x", "declared", "y", "undeclared"),
			want:   list("declared", false),
		},
		{
			name:      "valuefrom_wf_step_other: valueFrom reads the undeclared input first",
			valueFrom: "$(inputs." + undeclaredIn + ")",
			inputs:    object("x", "declared", "y", "from the extra"),
			want:      list("from the extra", false),
		},
		{
			name:   "cond-wf-003.1: an admitting when gates on the undeclared input",
			when:   "$(inputs." + undeclaredIn + " === 'go')",
			inputs: object("x", "declared", "y", "go"),
			want:   list("declared", false),
		},
		{
			name:   "cond-wf-003.1: a refusing when gates on the undeclared input",
			when:   "$(inputs." + undeclaredIn + " === 'go')",
			inputs: object("x", "declared", "y", "stop"),
			want:   nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			spec := undeclaredWorkflow(tc.when, tc.valueFrom)
			runner := mustRunner(t, spec, testRegistry(reportInputs()), nil)

			assertDeepEqual(t, "step inputs", mustRun(t, runner, tc.inputs).Outputs[gotPort], tc.want)
		})
	}
}

// TestProjectDeclaredInputsRejectsReadingAnUndeclaredParameter is the fail-unconnected.cwl half
// stated directly: the process itself reads the parameter the step wired but it never declared, and
// must not find it.
func TestProjectDeclaredInputsRejectsReadingAnUndeclaredParameter(t *testing.T) {
	t.Parallel()

	spec := undeclaredWorkflow("", "")
	registry := testRegistry(func(_ context.Context, call *StepCall) (Result, error) {
		value, err := call.Eval.Eval("$(inputs."+undeclaredIn+")", &cwlcore.EvalContext{Inputs: call.Inputs})
		if err != nil {
			return PermanentFail(err)
		}

		return Success(object(portA, value))
	})

	runner := mustRunner(t, spec, registry, nil)

	_, err := runner.Run(t.Context(), object("x", "declared", "y", "undeclared"))
	assertErrorIs(t, "Run", err, cwlcore.ErrExpressionEval)
}

// typedOutputWorkflow is two producer steps feeding one workflow output declared as typ, which is
// the shape every conditionals/cond-wf-00x.cwl takes.
func typedOutputWorkflow(t *testing.T, typ cwlcore.TypeRef, pick cwlcore.PickValueMethod) *cwlcore.Workflow {
	t.Helper()

	spec := wfSpec{
		inputs: []string{"x"},
		steps: []stepSpec{
			{name: "p1", in: []inSpec{{name: portIn, sources: []string{"x"}}}, out: []string{portA}},
			{name: "p2", in: []inSpec{{name: portIn, sources: []string{"x"}}}, out: []string{portA}},
		},
		outputs: []outSpec{{name: gotPort, sources: bothSources(), pickValue: pick}},
	}

	workflow := buildWorkflow(&spec)
	workflow.Outputs[0].Type = typ

	return workflow
}

// TestWorkflowOutputChecksItsDeclaredType covers the check a workflow output's resolved value has to
// pass, which is what makes all_non_null_multi_with_non_array_output[_nojs] the failure it is
// declared to be: `pickValue: all_non_null` "will produce a list", and a list is not a string.
//
// The rows either side of it are the ones that must not be traded away for it — an array output
// takes the same list, an optional output takes the null a skipped conditional branch leaves, a File
// output takes the *cwlcore.File the engine carries internally, and an output the document gave no
// type is not second-guessed.
func TestWorkflowOutputChecksItsDeclaredType(t *testing.T) {
	t.Parallel()

	str := cwlcore.NewPrimitiveType(cwlcore.PrimitiveString)
	file := &cwlcore.File{Basename: "out.txt", Path: "/w/out.txt", Contents: cwlcore.NewOptString("x")}

	cases := []struct {
		values  map[string]any
		typ     cwlcore.TypeRef
		name    string
		pick    cwlcore.PickValueMethod
		wantErr bool
	}{
		{
			name:    "all_non_null_multi_with_non_array_output: a list is not a string",
			typ:     str,
			pick:    cwlcore.PickAllNonNull,
			values:  object("p1", "foo 3", "p2", "Direct"),
			wantErr: true,
		},
		{
			name:   "all_non_null_one_non_null: an array output takes the list",
			typ:    cwlcore.NewArrayType(&cwlcore.ArraySchema{Items: str}),
			pick:   cwlcore.PickAllNonNull,
			values: object("p1", "foo 3", "p2", nil),
		},
		{
			name:   "direct_optional_null_result: an optional output takes null",
			typ:    cwlcore.NewUnionType([]cwlcore.TypeRef{cwlcore.NewPrimitiveType(cwlcore.PrimitiveNull), str}),
			pick:   cwlcore.PickFirstNonNull,
			values: object("p1", nil, "p2", "kept"),
		},
		{
			name:   "a File output takes the File value the engine carries",
			typ:    cwlcore.NewPrimitiveType(cwlcore.PrimitiveFile),
			pick:   cwlcore.PickFirstNonNull,
			values: object("p1", nil, "p2", file),
		},
		{
			name:   "an untyped output is not second-guessed",
			pick:   cwlcore.PickAllNonNull,
			values: object("p1", "foo 3", "p2", "Direct"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			workflow := typedOutputWorkflow(t, tc.typ, tc.pick)

			runner, err := NewRunner(t.Context(), workflow, producerRegistry(tc.values), nil)
			if err != nil {
				t.Fatalf("NewRunner: unexpected error: %v", err)
			}

			_, err = runner.Run(t.Context(), object("x", 0))

			if tc.wantErr {
				assertErrorIs(t, "Run", err, ErrOutputType)

				return
			}

			if err != nil {
				t.Fatalf("Run: unexpected error: %v", err)
			}
		})
	}
}

func TestSinkValueReportsAnUnavailableSource(t *testing.T) {
	t.Parallel()

	wiring := sink{Name: "v", Sources: []string{"nowhere"}}

	_, err := wiring.value(func(string) (any, bool) { return nil, false })
	assertErrorIs(t, "sink.value", err, ErrIncomplete)
}
