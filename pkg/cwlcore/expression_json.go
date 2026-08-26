package cwlcore

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// hexDigits indexes the lowercase hexadecimal digits used by \u escapes.
const hexDigits = "0123456789abcdef"

const (
	// decimalBase is the radix every JSON number is written in.
	decimalBase = 10

	// initialJSONBytes is the working buffer one encoding starts with.
	initialJSONBytes = 64

	// firstPrintableRune is the lowest rune a JSON string may carry
	// unescaped; everything below it is a control character.
	firstPrintableRune = 0x20

	// exponentBelow is the magnitude under which a float is written with an
	// exponent, matching where Python's float repr switches.
	exponentBelow = 1e-4

	// digitsAtOrAbove is the magnitude at or above which a computed float is
	// written as its full run of decimal digits rather than with an
	// exponent. See [formatJSONFloat] for why that is the integer spelling
	// and not the float one.
	digitsAtOrAbove = 1e16
)

// EncodeJSON renders value as the "textual JSON representation" the spec calls
// for when a parameter reference is embedded in a larger string.
//
// Two details are load-bearing:
//
//   - Object entries are sorted by key. The spec says so explicitly, and Go
//     map iteration order is randomised, so anything else would make
//     interpolation non-deterministic between runs.
//   - The layout matches Python's json.dumps, which is what the reference
//     implementation interpolates with: ", " between entries and ": " after a
//     key. The spec does not pin the whitespace, so matching the
//     implementation the conformance suite was written against is the safer
//     of two defensible choices.
//
// It is exported because that layout is not only an expression concern: a value
// staged to disk — the contents of an InitialWorkDirRequirement Dirent, say —
// has to be rendered the same way an interpolated one is, and a second
// implementation of it would be a second thing to keep in step with the suite.
//
// It expects values already in their expression shape, which is what the
// evaluator hands its own interpolation. Pass anything holding a typed model
// value through [ToExpressionValue] first: a *File reaching this directly is
// outside the JSON type system and is written as its Go string form, which is
// easy to miss when the *File is one element of an otherwise plain array.
//
// Rendering never fails. A value outside the JSON type system is rendered as
// its Go string form rather than aborting an otherwise valid interpolation.
func EncodeJSON(value any) string {
	return string(appendJSON(make([]byte, 0, initialJSONBytes), value))
}

// appendJSON appends the encoding of value to dst.
func appendJSON(dst []byte, value any) []byte {
	switch typed := value.(type) {
	case nil:
		return append(dst, "null"...)
	case bool:
		return strconv.AppendBool(dst, typed)
	case string:
		return appendJSONString(dst, typed)
	case salad.Decimal:
		return append(dst, typed.String()...)
	case []any:
		return appendJSONArray(dst, typed)
	case map[string]any:
		return appendJSONObject(dst, typed)
	default:
		return appendJSONOther(dst, value)
	}
}

// appendJSONOther handles the value kinds that are not one of the canonical
// decoded shapes: named types, sized integers, and the slices and maps a
// caller may hand in instead of []any and map[string]any.
func appendJSONOther(dst []byte, value any) []byte {
	if encoded, ok := appendJSONNumber(dst, value); ok {
		return encoded
	}

	if list, ok := asList(value); ok {
		return appendJSONArray(dst, list)
	}

	if object, ok := asMap(value); ok {
		return appendJSONObject(dst, object)
	}

	return appendJSONString(dst, fmt.Sprint(value))
}

// appendJSONNumber encodes any Go numeric type. ok is false for everything
// else.
func appendJSONNumber(dst []byte, value any) ([]byte, bool) {
	reflected := reflect.ValueOf(value)

	switch {
	case reflected.CanInt():
		return strconv.AppendInt(dst, reflected.Int(), decimalBase), true
	case reflected.CanUint():
		return strconv.AppendUint(dst, reflected.Uint(), decimalBase), true
	case reflected.CanFloat():
		return append(dst, formatJSONFloat(reflected.Float())...), true
	default:
		return dst, false
	}
}

