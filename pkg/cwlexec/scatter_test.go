package cwlexec

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

const (
	// threeKeys names the three-scatter-key row shared by several tables.
	threeKeys = "three keys"
	// outPort is the declared output port used throughout the gather fixtures.
	outPort = "out"
	// namePort is the second declared output port used by the multi-port fixtures.
	namePort = "name"
	// numPort is the first declared output port used by the multi-port fixtures.
	numPort = "num"
)

// list builds an []any from its arguments, keeping scatter fixtures readable.
func list(values ...any) []any {
	return values
}

// mustExpand expands a scatter plan or fails the test.
func mustExpand(t *testing.T, base map[string]any, keys []string, method ScatterMethod) ScatterPlan {
	t.Helper()

	plan, err := ExpandScatter(base, keys, method)
	if err != nil {
		t.Fatalf("ExpandScatter(%v, %s): %v", keys, method, err)
	}

	return plan
}

// mustGather gathers a plan's outputs or fails the test.
func mustGather(t *testing.T, plan *ScatterPlan, outputs map[int]map[string]any, ports []string) map[string]any {
	t.Helper()

	gathered, err := plan.Gather(outputs, ports)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	return gathered
}

// assertDeepEqual reports a mismatch between got and want under the given label.
func assertDeepEqual(t *testing.T, label string, got, want any) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s =\n  %#v\nwant\n  %#v", label, got, want)
	}
}

// assertInt reports a mismatch between two integers under the given label.
func assertInt(t *testing.T, label string, got, want int) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %d, want %d", label, got, want)
	}
}

// assertErrorIs reports that err does not wrap want.
func assertErrorIs(t *testing.T, label string, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("%s error = %v, want %v", label, err, want)
	}
}

// jobInputList projects a plan's sub-job inputs onto the given keys, one row per sub-job, so
// expansion order can be asserted compactly.
func jobInputList(plan *ScatterPlan, keys ...string) [][]any {
	rows := make([][]any, 0, len(plan.Jobs))
	for _, job := range plan.Jobs {
		row := make([]any, 0, len(keys))
		for _, key := range keys {
			row = append(row, job.Inputs[key])
		}

		rows = append(rows, row)
	}

	return rows
}

// jobIndexList projects a plan's sub-job coordinates.
func jobIndexList(plan *ScatterPlan) [][]int {
	got := make([][]int, 0, len(plan.Jobs))
	for _, job := range plan.Jobs {
		got = append(got, job.Index)
	}

	return got
}

// linearIndices builds the single-coordinate index list a flat plan of n sub-jobs must have.
func linearIndices(n int) [][]int {
	got := make([][]int, 0, n)
	for i := range n {
		got = append(got, []int{i})
	}

	return got
}

// sequence builds [0, n).
func sequence(n int) []int {
	got := make([]int, 0, n)
	for i := range n {
		got = append(got, i)
	}

	return got
}

func TestExpandScatterDotproduct(t *testing.T) {
	t.Parallel()

	tests := []struct {
		base       map[string]any
		name       string
		keys       []string
		wantInputs [][]any
		wantDims   []int
	}{
		{
			name:       "one key",
			base:       map[string]any{"a": list("x", "y", "z")},
			keys:       []string{"a"},
			wantInputs: [][]any{{"x"}, {"y"}, {"z"}},
			wantDims:   []int{3},
		},
		{
			name:       "two keys",
			base:       map[string]any{"a": list(1, 2, 3), "b": list("p", "q", "r")},
			keys:       []string{"a", "b"},
			wantInputs: [][]any{{1, "p"}, {2, "q"}, {3, "r"}},
			wantDims:   []int{3},
		},
		{
			name:       threeKeys,
			base:       map[string]any{"a": list(1, 2), "b": list("p", "q"), "c": list(true, false)},
			keys:       []string{"a", "b", "c"},
			wantInputs: [][]any{{1, "p", true}, {2, "q", false}},
			wantDims:   []int{2},
		},
		{
			name:       "single element",
			base:       map[string]any{"a": list(7), "b": list(8)},
			keys:       []string{"a", "b"},
			wantInputs: [][]any{{7, 8}},
			wantDims:   []int{1},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			plan := mustExpand(t, tc.base, tc.keys, Dotproduct)
			assertInt(t, "Cardinality", plan.Cardinality(), len(tc.wantInputs))
			assertDeepEqual(t, "sub-job inputs", jobInputList(&plan, tc.keys...), tc.wantInputs)
			assertDeepEqual(t, "OutShape.Dims", plan.OutShape.Dims, tc.wantDims)
			assertDeepEqual(t, "OutShape.Flat()", plan.OutShape.Flat(), true)
			assertDeepEqual(t, "sub-job indices", jobIndexList(&plan), linearIndices(len(tc.wantInputs)))
		})
	}
}

