package cwlexec

import (
	"errors"
	"fmt"
	"maps"
	"slices"
)

// ScatterMethod is the CWL v1.2 scatterMethod enum.
type ScatterMethod string

const (
	// Dotproduct pairs scattered arrays element-wise (equal lengths required).
	Dotproduct ScatterMethod = "dotproduct"
	// NestedCrossproduct enumerates the full grid, nesting outputs one level per scatter key.
	NestedCrossproduct ScatterMethod = "nested_crossproduct"
	// FlatCrossproduct enumerates the full grid, gathering outputs into a single flat array.
	FlatCrossproduct ScatterMethod = "flat_crossproduct"
)

// Errors reported by [ExpandScatter] and [ScatterPlan.Gather].
var (
	// ErrUnknownScatterMethod reports an unrecognized scatterMethod.
	ErrUnknownScatterMethod = errors.New("unknown scatterMethod")
	// ErrNoScatterKeys reports an empty scatter key list.
	ErrNoScatterKeys = errors.New("scatter requires at least one input id")
	// ErrDuplicateScatterKey reports the same input id listed twice in scatter.
	ErrDuplicateScatterKey = errors.New("duplicate scatter input id")
	// ErrScatterInputMissing reports a scatter key not found in the step's inputs.
	ErrScatterInputMissing = errors.New("scatter input is not present in the step inputs")
	// ErrScatterInputNotArray reports a scatter key whose runtime value is not an array.
	ErrScatterInputNotArray = errors.New("scatter input value is not an array")
	// ErrDotproductLength reports scattered arrays of differing length under dotproduct.
	ErrDotproductLength = errors.New("dotproduct requires all scattered arrays to have the same length")
	// ErrScatterOutputIndex reports a sub-job index outside the plan's range.
	ErrScatterOutputIndex = errors.New("sub-job index does not address a job of this scatter plan")
	// ErrScatterShape reports invalid coordinates or shape dimensions.
	ErrScatterShape = errors.New("scatter job coordinates do not match the plan output shape")
)

// OutShape describes the nesting dimensions of gathered output arrays, outermost first.
type OutShape struct {
	// Dims holds the length of each nesting level, outermost first.
	Dims []int
}

// Flat reports whether gathered outputs are a single un-nested array.
func (s OutShape) Flat() bool {
	return len(s.Dims) <= 1
}

// ScatterJob is one sub-job of a scattered step with its coordinates and inputs.
type ScatterJob struct {
	// Inputs is a shallow copy of base inputs with scatter keys replaced by this sub-job's elements.
	Inputs map[string]any
	// Index is the sub-job's coordinates in the gathered output array.
	Index []int
}

// ScatterPlan is the fully expanded set of sub-jobs for a scattered step.
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

// Cardinality returns the number of sub-jobs in the plan.
func (p *ScatterPlan) Cardinality() int {
	return len(p.Jobs)
}

// ExpandScatter builds a [ScatterPlan] from a step's base inputs, scatter keys, and method.
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

// emptyShape returns the gathered shape for a zero-cardinality plan.
func emptyShape(dims []int, empty int, method ScatterMethod) OutShape {
	if method != NestedCrossproduct {
		return OutShape{Dims: []int{0}}
	}

	return OutShape{Dims: slices.Clone(dims[:empty+1])}
}

// Gather assembles per-sub-job outputs into the step's output arrays, shaped by the plan.
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

// gatherPort builds the output array for one port by writing sub-jobs into a flat array then nesting.
func (p *ScatterPlan) gatherPort(outputs map[int]map[string]any, port string) []any {
	dims := p.OutShape.Dims

	flat := make([]any, shapeSize(dims))
	for i, job := range p.Jobs {
		flat[linearOffset(job.Index, dims)] = outputs[i][port]
	}

	return nest(flat, dims)
}

// validateShape rejects invalid nesting shapes (zero only allowed in innermost dimension).
func (p *ScatterPlan) validateShape() error {
	dims := p.OutShape.Dims
	for d, n := range dims {
		if n < 0 || (n == 0 && d != len(dims)-1) {
			return fmt.Errorf("%w: dimension %d of shape %v is not a valid nesting length", ErrScatterShape, d, dims)
		}
	}

	return nil
}

// validateOutputKeys rejects sub-job indices outside the plan's range.
func (p *ScatterPlan) validateOutputKeys(outputs map[int]map[string]any) error {
	for _, i := range slices.Sorted(maps.Keys(outputs)) {
		if i < 0 || i >= len(p.Jobs) {
			return fmt.Errorf("%w: index %d, plan has %d jobs", ErrScatterOutputIndex, i, len(p.Jobs))
		}
	}

	return nil
}

// validateCoordinates rejects jobs whose Index doesn't match OutShape.
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

// expander enumerates sub-jobs and output shape for one scatterMethod.
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
