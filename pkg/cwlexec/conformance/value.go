package conformance

import (
	"encoding/json"
	"math/big"
	"strconv"
	"strings"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// normalize renders value as the JSON tree cwltest compares.
//
// cwltest never sees a Go value. It reads the runner's stdout and hands json.loads' result
// to its comparison, so both sides of a comparison here are put through the same round
// trip: rendered by [cwlcore.EncodeJSON], which is the project's single definition of how
// a CWL value becomes JSON text, and read back with numbers left as their literal tokens.
//
// Keeping the tokens is what makes the numeric comparison faithful. Python parses a JSON
// integer literal as an arbitrary-precision int and anything with a point or an exponent
// as a float, and it compares the two exactly -- 10**42 != 1e42. Decoding into float64
// would erase that distinction and quietly turn a difference cwltest reports into a pass.
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

// measured renders a number taken from the filesystem in the same form a normalized
// document value has, so that the two compare on equal terms.
func measured(value int64) json.Number {
	return json.Number(strconv.FormatInt(value, decimalBase))
}

// render renders a value for a difference message.
//
// A number is written as the literal it was read from rather than re-encoded, so that a
// message about a magnitude no float can hold says what the document actually wrote.
func render(value any) string {
	number, ok := value.(json.Number)
	if ok {
		return number.String()
	}

	return cwlcore.EncodeJSON(value)
}

// decimalBase is the radix a JSON number literal is written in.
const decimalBase = 10

// equalScalar reports whether two normalized values are equal the way Python's == judges
// them, which for numbers is by value across the int/float divide rather than by
// representation.
//
// Containers never reach it: [compare] dispatches an expected object or list before the
// scalar case, and an actual value of a different kind is unequal to a scalar whatever it
// is.
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

// asNumber reads a value Python would compare as a number.
//
// A bool is one of them. In Python bool is a subclass of int, so True == 1 and False == 0;
// a comparison that said otherwise would disagree with cwltest over an output object that
// reports a flag where a count was expected, or the reverse.
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

// comparableEqual is == guarded against the one input that would panic: two values of the
// same uncomparable dynamic type. Only a map or a slice can be one, and either is unequal
// to every scalar an expectation can hold.
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

// equalNumber compares two JSON number literals as Python compares the values it parses
// them into.
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

// integerLiteral reads a literal Python would parse as an int, exactly and at any
// magnitude. A token carrying a point or an exponent is not one, however whole its value.
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

// equalIntegerAndFloat compares an exact integer against a float literal the way Python
// does: exactly, by widening both to a rational rather than rounding the integer to a
// float. It is what makes a 43-digit literal differ from the float64 nearest to it.
func equalIntegerAndFloat(integer *big.Int, n json.Number) bool {
	value, err := strconv.ParseFloat(n.String(), 64)
	if err != nil {
		return false
	}

	// SetFloat64 answers nil for a NaN or an infinity, neither of which equals an
	// integer.
	exact := new(big.Rat).SetFloat64(value)
	if exact == nil {
		return false
	}

	return exact.Cmp(new(big.Rat).SetInt(integer)) == 0
}