func TestExpandScatterDotproductUnequalLengths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		base map[string]any
		name string
		keys []string
	}{
		{
			name: "second longer",
			base: map[string]any{"a": list(1, 2), "b": list(1, 2, 3)},
			keys: []string{"a", "b"},
		},
		{
			name: "second shorter",
			base: map[string]any{"a": list(1, 2, 3), "b": list(1, 2)},
			keys: []string{"a", "b"},
		},
		{
			name: "third differs",
			base: map[string]any{"a": list(1, 2), "b": list(1, 2), "c": list(1, 2, 3)},
			keys: []string{"a", "b", "c"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := ExpandScatter(tc.base, tc.keys, Dotproduct)
			assertErrorIs(t, "ExpandScatter", err, ErrDotproductLength)
		})
	}
}

// TestExpandScatterFlatCrossproductOrder pins the grid traversal order: the first scatter key
// varies slowest and the last varies fastest.
func TestExpandScatterFlatCrossproductOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		base       map[string]any
		name       string
		keys       []string
		wantInputs [][]any
	}{
		{
			name:       "one key is the array itself",
			base:       map[string]any{"a": list(1, 2, 3)},
			keys:       []string{"a"},
			wantInputs: [][]any{{1}, {2}, {3}},
		},
		{
			name: "two keys, last varies fastest",
			base: map[string]any{"a": list(1, 2), "b": list("p", "q", "r")},
			keys: []string{"a", "b"},
			wantInputs: [][]any{
				{1, "p"},
				{1, "q"},
				{1, "r"},
				{2, "p"},
				{2, "q"},
				{2, "r"},
			},
		},
		{
			name: threeKeys,
			base: map[string]any{"a": list(1, 2), "b": list("p", "q"), "c": list(true, false)},
			keys: []string{"a", "b", "c"},
			wantInputs: [][]any{
				{1, "p", true},
				{1, "p", false},
				{1, "q", true},
				{1, "q", false},
				{2, "p", true},
				{2, "p", false},
				{2, "q", true},
				{2, "q", false},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			plan := mustExpand(t, tc.base, tc.keys, FlatCrossproduct)
			assertDeepEqual(t, "sub-job inputs", jobInputList(&plan, tc.keys...), tc.wantInputs)
			assertDeepEqual(t, "OutShape.Dims", plan.OutShape.Dims, []int{len(tc.wantInputs)})
			assertDeepEqual(t, "OutShape.Flat()", plan.OutShape.Flat(), true)
			assertDeepEqual(t, "sub-job indices", jobIndexList(&plan), linearIndices(len(tc.wantInputs)))
		})
	}
}

