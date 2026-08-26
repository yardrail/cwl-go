package cwlcore

import (
	"math"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// bigIntegerDigits is the 43-digit integer literal the paramref_arguments_inputs
// conformance test declares as a double default. It is too large for an int64
// and not representable in binary64, so it reaches the renderer as the
// [salad.Decimal] the document wrote — which is the only form that can come back
// out as the integer it is.
const bigIntegerDigits = "1000000000000000000000000000000000000000000"

// Literals these tables repeat, named because goconst counts occurrences
// package-wide.
const (
	litWholeFloat = "3.0"
	litAFloat     = "4.2"
)

// jsonDecimal is a literal a document wrote, as the renderer receives it.
func jsonDecimal(t *testing.T, text string) salad.Decimal {
	t.Helper()

	value, ok := salad.ParseDecimal(text)
	if !ok {
		t.Fatalf("ParseDecimal(%q) rejected a valid literal", text)
	}

	return value
}

// TestEncodeJSONRendersLiteralsFromTheirText pins the half of number rendering
// that the representation, not the renderer, is responsible for.
//
// Every value here is one a document wrote, so it carries its literal and is
// written back from it. That is what the reference implementation does —
// Builder.tostr runs a document's number through Python's decimal.Decimal — and
// it is why none of these rows depends on the float64 spelling rules below.
func TestEncodeJSONRendersLiteralsFromTheirText(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, text, want string }{
		// paramref_arguments_inputs: the value the whole representation
		// exists for. No point, no exponent, all 43 digits.
		{name: "a_double", text: bigIntegerDigits, want: bigIntegerDigits},
		{name: "its negation", text: "-" + bigIntegerDigits, want: "-" + bigIntegerDigits},

		// very_big_and_very_floats: four `float` defaults whose spellings the
		// suite compares byte for byte.
		{name: "annotation_prokka_evalue", text: "0.00001", want: "0.00001"},
		{name: "annotation_prokka_evalue2", text: "1.23e-05", want: "0.0000123"},
		{name: "annotation_prokka_evalue3", text: "1.23e5", want: "123000"},
		{name: "annotation_prokka_evalue4", text: "1230000", want: "1230000"},

		// An integer literal declared as a float gains no ".0", and a float
		// literal keeps the one it has. The float64 path cannot tell these
		// apart at all, which is the reason the literal travels with the
		// value.
		{name: "a whole float keeps its point", text: litWholeFloat, want: litWholeFloat},
		{name: "an integer gains none", text: "3", want: "3"},

		{name: "a_float", text: litAFloat, want: litAFloat},
		{name: "a_long", text: "4147483647", want: "4147483647"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := EncodeJSON(jsonDecimal(t, tc.text)); got != tc.want {
				t.Errorf("EncodeJSON(%q) = %s, want %s", tc.text, got, tc.want)
			}
		})
	}
}

// TestEncodeJSONLiteralsSurviveContainers checks the literal is not lost on the
// way through an array or an object, which is how paramref_arguments_inputs
// actually renders it: the whole input object goes through one interpolation.
func TestEncodeJSONLiteralsSurviveContainers(t *testing.T) {
	t.Parallel()

	value := map[string]any{
		"an_array_of_doubles": []any{
			jsonDecimal(t, bigIntegerDigits),
			jsonDecimal(t, "-"+bigIntegerDigits),
		},
		"a_double": jsonDecimal(t, bigIntegerDigits),
	}

	want := `{"a_double": ` + bigIntegerDigits +
		`, "an_array_of_doubles": [` + bigIntegerDigits + `, -` + bigIntegerDigits + `]}`

	if got := EncodeJSON(value); got != want {
		t.Errorf("EncodeJSON = %s, want %s", got, want)
	}
}

// namedInt stands in for any named integer type a caller might hand us.
type namedInt int

func TestEncodeJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		value any
		name  string
		want  string
	}{
		{name: "the null value", value: nil, want: nullSymbol},
		{name: "a true", value: true, want: wantTrue},
		{name: "a false", value: false, want: "false"},
		{name: "a string", value: "hi", want: `"hi"`},
		{name: "an empty string", value: "", want: `""`},

		{name: "an int", value: 3, want: "3"},
		{name: "an int64", value: int64(-7), want: "-7"},
		{name: "a uint8", value: uint8(255), want: "255"},
		{name: "a named int", value: namedInt(2), want: "2"},

		{name: "a whole float keeps a fraction", value: 3.0, want: "3.0"},
		{name: "a fractional float", value: 2.5, want: "2.5"},
		{name: "a negative float", value: -0.125, want: "-0.125"},
		{name: "a zero float", value: 0.0, want: "0.0"},
		{name: "a tiny float uses an exponent", value: 1.5e-7, want: "1.5e-07"},
		{name: "a huge float spells out its digits", value: 1e30, want: "1" + strings.Repeat("0", 30)},
		{name: "a float32", value: float32(1.5), want: "1.5"},

		{name: "an empty list", value: make([]any, 0), want: "[]"},
		{name: "a json list", value: []any{int64(1), "a", nil, true}, want: `[1, "a", null, true]`},
		{name: "a nested list", value: []any{[]any{int64(1)}}, want: "[[1]]"},
		{name: "a typed slice", value: []string{"a", "b"}, want: `["a", "b"]`},

		{name: "an empty object", value: make(map[string]any), want: "{}"},
		{name: "an object sorted by key", value: map[string]any{"b": 2, "a": 1}, want: `{"a": 1, "b": 2}`},
		{
			name:  "an object with mixed keys",
			value: map[string]any{"Z": 1, "a": 2, "0": 3},
			want:  `{"0": 3, "Z": 1, "a": 2}`,
		},
		{name: "a nested object", value: map[string]any{"o": map[string]any{"k": "v"}}, want: `{"o": {"k": "v"}}`},
		{name: "a typed map", value: map[string]string{"k": "v"}, want: `{"k": "v"}`},

		{name: "escapes", value: "a\"b\\c\nd\te", want: `"a\"b\\c\nd\te"`},
		{name: "carriage return, backspace and form feed", value: "a\rb\bc\fd", want: `"a\rb\bc\fd"`},
		{name: "a control character", value: "\x01", want: `"\u0001"`},
		{name: "non ascii passes through", value: "héllo", want: `"héllo"`},

		{name: "not a number", value: math.NaN(), want: "NaN"},
		{name: "positive infinity", value: math.Inf(1), want: "Infinity"},
		{name: "negative infinity", value: math.Inf(-1), want: "-Infinity"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := EncodeJSON(tc.value); got != tc.want {
				t.Errorf("EncodeJSON(%#v) = %s, want %s", tc.value, got, tc.want)
			}
		})
	}
}

