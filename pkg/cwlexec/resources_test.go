package cwlexec

import (
	"context"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// resourceStep is a single step whose ResourceRequirement is under test.
func resourceStep(requirement *cwlcore.ResourceRequirement) *wfSpec {
	reqs := make([]cwlcore.ProcessRequirement, 0, 1)
	if requirement != nil {
		reqs = append(reqs, requirement)
	}

	return &wfSpec{
		inputs: []string{"x"},
		steps: []stepSpec{
			{name: "s1", in: []inSpec{{name: portIn, sources: []string{"x"}}}, out: []string{portA}, reqs: reqs},
		},
		outputs: []outSpec{{name: portFinal, sources: []string{"s1/" + portA}}},
	}
}

// captureResources answers a call by publishing the reservation and directories it was given.
func captureResources(seen *StepCall) func(context.Context, *StepCall) (Result, error) {
	return func(_ context.Context, call *StepCall) (Result, error) {
		*seen = *call

		return Success(object(portA, "ok"))
	}
}

func TestDefaultResourceReservation(t *testing.T) {
	t.Parallel()

	var seen StepCall

	runner := mustRunner(t, resourceStep(nil), testRegistry(captureResources(&seen)), nil)
	mustRun(t, runner, object("x", 1))

	assertDeepEqual(t, "resources", seen.Resources, Resources{
		Cores: defaultCoresMin, RAMMiB: defaultRAMMinMiB,
		TmpDirMiB: defaultTmpDirMinMiB, OutDirMiB: defaultOutDirMinMiB,
	})
}

func TestResourceRequirementIsHonouredAndClamped(t *testing.T) {
	t.Parallel()

	requirement := &cwlcore.ResourceRequirement{
		CoresMin: cwlcore.NewResourceInt(2),
		CoresMax: cwlcore.NewResourceInt(8),
		RAMMin:   cwlcore.NewResourceExpression("$(inputs.n * 100)"),
		RAMMax:   cwlcore.NewResourceInt(4096),
	}

	var seen StepCall

	cfg := &Config{Resources: ResourceBudget{Cores: 4, RAMMiB: 2048}}
	runner := mustRunner(t, resourceStep(requirement), testRegistry(captureResources(&seen)), cfg)
	mustRun(t, runner, object("x", 3))

	assertDeepEqual(t, "cores", seen.Resources.Cores, 4.0)
	assertDeepEqual(t, "ram", seen.Resources.RAMMiB, int64(2048))
}

func TestResourceMinimumBeyondTheBudgetFailsTheStep(t *testing.T) {
	t.Parallel()

	requirement := &cwlcore.ResourceRequirement{CoresMin: cwlcore.NewResourceInt(8)}
	cfg := &Config{Resources: ResourceBudget{Cores: 2}}

	runner := mustRunner(t, resourceStep(requirement), testRegistry(constOutputs(nil)), cfg)

	_, err := runner.Run(t.Context(), object("x", 1))
	assertErrorIs(t, "Run", err, ErrResourcesUnavailable)
}

func TestResourceExpressionFailuresFailTheStep(t *testing.T) {
	t.Parallel()

	cases := []struct {
		requirement *cwlcore.ResourceRequirement
		name        string
	}{
		{name: "unevaluable", requirement: &cwlcore.ResourceRequirement{
			CoresMin: cwlcore.NewResourceExpression("$(inputs.missing.deeper)"),
		}},
		{name: notANumber, requirement: &cwlcore.ResourceRequirement{
			RAMMin: cwlcore.NewResourceExpression("$(inputs.n)"),
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runner := mustRunner(t, resourceStep(tc.requirement), testRegistry(constOutputs(nil)), nil)

			_, err := runner.Run(t.Context(), object("x", notANumber))
			assertErrorIs(t, "Run", err, ErrResourceExpression)
		})
	}
}

func TestUnbudgetedClassSkipsResourceSelection(t *testing.T) {
	t.Parallel()

	var seen StepCall

	registry := NewRegistry()
	registry.Register(fakeClass, HandlerFunc(captureResources(&seen)), Unbudgeted())

	requirement := &cwlcore.ResourceRequirement{CoresMin: cwlcore.NewResourceInt(64)}
	cfg := &Config{Resources: ResourceBudget{Cores: 1}}

	runner := mustRunner(t, resourceStep(requirement), registry, cfg)
	mustRun(t, runner, object("x", 1))

	assertDeepEqual(t, "resources", seen.Resources, Resources{})
}

func TestConfigSelectResourcesHookReplacesTheDefault(t *testing.T) {
	t.Parallel()

	var seen StepCall

	cfg := &Config{SelectResources: func(_ ResourceRequest, _ ResourceBudget) (Resources, error) {
		return Resources{Cores: 99}, nil
	}}

	runner := mustRunner(t, resourceStep(nil), testRegistry(captureResources(&seen)), cfg)
	mustRun(t, runner, object("x", 1))

	assertDeepEqual(t, "cores", seen.Resources.Cores, 99.0)
}

func TestDefaultSelectResourcesTable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		request ResourceRequest
		budget  ResourceBudget
		want    Resources
	}{
		{
			name:    "no ceiling gives the maximum asked for",
			request: ResourceRequest{CoresMin: 1, CoresMax: 4, RAMMinMiB: 256, RAMMaxMiB: 1024},
			want:    Resources{Cores: 4, RAMMiB: 1024},
		},
		{
			name:    "the budget is the tighter ceiling",
			request: ResourceRequest{CoresMin: 1, CoresMax: 16, RAMMinMiB: 256, RAMMaxMiB: 4096},
			budget:  ResourceBudget{Cores: 2, RAMMiB: 512},
			want:    Resources{Cores: 2, RAMMiB: 512},
		},
		{
			name:    "the minimum is a floor even past the maximum",
			request: ResourceRequest{CoresMin: 3, CoresMax: 1, RAMMinMiB: 300, RAMMaxMiB: 100},
			want:    Resources{Cores: 3, RAMMiB: 300},
		},
		{
			name:    "space is reserved at the minimum",
			request: ResourceRequest{TmpDirMinMiB: 64, OutDirMinMiB: 32},
			want:    Resources{TmpDirMiB: 64, OutDirMiB: 32},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := DefaultSelectResources(tc.request, tc.budget)
			if err != nil {
				t.Fatalf("DefaultSelectResources: unexpected error: %v", err)
			}

			assertDeepEqual(t, "resources", got, tc.want)
		})
	}
}