func TestExpandScatterNestedCrossproductCoordinates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		base        map[string]any
		name        string
		keys        []string
		wantIndices [][]int
		wantDims    []int
	}{
		{
			name:        "one key behaves like flat",
			base:        map[string]any{"a": list(1, 2, 3)},
			keys:        []string{"a"},
			wantIndices: [][]int{{0}, {1}, {2}},
			wantDims:    []int{3},
		},
		{
			name: "two keys",
			base: map[string]any{"a": list(1, 2), "b": list("p", "q", "r")},
			keys: []string{"a", "b"},
			wantIndices: [][]int{
				{0, 0},
				{0, 1},
				{0, 2},
				{1, 0},
				{1, 1},
				{1, 2},
			},
			wantDims: []int{2, 3},
		},
		{
			name: threeKeys,
			base: map[string]any{"a": list(1, 2), "b": list("p", "q"), "c": list(true, false)},
			keys: []string{"a", "b", "c"},
			wantIndices: [][]int{
				{0, 0, 0},
				{0, 0, 1},
				{0, 1, 0},
				{0, 1, 1},
				{1, 0, 0},
				{1, 0, 1},
				{1, 1, 0},
				{1, 1, 1},
			},
			wantDims: []int{2, 2, 2},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			plan := mustExpand(t, tc.base, tc.keys, NestedCrossproduct)
			assertDeepEqual(t, "sub-job indices", jobIndexList(&plan), tc.wantIndices)
			assertDeepEqual(t, "OutShape.Dims", plan.OutShape.Dims, tc.wantDims)
			assertDeepEqual(t, "OutShape.Flat()", plan.OutShape.Flat(), len(tc.wantDims) == 1)
		})
	}
}

// TestExpandScatterCrossproductsAgreeOnJobInputs checks the two crossproducts enumerate the very
// same grid in the very same order; only the coordinates they record differ.
func TestExpandScatterCrossproductsAgreeOnJobInputs(t *testing.T) {
	t.Parallel()

	base := map[string]any{"a": list(1, 2, 3), "b": list("p", "q"), "c": list(true, false)}
	keys := []string{"a", "b", "c"}

	flat := mustExpand(t, base, keys, FlatCrossproduct)
	nested := mustExpand(t, base, keys, NestedCrossproduct)

	assertInt(t, "flat Cardinality", flat.Cardinality(), 12)
	assertInt(t, "nested Cardinality", nested.Cardinality(), 12)
	assertDeepEqual(t, "nested sub-job inputs", jobInputList(&nested, keys...), jobInputList(&flat, keys...))
}

func TestExpandScatterCardinality(t *testing.T) {
	t.Parallel()

	base := map[string]any{"a": list(1, 2, 3, 4), "b": list("p", "q"), "c": list(true, false, true)}

	tests := []struct {
		name   string
		method ScatterMethod
		keys   []string
		want   int
	}{
		{name: "dot one key", keys: []string{"a"}, method: Dotproduct, want: 4},
		{name: "dot other key", keys: []string{"b"}, method: Dotproduct, want: 2},
		{name: "flat one key", keys: []string{"a"}, method: FlatCrossproduct, want: 4},
		{name: "flat two keys", keys: []string{"a", "b"}, method: FlatCrossproduct, want: 8},
		{name: "flat three keys", keys: []string{"a", "b", "c"}, method: FlatCrossproduct, want: 24},
		{name: "nested one key", keys: []string{"a"}, method: NestedCrossproduct, want: 4},
		{name: "nested two keys", keys: []string{"a", "b"}, method: NestedCrossproduct, want: 8},
		{name: "nested three keys", keys: []string{"a", "b", "c"}, method: NestedCrossproduct, want: 24},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			plan := mustExpand(t, base, tc.keys, tc.method)
			assertInt(t, "Cardinality", plan.Cardinality(), tc.want)
			assertInt(t, "len(Jobs)", len(plan.Jobs), tc.want)
		})
	}
}

