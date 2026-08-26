package salad

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// Literals these tables repeat, named because goconst counts occurrences
// package-wide.
const (
	litWhole     = "3.0"
	litFraction  = "3.5"
	litTenth     = "0.1"
	litTinyExp   = "1.23e-05"
	litTinyFlat  = "0.00001"
	litPastLong  = "9223372036854775808"
	litUnderLong = "-9223372036854775809"
	litBigExp    = "1.23e5"
	litHugeExp   = "1e400"
)

// mustDecimal parses a literal that the test asserts is a valid one.
func mustDecimal(t *testing.T, text string) Decimal {
	t.Helper()

	value, ok := ParseDecimal(text)
	if !ok {
		t.Fatalf("ParseDecimal(%q) rejected a valid literal", text)
	}

	return value
}

// TestDecimalStringMatchesPython pins the rendering against the reference
// implementation, one row per literal.
//
// Every want below is the output of cwltool's own expression, run under
// CPython 3 over the same literal:
//
//	d = decimal.Decimal(text)
//	format(d, "f") if "E" in str(d) else str(d)
//
// That is Builder.tostr's ScalarFloat arm verbatim (cwltool 9f6fcba,
// cwltool/builder.py), which is the function that decides what a number a
// document wrote looks like on a command line and in interpolated JSON. Each
// group names the property it protects, because a row that drifts here is a
// conformance failure somewhere far away from this file.
func TestDecimalStringMatchesPython(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, text, want string }{
		// The four the design turns on.
		{name: "a whole float keeps its point", text: litWhole, want: litWhole},
		{name: "an exponent integer spells out its digits", text: "1e42", want: bigIntegerLiteral},
		{name: "a small exponent float loses its exponent", text: litTinyExp, want: "0.0000123"},
		{name: "an integer literal gains no point", text: "1230000", want: "1230000"},

		// very_big_and_very_floats: four `float` defaults that must reach a
		// command line as the document wrote them.
		{name: "annotation_prokka_evalue", text: litTinyFlat, want: litTinyFlat},
		{name: "annotation_prokka_evalue2", text: litTinyExp, want: "0.0000123"},
		{name: "annotation_prokka_evalue3", text: litBigExp, want: "123000"},
		{name: "annotation_prokka_evalue4", text: "1230000", want: "1230000"},

		// paramref_arguments_inputs and record_with_default: the 43-digit
		// integers declared as doubles.
		{name: "a_double", text: bigIntegerLiteral, want: bigIntegerLiteral},
		{name: "its negation", text: "-" + bigIntegerLiteral, want: "-" + bigIntegerLiteral},
		{
			name: "record_with_default fifth",
			text: "4200000000000000000000000000000000000000000",
			want: "4200000000000000000000000000000000000000000",
		},
		{name: "a_float", text: "4.2", want: "4.2"},
		{name: "a_long", text: "4147483647", want: "4147483647"},

		// Zero and its signs, where Python keeps distinctions a float64 does
		// not.
		{name: "the integer zero", text: "0", want: "0"},
		{name: "a whole zero", text: "0.0", want: "0.0"},
		{name: "a negative zero", text: "-0.0", want: "-0.0"},
		{name: "a negative integer zero", text: "-0", want: "-0"},
		{name: "zero with a scale", text: "0.000", want: "0.000"},

		// Coefficient handling: trailing zeros are significant, leading ones
		// are not, and a plus sign is not written back.
		{name: "trailing zeros are kept", text: "1.230", want: "1.230"},
		{name: "leading zeros are dropped", text: "000123", want: "123"},
		{name: "a plus sign is dropped", text: "+3", want: "3"},

		// The float64 boundaries, where the old renderer's 1e16 rule lived.
		{name: "the exponent boundary", text: "1e16", want: "1" + strings.Repeat("0", 16)},
		{name: "just below it", text: "1e15", want: "1" + strings.Repeat("0", 15)},
		{name: "the smallest plain decimal", text: "1e-4", want: "0.0001"},
		{name: "just under it", text: "1e-5", want: litTinyFlat},
		{name: "a tiny float", text: "1.5e-7", want: "0.00000015"},

		// Magnitudes a float64 cannot hold at all, which the literal still can.
		{name: "past MaxInt64", text: litPastLong, want: litPastLong},
		{name: "past MinInt64", text: "-9223372036854775808", want: "-9223372036854775808"},
		{name: "past MaxFloat64", text: litHugeExp, want: "1" + strings.Repeat("0", 400)},
		{name: "under MinSubnormal", text: "1e-400", want: "0." + strings.Repeat("0", 399) + "1"},

		{name: "a fraction", text: litTenth, want: litTenth},
		{name: "digits on both sides", text: "123.456", want: "123.456"},
		{name: "a small integer", text: "42", want: "42"},
		{name: "a negative integer", text: "-7", want: "-7"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := mustDecimal(t, tc.text).String(); got != tc.want {
				t.Errorf("ParseDecimal(%q).String() = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

// TestDecimalZeroValueIsZero pins the useful zero value: an unset Decimal is the
// integer 0 rather than an empty rendering.
func TestDecimalZeroValueIsZero(t *testing.T) {
	t.Parallel()

	var zero Decimal

	if got := zero.String(); got != "0" {
		t.Errorf("Decimal{}.String() = %q, want %q", got, "0")
	}

	if got := zero.Float64(); got != 0 {
		t.Errorf("Decimal{}.Float64() = %v, want 0", got)
	}

	if got, ok := zero.Int64(); !ok || got != 0 {
		t.Errorf("Decimal{}.Int64() = %v, %v, want 0, true", got, ok)
	}

	if zero.IsFloatForm() {
		t.Error("Decimal{}.IsFloatForm() = true, want false")
	}
}

func TestParseDecimalRejects(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, text string }{
		{name: "empty text", text: ""},
		{name: "a sign alone", text: "-"},
		{name: "a bare point", text: "."},
		{name: "a word", text: litHello},
		{name: "trailing text", text: "1e40x"},
		{name: "two points", text: "1.2.3"},
		{name: "a bare exponent", text: "1e"},
		{name: "an exponent with no digits", text: "1e+"},
		{name: "hexadecimal", text: "0x1f"},
		{name: "digit separators", text: "1_000"},
		{name: "a float name", text: "NaN"},
		{name: "leading whitespace", text: " 1"},

		// The guard: thirteen source characters that would render as a
		// gigabyte of digits.
		{name: "an absurd exponent", text: "1e999999999"},
		{name: "an absurd negative exponent", text: "1e-999999999"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if value, ok := ParseDecimal(tc.text); ok {
				t.Errorf("ParseDecimal(%q) = %#v, true; want false", tc.text, value)
			}
		})
	}
}

// TestDecimalIsFloatForm pins the distinction Schema Salad draws between
// int/long and float/double, which is a property of the spelling and not of the
// value: 1.23e5 and 123000 are the same number and only one of them is an
// integer as a document means it.
func TestDecimalIsFloatForm(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, text string
		want       bool
	}{
		{name: "an integer", text: "3", want: false},
		{name: "a signed integer", text: "-3", want: false},
		{name: "a huge integer", text: "1" + strings.Repeat("0", 42), want: false},
		{name: "a point", text: litWhole, want: true},
		{name: "a trailing point", text: "3.", want: true},
		{name: "a leading point", text: ".5", want: true},
		{name: "an exponent", text: "1e42", want: true},
		{name: "a zero exponent", text: "3e0", want: true},
		{name: "a point and an exponent", text: "1.5e3", want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := mustDecimal(t, tc.text).IsFloatForm(); got != tc.want {
				t.Errorf("ParseDecimal(%q).IsFloatForm() = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

// TestDecimalIntegerAccessorsAreExact pins the property that makes DECIDED-10's
// range check exact: Int64 answers from the digits, so it tells 2^63-1 from 2^63
// where a float64 rounds them together.
func TestDecimalIntegerAccessorsAreExact(t *testing.T) {
	t.Parallel()

	cases := []decimalIntCase{
		{name: "the integer zero", text: "0", want: 0, fits: true, integral: true},
		{name: "MaxInt32", text: "2147483647", want: math.MaxInt32, fits: true, integral: true},
		{name: "MaxInt32 plus one", text: "2147483648", want: 1 << 31, fits: true, integral: true},
		{name: "MinInt32", text: "-2147483648", want: math.MinInt32, fits: true, integral: true},
		{name: "MaxInt64", text: "9223372036854775807", want: math.MaxInt64, fits: true, integral: true},
		{name: "MaxInt64 plus one", text: litPastLong, fits: false, integral: true},
		{name: "MinInt64", text: "-9223372036854775808", want: math.MinInt64, fits: true, integral: true},
		{name: "MinInt64 minus one", text: litUnderLong, fits: false, integral: true},
		{name: "an exponent that is whole", text: litBigExp, want: 123000, fits: true, integral: true},
		{name: "a scale that is all zeros", text: "3.000", want: 3, fits: true, integral: true},
		{name: "a whole float", text: litWhole, want: 3, fits: true, integral: true},
		{name: "a fraction is not whole", text: litFraction, fits: false, integral: false},
		{name: "a small fraction is not whole", text: "0.0001", fits: false, integral: false},
		{name: "a huge integer does not fit", text: "1" + strings.Repeat("0", 42), fits: false, integral: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			value := mustDecimal(t, tc.text)

			got, fits := value.Int64()
			if fits != tc.fits || (fits && got != tc.want) {
				t.Errorf("ParseDecimal(%q).Int64() = %d, %v; want %d, %v", tc.text, got, fits, tc.want, tc.fits)
			}

			if got := value.IsIntegral(); got != tc.integral {
				t.Errorf("ParseDecimal(%q).IsIntegral() = %v, want %v", tc.text, got, tc.integral)
			}

			assertBigInt(t, value, tc)
		})
	}
}

// decimalIntCase is one row of the integer-accessor table: a literal, the int64
// it should yield, and whether it fits one and is whole at all.
type decimalIntCase struct {
	name     string
	text     string
	want     int64
	fits     bool
	integral bool
}

// assertBigInt checks BigInt agrees with Int64 and with IsIntegral.
func assertBigInt(t *testing.T, value Decimal, tc decimalIntCase) {
	t.Helper()

	got, ok := value.BigInt()
	if ok != tc.integral {
		t.Errorf("ParseDecimal(%q).BigInt() ok = %v, want %v", tc.text, ok, tc.integral)

		return
	}

	if ok && tc.fits && got.Int64() != tc.want {
		t.Errorf("ParseDecimal(%q).BigInt() = %s, want %d", tc.text, got, tc.want)
	}
}

func TestDecimalFloat64(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, text string
		want       float64
	}{
		{name: "an integer", text: "42", want: 42},
		{name: "a fraction", text: litTenth, want: 0.1},
		{name: "an exponent", text: litTinyExp, want: 1.23e-5},
		{name: "a huge integer rounds", text: "1" + strings.Repeat("0", 42), want: 1e42},
		{name: "past MaxFloat64 saturates", text: litHugeExp, want: math.Inf(1)},
		{name: "past MinFloat64 saturates", text: "-1e400", want: math.Inf(-1)},
		{name: "under the smallest subnormal", text: "1e-400", want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := mustDecimal(t, tc.text).Float64(); got != tc.want {
				t.Errorf("ParseDecimal(%q).Float64() = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

// TestNumberNodeKeepsTheLiteral pins the mapping from a literal to the scalar
// kind it resolves to, and that the literal travels with it.
//
// The kind is decided by the spelling, not the value: 1.23e5 is a float and
// 123000 is not, even though they are the same number. An integer that fits an
// int64 stays an IntScalar so that every AsInt caller is untouched, and only one
// that does not becomes a DecimalScalar.
func TestNumberNodeKeepsTheLiteral(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, text string
		kind       ScalarKind
		value      any
	}{
		{name: "an ordinary integer", text: "42", kind: IntScalar, value: int64(42)},
		{name: "a negative integer", text: "-7", kind: IntScalar, value: int64(-7)},
		{name: "MaxInt64", text: "9223372036854775807", kind: IntScalar, value: int64(math.MaxInt64)},
		{name: "past MaxInt64", text: litPastLong, kind: DecimalScalar},
		{name: "the 43-digit integer", text: bigIntegerLiteral, kind: DecimalScalar},
		{name: "past MinInt64", text: litUnderLong, kind: DecimalScalar},
		{name: "a float", text: litWhole, kind: FloatScalar},
		{name: "an exponent that is whole", text: litBigExp, kind: FloatScalar},
		{name: "a huge exponent float", text: litHugeExp, kind: FloatScalar},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			literal := mustDecimal(t, tc.text)
			node := NewNumberNode(SourceLine{}, literal)

			if got := node.Kind(); got != tc.kind {
				t.Errorf("Kind() = %v, want %v", got, tc.kind)
			}

			assertCarriesLiteral(t, node, literal, tc.value)
		})
	}
}

// assertCarriesLiteral checks a numeric node hands back the literal it was built
// from, and boxes itself as want — an int64 where the kind is IntScalar, and the
// literal itself everywhere else.
func assertCarriesLiteral(t *testing.T, node *ScalarNode, literal Decimal, want any) {
	t.Helper()

	got, written := node.AsDecimal()
	if !written || got != literal {
		t.Errorf("AsDecimal() = %#v, %v; want the literal back", got, written)
	}

	if want == nil {
		want = literal
	}

	if value := node.Value(); value != want {
		t.Errorf("Value() = %#v, want %#v", value, want)
	}
}

// TestAsDecimalReportsAbsence pins the other half of AsDecimal: a number with no
// literal behind it says so, which is what keeps a computed float rendering
// through the float64 rules rather than through a fabricated literal.
func TestAsDecimalReportsAbsence(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		node *ScalarNode
	}{
		{name: "a computed float", node: NewFloatNode(SourceLine{}, 1.5)},
		{name: "an integer built from a value", node: NewIntNode(SourceLine{}, 3)},
		{name: "a string", node: NewStringNode(SourceLine{}, litHello)},
		{name: "the null scalar", node: NewNullNode(SourceLine{})},
		{name: "a nil node", node: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got, written := tc.node.AsDecimal(); written {
				t.Errorf("AsDecimal() = %#v, true; want false", got)
			}
		})
	}
}

// TestDecimalMarshalsAsANumber pins that a persisted value keeps its number.
// A struct of unexported fields would otherwise reach encoding/json as "{}",
// which is how a resumed run would come back holding an empty object where the
// document wrote 10^42.
func TestDecimalMarshalsAsANumber(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(map[string]any{"v": mustDecimal(t, bigIntegerLiteral)})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	if want := `{"v":` + bigIntegerLiteral + `}`; string(encoded) != want {
		t.Errorf("json.Marshal = %s, want %s", encoded, want)
	}
}

// TestDecimalScalarStringsItsDigits pins the two diagnostic spellings apart. An
// integer too large for an int64 is quoted in full, because that is what the
// document wrote and it is as long as the document made it; a float is quoted
// the compact way Go spells a float64, because 1e+300 written out is three
// hundred digits of nothing.
func TestDecimalScalarStringsItsDigits(t *testing.T) {
	t.Parallel()

	huge := NewNumberNode(SourceLine{}, mustDecimal(t, bigIntegerLiteral))
	if got := huge.String(); got != bigIntegerLiteral {
		t.Errorf("String() = %q, want all %d digits", got, len(bigIntegerLiteral))
	}

	wide := NewNumberNode(SourceLine{}, mustDecimal(t, "1.0e+300"))
	if got := wide.String(); got != "1e+300" {
		t.Errorf("String() = %q, want %q", got, "1e+300")
	}
}
