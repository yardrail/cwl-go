package cwlexec

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// toolID is the resolved identifier of the process the fixtures run, and outID one of its output
// parameters, spelled the way decoding resolves them: absolute, with the process and the parameter
// in the fragment.
const (
	toolID  = "file:///w.cwl#tool"
	outID   = "file:///w.cwl#tool/out"
	extraID = "file:///w.cwl#tool/extra"
	stepID  = "compute"
	// extraPort is the short name of extraID, and outPort (declared in scatter_test.go) that
	// of outID.
	extraPort = "extra"
	// outDir is the output directory a call is given.
	outDir = "/out"
)

// errBoom is the failure a handler reports when the reason does not matter.
var errBoom = errors.New("boom")

// newExpressionTool builds an ExpressionTool with the given expression and declared output ports,
// identified the way a decoded document would be.
func newExpressionTool(expr string, outIDs ...string) *cwlcore.ExpressionTool {
	tool := &cwlcore.ExpressionTool{Expression: cwlcore.Expression(expr)}
	tool.ID = toolID
	tool.Outputs = make([]cwlcore.ExpressionToolOutputParameter, 0, len(outIDs))

	for _, id := range outIDs {
		out := cwlcore.ExpressionToolOutputParameter{}
		out.IDField = id
		tool.Outputs = append(tool.Outputs, out)
	}

	return tool
}

// jsScope returns a requirement scope carrying an InlineJavascriptRequirement with lib.
func jsScope(lib []string) *cwlcore.RequirementScope {
	proc := &cwlcore.ExpressionTool{}
	proc.Requirements = []cwlcore.ProcessRequirement{
		&cwlcore.InlineJavascriptRequirement{ExpressionLib: lib},
	}

	return cwlcore.NewScope(proc)
}

// recordingHandler is a StepHandler that records the call it was given and replays a fixed result.
type recordingHandler struct {
	result Result
	err    error
	seen   *StepCall
}

// Execute records the call and returns the fixed outcome.
func (h *recordingHandler) Execute(_ context.Context, call *StepCall) (Result, error) {
	h.seen = call

	return h.result, h.err
}