// TestExpandScatterEmptyArrayRule covers the empty-array rule for every method, and for an empty
// array in the first, a middle, and the last scatter key.
//
// No work is ever done. The gathered shape follows the conformance suite rather than the spec's
// literal "all outputs are set to empty arrays": an empty outer factor still collapses to a flat
// empty array, but an empty *inner* nested_crossproduct factor keeps the outer levels and puts an
// empty array in each of them. See [ExpandScatter].
func TestExpandScatterEmptyArrayRule(t *testing.T) {
	t.Parallel()

	keys := []string{"a", "b", "c"}
	full := map[string]any{"a": list(1, 2), "b": list("p", "q"), "c": list(true, false)}
	methods := []ScatterMethod{Dotproduct, FlatCrossproduct, NestedCrossproduct}

	// nestedDims is the shape a nested_crossproduct gathers when the named key is the empty
	// one: the factors outside it, then the empty level itself.
	nestedDims := map[string][]int{"a": {0}, "b": {2, 0}, "c": {2, 2, 0}}

	for empty, dims := range nestedDims {
		for _, method := range methods {
			t.Run(fmt.Sprintf("%s empty/%s", empty, method), func(t *testing.T) {
				t.Parallel()

				base := maps.Clone(full)
				base[empty] = make([]any, 0)

				want := []int{0}
				if method == NestedCrossproduct {
					want = dims
				}

				assertEmptyArrayRule(t, base, keys, method, want)
			})
		}
	}
}

// assertEmptyArrayRule checks that expansion produces no work and that gathering produces the
// nesting wantDims describes for every declared output port.
func assertEmptyArrayRule(t *testing.T, base map[string]any, keys []string, method ScatterMethod, wantDims []int) {
	t.Helper()

	plan := mustExpand(t, base, keys, method)
	assertInt(t, "Cardinality", plan.Cardinality(), 0)
	assertDeepEqual(t, "OutShape.Dims", plan.OutShape.Dims, wantDims)
	assertDeepEqual(t, "Keys", plan.Keys, keys)

	empty := emptyNesting(wantDims)

	gathered := mustGather(t, &plan, nil, []string{"out1", "out2"})
	want := map[string]any{"out1": empty, "out2": empty}
	assertDeepEqual(t, "Gather", gathered, want)
}

// emptyNesting builds the gathered value a zero-cardinality plan of the given shape must produce:
// the innermost level is an empty array, and each level outside it repeats the level below.
func emptyNesting(dims []int) []any {
	level := make([]any, 0)

	for d := len(dims) - 1; d >= 1; d-- {
		outer := make([]any, 0, dims[d-1])
		for range dims[d-1] {
			outer = append(outer, level)
		}

		level = outer
	}

	return level
}

// scatterInp1 and scatterInp2 are the scattered inputs the conformance suite's empty-array
// workflows declare.
const (
	scatterInp1 = "inp1"
	scatterInp2 = "inp2"
)

// TestExpandScatterMatchesTheConformanceSuiteEmptyShapes pins the three shapes the cwl-v1.2 suite
// asserts, which is what settles the deviation recorded on [ExpandScatter].
func TestExpandScatterMatchesTheConformanceSuiteEmptyShapes(t *testing.T) {
	t.Parallel()

	two := list("one", "two")
	none := make([]any, 0)
	keys := []string{scatterInp1, scatterInp2}

	tests := []struct {
		base   map[string]any
		name   string
		method ScatterMethod
		want   []any
	}{
		{
			name:   "wf_scatter_nested_crossproduct_secondempty",
			base:   map[string]any{scatterInp1: two, scatterInp2: none},
			method: NestedCrossproduct,
			want:   list(make([]any, 0), make([]any, 0)),
		},
		{
			name:   "wf_scatter_nested_crossproduct_firstempty",
			base:   map[string]any{scatterInp1: none, scatterInp2: two},
			method: NestedCrossproduct,
			want:   make([]any, 0),
		},
		{
			name:   "wf_scatter_flat_crossproduct_oneempty",
			base:   map[string]any{scatterInp1: two, scatterInp2: none},
			method: FlatCrossproduct,
			want:   make([]any, 0),
		},
		{
			name:   "wf_scatter_dotproduct_twoempty",
			base:   map[string]any{scatterInp1: none, scatterInp2: none},
			method: Dotproduct,
			want:   make([]any, 0),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			plan := mustExpand(t, tc.base, keys, tc.method)
			assertInt(t, "Cardinality", plan.Cardinality(), 0)

			gathered := mustGather(t, &plan, nil, []string{"out"})
			assertDeepEqual(t, "Gather", gathered["out"], tc.want)
		})
	}
}

