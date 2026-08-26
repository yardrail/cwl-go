package cwlexec

import (
	"fmt"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// tenTimesOutputs builds the completion map for every sub-job of a plan: sub-job i reports i*10 on
// the standard output port.
func tenTimesOutputs(plan *ScatterPlan) map[int]map[string]any {
	outputs := make(map[int]map[string]any, plan.Cardinality())
	for i := range plan.Jobs {
		outputs[i] = map[string]any{outPort: i * 10}
	}

	return outputs
}

func TestGatherFlatMethods(t *testing.T) {
	t.Parallel()

	base := map[string]any{"a": list(1, 2, 3), "b": list("p", "q")}

	tests := []struct {
		name   string
		method ScatterMethod
		keys   []string
		want   []any
	}{
		{
			name:   "dotproduct one key",
			keys:   []string{"a"},
			method: Dotproduct,
			want:   list(0, 10, 20),
		},
		{
			name:   "flat_crossproduct two keys",
			keys:   []string{"a", "b"},
			method: FlatCrossproduct,
			want:   list(0, 10, 20, 30, 40, 50),
		},
		{
			name:   "nested_crossproduct one key stays flat",
			keys:   []string{"a"},
			method: NestedCrossproduct,
			want:   list(0, 10, 20),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			plan := mustExpand(t, base, tc.keys, tc.method)
			gathered := mustGather(t, &plan, tenTimesOutputs(&plan), []string{outPort})
			assertDeepEqual(t, "Gather["+outPort+"]", gathered[outPort], tc.want)
		})
	}
}

// TestGatherNestedCrossproductShape asserts the gathered nesting against hand-computed structures:
// one array level per scatter key, the outermost level being the first key.
func TestGatherNestedCrossproductShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		base map[string]any
		name string
		keys []string
		want []any
	}{
		{
			name: "2x3",
			base: map[string]any{"a": list(1, 2), "b": list("p", "q", "r")},
			keys: []string{"a", "b"},
			want: list(
				list(0, 10, 20),
				list(30, 40, 50),
			),
		},
		{
			name: "3x1",
			base: map[string]any{"a": list(1, 2, 3), "b": list("p")},
			keys: []string{"a", "b"},
			want: list(
				list(0),
				list(10),
				list(20),
			),
		},
		{
			name: "1x3",
			base: map[string]any{"a": list(1), "b": list("p", "q", "r")},
			keys: []string{"a", "b"},
			want: list(
				list(0, 10, 20),
			),
		},
		{
			name: "2x2x2",
			base: map[string]any{"a": list(1, 2), "b": list("p", "q"), "c": list(true, false)},
			keys: []string{"a", "b", "c"},
			want: list(
				list(list(0, 10), list(20, 30)),
				list(list(40, 50), list(60, 70)),
			),
		},
		{
			name: "2x3x2",
			base: map[string]any{"a": list(1, 2), "b": list("p", "q", "r"), "c": list(true, false)},
			keys: []string{"a", "b", "c"},
			want: list(
				list(list(0, 10), list(20, 30), list(40, 50)),
				list(list(60, 70), list(80, 90), list(100, 110)),
			),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			plan := mustExpand(t, tc.base, tc.keys, NestedCrossproduct)
			gathered := mustGather(t, &plan, tenTimesOutputs(&plan), []string{outPort})
			assertDeepEqual(t, "Gather["+outPort+"]", gathered[outPort], tc.want)
		})
	}
}

// TestGatherNestedMatchesFlatContents checks that nested_crossproduct and flat_crossproduct gather
// the same values in the same traversal order, differing only in nesting.
func TestGatherNestedMatchesFlatContents(t *testing.T) {
	t.Parallel()

	base := map[string]any{"a": list(1, 2, 3), "b": list("p", "q")}
	keys := []string{"a", "b"}

	flat := mustExpand(t, base, keys, FlatCrossproduct)
	nested := mustExpand(t, base, keys, NestedCrossproduct)

	flatOut := mustGather(t, &flat, tenTimesOutputs(&flat), []string{outPort})
	nestedOut := mustGather(t, &nested, tenTimesOutputs(&nested), []string{outPort})

	assertDeepEqual(t, "flat gather", flatOut[outPort], list(0, 10, 20, 30, 40, 50))
	assertDeepEqual(t, "nested gather", nestedOut[outPort], list(list(0, 10), list(20, 30), list(40, 50)))
}