func TestShortName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		id   string
		want string
	}{
		{name: "absolute with nested fragment", id: outID, want: outPort},
		{name: "fragment with no slash", id: "file:///w.cwl#main", want: "main"},
		{name: "deeply nested fragment", id: "file:///w.cwl#wf/step/param", want: "param"},
		{name: "already short", id: outPort, want: outPort},
		{name: "path only", id: "file:///dir/tool.cwl", want: "tool.cwl"},
		{name: "empty fragment falls back to the path", id: "file:///dir/tool.cwl#", want: "tool.cwl"},
		{name: "empty identifier", id: "", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := ShortName(tc.id); got != tc.want {
				t.Fatalf("ShortName(%q) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}

func TestResultConstructors(t *testing.T) {
	t.Parallel()

	outputs := map[string]any{outPort: 1}

	result, err := Success(outputs)
	if err != nil || result.Status != StatusSuccess || !maps.Equal(result.Outputs, outputs) {
		t.Fatalf("Success = %+v, %v", result, err)
	}

	result, err = PermanentFail(errBoom)
	if !errors.Is(err, errBoom) || result.Status != StatusPermanentFail {
		t.Fatalf("PermanentFail = %+v, %v", result, err)
	}

	result, err = TemporaryFail(errBoom)
	if !errors.Is(err, errBoom) || result.Status != StatusTemporaryFail {
		t.Fatalf("TemporaryFail = %+v, %v", result, err)
	}
}

// outcomeCase is one row of the Outcome table: what a handler returned, and the normalized outcome
// the scheduler must see.
type outcomeCase struct {
	in          Result
	inErr       error
	wantErr     error
	name        string
	want        Status
	wantData    bool
	wantSuspend bool
}

// run normalizes the row and asserts the status, the error and which arm of the Result survived.
func (c *outcomeCase) run(t *testing.T) {
	t.Helper()

	got, err := Outcome(c.in, c.inErr)

	if got.Status != c.want {
		t.Fatalf("Outcome status = %q, want %q", got.Status, c.want)
	}

	c.assertError(t, err)

	if (got.Outputs != nil) != c.wantData {
		t.Fatalf("Outcome outputs = %v, want present: %t", got.Outputs, c.wantData)
	}

	if (got.Suspension != nil) != c.wantSuspend {
		t.Fatalf("Outcome suspension = %v, want present: %t", got.Suspension, c.wantSuspend)
	}
}

// assertError checks the error against the row, which either names a sentinel or expects none.
func (c *outcomeCase) assertError(t *testing.T, err error) {
	t.Helper()

	if c.wantErr == nil {
		if err != nil {
			t.Fatalf("Outcome error = %v, want nil", err)
		}

		return
	}

	if !errors.Is(err, c.wantErr) {
		t.Fatalf("Outcome error = %v, want %v", err, c.wantErr)
	}
}

func TestOutcome(t *testing.T) {
	t.Parallel()

	outputs := map[string]any{outPort: 1}
	suspension := &Suspension{StepID: stepID}

	cases := []outcomeCase{
		{
			name: "success passes through", in: Result{Status: StatusSuccess, Outputs: outputs},
			want: StatusSuccess, wantData: true,
		},
		{
			name: "skipped passes through", in: Result{Status: StatusSkipped, Outputs: outputs},
			want: StatusSkipped, wantData: true,
		},
		{
			name: "suspended passes through",
			in:   Result{Status: StatusSuspended, Suspension: suspension},
			want: StatusSuspended, wantSuspend: true,
		},
		{
			name: "failure without an error is still a failure",
			in:   Result{Status: StatusPermanentFail}, want: StatusPermanentFail,
		},
		{
			name: "zero status with an error is a permanent failure",
			in:   Result{}, inErr: errBoom, want: StatusPermanentFail, wantErr: errBoom,
		},
		{
			name: "permanent failure keeps its status and drops outputs",
			in:   Result{Status: StatusPermanentFail, Outputs: outputs}, inErr: errBoom,
			want: StatusPermanentFail, wantErr: errBoom,
		},
		{
			name: "temporary failure keeps its status",
			in:   Result{Status: StatusTemporaryFail}, inErr: errBoom, want: StatusTemporaryFail, wantErr: errBoom,
		},
		{
			name: "success with an error is demoted",
			in:   Result{Status: StatusSuccess, Outputs: outputs}, inErr: errBoom,
			want: StatusPermanentFail, wantErr: errBoom,
		},
		{
			name: "suspended with an error is demoted and loses the suspension",
			in:   Result{Status: StatusSuspended, Suspension: suspension}, inErr: errBoom,
			want: StatusPermanentFail, wantErr: errBoom,
		},
		{
			name: "unknown status", in: Result{Status: "finished"},
			want: StatusPermanentFail, wantErr: ErrResultInvariant,
		},
		{
			name: "success carrying a suspension", in: Result{Status: StatusSuccess, Suspension: suspension},
			want: StatusPermanentFail, wantErr: ErrResultInvariant,
		},
		{
			name: "skipped carrying a suspension", in: Result{Status: StatusSkipped, Suspension: suspension},
			want: StatusPermanentFail, wantErr: ErrResultInvariant,
		},
		{
			name: "failure carrying a suspension",
			in:   Result{Status: StatusTemporaryFail, Suspension: suspension},
			want: StatusPermanentFail, wantErr: ErrResultInvariant,
		},
		{
			name: "suspended without a suspension", in: Result{Status: StatusSuspended},
			want: StatusPermanentFail, wantErr: ErrResultInvariant,
		},
		{
			name: "suspended carrying outputs",
			in:   Result{Status: StatusSuspended, Suspension: suspension, Outputs: outputs},
			want: StatusPermanentFail, wantErr: ErrResultInvariant,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t)
		})
	}
}

func TestStepCallSuspendIsOpaqueAndSelfAddressed(t *testing.T) {
	t.Parallel()

	// Deliberately not valid UTF-8 and not valid JSON: cwlexec must carry the payload without
	// looking at it, so nothing here may depend on it being readable.
	payload := []byte{0xff, 0x00, 0x1b, 'x'}
	index := []int{2, 0}

	call := &StepCall{StepID: stepID, ScatterIndex: index}

	result, err := call.Suspend("approval:42", payload)
	if err != nil {
		t.Fatalf("Suspend error = %v", err)
	}

	_, err = Outcome(result, err)
	if err != nil {
		t.Fatalf("a suspended result must satisfy the Result invariants: %v", err)
	}

	got := result.Suspension

	if got.StepID != stepID || !slices.Equal(got.ScatterIndex, index) {
		t.Fatalf("Suspension addressing = %q %v, want %q %v", got.StepID, got.ScatterIndex, stepID, index)
	}

	if got.Token != "approval:42" || !slices.Equal(got.Payload, payload) {
		t.Fatalf("Suspension carried token %q payload %v, want them unchanged", got.Token, got.Payload)
	}

	// The scatter coordinates are copied, so a scheduler reusing its index buffer cannot
	// retroactively re-address a suspension it has already handed to the caller.
	index[0] = 9

	if got.ScatterIndex[0] != 2 {
		t.Fatalf("Suspension.ScatterIndex aliases the call: %v", got.ScatterIndex)
	}
}

func TestStepCallRuntimeContextLeavesUnresolvedFieldsUndefined(t *testing.T) {
	t.Parallel()

	call := &StepCall{OutDir: outDir, TmpDir: "/tmp/x"}
	runtime := call.RuntimeContext()

	if runtime.Outdir != outDir || runtime.Tmpdir != "/tmp/x" {
		t.Fatalf("directories = %q %q", runtime.Outdir, runtime.Tmpdir)
	}

	// Undefined, not zero: an expression reading runtime.cores must fail loudly rather than see
	// an unresolved reservation as "zero cores".
	if runtime.Cores != nil || runtime.RAM != nil || runtime.OutdirSize != nil || runtime.TmpdirSize != nil {
		t.Fatalf("zero Resources must leave runtime.* undefined, got %+v", runtime)
	}
}

func TestStepCallRuntimeContextExposesResources(t *testing.T) {
	t.Parallel()

	call := &StepCall{Resources: Resources{Cores: 1.2, RAMMiB: 512, TmpDirMiB: 64, OutDirMiB: 32}}
	runtime := call.RuntimeContext()

	cases := []struct {
		got  *int64
		name string
		want int64
	}{
		// 1.2 cores rounds up: the specification's runtime.cores is a whole number.
		{name: "cores", got: runtime.Cores, want: 2},
		{name: "ram", got: runtime.RAM, want: 512},
		{name: "tmpdirSize", got: runtime.TmpdirSize, want: 64},
		{name: "outdirSize", got: runtime.OutdirSize, want: 32},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.got == nil || *tc.got != tc.want {
				t.Fatalf("runtime.%s = %v, want %d", tc.name, tc.got, tc.want)
			}
		})
	}
}

