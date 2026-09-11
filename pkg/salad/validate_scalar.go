package salad

import (
	"math"
	"strings"
)

// Numeric range bounds for int/long fit checks.
const (
	// maxIntPlusOne is 2^31, the first value an int cannot hold.
	maxIntPlusOne = 2147483648
	// minIntValue is -2^31, the smallest value an int can hold.
	minIntValue = -2147483648
	// maxLongPlusOne is 2^63, the first value a long cannot hold.
	maxLongPlusOne = 9223372036854775808
	// minLongValue is -2^63, the smallest value a long can hold.
	minLongValue = -9223372036854775808
)

// maxListedSymbols bounds how many enum symbols a diagnostic spells out.
const maxListedSymbols = 20

// Expression enum names and openers for validation rule 9.
const (
	nameExpression    = "Expression"
	exprReferenceOpen = "$("
	exprBodyOpen      = "${"
)

// checkPrimitive validates n against one of Schema Salad's primitive types.
func (v *validator) checkPrimitive(p *PrimitiveType, n Node) *Error {
	switch p.Kind {
	case PrimitiveNull:
		return v.checkNull(n)
	case PrimitiveAny:
		return v.checkAny(n)
	case PrimitiveBoolean:
		return v.checkBoolean(n)
	case PrimitiveString:
		return v.checkString(n)
	case PrimitiveInt, PrimitiveLong:
		return v.checkInteger(p.Kind, n)
	case PrimitiveFloat, PrimitiveDouble:
		return v.checkFloating(p.Kind, n)
	default:
		return v.wrongType(n, p.TypeName())
	}
}

// checkNull validates that n is null.
func (v *validator) checkNull(n Node) *Error {
	if IsNull(n) {
		return nil
	}

	return v.wrongType(n, nameNull)
}

// checkAny validates Schema Salad's Any, which admits any value except null.
func (v *validator) checkAny(n Node) *Error {
	if IsNull(n) {
		return v.fail(nodeLoc(n), "the value is null, but %s requires a value", nameAny)
	}

	return nil
}

// checkBoolean validates that n is a boolean.
func (v *validator) checkBoolean(n Node) *Error {
	if s, ok := AsScalar(n); ok && s.IsBool() {
		return nil
	}

	return v.wrongType(n, nameBoolean)
}

// checkString validates that n is a string.
func (v *validator) checkString(n Node) *Error {
	if _, ok := AsString(n); ok {
		return nil
	}

	return v.wrongType(n, nameString)
}

// checkInteger validates that n is an integer that fits the given width.
func (v *validator) checkInteger(k PrimitiveKind, n Node) *Error {
	s, ok := AsScalar(n)
	if !ok {
		return v.wrongType(n, k.String())
	}

	switch s.Kind() {
	case IntScalar, DecimalScalar:
		if val, isInt := s.AsInt(); isInt && intFits(k, val) {
			return nil
		}

		return v.fail(nodeLoc(n), "the value %s does not fit in %s", s.String(), k.String())
	case FloatScalar:
		return v.rejectFloatAsInteger(k, s)
	default:
		return v.wrongType(n, k.String())
	}
}

// rejectFloatAsInteger explains why a float was rejected as an integer.
func (v *validator) rejectFloatAsInteger(k PrimitiveKind, s *ScalarNode) *Error {
	f, _ := s.AsFloat()
	if f == math.Trunc(f) && !floatFitsInteger(k, f) {
		return v.fail(s.Loc(), "the value %s does not fit in %s", s.String(), k.String())
	}

	return v.fail(s.Loc(), "the value is %s, but %s requires a whole number written without a decimal point",
		describe(s), k.String())
}

// checkFloating validates that n is a number fitting the given precision.
func (v *validator) checkFloating(k PrimitiveKind, n Node) *Error {
	s, ok := AsScalar(n)
	if !ok {
		return v.wrongType(n, k.String())
	}

	f, isNumber := s.AsFloat()
	if !isNumber {
		return v.wrongType(n, k.String())
	}

	if k == PrimitiveFloat && !fitsFloat32(f) {
		return v.fail(nodeLoc(n), "the value %s does not fit in %s", s.String(), k.String())
	}

	return nil
}

// intFits reports whether an int64 fits the given integer width.
func intFits(k PrimitiveKind, val int64) bool {
	if k == PrimitiveInt {
		return val >= math.MinInt32 && val <= math.MaxInt32
	}

	return true
}

// floatFitsInteger reports whether a whole-numbered float is in int/long range.
func floatFitsInteger(k PrimitiveKind, f float64) bool {
	if k == PrimitiveInt {
		return f >= minIntValue && f < maxIntPlusOne
	}

	return f >= minLongValue && f < maxLongPlusOne
}

// fitsFloat32 reports whether a value fits in float32 range.
func fitsFloat32(f float64) bool {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return true
	}

	return math.Abs(f) <= math.MaxFloat32
}

// checkEnum validates that n is one of an enum's symbols.
func (v *validator) checkEnum(e *EnumType, n Node) *Error {
	sym, ok := AsString(n)
	if !ok {
		return v.wrongType(n, typeLabel(e))
	}

	if e.HasSymbol(sym) {
		return nil
	}

	if isExpressionEnum(e) {
		return v.checkExpression(e, sym, nodeLoc(n))
	}

	return v.fail(nodeLoc(n),
		"the value %q is not a symbol of %s; expected one of: %s", sym, typeLabel(e), symbolNames(e))
}

// checkExpression applies validation rule 9: Expression enums also accept $(...) or ${...} strings.
func (v *validator) checkExpression(e *EnumType, sym string, loc SourceLine) *Error {
	if strings.Contains(sym, exprReferenceOpen) || strings.Contains(sym, exprBodyOpen) {
		return nil
	}

	return v.fail(loc,
		"the value %q contains no parameter reference or expression in the form %s...) or %s...}, "+
			"which is what %s accepts", sym, exprReferenceOpen, exprBodyOpen, typeLabel(e))
}

// isExpressionEnum reports whether the enum's short name is "Expression".
func isExpressionEnum(e *EnumType) bool {
	return shortName(e.TypeName()) == nameExpression
}

// symbolNames lists an enum's symbols by short name for diagnostics.
func symbolNames(e *EnumType) string {
	names := make([]string, 0, len(e.Symbols))

	for _, sym := range e.Symbols {
		if len(names) == maxListedSymbols {
			names = append(names, labelEllipsis)

			break
		}

		names = append(names, shortName(sym))
	}

	return strings.Join(names, ", ")
}
