package cwlexec

import (
	"errors"
	"fmt"
	"maps"
	"slices"
)

// ScatterMethod is the CWL v1.2 `scatterMethod` enum, selecting how multiple scattered input
// arrays are combined into sub-jobs.
//
// See https://www.commonwl.org/v1.2/Workflow.html#WorkflowStep.
type ScatterMethod string

const (
	// Dotproduct pairs the scattered arrays element-wise. Every scattered array must have the
	// same length; the resulting cardinality is that length.
	Dotproduct ScatterMethod = "dotproduct"
	// NestedCrossproduct enumerates the full N-dimensional grid of the scattered arrays and
	// gathers outputs nested one level per scatter key, outermost level being the first key.
	NestedCrossproduct ScatterMethod = "nested_crossproduct"
	// FlatCrossproduct enumerates the same grid as NestedCrossproduct but gathers outputs into a
	// single flat array, in grid traversal order (the last key varies fastest).
	FlatCrossproduct ScatterMethod = "flat_crossproduct"
)

// Errors reported by [ExpandScatter] and [ScatterPlan.Gather]. They are wrapped with context, so
// callers should test them with [errors.Is].
var (
	// ErrUnknownScatterMethod reports a scatterMethod that is not one of the three CWL v1.2 values.
	ErrUnknownScatterMethod = errors.New("unknown scatterMethod")
	// ErrNoScatterKeys reports an empty scatter key list; a scattered step scatters over at least
	// one input id.
	ErrNoScatterKeys = errors.New("scatter requires at least one input id")
	// ErrDuplicateScatterKey reports the same input id listed twice in scatter.
	ErrDuplicateScatterKey = errors.New("duplicate scatter input id")
	// ErrScatterInputMissing reports a scatter key naming an input that is absent from the step's
	// input object.
	ErrScatterInputMissing = errors.New("scatter input is not present in the step inputs")
	// ErrScatterInputNotArray reports a scatter key whose runtime value is not an array.
	ErrScatterInputNotArray = errors.New("scatter input value is not an array")
	// ErrDotproductLength reports scattered arrays of differing length under dotproduct.
	ErrDotproductLength = errors.New("dotproduct requires all scattered arrays to have the same length")
	// ErrScatterOutputIndex reports a sub-job index in the gather input that does not address any
	// job of the plan.
	ErrScatterOutputIndex = errors.New("sub-job index does not address a job of this scatter plan")
	// ErrScatterShape reports a plan whose OutShape is not a valid nesting shape, or a ScatterJob
	// whose Index does not address a slot of it.
	ErrScatterShape = errors.New("scatter job coordinates do not match the plan output shape")
)

// OutShape describes the shape that gathered outputs must take: the length of each nesting level
// of the output array, outermost level first.
//
// A single dimension means a flat output array — the shape produced by dotproduct,
// flat_crossproduct, and by a nested_crossproduct over exactly one scatter key. More than one
// dimension means one nesting level per scatter key, so len(Dims) == len(ScatterPlan.Keys) and
// Dims[i] is the length of the array scattered over Keys[i].
//
// A zero-cardinality plan may still have a shape with structure: only the innermost dimension may
// be zero, and a nested_crossproduct whose inner factor is empty gathers as one empty array per
// outer element — Dims []int{2, 0} renders as [[], []]. See [ExpandScatter] for the rule.
type OutShape struct {
	// Dims holds the length of each nesting level, outermost first. It is never empty for a plan
	// built by ExpandScatter.
	Dims []int
}

// Flat reports whether gathered outputs are a single un-nested array.
func (s OutShape) Flat() bool {
	return len(s.Dims) <= 1
}

// ScatterJob is one expanded sub-job of a scattered step: its coordinates within the scatter, and
// the input object it must be run with.
type ScatterJob struct {
	// Inputs is a shallow copy of the step's base inputs with each scatter key replaced by this
	// sub-job's element of that key's array. Element values are shared with the base inputs and
	// must be treated as immutable.
	Inputs map[string]any
	// Index is the sub-job's coordinates in the gathered output: a single element for dotproduct
	// and flat_crossproduct, one element per scatter key for a multi-key nested_crossproduct.
	// len(Index) always equals len(ScatterPlan.OutShape.Dims).
	Index []int
}

// ScatterPlan is the eager, fully-enumerated expansion of a scattered step: one entry per sub-job,
// each carrying its scatter coordinates and its per-element input object.
//
// Expansion happens once, upfront, so gathered output arrays can be pre-sized to the exact final
// cardinality and filled by integer index. Sub-jobs may therefore complete in any order — out of
// order completion cannot change the gathered result.
type ScatterPlan struct {
	// Method is the scatterMethod this plan was expanded with.
	Method ScatterMethod
	// Keys holds the scattered input ids in declaration order.
	Keys []string
	// Jobs holds one entry per sub-job; len(Jobs) == Cardinality.
	Jobs []ScatterJob
	// OutShape describes how gathered outputs must be nested.
	OutShape OutShape
}

