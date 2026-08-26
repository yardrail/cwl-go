package salad

import (
	"errors"
	"math/big"
	"strconv"
	"strings"
)

// The bounds a decimal literal is accepted within.
const (
	// maxDecimalText caps the number of characters the fixed-point rendering
	// of a literal may run to.
	//
	// A decimal exponent is written in a handful of source bytes and expands
	// into that many digits, so "1e999999999" is thirteen characters that
	// render as a gigabyte. Python has no such guard because it allocates the
	// same string; refusing the literal here instead sends it down the float
	// path, where it becomes the infinity it always was.
	maxDecimalText = 4096

	// decimalTextPadding is room for the sign and the "0." a fully-scaled
	// value opens with, so that rendering allocates once.
	decimalTextPadding = 3
)

// Decimal is a number as a document wrote it: an exact decimal, not a binary
// float.
//
// It exists because the reference implementation renders a number from the
// literal a document supplied rather than from its parsed value. ruamel hands
// cwltool a round-trip scalar carrying the original lexeme, and Builder.tostr
// renders it through Python's decimal.Decimal, so a float written 1.23e-05 is
// written back as 0.0000123, an integer literal too large for an int64 is
// written back in full, and 1230000 declared as a float stays 1230000 rather
// than acquiring a ".0". A float64 can represent none of those faithfully: the
// first two lose digits to binary rounding and the third loses the distinction
// entirely.
//
// The value is sign * digits * 10^exponent, with digits held as text so that no
// step of the round trip goes through a binary float. The zero value is the
// integer zero.
type Decimal struct {
	// digits is the coefficient, written without a sign and with leading
	// zeros stripped. An empty string is the coefficient 0, which is what
	// makes the zero value useful.
	digits string

	// exp is the power of ten the coefficient is scaled by.
	exp int

	// neg records the sign, which a zero coefficient carries too: a document
	// that wrote -0.0 gets -0.0 back.
	neg bool

	// floatForm records that the literal was written with a decimal point or
	// an exponent. It is not derivable from the value — 1.23e5 and 123000 are
	// the same number written two ways — and it is what tells an integer-typed
	// document value apart from a float-typed one.
	floatForm bool
}

// ParseDecimal parses a decimal literal, reporting false for text that is not
// one or whose fixed-point rendering would be unreasonably long.
//
// The grammar is the YAML 1.2 core schema's base-ten int and float resolutions
// taken together, which is also JSON's number grammar plus a leading plus sign:
// an optional sign, digits with an optional decimal point, and an optional
// exponent.
func ParseDecimal(text string) (Decimal, bool) {
	signed, ok := decimalSign(text)
	if !ok {
		return Decimal{}, false
	}

	scaled, ok := decimalExponent(signed.body)
	if !ok {
		return Decimal{}, false
	}

	pointed := decimalPoint(scaled.mantissa)
	if !decimalDigitsOnly(pointed.whole) || !decimalDigitsOnly(pointed.fraction) {
		return Decimal{}, false
	}

	if pointed.whole == "" && pointed.fraction == "" {
		return Decimal{}, false
	}

	value := Decimal{
		digits:    strings.TrimLeft(pointed.whole+pointed.fraction, "0"),
		exp:       scaled.exp - len(pointed.fraction),
		neg:       signed.neg,
		floatForm: pointed.hasPoint || strings.ContainsAny(signed.body, "eE"),
	}

	if len(value.digits)+abs(value.exp) > maxDecimalText {
		return Decimal{}, false
	}

	return value, true
}

// signedText is a literal split from its sign: the digits and exponent that
// follow, and whether a minus preceded them.
type signedText struct {
	body string
	neg  bool
}

// decimalSign splits off an optional leading sign, reporting false for text with
// nothing after it.
func decimalSign(text string) (signedText, bool) {
	if text == "" {
		return signedText{}, false
	}

	switch text[0] {
	case '-':
		return signedText{body: text[1:], neg: true}, true
	case '+':
		return signedText{body: text[1:]}, true
	default:
		return signedText{body: text}, true
	}
}

// scaledText is a literal split from its exponent: the mantissa, and the power
// of ten the exponent named.
type scaledText struct {
	mantissa string
	exp      int
}

// decimalExponent splits off an optional exponent suffix, reporting false for
// one that is not an integer.
func decimalExponent(body string) (scaledText, bool) {
	mantissa, digits, found := strings.Cut(body, "e")
	if !found {
		mantissa, digits, found = strings.Cut(body, "E")
	}

	if !found {
		return scaledText{mantissa: body}, true
	}

	exp, err := strconv.Atoi(digits)
	if err != nil {
		return scaledText{}, false
	}

	return scaledText{mantissa: mantissa, exp: exp}, true
}

// pointedText is a mantissa split at its decimal point: the digits on each side,
// and whether there was a point at all.
type pointedText struct {
	whole    string
	fraction string
	hasPoint bool
}

// decimalPoint splits a mantissa at its decimal point.
func decimalPoint(mantissa string) pointedText {
	whole, fraction, found := strings.Cut(mantissa, ".")

	return pointedText{whole: whole, fraction: fraction, hasPoint: found}
}

// decimalDigitsOnly reports whether text is made entirely of ASCII digits, which
// is what the core schema means by a digit. An empty run counts, because a
// literal may write digits on only one side of its decimal point.
func decimalDigitsOnly(text string) bool {
	for i := range len(text) {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}

	return true
}

