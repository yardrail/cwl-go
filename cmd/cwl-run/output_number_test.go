package main

import (
	"math/big"
	"strings"
	"testing"
)

// tenToThe42 is the 43-digit integer literal from the conformance suite's
// paramref_arguments_inputs. It is too large for an int64, so pkg/salad widens
// it to a float to have something to range-check, and it is not representable
// in binary64 — float(1e42) != 10**42 — so the only spelling that compares
// equal to the integer the expected object holds is the full run of digits.
const tenToThe42 = "1000000000000000000000000000000000000000000"

// largeDoubleFixture carries that literal, and a value under the small
// threshold, through an expression to the output object.
const largeDoubleFixture = "large_double.cwl"

func TestOutputObjectKeepsALargeIntegerExact(t *testing.T) {
	t.Parallel()

	got := exerciseIn(t, t.TempDir(), fixture(largeDoubleFixture))
	if got.err != nil {
		t.Fatalf("run: %v\n%s", got.err, got.stderr)
	}

	// outputs also proves stdout is exactly one JSON object and nothing
	// else, which is what cwltest parses.
	if got.outputs(t)["out_double"] == nil {
		t.Fatal("the output object has no out_double")
	}

	token := numberToken(t, got.stdout, "out_double")

	if strings.ContainsAny(token, ".eE") {
		t.Fatalf("out_double rendered as %s; a point or an exponent reparses as a float, not as 10^42", token)
	}

	parsed, ok := new(big.Int).SetString(token, 10)
	if !ok {
		t.Fatalf("out_double rendered as %s, which does not parse as an integer", token)
	}

	want := new(big.Int).Exp(big.NewInt(10), big.NewInt(42), nil)
	if parsed.Cmp(want) != 0 {
		t.Errorf("out_double is %v, want exactly 10^42", parsed)
	}
}

func TestOutputObjectUsesTheProjectFloatSpelling(t *testing.T) {
	t.Parallel()

	got := exerciseIn(t, t.TempDir(), fixture(largeDoubleFixture))
	if got.err != nil {
		t.Fatalf("run: %v\n%s", got.err, got.stderr)
	}

	// The standard library would write 0.00001 here. The spelling has to
	// be the one pkg/cwlcore uses, because the same value interpolated
	// into a string by an expression goes through that encoder, and an
	// engine that spells a number two ways has a bug waiting.
	if token := numberToken(t, got.stdout, "out_small"); token != "1e-05" {
		t.Errorf("out_small rendered as %s, want 1e-05", token)
	}
}

func TestOutputObjectRendersFloatBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value float64
		want  string
	}{
		// At and above 1e16 a float64 has no fraction left to print, so
		// it is written as an integer would be: every digit, no ".0".
		{name: "at the integer threshold", value: 1e16, want: "10000000000000000"},
		{name: "just under it", value: 1e15, want: "1000000000000000.0"},
		{name: "far above it", value: 1e42, want: tenToThe42},

		// And the exponent switch at the small end.
		{name: "at the small threshold", value: 1e-4, want: "0.0001"},
		{name: "just under it", value: 1e-5, want: "1e-05"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rendered := render(t, map[string]any{"n": tc.value})

			if got := numberToken(t, rendered, "n"); got != tc.want {
				t.Errorf("rendered %v as %s, want %s", tc.value, got, tc.want)
			}
		})
	}
}

// numberToken pulls the raw JSON text of one key's value out of a rendered
// output object.
//
// The assertions have to be made against the text rather than against a parsed
// value, because parsing is what loses the distinction: encoding/json turns
// every JSON number into a float64, which is the very narrowing the rendering
// exists to survive.
func numberToken(t *testing.T, rendered, key string) string {
	t.Helper()

	_, after, found := strings.Cut(rendered, `"`+key+`": `)
	if !found {
		t.Fatalf("the output object has no %q:\n%s", key, rendered)
	}

	token, _, _ := strings.Cut(after, "\n")

	return strings.TrimSuffix(strings.TrimSpace(token), ",")
}