// Cardinality reports the number of sub-jobs in the plan — the exact number of output slots
// gathered arrays are pre-allocated to.
//
// The methods take a pointer receiver because a ScatterPlan is larger than the value-copy
// threshold the linter enforces; they never modify the plan.
func (p *ScatterPlan) Cardinality() int {
	return len(p.Jobs)
}

// ExpandScatter builds the ScatterPlan for a scattered step from its base input object, the
// scattered input ids in declaration order, and its scatterMethod.
//
// It is an error for keys to be empty, to repeat an id, or to name an input that is absent from
// base or whose value is not an array. dotproduct additionally requires every scattered array to
// have the same length.
//
// An empty scattered array yields a zero-job plan, and its gathered shape follows the conformance
// suite rather than the specification's own sentence about it:
//
//   - An empty *outer* factor collapses the whole result to a flat empty array. There is no outer
//     element to nest anything inside, so there is nothing for a nesting level to hold.
//   - An empty *inner* nested_crossproduct factor yields one empty array per outer element:
//     {inp1: ["one", "two"], inp2: []} gathers as [[], []].
//
// This deviates from the normative text, which says only "If any scattered parameter runtime value
// is an empty array, all outputs are set to empty arrays and no work is done for the step"
// (Workflow.html#WorkflowStep) — read literally, a flat [] whatever the method and whichever factor
// is empty. The conformance suite is what defines conformance, and it asserts the nested shape:
// wf_scatter_nested_crossproduct_secondempty expects [[], []] where the literal reading gives [].
// The suite's other two empty cases — wf_scatter_nested_crossproduct_firstempty and
// wf_scatter_dotproduct_twoempty — expect [], which both readings agree on. cwltool produces the
// same shapes as are implemented here.
//
// dotproduct and flat_crossproduct have only one nesting level to work with, so any empty factor
// gives them a flat empty array. dotproduct's equal-length check is not reached when a factor is
// empty; the empty-array rule is stated without qualification, so it wins.
func ExpandScatter(base map[string]any, keys []string, method ScatterMethod) (ScatterPlan, error) {
	expand, err := expanderFor(method)
	if err != nil {
		return ScatterPlan{}, err
	}

	if len(keys) == 0 {
		return ScatterPlan{}, ErrNoScatterKeys
	}

	arrays, err := scatterArrays(base, keys)
	if err != nil {
		return ScatterPlan{}, err
	}

	plan := ScatterPlan{
		Method:   method,
		Keys:     slices.Clone(keys),
		Jobs:     make([]ScatterJob, 0),
		OutShape: OutShape{Dims: []int{0}},
	}

	dims := dimensions(arrays)
	if empty := slices.Index(dims, 0); empty >= 0 {
		plan.OutShape = emptyShape(dims, empty, method)

		return plan, nil
	}

	jobs, shape, err := expand(base, keys, arrays)
	if err != nil {
		return ScatterPlan{}, err
	}

	plan.Jobs, plan.OutShape = jobs, shape

	return plan, nil
}

// emptyShape is the gathered shape of a zero-cardinality plan, given the scattered array lengths
// and the position of the first empty one. See [ExpandScatter] for the rule it implements.
func emptyShape(dims []int, empty int, method ScatterMethod) OutShape {
	if method != NestedCrossproduct {
		return OutShape{Dims: []int{0}}
	}

	// dims[:empty+1] keeps every outer factor and the empty one itself, and drops the factors
	// inside it, which have no level to occupy. When the outer factor is the empty one this is
	// []int{0}, the flat empty array, which is what the outer rule asks for.
	return OutShape{Dims: slices.Clone(dims[:empty+1])}
}

// Gather assembles the per-sub-job output objects into the step's output object.
//
// outputs is keyed by sub-job index — the position of the sub-job in ScatterPlan.Jobs — and holds
// that sub-job's output object. Each declared output port becomes one array in the result, pre-sized
// to the plan's shape and written by ScatterJob.Index, so the order in which outputs is populated
// does not affect the result. dotproduct and flat_crossproduct produce flat arrays;
// nested_crossproduct produces arrays nested one level per scatter key.
//
// A sub-job that is absent from outputs, or whose output object omits a port, contributes null in
// its slot: this is how a `when:` gate that skipped an individual scatter element is represented.
// On a zero-cardinality plan every declared output port is set to an empty array.
func (p *ScatterPlan) Gather(outputs map[int]map[string]any, declaredOut []string) (map[string]any, error) {
	err := p.validateOutputKeys(outputs)
	if err != nil {
		return nil, err
	}

	err = p.validateShape()
	if err != nil {
		return nil, err
	}

	err = p.validateCoordinates()
	if err != nil {
		return nil, err
	}

	gathered := make(map[string]any, len(declaredOut))
	for _, port := range declaredOut {
		gathered[port] = p.gatherPort(outputs, port)
	}

	return gathered, nil
}