// TestExpandScatterEmptyBeatsDotproductLengthCheck checks the empty-array rule short-circuits
// ahead of the dotproduct equal-length check: no work is done, so there is nothing to disagree on.
func TestExpandScatterEmptyBeatsDotproductLengthCheck(t *testing.T) {
	t.Parallel()

	base := map[string]any{"a": make([]any, 0), "b": list(1, 2, 3)}

	plan := mustExpand(t, base, []string{"a", "b"}, Dotproduct)
	assertInt(t, "Cardinality", plan.Cardinality(), 0)
}

func TestExpandScatterErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		base    map[string]any
		wantErr error
		name    string
		method  ScatterMethod
		keys    []string
	}{
		{
			name:    "unknown method",
			base:    map[string]any{"a": list(1)},
			keys:    []string{"a"},
			method:  ScatterMethod("crossproduct"),
			wantErr: ErrUnknownScatterMethod,
		},
		{
			name:    "empty method",
			base:    map[string]any{"a": list(1)},
			keys:    []string{"a"},
			method:  ScatterMethod(""),
			wantErr: ErrUnknownScatterMethod,
		},
		{
			name:    "no keys",
			base:    map[string]any{"a": list(1)},
			keys:    nil,
			method:  Dotproduct,
			wantErr: ErrNoScatterKeys,
		},
		{
			name:    "duplicate key",
			base:    map[string]any{"a": list(1)},
			keys:    []string{"a", "a"},
			method:  FlatCrossproduct,
			wantErr: ErrDuplicateScatterKey,
		},
		{
			name:    "missing input",
			base:    map[string]any{"a": list(1)},
			keys:    []string{"a", "b"},
			method:  Dotproduct,
			wantErr: ErrScatterInputMissing,
		},
		{
			name:    "nil base",
			base:    nil,
			keys:    []string{"a"},
			method:  Dotproduct,
			wantErr: ErrScatterInputMissing,
		},
		{
			name:    "scalar value",
			base:    map[string]any{"a": 3},
			keys:    []string{"a"},
			method:  Dotproduct,
			wantErr: ErrScatterInputNotArray,
		},
		{
			name:    "null value",
			base:    map[string]any{"a": nil},
			keys:    []string{"a"},
			method:  NestedCrossproduct,
			wantErr: ErrScatterInputNotArray,
		},
		{
			name:    "record value",
			base:    map[string]any{"a": map[string]any{outKeyClass: cwlcore.ClassFile}},
			keys:    []string{"a"},
			method:  FlatCrossproduct,
			wantErr: ErrScatterInputNotArray,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := ExpandScatter(tc.base, tc.keys, tc.method)
			assertErrorIs(t, "ExpandScatter", err, tc.wantErr)
		})
	}
}

// TestExpandScatterInputsIsolated checks that sub-job input objects carry the step's unscattered
// inputs, that they are independent copies, and that the caller's base object is never mutated.
func TestExpandScatterInputsIsolated(t *testing.T) {
	t.Parallel()

	base := map[string]any{"a": list(1, 2), "b": list("p", "q"), "threshold": 10}
	plan := mustExpand(t, base, []string{"a", "b"}, FlatCrossproduct)

	for i, job := range plan.Jobs {
		assertDeepEqual(t, fmt.Sprintf("job %d unscattered input", i), job.Inputs["threshold"], 10)

		job.Inputs["scratch"] = i
	}

	_, leaked := base["scratch"]
	assertDeepEqual(t, "base inputs gained a sub-job key", leaked, false)
	assertDeepEqual(t, "base scatter array", base["a"], list(1, 2))

	_, firstWritten := plan.Jobs[0].Inputs["scratch"]
	assertDeepEqual(t, "sub-job 0 is independently writable", firstWritten, true)

	_, secondWritten := plan.Jobs[1].Inputs["scratch"]
	assertDeepEqual(t, "sub-job 1 is independently writable", secondWritten, true)
}

