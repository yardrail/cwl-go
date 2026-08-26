package cwlcli

import (
	"encoding/json"
	"math"
	"math/big"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// tenToThe42 is the integer literal that a CWL document can write and an int64
// cannot hold, so pkg/salad widens it to a float. It is the value the
// paramref_arguments_inputs conformance test compares against.
const tenToThe42 = "1000000000000000000000000000000000000000000"

// renderOne renders a single value as the whole of a one-key object and
// returns just that value's JSON text.
func renderOne(t *testing.T, value any) string {
	t.Helper()

	encoded, err := JSON(NewObject().Set("v", value))
	if err != nil {
		t.Fatalf("JSON(%v): %v", value, err)
	}

	_, text, found := strings.Cut(string(encoded), `"v": `)
	if !found {
		t.Fatalf("rendered %q, want a one-key object", encoded)
	}

	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), "}"))
}

func TestFloatsUseTheProjectSpelling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value float64
		want  string
	}{
		// Above the integer threshold: the full digit run, no
		// exponent, and no ".0". The standard encoder says "1e+42".
		{name: "integer too large for an int64", value: 1e42, want: tenToThe42},
		{name: "negative, same rule", value: -1e42, want: "-" + tenToThe42},
		{name: "at the threshold", value: 1e16, want: "10000000000000000"},

		// Below it, a Python float repr: a whole number keeps its ".0".
		{name: "just under the threshold", value: 1e15, want: "1000000000000000.0"},
		{name: "whole", value: 2, want: "2.0"},
		{name: "zero", value: 0, want: "0.0"},
		{name: "fractional", value: 1.5, want: "1.5"},

		// And the exponent switch at the small end, where the standard
		// encoder would say "0.00001".
		{name: "at the small threshold", value: 1e-4, want: "0.0001"},
		{name: "under the small threshold", value: 1e-5, want: "1e-05"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := renderOne(t, tc.value); got != tc.want {
				t.Errorf("rendered %v as %s, want %s", tc.value, got, tc.want)
			}
		})
	}
}

func TestALargeIntegerReparsesExactly(t *testing.T) {
	t.Parallel()

	// This is the property the conformance suite actually tests. cwltest
	// compares parsed values, and float(1e42) != 10**42 because 10^42 is
	// not representable in binary64. Only a bare run of digits reparses to
	// the integer the expected object holds — "1e+42" reparses to a float,
	// and even "1000....0" would, because the point makes it one.
	got := renderOne(t, 1e42)

	if strings.ContainsAny(got, ".eE") {
		t.Fatalf("rendered %s; a point or an exponent makes it reparse as a float", got)
	}

	parsed, ok := new(big.Int).SetString(got, 10)
	if !ok {
		t.Fatalf("rendered %s, which does not parse as an integer", got)
	}

	want := new(big.Int).Exp(big.NewInt(10), big.NewInt(42), nil)
	if parsed.Cmp(want) != 0 {
		t.Errorf("rendered %s, which is %v, want exactly 10^42", got, parsed)
	}
}

func TestFloatsInsideContainersUseTheSameSpelling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value any
		name  string
	}{
		{name: "in a slice", value: []any{1e42}},
		{name: "in a Go map", value: map[string]any{"k": 1e42}},
		{name: "in a nested object", value: NewObject().Set("k", 1e42)},
		{name: "under two levels", value: []any{map[string]any{"k": []any{1e42}}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// A container reaching the leaf encoder is rendered by
			// cwlcore whole, so its own leaves must not fall back to
			// the standard encoder on the way down.
			if got := renderOne(t, tc.value); !strings.Contains(got, tenToThe42) {
				t.Errorf("rendered %s, want it to contain the full digit run", got)
			}
		})
	}
}

func TestAgreesWithCwlcoreEncoder(t *testing.T) {
	t.Parallel()

	// The point of delegating is that there is one spelling, so assert
	// that directly across the shapes a CWL value can take. Whitespace
	// differs by design — this package indents, cwlcore matches json.dumps
	// — so the comparison is over the text with whitespace removed.
	values := []any{
		1e42, 1e16, 1e15, 1e-4, 1e-5, 0.0, 2.0, -1.5,
		int64(7), 42, uint8(3), "a<b&c>", true, nil,
		[]any{1e42, "x", nil},
		map[string]any{"z": 1, "a": 1e-5, "nested": map[string]any{"q": 2.0}},
	}

	for _, value := range values {
		got := squeeze(renderOne(t, value))

		want := squeeze(cwlcore.EncodeJSON(value))
		if got != want {
			t.Errorf("rendered %v as %s, but cwlcore.EncodeJSON says %s", value, got, want)
		}
	}
}

// squeeze removes all whitespace, so that two layouts of the same JSON compare
// equal. It is safe here because no test value holds a string with a space in
// it, which is the only place whitespace is significant.
func squeeze(text string) string {
	return strings.Join(strings.Fields(text), "")
}

func TestRejectsValuesOutsideTheJSONTypeSystem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value any
		name  string
	}{
		{name: "channel", value: make(chan int)},
		{name: "function", value: func() {}},
		{name: "complex", value: complex(1, 2)},
		{name: "not a number", value: math.NaN()},
		{name: "infinity", value: math.Inf(1)},
		{name: "nested channel", value: map[string]any{"k": make(chan int)}},
		{name: "channel in a slice", value: []any{make(chan int)}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// cwlcore renders these approximately rather than
			// failing — a channel becomes its address, a NaN becomes
			// the unparseable token NaN — so refusing is this
			// package's job, not its delegate's.
			encoded, err := JSON(NewObject().Set("v", tc.value))
			if err == nil {
				t.Fatalf("JSON(%v) succeeded, rendering %s", tc.name, encoded)
			}

			if encoded != nil {
				t.Errorf("a failed rendering returned %q, want nothing", encoded)
			}
		})
	}
}

func TestRenderingIsStableAcrossRuns(t *testing.T) {
	t.Parallel()

	// Determinism is the feature the dumps are diffed on, and delegating
	// the number spelling must not have introduced a map walk that is not.
	value := NewObject().
		Set("floats", []any{1e42, 1e-5, 2.0}).
		Set("map", map[string]any{"z": 1, "a": 2, "m": 3}).
		Set("nested", NewObject().Set("second", 1).Set("first", 2))

	first := renderOne(t, value)
	for range 20 {
		if got := renderOne(t, value); got != first {
			t.Fatalf("two renders differ:\n%s\n%s", first, got)
		}
	}

	// Insertion order survives for an Object; a Go map is sorted, because
	// there is no author's order to preserve.
	if strings.Index(first, `"second"`) > strings.Index(first, `"first"`) {
		t.Errorf("an Object lost its insertion order:\n%s", first)
	}

	if strings.Index(first, `"a": 2`) > strings.Index(first, `"z": 1`) {
		t.Errorf("a Go map was not sorted by key:\n%s", first)
	}

	var decoded map[string]any

	err := json.Unmarshal([]byte(`{"v": `+first+"}"), &decoded)
	if err != nil {
		t.Errorf("the rendering does not parse: %v\n%s", err, first)
	}
}
