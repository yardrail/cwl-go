package cwlexec

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// decimalFour is the literal 4 as a document writes it.
func decimalFour() salad.Decimal {
	literal, _ := salad.ParseDecimal("4")

	return literal
}

// intArray is the int[] type.
func intArray() cwlcore.TypeRef {
	return cwlcore.NewArrayType(&cwlcore.ArraySchema{Items: primitive(cwlcore.PrimitiveInt)})
}

func TestCoerceIntegers(t *testing.T) {
	t.Parallel()

	record := cwlcore.NewRecordType(&cwlcore.RecordSchema{Fields: []cwlcore.RecordField{
		{Name: "file:///s#tally/count", Type: primitive(cwlcore.PrimitiveLong)},
		{Name: "file:///s#tally/ratio", Type: primitive(cwlcore.PrimitiveDouble)},
	}})
	intOrFloat := cwlcore.NewUnionType([]cwlcore.TypeRef{
		primitive(cwlcore.PrimitiveInt), primitive(cwlcore.PrimitiveFloat),
	})

	cases := []struct {
		value    any
		want     any
		declared cwlcore.TypeRef
		name     string
	}{
		{name: "whole float to int", value: 3.0, declared: primitive(cwlcore.PrimitiveInt), want: int64(3)},
		{name: "whole float to long", value: -7.0, declared: primitive(cwlcore.PrimitiveLong), want: int64(-7)},
		{name: "float32 to int", value: float32(2), declared: primitive(cwlcore.PrimitiveInt), want: int64(2)},
		{name: "fractional float kept", value: 1.5, declared: primitive(cwlcore.PrimitiveInt), want: 1.5},
		{name: "float type kept", value: 3.0, declared: primitive(cwlcore.PrimitiveFloat), want: 3.0},
		{name: "any kept", value: 3.0, declared: primitive(cwlcore.PrimitiveAny), want: 3.0},
		{name: "int kept", value: int64(4), declared: primitive(cwlcore.PrimitiveInt), want: int64(4)},
		{
			name: "decimal kept", value: decimalFour(), declared: primitive(cwlcore.PrimitiveInt),
			want: decimalFour(),
		},
		{
			name: "int array", value: []any{0.0, 1.0, 2.0}, declared: intArray(),
			want: []any{int64(0), int64(1), int64(2)},
		},
		{name: "optional int", value: 5.0, declared: optional(primitive(cwlcore.PrimitiveInt)), want: int64(5)},
		{name: "optional int null", value: nil, declared: optional(primitive(cwlcore.PrimitiveInt)), want: nil},
		{name: "int or float whole", value: 5.0, declared: intOrFloat, want: int64(5)},
		{name: "int or float fractional", value: 5.5, declared: intOrFloat, want: 5.5},
		{
			name:     "record fields",
			value:    object("count", 9.0, "ratio", 1.0, "extra", 2.0),
			declared: record,
			want:     object("count", int64(9), "ratio", 1.0, "extra", 2.0),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assertDeepEqual(t, "coerced", coerceIntegers(tc.value, tc.declared), tc.want)
		})
	}
}

func TestCoerceIntegersLeavesTheCallersArrayAlone(t *testing.T) {
	t.Parallel()

	original := []any{1.0, 2.0}
	coerceIntegers(original, intArray())

	assertDeepEqual(t, "original", original, []any{1.0, 2.0})
}

// TestValidateInputsNarrowsJSONNumbers covers a job order decoded by encoding/json, which holds
// every number as a float64.
func TestValidateInputsNarrowsJSONNumbers(t *testing.T) {
	t.Parallel()

	process := validationProc(
		validationParam("count", primitive(cwlcore.PrimitiveInt), nil),
		validationParam("big", primitive(cwlcore.PrimitiveLong), nil),
		validationParam("shards", intArray(), nil),
		validationParam("maybe", optional(primitive(cwlcore.PrimitiveInt)), nil),
		validationParam("ratio", primitive(cwlcore.PrimitiveDouble), nil),
	)

	merged, err := ValidateInputs(process, object(
		"count", 3.0, "big", 1e12, "shards", []any{0.0, 1.0}, "maybe", 4.0, "ratio", 2.0,
	))
	if err != nil {
		t.Fatalf("ValidateInputs: unexpected error: %v", err)
	}

	assertDeepEqual(t, "merged", merged, object(
		"count", int64(3), "big", int64(1e12), "shards", []any{int64(0), int64(1)}, "maybe", int64(4),
		"ratio", 2.0,
	))
}

func TestValidateInputsRejectsAFractionalInteger(t *testing.T) {
	t.Parallel()

	cases := []struct {
		value    any
		declared cwlcore.TypeRef
		name     string
	}{
		{name: "fractional int", value: 1.5, declared: primitive(cwlcore.PrimitiveInt)},
		{name: "fractional long", value: 0.25, declared: primitive(cwlcore.PrimitiveLong)},
		{name: "int array item", value: []any{1.0, 2.5}, declared: intArray()},
		{name: "optional int", value: 1.5, declared: optional(primitive(cwlcore.PrimitiveInt))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			process := validationProc(validationParam("n", tc.declared, nil))

			_, err := ValidateInputs(process, object("n", tc.value))
			assertErrorIs(t, "ValidateInputs", err, ErrOutputType)
		})
	}
}

// TestStepInputsNarrowJSONNumbersPerScatterJob covers values that reach a step without passing
// through ValidateInputs: an upstream step whose outputs were decoded from JSON hands a scattered
// step an array of float64s, and each job must see the int its run process declares.
func TestStepInputsNarrowJSONNumbersPerScatterJob(t *testing.T) {
	t.Parallel()

	run := newOperation(wfID(sinkStep)+"/run", []string{sinkPort}, []string{portA}, nil)
	run.Inputs[0].Type = primitive(cwlcore.PrimitiveInt)

	spec := loadingWorkflow(run)
	spec.steps[1].scatter = []string{sinkPort}

	var (
		mu   sync.Mutex
		seen []any
	)

	registry := testRegistry(func(_ context.Context, call *StepCall) (Result, error) {
		if call.StepID != sinkStep {
			return Success(object(portA, []any{0.0, 1.0, 2.0}))
		}

		mu.Lock()

		seen = append(seen, call.Inputs[sinkPort])
		mu.Unlock()

		return Success(object(portA, call.Inputs[sinkPort]))
	})

	runner, err := NewRunner(t.Context(), buildWorkflow(spec), registry, nil)
	if err != nil {
		t.Fatalf("NewRunner: unexpected error: %v", err)
	}

	mustRun(t, runner, object("x", 0))

	slices.SortFunc(seen, func(a, b any) int {
		left, _ := integerValue(a)
		right, _ := integerValue(b)

		return int(left - right)
	})
	assertDeepEqual(t, "seen", seen, []any{int64(0), int64(1), int64(2)})
}