// TestExpandScatterKeysAreCopied checks the plan does not alias the caller's key slice.
func TestExpandScatterKeysAreCopied(t *testing.T) {
	t.Parallel()

	keys := []string{"a", "b"}
	base := map[string]any{"a": list(1), "b": list(2)}

	plan := mustExpand(t, base, keys, Dotproduct)
	keys[0] = "mutated"

	assertDeepEqual(t, "Keys", plan.Keys, []string{"a", "b"})
}

// TestScatterIndexCoverage asserts the round-trip property: every ScatterJob.Index is unique and
// the indices cover the plan's output shape exactly — for the flat methods that means exactly
// [0, Cardinality), for nested_crossproduct the whole grid.
func TestScatterIndexCoverage(t *testing.T) {
	t.Parallel()

	base := map[string]any{"a": list(1, 2, 3), "b": list("p", "q"), "c": list(true, false)}

	tests := []struct {
		name   string
		method ScatterMethod
		keys   []string
	}{
		{name: "dot 1 key", keys: []string{"a"}, method: Dotproduct},
		{name: "dot 2 keys", keys: []string{"b", "c"}, method: Dotproduct},
		{name: "flat 1 key", keys: []string{"a"}, method: FlatCrossproduct},
		{name: "flat 2 keys", keys: []string{"a", "b"}, method: FlatCrossproduct},
		{name: "flat 3 keys", keys: []string{"a", "b", "c"}, method: FlatCrossproduct},
		{name: "nested 1 key", keys: []string{"a"}, method: NestedCrossproduct},
		{name: "nested 2 keys", keys: []string{"a", "b"}, method: NestedCrossproduct},
		{name: "nested 3 keys", keys: []string{"a", "b", "c"}, method: NestedCrossproduct},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			plan := mustExpand(t, base, tc.keys, tc.method)
			assertIndicesCoverShape(t, &plan)
		})
	}
}

// assertIndicesCoverShape checks the plan's coordinates fill its output shape exactly once.
func assertIndicesCoverShape(t *testing.T, plan *ScatterPlan) {
	t.Helper()

	dims := plan.OutShape.Dims
	assertInt(t, fmt.Sprintf("slots in shape %v", dims), shapeSlots(dims), plan.Cardinality())
	assertCoordinatesDistinct(t, plan)
	assertFlatIndicesPermuted(t, plan)
}

// shapeSlots reports how many leaf slots a nesting shape holds.
func shapeSlots(dims []int) int {
	slots := 1
	for _, n := range dims {
		slots *= n
	}

	return slots
}

// assertCoordinatesDistinct checks no two sub-jobs share coordinates and that each addresses a
// slot of the plan's shape.
func assertCoordinatesDistinct(t *testing.T, plan *ScatterPlan) {
	t.Helper()

	seen := make(map[string]int, plan.Cardinality())
	for i, job := range plan.Jobs {
		key := fmt.Sprint(job.Index)
		if prev, dup := seen[key]; dup {
			t.Fatalf("sub-jobs %d and %d share coordinates %v", prev, i, job.Index)
		}

		seen[key] = i

		assertInBounds(t, i, job.Index, plan.OutShape.Dims)
	}
}

// assertInBounds checks one sub-job's coordinates against the plan's nesting shape.
func assertInBounds(t *testing.T, job int, idx, dims []int) {
	t.Helper()

	if len(idx) != len(dims) {
		t.Fatalf("sub-job %d Index %v does not match shape %v", job, idx, dims)
	}

	for d, coord := range idx {
		if coord < 0 || coord >= dims[d] {
			t.Fatalf("sub-job %d Index %v out of range for shape %v", job, idx, dims)
		}
	}
}

// assertFlatIndicesPermuted checks a flat plan's indices are exactly [0, Cardinality).
func assertFlatIndicesPermuted(t *testing.T, plan *ScatterPlan) {
	t.Helper()

	if !plan.OutShape.Flat() {
		return
	}

	linear := make([]int, 0, plan.Cardinality())
	for _, job := range plan.Jobs {
		linear = append(linear, job.Index[0])
	}

	slices.Sort(linear)
	assertDeepEqual(t, "sorted flat indices", linear, sequence(plan.Cardinality()))
}