// TestEncodeJSONNumbers pins the number spellings, which are the part of the
// encoding the conformance suite compares most closely: a value that reparses
// as a different Python type, or to a different Python value, fails a test that
// is otherwise correct. Each case names the behaviour it protects.
func TestEncodeJSONNumbers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		value any
		name  string
		want  string
	}{
		// A large integer that has been through a JavaScript expression
		// comes back as a float64, its literal spent: Node has one number
		// type. Writing every digit is what still makes it compare equal
		// to the integer the document wrote, where an exponent would not.
		// See TestEncodeJSONRendersLiteralsFromTheirText for the path that
		// never loses the literal in the first place.
		{name: "a big integer that lost its literal", value: 1e42, want: bigIntegerDigits},
		{name: "its negation", value: -1e42, want: "-" + bigIntegerDigits},

		// The boundary. Below it a float is a float, with the ".0" Python
		// gives a whole one; at and above it every float64 is an integer,
		// and is written as one. This is the one place the computed-float
		// spelling is deliberately not Python's repr, which would say
		// "1e+16"; see formatJSONFloat.
		{name: "just below the boundary", value: 1e15, want: "1000000000000000.0"},
		{name: "at the boundary", value: 1e16, want: "1" + strings.Repeat("0", 16)},

		// The small end is unchanged: Python switches to an exponent
		// below 1e-4, and so do we.
		{name: "the smallest plain decimal", value: 1e-4, want: "0.0001"},
		{name: "just under it uses an exponent", value: 1e-5, want: "1e-05"},
		{name: "a very small float", value: 1e-42, want: "1e-42"},

		{name: "a fraction", value: 0.1, want: "0.1"},
		{name: "a whole float keeps its point", value: 3.0, want: "3.0"},
		{name: "negative zero keeps its sign", value: math.Copysign(0, -1), want: "-0.0"},
		{name: "the largest float", value: math.MaxFloat64, want: "17976931348623157" + strings.Repeat("0", 292)},

		// An int64 never reaches the float path at all, whatever its size.
		{name: "a small integer", value: 42, want: "42"},
		{name: "a negative integer", value: -7, want: "-7"},
		{name: "the largest int64", value: int64(math.MaxInt64), want: "9223372036854775807"},

		// The values the currently-passing numeric tests carry, in the
		// shape they take once a JavaScript expression has spent their
		// literals. From paramref_arguments_inputs:
		{name: "a_float", value: 4.2, want: "4.2"},
		{name: "an_array_of_floats", value: []any{2.3, 4.2}, want: "[2.3, 4.2]"},
		{name: "a_long", value: int64(4147483647), want: "4147483647"},
		{name: "an_array_of_ints", value: []any{int64(42), int64(23)}, want: "[42, 23]"},
		{
			name:  "an_array_of_doubles",
			value: []any{1e42, -1e42},
			want:  "[" + bigIntegerDigits + ", -" + bigIntegerDigits + "]",
		},

		// From params.cwl, whose integers pass through untouched:
		{name: "a params.cwl integer", value: int64(2), want: "2"},
		{name: "a params.cwl integer in an object", value: map[string]any{"b az": int64(2)}, want: `{"b az": 2}`},

		// From floats_small_and_large, in the shape they take after a
		// JavaScript round trip. The document's own spellings — 0.00001,
		// 0.0000123, 123000, 1230000 — are what the suite compares, and
		// they come from the literal, not from here:
		{name: "annotation_prokka_evalue", value: 0.00001, want: "1e-05"},
		{name: "annotation_prokka_evalue2", value: 1.23e-05, want: "1.23e-05"},
		{name: "annotation_prokka_evalue3", value: 1.23e5, want: "123000.0"},
		{name: "annotation_prokka_evalue4", value: 1230000.0, want: "1230000.0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := EncodeJSON(tc.value); got != tc.want {
				t.Errorf("EncodeJSON(%#v) = %s, want %s", tc.value, got, tc.want)
			}
		})
	}
}

// TestEncodeJSONFallsBackForUnrecognizedValues covers appendJSONOther's final
// fallback: a value that is neither a recognized number, list nor map kind is
// rendered as its Go string form rather than aborting the encoding.
func TestEncodeJSONFallsBackForUnrecognizedValues(t *testing.T) {
	t.Parallel()

	type opaque struct{ X int }

	if got, want := EncodeJSON(opaque{X: 1}), `"{1}"`; got != want {
		t.Errorf("EncodeJSON(struct) = %s, want %s", got, want)
	}

	if got, want := EncodeJSON(complex(1, 2)), `"(1+2i)"`; got != want {
		t.Errorf("EncodeJSON(complex) = %s, want %s", got, want)
	}
}

func TestInterpolatedText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		value any
		name  string
		want  string
	}{
		{name: "a string loses its quotes", value: "a\"b", want: `a"b`},
		{name: "a number does not", value: int64(3), want: "3"},
		{name: "the null value", value: nil, want: nullSymbol},
		{name: "a json object", value: map[string]any{"a": int64(1)}, want: `{"a": 1}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := interpolatedText(tc.value); got != tc.want {
				t.Errorf("interpolatedText(%#v) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}