func TestStepCallRuntimeContextIsVisibleToExpressions(t *testing.T) {
	t.Parallel()

	call := &StepCall{OutDir: outDir, Resources: Resources{Cores: 4}}

	value, err := call.Evaluator().Eval("$(runtime.cores)", &cwlcore.EvalContext{Runtime: call.RuntimeContext()})
	if err != nil {
		t.Fatalf("Eval error = %v", err)
	}

	if value != int64(4) {
		t.Fatalf("runtime.cores = %#v, want 4", value)
	}
}

func TestStepCallLog(t *testing.T) {
	t.Parallel()

	call := &StepCall{}
	if call.Log() != slog.Default() {
		t.Fatal("a StepCall with no logger must fall back to the default logger")
	}

	logger := slog.New(slog.DiscardHandler)
	call.Logger = logger

	if call.Log() != logger {
		t.Fatal("Log must return the logger the scheduler supplied")
	}
}

func TestStepCallEvaluator(t *testing.T) {
	t.Parallel()

	t.Run("uses the supplied evaluator", func(t *testing.T) {
		t.Parallel()

		eval := cwlcore.NewEvaluator()
		call := &StepCall{Eval: eval}

		if call.Evaluator() != eval {
			t.Fatal("Evaluator must return the evaluator the scheduler supplied")
		}
	})

	t.Run("derives one from the requirement scope", func(t *testing.T) {
		t.Parallel()

		call := &StepCall{Requirements: jsScope(nil)}

		value, err := call.Evaluator().Eval("${return 1 + 1;}", nil)
		if err != nil {
			t.Fatalf("derived evaluator must have JavaScript enabled: %v", err)
		}

		if value != int64(2) {
			t.Fatalf("derived evaluator returned %#v, want 2", value)
		}
	})

	t.Run("derives a references-only evaluator without the requirement", func(t *testing.T) {
		t.Parallel()

		call := &StepCall{}

		_, err := call.Evaluator().Eval("${return 1;}", nil)
		if !errors.Is(err, cwlcore.ErrJavaScript) {
			t.Fatalf("error = %v, want ErrJavaScript", err)
		}
	})
}

func TestHandlerFunc(t *testing.T) {
	t.Parallel()

	var seen *StepCall

	handler := HandlerFunc(func(_ context.Context, call *StepCall) (Result, error) {
		seen = call

		return Success(map[string]any{outPort: call.StepID})
	})

	call := &StepCall{StepID: stepID}

	result, err := handler.Execute(t.Context(), call)
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if seen != call || result.Outputs[outPort] != stepID {
		t.Fatalf("HandlerFunc did not forward the call: %+v", result)
	}
}