// formatJSONFloat formats a float that carries no literal — one this engine
// computed rather than one a document wrote.
//
// A number a document wrote never reaches here. It arrives as a [salad.Decimal]
// and is rendered from its own digits, which is how 1.23e-05 goes back out as
// 0.0000123 and a 43-digit integer keeps all 43. This function is what is left:
// the result of arithmetic, of a JavaScript expression, or of a JSON reparse.
//
// Below [digitsAtOrAbove] this is Python's float repr — a whole number keeps its
// ".0", and the switch to exponent notation at the small end happens where
// Python's does.
//
// At or above it, the value is written as its full run of digits with no
// exponent and no ".0", where Python's repr would say "1e+42". The difference is
// deliberate and it is the one place this function is not repr. A float64 that
// large is always a whole number — the gap between neighbouring floats passes 1
// at 2^52, well under 1e16 — so the full spelling loses nothing and reparses to
// the identical float64. What it buys is the case where a large integer has been
// through a JavaScript expression, which is the one way a document's literal can
// still lose its lexeme: Node has a single number type, so the value comes back
// as a float64 and only the digits keep it comparing equal to the integer the
// document wrote. The reference implementation writes "1e+42" there and could
// not pass such a test itself.
//
// NaN and the infinities have no JSON spelling; they are written the way
// json.dumps writes them, which keeps an interpolated string readable even
// though the result is then not parseable JSON.
func formatJSONFloat(value float64) string {
	switch {
	case math.IsNaN(value):
		return "NaN"
	case math.IsInf(value, 1):
		return "Infinity"
	case math.IsInf(value, -1):
		return "-Infinity"
	default:
	}

	magnitude := math.Abs(value)
	if magnitude >= digitsAtOrAbove {
		return strconv.FormatFloat(value, 'f', -1, 64)
	}

	if value != 0 && magnitude < exponentBelow {
		return strconv.FormatFloat(value, 'e', -1, 64)
	}

	text := strconv.FormatFloat(value, 'f', -1, 64)
	if !strings.ContainsRune(text, '.') {
		text += ".0"
	}

	return text
}

// appendJSONArray encodes a list.
func appendJSONArray(dst []byte, list []any) []byte {
	dst = append(dst, '[')

	for i, item := range list {
		if i > 0 {
			dst = append(dst, ", "...)
		}

		dst = appendJSON(dst, item)
	}

	return append(dst, ']')
}

// appendJSONObject encodes an object with its entries sorted by key.
func appendJSONObject(dst []byte, object map[string]any) []byte {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	dst = append(dst, '{')

	for i, key := range keys {
		if i > 0 {
			dst = append(dst, ", "...)
		}

		dst = appendJSONString(dst, key)
		dst = append(dst, ": "...)
		dst = appendJSON(dst, object[key])
	}

	return append(dst, '}')
}

// appendJSONString encodes a JSON string literal. Non-ASCII runes are written
// through as UTF-8 rather than escaped: the result is valid JSON either way,
// and readable output matters more here than byte-for-byte agreement with a
// particular encoder's ensure_ascii default.
func appendJSONString(dst []byte, text string) []byte {
	dst = append(dst, '"')

	for _, char := range text {
		dst = appendJSONRune(dst, char)
	}

	return append(dst, '"')
}

// appendJSONRune escapes one rune of a JSON string literal.
func appendJSONRune(dst []byte, char rune) []byte {
	switch char {
	case '"':
		return append(dst, `\"`...)
	case '\\':
		return append(dst, `\\`...)
	case '\n':
		return append(dst, `\n`...)
	case '\r':
		return append(dst, `\r`...)
	case '\t':
		return append(dst, `\t`...)
	case '\b':
		return append(dst, `\b`...)
	case '\f':
		return append(dst, `\f`...)
	default:
	}

	if char >= firstPrintableRune {
		return utf8.AppendRune(dst, char)
	}

	dst = append(dst, `\u00`...)
	dst = append(dst, hexDigits[char>>4])

	return append(dst, hexDigits[char&0xf])
}
