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

	// firstPrintableRune is the lowest rune a JSON string may carry unescaped.
	firstPrintableRune = 0x20

	// exponentBelow is the magnitude under which a float uses exponent notation.
	exponentBelow = 1e-4

	// digitsAtOrAbove is the magnitude at or above which a float is written as full digits.
	digitsAtOrAbove = 1e16
)

// EncodeJSON renders value as the CWL "textual JSON representation".
// Keys sorted, layout matches Python's json.dumps. Values must be in expression shape;
// pass typed model values through [ToExpressionValue] first.
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

// appendJSONOther handles named types, sized integers, and typed slices/maps.
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

// appendJSONNumber encodes any Go numeric type.
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

// formatJSONFloat formats a computed float (not a document literal).
// Matches Python's float repr, except large integers (>=1e16) use full digits
// to preserve round-trip equality through JavaScript's single number type.
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

// appendJSONString encodes a JSON string literal. Non-ASCII runes pass through as UTF-8.
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
