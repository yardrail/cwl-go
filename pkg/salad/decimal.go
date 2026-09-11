package salad

import (
	"errors"
	"math/big"
	"strconv"
	"strings"
)

// The bounds a decimal literal is accepted within.
const (
	// maxDecimalText caps the fixed-point rendering length to prevent exponent bombs.
	maxDecimalText = 4096

	// decimalTextPadding is room for sign and "0." prefix in rendering.
	decimalTextPadding = 3
)

// Decimal is an exact decimal number preserving the document's original literal.
// Value is sign * digits * 10^exponent. The zero value is zero.
type Decimal struct {
	// digits is the unsigned coefficient with leading zeros stripped. Empty means 0.
	digits string

	// exp is the power of ten the coefficient is scaled by.
	exp int

	// neg records the sign (preserved even for zero).
	neg bool

	// floatForm records that the literal had a decimal point or exponent.
	floatForm bool
}

// ParseDecimal parses a decimal literal. Returns false for invalid text or
// unreasonably large expansions.
func ParseDecimal(text string) (Decimal, bool) {
	signed, ok := decimalSign(text)
	if !ok {
		return Decimal{digits: "", exp: 0, neg: false, floatForm: false}, false
	}

	scaled, ok := decimalExponent(signed.body)
	if !ok {
		return Decimal{digits: "", exp: 0, neg: false, floatForm: false}, false
	}

	pointed := decimalPoint(scaled.mantissa)
	if !decimalDigitsOnly(pointed.whole) || !decimalDigitsOnly(pointed.fraction) {
		return Decimal{digits: "", exp: 0, neg: false, floatForm: false}, false
	}

	if pointed.whole == "" && pointed.fraction == "" {
		return Decimal{digits: "", exp: 0, neg: false, floatForm: false}, false
	}

	value := Decimal{
		digits:    strings.TrimLeft(pointed.whole+pointed.fraction, "0"),
		exp:       scaled.exp - len(pointed.fraction),
		neg:       signed.neg,
		floatForm: pointed.hasPoint || strings.ContainsAny(signed.body, "eE"),
	}

	if len(value.digits)+abs(value.exp) > maxDecimalText {
		return Decimal{digits: "", exp: 0, neg: false, floatForm: false}, false
	}

	return value, true
}

// signedText is a literal split from its leading sign.
type signedText struct {
	body string
	neg  bool
}

// decimalSign splits off an optional leading sign.
func decimalSign(text string) (signedText, bool) {
	if text == "" {
		return signedText{body: "", neg: false}, false
	}

	switch text[0] {
	case '-':
		return signedText{body: text[1:], neg: true}, true
	case '+':
		return signedText{body: text[1:], neg: false}, true
	default:
		return signedText{body: text, neg: false}, true
	}
}

// scaledText is a literal split from its exponent.
type scaledText struct {
	mantissa string
	exp      int
}

// decimalExponent splits off an optional exponent suffix.
func decimalExponent(body string) (scaledText, bool) {
	mantissa, digits, found := strings.Cut(body, "e")
	if !found {
		mantissa, digits, found = strings.Cut(body, "E")
	}

	if !found {
		return scaledText{mantissa: body, exp: 0}, true
	}

	exp, err := strconv.Atoi(digits)
	if err != nil {
		return scaledText{mantissa: "", exp: 0}, false
	}

	return scaledText{mantissa: mantissa, exp: exp}, true
}

// pointedText is a mantissa split at its decimal point.
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

// decimalDigitsOnly reports whether text is entirely ASCII digits. Empty is true.
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

// String renders the number in fixed-point form, matching the reference implementation.
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

// MarshalJSON writes the number as a JSON number literal.
func (d Decimal) MarshalJSON() ([]byte, error) {
	return []byte(d.String()), nil
}

// Float64 returns the nearest float64. Out-of-range values saturate to infinity.
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

// IsFloatForm reports whether the literal had a decimal point or exponent.
func (d Decimal) IsFloatForm() bool {
	return d.floatForm
}

// IsIntegral reports whether the value is a whole number.
func (d Decimal) IsIntegral() bool {
	_, ok := d.integralDigits()

	return ok
}

// BigInt returns the exact integer value, or false if not integral.
func (d Decimal) BigInt() (*big.Int, bool) {
	digits, ok := d.integralDigits()
	if !ok {
		return nil, false
	}

	value, ok := new(big.Int).SetString(digits, decimalBase)
	if !ok {
		// Unreachable: integralDigits always returns either "0" or a run of
		// ASCII digit characters, which SetString(_, 10) never rejects.
		return nil, false
	}

	if d.neg {
		value.Neg(value)
	}

	return value, true
}

// Int64 returns the exact int64 value, or false if not integral or out of range.
func (d Decimal) Int64() (int64, bool) {
	value, ok := d.BigInt()
	if !ok || !value.IsInt64() {
		return 0, false
	}

	return value.Int64(), true
}

// coefficient returns the digit string, defaulting empty to "0".
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

// integralDigits returns the unsigned digit string if the value is integral.
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
