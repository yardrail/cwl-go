package conformance

import (
	"encoding/json"
	"math/big"
	"strconv"
	"strings"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// normalize JSON-round-trips a value with UseNumber, matching cwltest's comparison semantics.
func normalize(value any) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(cwlcore.EncodeJSON(value)))
	decoder.UseNumber()

	var out any

	err := decoder.Decode(&out)
	if err != nil {
		return nil, err
	}

	return out, nil
}

// measured renders a filesystem-measured number as a json.Number.
func measured(value int64) json.Number {
	return json.Number(strconv.FormatInt(value, decimalBase))
}

// render renders a value for a difference message.
func render(value any) string {
	number, ok := value.(json.Number)
	if ok {
		return number.String()
	}

	return cwlcore.EncodeJSON(value)
}

// decimalBase is the radix a JSON number literal is written in.
const decimalBase = 10

// equalScalar compares two normalized scalars using Python's == semantics for numbers.
func equalScalar(a, b any) bool {
	left, leftIsNumber := asNumber(a)
	right, rightIsNumber := asNumber(b)

	if leftIsNumber && rightIsNumber {
		return equalNumber(left, right)
	}

	if leftIsNumber || rightIsNumber {
		return false
	}

	return comparableEqual(a, b)
}

// asNumber reads a value Python would compare as a number. Bools count (True==1, False==0).
func asNumber(value any) (json.Number, bool) {
	switch typed := value.(type) {
	case json.Number:
		return typed, true
	case bool:
		if typed {
			return json.Number("1"), true
		}

		return json.Number("0"), true
	default:
		return "", false
	}
}

// comparableEqual is == guarded against uncomparable types (maps, slices).
func comparableEqual(a, b any) bool {
	switch a.(type) {
	case map[string]any, []any:
		return false
	default:
	}

	switch b.(type) {
	case map[string]any, []any:
		return false
	default:
	}

	return a == b
}

// equalNumber compares two JSON number literals using Python's numeric semantics.
func equalNumber(a, b json.Number) bool {
	left, leftIsInt := integerLiteral(a)
	right, rightIsInt := integerLiteral(b)

	switch {
	case leftIsInt && rightIsInt:
		return left.Cmp(right) == 0
	case leftIsInt:
		return equalIntegerAndFloat(left, b)
	case rightIsInt:
		return equalIntegerAndFloat(right, a)
	default:
		return equalFloat(a, b)
	}
}

// integerLiteral parses n as an exact integer, or returns false if it has a decimal/exponent.
func integerLiteral(n json.Number) (*big.Int, bool) {
	text := n.String()
	if strings.ContainsAny(text, ".eE") {
		return nil, false
	}

	return new(big.Int).SetString(text, decimalBase)
}

// equalFloat compares two literals Python would both parse as floats.
func equalFloat(a, b json.Number) bool {
	left, leftErr := strconv.ParseFloat(a.String(), 64)
	right, rightErr := strconv.ParseFloat(b.String(), 64)

	return leftErr == nil && rightErr == nil && left == right
}

// equalIntegerAndFloat compares an integer and float via exact rational comparison.
func equalIntegerAndFloat(integer *big.Int, n json.Number) bool {
	value, err := strconv.ParseFloat(n.String(), 64)
	if err != nil {
		return false
	}

	exact := new(big.Rat).SetFloat64(value)
	if exact == nil {
		return false
	}

	return exact.Cmp(new(big.Rat).SetInt(integer)) == 0
}