// gatherPort builds the output array for a single output port: every sub-job's value is written
// into a pre-sized flat backing array at the offset its coordinates name — so the order sub-jobs
// completed in is irrelevant — and the filled array is then folded into its nesting levels.
//
// It cannot fail: Gather has already checked the shape and every job coordinate.
func (p *ScatterPlan) gatherPort(outputs map[int]map[string]any, port string) []any {
	dims := p.OutShape.Dims

	flat := make([]any, shapeSize(dims))
	for i, job := range p.Jobs {
		flat[linearOffset(job.Index, dims)] = outputs[i][port]
	}

	return nest(flat, dims)
}

// validateShape rejects a nesting shape no expansion could have produced.
//
// A zero is legal only in the innermost dimension, which is where the empty-array rule puts it: an
// outer factor that is empty leaves nothing for an inner level to sit in, so [emptyShape] truncates
// the shape at the first empty factor and there can never be a level below one.
func (p *ScatterPlan) validateShape() error {
	dims := p.OutShape.Dims
	for d, n := range dims {
		if n < 0 || (n == 0 && d != len(dims)-1) {
			return fmt.Errorf("%w: dimension %d of shape %v is not a valid nesting length", ErrScatterShape, d, dims)
		}
	}

	return nil
}

// validateOutputKeys rejects sub-job indices that do not address a job of the plan. Keys are
// visited in sorted order so the reported error is deterministic.
func (p *ScatterPlan) validateOutputKeys(outputs map[int]map[string]any) error {
	for _, i := range slices.Sorted(maps.Keys(outputs)) {
		if i < 0 || i >= len(p.Jobs) {
			return fmt.Errorf("%w: index %d, plan has %d jobs", ErrScatterOutputIndex, i, len(p.Jobs))
		}
	}

	return nil
}

// validateCoordinates rejects jobs whose Index disagrees with OutShape. Plans built by
// ExpandScatter always agree; this guards hand-assembled plans.
func (p *ScatterPlan) validateCoordinates() error {
	dims := p.OutShape.Dims
	for i, job := range p.Jobs {
		if len(job.Index) != len(dims) {
			return fmt.Errorf("%w: job %d has %d coordinates, shape %v has %d levels",
				ErrScatterShape, i, len(job.Index), dims, len(dims))
		}

		err := checkBounds(job.Index, dims, i)
		if err != nil {
			return err
		}
	}

	return nil
}

// checkBounds reports whether every coordinate of idx addresses a slot of dims.
func checkBounds(idx, dims []int, job int) error {
	for d, c := range idx {
		if c < 0 || c >= dims[d] {
			return fmt.Errorf("%w: job %d coordinates %v out of range for shape %v", ErrScatterShape, job, idx, dims)
		}
	}

	return nil
}

// expander enumerates the sub-jobs and output shape for one scatterMethod. arrays holds the
// runtime value of each key, in the same order as keys, and is known to be non-empty.
type expander func(base map[string]any, keys []string, arrays [][]any) ([]ScatterJob, OutShape, error)

// expanderFor selects the expansion strategy for a scatterMethod.
func expanderFor(method ScatterMethod) (expander, error) {
	switch method {
	case Dotproduct:
		return dotproduct, nil
	case FlatCrossproduct:
		return flatCrossproduct, nil
	case NestedCrossproduct:
		return nestedCrossproduct, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownScatterMethod, string(method))
	}
}

// dotproduct pairs the scattered arrays element-wise, erroring if they are not all the same length.
func dotproduct(base map[string]any, keys []string, arrays [][]any) ([]ScatterJob, OutShape, error) {
	size := len(arrays[0])
	for i, arr := range arrays[1:] {
		if len(arr) != size {
			return nil, OutShape{}, fmt.Errorf("%w: %q has %d elements but %q has %d",
				ErrDotproductLength, keys[0], size, keys[i+1], len(arr))
		}
	}

	jobs := make([]ScatterJob, 0, size)

	coords := make([]int, len(keys))
	for i := range size {
		for k := range coords {
			coords[k] = i
		}

		jobs = append(jobs, ScatterJob{Index: []int{i}, Inputs: jobInputs(base, keys, arrays, coords)})
	}

	return jobs, OutShape{Dims: []int{size}}, nil
}