// TestGatherMultiplePorts checks each declared output port gets its own pre-sized array, and that
// a port no sub-job produced still gathers as an array of nulls.
func TestGatherMultiplePorts(t *testing.T) {
	t.Parallel()

	base := map[string]any{"a": list(1, 2, 3)}
	plan := mustExpand(t, base, []string{"a"}, Dotproduct)

	outputs := make(map[int]map[string]any, plan.Cardinality())
	for i := range plan.Jobs {
		outputs[i] = map[string]any{numPort: i, namePort: fmt.Sprintf("job%d", i)}
	}

	gathered := mustGather(t, &plan, outputs, []string{numPort, namePort, "absent"})
	want := map[string]any{
		numPort:  list(0, 1, 2),
		namePort: list("job0", "job1", "job2"),
		"absent": list(nil, nil, nil),
	}
	assertDeepEqual(t, "Gather", gathered, want)
}

func TestGatherZeroCardinality(t *testing.T) {
	t.Parallel()

	methods := []ScatterMethod{Dotproduct, FlatCrossproduct, NestedCrossproduct}
	for _, method := range methods {
		t.Run(string(method), func(t *testing.T) {
			t.Parallel()

			base := map[string]any{"a": make([]any, 0), "b": list(1, 2)}
			plan := mustExpand(t, base, []string{"a", "b"}, method)

			gathered := mustGather(t, &plan, make(map[int]map[string]any), []string{"x", "y", "z"})
			want := map[string]any{"x": make([]any, 0), "y": make([]any, 0), "z": make([]any, 0)}
			assertDeepEqual(t, "Gather", gathered, want)
		})
	}
}

func TestGatherNoDeclaredOutputs(t *testing.T) {
	t.Parallel()

	base := map[string]any{"a": list(1, 2)}
	plan := mustExpand(t, base, []string{"a"}, Dotproduct)

	gathered := mustGather(t, &plan, tenTimesOutputs(&plan), nil)
	assertInt(t, "len(Gather)", len(gathered), 0)
}

// completionOrder is a fixed permutation standing in for parallel sub-jobs finishing out of order.
// The orders are hard-coded rather than randomised so a failure always reproduces.
type completionOrder struct {
	name  string
	order []int
}

