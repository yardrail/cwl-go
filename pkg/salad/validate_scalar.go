package salad

import (
	"math"
	"strings"
)

// Bounds used by the numeric fit check the specification requires: "the value
// appearing in the document must fit into the specified type". They are spelled
// as float64 because the out-of-range case can only reach the validator as a
// float — an integer literal too large for an int64 is parsed as one.
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

// Spellings that Schema Salad validation rule 9 recognizes. nameExpression is
// the short name of the enum type the rule singles out; the two openers are the
// forms a parameter reference and an expression body are written in.
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

// checkNull validates that n is null. An absent value counts as null, which is
// what makes an omitted optional field valid.
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
//
// A float is never accepted for int or long, not even when it holds a whole
// number: YAML and JSON both distinguish 3 from 3.0, the specification lists
// int/long and float/double as separate types, and silently narrowing 3.0 to 3
// would hide a real mistake in a document. The two ways that can go wrong are
// told apart, because "3.0 is not an integer" and "1e30 does not fit in an int"
// call for different fixes.
func (v *validator) checkInteger(k PrimitiveKind, n Node) *Error {
	s, ok := AsScalar(n)
	if !ok {
		return v.wrongType(n, k.String())
	}

	if val, isInt := s.AsInt(); isInt {
		if intFits(k, val) {
			return nil
		}

		return v.fail(nodeLoc(n), "the value %s does not fit in %s", s.String(), k.String())
	}

	if s.Kind() != FloatScalar {
		return v.wrongType(n, k.String())
	}

	return v.rejectFloatAsInteger(k, s)
}

// rejectFloatAsInteger explains why a float cannot stand in for an integer,
// distinguishing a value that is not whole from one that is simply too large.
func (v *validator) rejectFloatAsInteger(k PrimitiveKind, s *ScalarNode) *Error {
	f, _ := s.AsFloat()
	if f == math.Trunc(f) && !floatFitsInteger(k, f) {
		return v.fail(s.Loc(), "the value %s does not fit in %s", s.String(), k.String())
	}

	return v.fail(s.Loc(), "the value is %s, but %s requires a whole number written without a decimal point",
		describe(s), k.String())
}

// checkFloating validates that n is a number that fits the given precision.
// Integers are accepted, as the specification's numeric hierarchy requires.
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

// intFits reports whether an integer already held as an int64 also fits the
// narrower int. Every int64 fits a long by construction.
func intFits(k PrimitiveKind, val int64) bool {
	if k == PrimitiveInt {
		return val >= math.MinInt32 && val <= math.MaxInt32
	}

	return true
}

// floatFitsInteger reports whether a whole-numbered float lies within the range
// of an int or a long. It is the only way a value too large for an int64 can be
// range-checked, because such a literal is parsed as a float in the first place.
func floatFitsInteger(k PrimitiveKind, f float64) bool {
	if k == PrimitiveInt {
		return f >= minIntValue && f < maxIntPlusOne
	}

	return f >= minLongValue && f < maxLongPlusOne
}

// fitsFloat32 reports whether a value survives narrowing to single precision.
// Losing significant digits is not a failure to fit; overflowing to infinity is.
func fitsFloat32(f float64) bool {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return true
	}

	return math.Abs(f) <= math.MaxFloat32
}

// checkEnum validates that n is one of an enum's symbols.
//
// Symbols are stored as fully-qualified IRIs but documents spell them with their
// short names, which is the comparison the specification's validation rule 8 for
// enums prescribes and which EnumType.HasSymbol implements.
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

// checkExpression applies Schema Salad validation rule 9, which the vendored
// metaschema states in its Schema_Validation section as:
//
//	As a special case, a field with the `Expression` type validates string
//	values which contain a CWL parameter reference or expression in the form
//	`$(...)` or `${...}`
//
// The rule widens the type rather than replacing it: an enum named Expression
// accepts its declared symbols as any enum does, and additionally accepts any
// string carrying expression syntax. The reference implementation instead
// hard-codes the fully-qualified name org.w3id.cwl.cwl.Expression; matching on
// the short name keeps this a Salad rule about a conventionally-named type
// rather than a dependency of this package on the CWL vocabulary, which the
// layering forbids. This is not CWL knowledge leaking in — the rule is stated by
// the Salad metaschema and by the Salad specification's own validation rules, so
// honouring it here is following the Salad specification.
func (v *validator) checkExpression(e *EnumType, sym string, loc SourceLine) *Error {
	if strings.Contains(sym, exprReferenceOpen) || strings.Contains(sym, exprBodyOpen) {
		return nil
	}

	return v.fail(loc,
		"the value %q contains no parameter reference or expression in the form %s...) or %s...}, "+
			"which is what %s accepts", sym, exprReferenceOpen, exprBodyOpen, typeLabel(e))
}

// isExpressionEnum reports whether an enum is subject to validation rule 9,
// which is decided by the short name of the type.
func isExpressionEnum(e *EnumType) bool {
	return shortName(e.TypeName()) == nameExpression
}

// symbolNames lists an enum's symbols by short name, trailing off after
// maxListedSymbols of them.
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