// abs returns the magnitude of an int.
func abs(v int) int {
	if v < 0 {
		return -v
	}

	return v
}

// String renders the number the way the reference implementation renders it onto
// a command line and into interpolated JSON.
//
// That is Python's str(Decimal(literal)) with one adjustment, which cwltool's
// Builder.tostr makes explicitly: where str would reach for scientific notation
// the fixed-point spelling is used instead. Since str already writes every other
// magnitude in fixed point, the result is simply the exact value written out in
// full — trailing zeros of the coefficient kept, no exponent, and no ".0" added
// to a literal that did not have one.
func (d Decimal) String() string {
	out := make([]byte, 0, len(d.digits)+abs(d.exp)+decimalTextPadding)
	if d.neg {
		out = append(out, '-')
	}

	return string(appendFixedPoint(out, d.coefficient(), d.exp))
}

// appendFixedPoint appends digits * 10^exp, written without an exponent.
func appendFixedPoint(dst []byte, digits string, exp int) []byte {
	if exp >= 0 {
		dst = append(dst, digits...)

		return append(dst, strings.Repeat("0", exp)...)
	}

	scale := -exp
	if scale < len(digits) {
		dst = append(dst, digits[:len(digits)-scale]...)
		dst = append(dst, '.')

		return append(dst, digits[len(digits)-scale:]...)
	}

	dst = append(dst, "0."...)
	dst = append(dst, strings.Repeat("0", scale-len(digits))...)

	return append(dst, digits...)
}

// MarshalJSON writes the number as JSON, which is the literal itself: every
// spelling ParseDecimal accepts is also a JSON number, minus a leading plus and
// leading zeros that String has already normalized away.
//
// It exists because a Decimal reaches encoding/json wherever a value carrying one
// is persisted — a suspended run's state, a nested run's payload — and a struct
// of unexported fields would otherwise be written as "{}", silently replacing the
// number with an empty object.
func (d Decimal) MarshalJSON() ([]byte, error) {
	return []byte(d.String()), nil
}

// Float64 returns the nearest float64, which is the value a runtime that has
// only binary floats — a JavaScript engine, or this engine's own arithmetic —
// can work with.
//
// A magnitude outside float64's range saturates to the signed infinity IEEE 754
// rounding produces rather than failing, matching how an over-large float
// literal has always been parsed.
func (d Decimal) Float64() float64 {
	// A digit run and an exponent is always syntactically a float, so the only
	// failure strconv can report here is a range error — and that one returns
	// the saturated infinity or signed zero this wants. The guard is for the
	// case that cannot arise, and answers it with the zero value.
	value, err := strconv.ParseFloat(d.scientific(), bitsPerFloat64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return 0
	}

	return value
}

// IsFloatForm reports whether the literal was written with a decimal point or an
// exponent.
//
// It is the distinction Schema Salad draws between int/long and float/double,
// and it is not recoverable from the value: 1.23e5 and 123000 are the same
// number, and only one of them is an integer as a document means it.
func (d Decimal) IsFloatForm() bool {
	return d.floatForm
}

// IsIntegral reports whether the value is a whole number. A literal written in
// float form can still be integral — 1.23e5 is 123000 — which is exactly the
// case that makes IsFloatForm a separate question.
func (d Decimal) IsIntegral() bool {
	_, ok := d.integralDigits()

	return ok
}

// BigInt returns the exact integer value, and whether the number is one. No
// rounding happens: a value with a non-zero fractional part reports false rather
// than being truncated.
func (d Decimal) BigInt() (*big.Int, bool) {
	digits, ok := d.integralDigits()
	if !ok {
		return nil, false
	}

	value, ok := new(big.Int).SetString(digits, decimalBase)
	if !ok {
		return nil, false
	}

	if d.neg {
		value.Neg(value)
	}

	return value, true
}

// Int64 returns the exact int64 value, and whether the number is a whole one
// that fits. It is the accessor a range check should use: it answers exactly
// where a float64 round trip cannot tell 2^63 from 2^63-1.
func (d Decimal) Int64() (int64, bool) {
	value, ok := d.BigInt()
	if !ok || !value.IsInt64() {
		return 0, false
	}

	return value.Int64(), true
}

// coefficient returns the digit string, spelling the zero value's empty one as
// the single digit it means.
func (d Decimal) coefficient() string {
	if d.digits == "" {
		return "0"
	}

	return d.digits
}

// scientific renders the value in the digits-and-exponent form strconv parses.
func (d Decimal) scientific() string {
	sign := ""
	if d.neg {
		sign = "-"
	}

	return sign + d.coefficient() + "e" + strconv.Itoa(d.exp)
}

// integralDigits returns the unsigned digit string of the whole value, and
// whether the number has no fractional part.
func (d Decimal) integralDigits() (string, bool) {
	digits := d.coefficient()

	if d.exp >= 0 {
		return digits + strings.Repeat("0", d.exp), true
	}

	scale := -d.exp
	if scale >= len(digits) {
		return "0", strings.Trim(digits, "0") == ""
	}

	if strings.Trim(digits[len(digits)-scale:], "0") != "" {
		return "", false
	}

	return digits[:len(digits)-scale], true
}