// flatCrossproduct enumerates the full grid and addresses it as one flat dimension.
func flatCrossproduct(base map[string]any, keys []string, arrays [][]any) ([]ScatterJob, OutShape, error) {
	grid := gridCoordinates(dimensions(arrays))

	jobs := make([]ScatterJob, 0, len(grid))
	for i, coords := range grid {
		jobs = append(jobs, ScatterJob{Index: []int{i}, Inputs: jobInputs(base, keys, arrays, coords)})
	}

	return jobs, OutShape{Dims: []int{len(grid)}}, nil
}

// nestedCrossproduct enumerates the full grid and keeps its coordinates, so gathered outputs nest
// one level per scatter key.
func nestedCrossproduct(base map[string]any, keys []string, arrays [][]any) ([]ScatterJob, OutShape, error) {
	dims := dimensions(arrays)
	grid := gridCoordinates(dims)

	jobs := make([]ScatterJob, 0, len(grid))
	for _, coords := range grid {
		jobs = append(jobs, ScatterJob{Index: coords, Inputs: jobInputs(base, keys, arrays, coords)})
	}

	return jobs, OutShape{Dims: dims}, nil
}

// scatterArrays resolves each scatter key to its runtime array value, rejecting repeated keys,
// absent inputs, and non-array values.
func scatterArrays(base map[string]any, keys []string) ([][]any, error) {
	seen := make(map[string]struct{}, len(keys))

	arrays := make([][]any, 0, len(keys))
	for _, key := range keys {
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateScatterKey, key)
		}

		seen[key] = struct{}{}

		value, present := base[key]
		if !present {
			return nil, fmt.Errorf("%w: %q", ErrScatterInputMissing, key)
		}

		arr, isArray := value.([]any)
		if !isArray {
			return nil, fmt.Errorf("%w: %q has Go type %T", ErrScatterInputNotArray, key, value)
		}

		arrays = append(arrays, arr)
	}

	return arrays, nil
}

// jobInputs shallow-copies base and replaces each scatter key with the element that coords selects
// from that key's array.
func jobInputs(base map[string]any, keys []string, arrays [][]any, coords []int) map[string]any {
	inputs := make(map[string]any, len(base))
	maps.Copy(inputs, base)

	for k, key := range keys {
		inputs[key] = arrays[k][coords[k]]
	}

	return inputs
}

// dimensions reports the length of each scattered array, in key order.
func dimensions(arrays [][]any) []int {
	dims := make([]int, 0, len(arrays))
	for _, arr := range arrays {
		dims = append(dims, len(arr))
	}

	return dims
}

// gridCoordinates enumerates every coordinate of the grid described by dims, in row-major order:
// the first dimension varies slowest and the last varies fastest. Every dimension must be
// positive. Each returned slice is freshly allocated and safe to retain.
func gridCoordinates(dims []int) [][]int {
	total := 1
	for _, n := range dims {
		total *= n
	}

	grid := make([][]int, 0, total)

	odometer := make([]int, len(dims))
	for range total {
		grid = append(grid, slices.Clone(odometer))
		advance(odometer, dims)
	}

	return grid
}

// advance increments the odometer by one position, carrying from the last dimension towards the
// first and wrapping to all zeroes past the final coordinate.
func advance(odometer, dims []int) {
	for d, coord := range slices.Backward(odometer) {
		if coord+1 < dims[d] {
			odometer[d] = coord + 1

			return
		}

		odometer[d] = 0
	}
}

// shapeSize reports how many leaf slots a nesting shape holds. A shape with no dimensions holds
// none, so the zero ScatterPlan gathers an empty array per output port.
func shapeSize(dims []int) int {
	if len(dims) == 0 {
		return 0
	}

	size := 1
	for _, n := range dims {
		size *= n
	}

	return size
}

// linearOffset converts a sub-job's coordinates into its offset in the row-major flat backing
// array — the first dimension varies slowest, matching the grid traversal order.
func linearOffset(idx, dims []int) int {
	offset := 0
	for d, coord := range idx {
		offset = offset*dims[d] + coord
	}

	return offset
}

// nest folds a filled flat backing array into one array level per dimension, innermost level
// first. The levels share the backing array and are capped so an append cannot reach a sibling.
//
// The number of groups at each level is the product of the dimensions outside it rather than
// len(level)/width, because width may be zero: a nested_crossproduct whose inner factor is empty
// has a shape such as {2, 0}, and its two empty groups are exactly what that shape means.
func nest(flat []any, dims []int) []any {
	level := flat

	for d, width := range slices.Backward(dims) {
		if d == 0 {
			break
		}

		groups := shapeSize(dims[:d])

		grouped := make([]any, 0, groups)
		for g := range groups {
			grouped = append(grouped, level[g*width:g*width+width:g*width+width])
		}

		level = grouped
	}

	return level
}