func TestDefaultSelectResourcesRejectsEveryUnmeetableMinimum(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		request ResourceRequest
		budget  ResourceBudget
	}{
		{name: "cores", request: ResourceRequest{CoresMin: 4}, budget: ResourceBudget{Cores: 1}},
		{name: "ram", request: ResourceRequest{RAMMinMiB: 4096}, budget: ResourceBudget{RAMMiB: 256}},
		{name: "tmpdir", request: ResourceRequest{TmpDirMinMiB: 4096}, budget: ResourceBudget{TmpDirMiB: 256}},
		{name: "outdir", request: ResourceRequest{OutDirMinMiB: 4096}, budget: ResourceBudget{OutDirMiB: 256}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := DefaultSelectResources(tc.request, tc.budget)
			assertErrorIs(t, "DefaultSelectResources", err, ErrResourcesUnavailable)
		})
	}
}

func TestAsNumberRejectsNonNumbers(t *testing.T) {
	t.Parallel()

	for _, value := range []any{"3", true, nil, list(1)} {
		if _, numeric := asNumber(value); numeric {
			t.Fatalf("asNumber(%v) reported a number", value)
		}
	}

	for _, value := range []any{3, int64(3), 3.0} {
		number, numeric := asNumber(value)
		if !numeric || number != 3 {
			t.Fatalf("asNumber(%v) = %v, %v; want 3, true", value, number, numeric)
		}
	}
}

func TestStepCallDirectoriesAreAllocatedPerInvocation(t *testing.T) {
	t.Parallel()

	seen := make(map[string][2]string)

	registry := testRegistry(func(_ context.Context, call *StepCall) (Result, error) {
		seen[call.OutDir] = [2]string{call.OutDir, call.TmpDir}

		return Success(object(portA, call.Inputs[portIn]))
	})

	cfg := &Config{OutDir: outDir, TmpDirPrefix: "/scratch", MaxParallel: 1}
	runner := mustRunner(t, scatterOverInput(), registry, cfg)
	mustRun(t, runner, object("xs", list("a", "b")))

	assertDeepEqual(t, "invocation directories", seen, map[string][2]string{
		outDir + "/fan_0": {outDir + "/fan_0", "/scratch/fan_0"},
		outDir + "/fan_1": {outDir + "/fan_1", "/scratch/fan_1"},
	})
}

func TestDirsForLeavesUnconfiguredBasesUnset(t *testing.T) {
	t.Parallel()

	cfg := &Config{}

	assertDeepEqual(t, "directories", cfg.dirsFor("s1", nil), stepDirs{})
}

func TestSanitizePathSegment(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "step", want: "step"},
		{name: "slashes", in: "a/b\\c", want: "a_b_c"},
		{name: "an empty segment", in: "", want: "_"},
		{name: "dot", in: ".", want: "_"},
		{name: "parent", in: "..", want: "_"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assertDeepEqual(t, "segment", sanitizePathSegment(tc.in), tc.want)
		})
	}
}