// completionOrders covers up to twelve sub-jobs; longer plans are not used by these tests.
var completionOrders = []completionOrder{
	{name: "in order", order: []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}},
	{name: "reversed", order: []int{11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 0}},
	{name: "odds then evens", order: []int{1, 3, 5, 7, 9, 11, 0, 2, 4, 6, 8, 10}},
	{name: "scrambled", order: []int{7, 0, 11, 4, 2, 9, 1, 6, 10, 3, 8, 5}},
	{name: "last first", order: []int{11, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
}

// TestGatherOutOfOrderCompletion is the property that makes parallel execution safe: sub-jobs may
// report back in any order and the gathered result is identical to in-order completion.
func TestGatherOutOfOrderCompletion(t *testing.T) {
	t.Parallel()

	base := map[string]any{"a": list(1, 2, 3), "b": list("p", "q"), "c": list(true, false)}
	ports := []string{numPort, namePort}

	methods := []struct {
		name   string
		method ScatterMethod
		keys   []string
	}{
		{name: "dotproduct", keys: []string{"a"}, method: Dotproduct},
		{name: "flat_crossproduct", keys: []string{"a", "b", "c"}, method: FlatCrossproduct},
		{name: "nested_crossproduct", keys: []string{"a", "b", "c"}, method: NestedCrossproduct},
	}

	for _, mc := range methods {
		t.Run(mc.name, func(t *testing.T) {
			t.Parallel()

			plan := mustExpand(t, base, mc.keys, mc.method)
			reference := gatherInOrder(t, &plan, ports, completionOrders[0].order)

			for _, co := range completionOrders {
				got := gatherInOrder(t, &plan, ports, co.order)
				assertDeepEqual(t, "gather after completion order "+co.name, got, reference)
			}
		})
	}
}

// gatherInOrder simulates sub-jobs completing in the given order — later completions are recorded
// after earlier ones — and gathers the result.
func gatherInOrder(t *testing.T, plan *ScatterPlan, ports []string, order []int) map[string]any {
	t.Helper()

	outputs := make(map[int]map[string]any, plan.Cardinality())
	for _, i := range order {
		if i >= plan.Cardinality() {
			continue
		}

		outputs[i] = map[string]any{ports[0]: i * 10, ports[1]: fmt.Sprintf("job%d", i)}
	}

	assertInt(t, "sub-jobs covered by the completion order", len(outputs), plan.Cardinality())

	return mustGather(t, plan, outputs, ports)
}

// TestGatherSkippedSubJobsFlat covers a `when:` gate skipping individual scatter elements: a
// sub-job that reports a null output leaves null in its slot.
func TestGatherSkippedSubJobsFlat(t *testing.T) {
	t.Parallel()

	base := map[string]any{"a": list(1, 2), "b": list("p", "q", "r")}
	plan := mustExpand(t, base, []string{"a", "b"}, FlatCrossproduct)

	outputs := tenTimesOutputs(&plan)
	// Sub-jobs 1 and 4 were skipped: an all-null output object, as SkippedOutputs builds.
	outputs[1] = map[string]any{outPort: nil}
	outputs[4] = map[string]any{outPort: nil}

	gathered := mustGather(t, &plan, outputs, []string{outPort})
	assertDeepEqual(t, "Gather["+outPort+"]", gathered[outPort], list(0, nil, 20, 30, nil, 50))
}

// TestGatherSkippedSubJobsNested checks a sub-job absent from the completion map also leaves null
// in its slot, at the right coordinates of the nested output.
func TestGatherSkippedSubJobsNested(t *testing.T) {
	t.Parallel()

	base := map[string]any{"a": list(1, 2), "b": list("p", "q", "r")}
	plan := mustExpand(t, base, []string{"a", "b"}, NestedCrossproduct)

	outputs := tenTimesOutputs(&plan)
	delete(outputs, 2)
	delete(outputs, 3)

	gathered := mustGather(t, &plan, outputs, []string{outPort})
	want := list(
		list(0, 10, nil),
		list(nil, 40, 50),
	)
	assertDeepEqual(t, "Gather["+outPort+"]", gathered[outPort], want)
}

// TestGatherEverySubJobSkipped checks a fully-gated scatter still gathers the correctly-shaped
// all-null structure rather than an empty array.
func TestGatherEverySubJobSkipped(t *testing.T) {
	t.Parallel()

	base := map[string]any{"a": list(1, 2), "b": list("p", "q", "r")}
	plan := mustExpand(t, base, []string{"a", "b"}, NestedCrossproduct)

	gathered := mustGather(t, &plan, nil, []string{outPort})
	want := list(
		list(nil, nil, nil),
		list(nil, nil, nil),
	)
	assertDeepEqual(t, "Gather["+outPort+"]", gathered[outPort], want)
}

func TestGatherRejectsUnknownSubJobIndex(t *testing.T) {
	t.Parallel()

	base := map[string]any{"a": list(1, 2)}

	tests := []struct {
		name  string
		index int
	}{
		{name: "negative", index: -1},
		{name: "past the end", index: 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			plan := mustExpand(t, base, []string{"a"}, Dotproduct)
			outputs := tenTimesOutputs(&plan)
			outputs[tc.index] = map[string]any{outPort: 99}

			_, err := plan.Gather(outputs, []string{outPort})
			assertErrorIs(t, "Gather", err, ErrScatterOutputIndex)
		})
	}
}

func TestGatherRejectsInconsistentPlan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		plan ScatterPlan
	}{
		{
			name: "coordinate count disagrees with shape",
			plan: ScatterPlan{
				Method:   NestedCrossproduct,
				Keys:     []string{"a", "b"},
				Jobs:     []ScatterJob{{Index: []int{0}, Inputs: make(map[string]any)}},
				OutShape: OutShape{Dims: []int{1, 1}},
			},
		},
		{
			name: "coordinate out of range",
			plan: ScatterPlan{
				Method:   Dotproduct,
				Keys:     []string{"a"},
				Jobs:     []ScatterJob{{Index: []int{5}, Inputs: make(map[string]any)}},
				OutShape: OutShape{Dims: []int{1}},
			},
		},
		{
			name: "negative coordinate",
			plan: ScatterPlan{
				Method:   Dotproduct,
				Keys:     []string{"a"},
				Jobs:     []ScatterJob{{Index: []int{-1}, Inputs: make(map[string]any)}},
				OutShape: OutShape{Dims: []int{1}},
			},
		},
		{
			// A zero is legal only innermost: {2, 0} is the shape the empty-array rule
			// builds for an empty inner factor, but nothing sits inside an empty level.
			name: "empty outer nesting level",
			plan: ScatterPlan{
				Method:   NestedCrossproduct,
				Keys:     []string{"a", "b"},
				Jobs:     make([]ScatterJob, 0),
				OutShape: OutShape{Dims: []int{0, 2}},
			},
		},
		{
			name: "negative dimension",
			plan: ScatterPlan{
				Method:   Dotproduct,
				Keys:     []string{"a"},
				Jobs:     make([]ScatterJob, 0),
				OutShape: OutShape{Dims: []int{-1}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := tc.plan.Gather(make(map[int]map[string]any), []string{outPort})
			assertErrorIs(t, "Gather", err, ErrScatterShape)
		})
	}
}

func TestOutShapeFlat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dims []int
		want bool
	}{
		{name: "no dimensions", dims: nil, want: true},
		{name: "zero cardinality", dims: []int{0}, want: true},
		{name: "one dimension", dims: []int{7}, want: true},
		{name: "two dimensions", dims: []int{2, 3}, want: false},
		{name: "three dimensions", dims: []int{2, 3, 4}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assertDeepEqual(t, "Flat()", OutShape{Dims: tc.dims}.Flat(), tc.want)
		})
	}
}

// TestGatherPreservesValueIdentity checks that gathered values are the sub-job outputs themselves,
// not copies, so File and Directory objects survive the gather untouched.
func TestGatherPreservesValueIdentity(t *testing.T) {
	t.Parallel()

	base := map[string]any{"a": list(1, 2)}
	plan := mustExpand(t, base, []string{"a"}, Dotproduct)

	file := map[string]any{outKeyClass: cwlcore.ClassFile, "path": "/tmp/out.txt"}
	outputs := map[int]map[string]any{
		0: {outPort: file},
		1: {outPort: nil},
	}

	gathered := mustGather(t, &plan, outputs, []string{outPort})
	assertDeepEqual(t, "Gather["+outPort+"]", gathered[outPort], list(file, nil))

	arr, isArray := gathered[outPort].([]any)
	if !isArray {
		t.Fatalf("Gather[%s] is %T, want []any", outPort, gathered[outPort])
	}

	slot, isRecord := arr[0].(map[string]any)
	if !isRecord {
		t.Fatalf("slot 0 is %T, want map[string]any", arr[0])
	}

	slot["marker"] = true

	_, shared := file["marker"]
	assertDeepEqual(t, "gathered value is the sub-job output itself", shared, true)
}

// TestGatherZeroValuePlan checks the zero ScatterPlan is useful: no jobs and no nesting shape
// gather an empty array per declared output port rather than panicking.
func TestGatherZeroValuePlan(t *testing.T) {
	t.Parallel()

	plan := ScatterPlan{}

	gathered := mustGather(t, &plan, nil, []string{outPort, namePort})
	want := map[string]any{outPort: make([]any, 0), namePort: make([]any, 0)}
	assertDeepEqual(t, "Gather", gathered, want)
	assertInt(t, "Cardinality", plan.Cardinality(), 0)
}
