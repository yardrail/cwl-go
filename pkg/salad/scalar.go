package salad

import "strconv"

// ScalarKind discriminates the kind of value a ScalarNode holds.
type ScalarKind int

const (
	// NullScalar is the YAML/JSON null value.
	NullScalar ScalarKind = iota
	// BoolScalar is a boolean.
	BoolScalar
	// IntScalar is an integer, held as an int64.
	IntScalar
	// FloatScalar is a floating-point number, held as a float64.
	FloatScalar
	// StringScalar is a string.
	StringScalar
	// DecimalScalar is an integer the document wrote that does not fit an
	// int64, held as the exact [Decimal] it was written as.
	//
	// It exists because CWL documents do carry such values, and because the
	// reference implementation never loses them: Python parses an integer
	// literal at any magnitude as an int and writes every digit back.
	// Widening to a float was the previous answer and it does not round-trip
	// — float(1e42) is not 10**42.
	//
	// It is a separate kind rather than the representation of every integer
	// so that the common case stays an int64, which is what every existing
	// [ScalarNode.AsInt] caller reads and what an array index or a File size
	// wants to be.
	DecimalScalar
)

// scalarKindNames maps each ScalarKind to the name used in error messages.
//
// DecimalScalar shares int's name deliberately. It is only ever reached by an
// integer literal, the distinction from IntScalar is one of storage rather than
// of what the document wrote, and a diagnostic naming it separately would be
// describing this implementation instead of the document.
var scalarKindNames = [...]string{
	NullScalar:    nameNull,
	BoolScalar:    nameBoolean,
	IntScalar:     nameInt,
	FloatScalar:   nameFloat,
	StringScalar:  nameString,
	DecimalScalar: nameInt,
}

// String returns the name of the scalar kind, as used in error messages.
func (k ScalarKind) String() string {
	if k < 0 || int(k) >= len(scalarKindNames) {
		return nameUnknown
	}

	return scalarKindNames[k]
}

// ScalarNode is a leaf value: null, a boolean, an integer, a float, or a string.
//
// A ScalarNode is immutable once constructed. Use the As* accessors to read it;
// each reports whether the node actually holds that kind, so callers never have
// to guess.
//
// Every method tolerates a nil receiver and treats it as the null scalar, so a
// missing document key can be read without a guard at each call site. The
// accessors below check that explicitly rather than relying on Kind, so the
// tolerance is visible to both readers and static analysis.
//
// A numeric node may also carry the literal it was written as. That is not
// redundant with its value: the reference implementation renders a number from
// its lexeme, so 1.23e-05 goes back out as 0.0000123 and an integer literal
// declared as a float never acquires a ".0". A float this package computed
// rather than read has no lexeme, and [ScalarNode.AsDecimal] says so.
type ScalarNode struct {
	str     string
	dec     Decimal
	loc     SourceLine
	kind    ScalarKind
	num     int64
	flt     float64
	truth   bool
	written bool
}

var _ Node = (*ScalarNode)(nil)

// NewNullNode builds the null scalar located at loc.
func NewNullNode(loc SourceLine) *ScalarNode {
	return &ScalarNode{
		str:     "",
		dec:     Decimal{digits: "", exp: 0, neg: false, floatForm: false},
		loc:     loc,
		kind:    NullScalar,
		num:     0,
		flt:     0,
		truth:   false,
		written: false,
	}
}

// NewBoolNode builds a boolean scalar located at loc.
func NewBoolNode(loc SourceLine, v bool) *ScalarNode {
	return &ScalarNode{
		str:     "",
		dec:     Decimal{digits: "", exp: 0, neg: false, floatForm: false},
		loc:     loc,
		kind:    BoolScalar,
		num:     0,
		flt:     0,
		truth:   v,
		written: false,
	}
}

// NewIntNode builds an integer scalar located at loc.
func NewIntNode(loc SourceLine, v int64) *ScalarNode {
	return &ScalarNode{
		str:     "",
		dec:     Decimal{digits: "", exp: 0, neg: false, floatForm: false},
		loc:     loc,
		kind:    IntScalar,
		num:     v,
		flt:     0,
		truth:   false,
		written: false,
	}
}

// NewNumberNode builds the scalar a numeric literal resolves to, keeping the
// literal itself so that rendering can reproduce it.
//
// The kind follows what the document wrote, in the order Schema Salad's own
// numeric hierarchy is spelled:
//
//   - a literal written with a decimal point or an exponent is a FloatScalar,
//     whatever its value, because 1.23e5 is a float and 123000 is not;
//   - an integer literal an int64 can hold is an IntScalar, which is what every
//     ordinary document integer is and what [ScalarNode.AsInt] reads;
//   - an integer literal that does not fit is a [DecimalScalar], the only kind
//     that can hold it without losing digits.
func NewNumberNode(loc SourceLine, d Decimal) *ScalarNode {
	if d.IsFloatForm() {
		return &ScalarNode{
			str:     "",
			dec:     d,
			loc:     loc,
			kind:    FloatScalar,
			num:     0,
			flt:     d.Float64(),
			truth:   false,
			written: true,
		}
	}

	if value, fits := d.Int64(); fits {
		return &ScalarNode{str: "", dec: d, loc: loc, kind: IntScalar, num: value, flt: 0, truth: false, written: true}
	}

	return &ScalarNode{str: "", dec: d, loc: loc, kind: DecimalScalar, num: 0, flt: 0, truth: false, written: true}
}

// NewFloatNode builds a floating-point scalar located at loc.
//
// The result carries no literal, which is correct for the values this
// constructor serves: a float that arrived as a float64 was computed rather than
// written, and is rendered from its value the way the reference implementation
// renders a computed one. Use [NewNumberNode] for a float a document wrote.
func NewFloatNode(loc SourceLine, v float64) *ScalarNode {
	return &ScalarNode{
		str:     "",
		dec:     Decimal{digits: "", exp: 0, neg: false, floatForm: false},
		loc:     loc,
		kind:    FloatScalar,
		num:     0,
		flt:     v,
		truth:   false,
		written: false,
	}
}

// NewStringNode builds a string scalar located at loc.
func NewStringNode(loc SourceLine, v string) *ScalarNode {
	return &ScalarNode{
		str:     v,
		dec:     Decimal{digits: "", exp: 0, neg: false, floatForm: false},
		loc:     loc,
		kind:    StringScalar,
		num:     0,
		flt:     0,
		truth:   false,
		written: false,
	}
}

// Loc reports where in the source document this scalar came from.
func (s *ScalarNode) Loc() SourceLine {
	if s == nil {
		return SourceLine{
			File:  "",
			Start: Position{Line: 0, Column: 0, Offset: 0},
			End:   Position{Line: 0, Column: 0, Offset: 0},
		}
	}

	return s.loc
}

// Kind reports which kind of value this scalar holds. A nil *ScalarNode reports
// NullScalar.
func (s *ScalarNode) Kind() ScalarKind {
	if s == nil {
		return NullScalar
	}

	return s.kind
}

// IsNull reports whether this scalar is null.
func (s *ScalarNode) IsNull() bool {
	return s.Kind() == NullScalar
}

// AsString returns the string value, and whether this scalar is a string.
func (s *ScalarNode) AsString() (string, bool) {
	if s == nil || s.kind != StringScalar {
		return "", false
	}

	return s.str, true
}

// IsBool reports whether this scalar is a boolean.
func (s *ScalarNode) IsBool() bool {
	return s.Kind() == BoolScalar
}

// AsBool returns the boolean value, or false when this scalar is not a boolean.
//
// Unlike the other accessors it does not report a separate "is that kind" result,
// because a (bool, bool) signature reads ambiguously at the call site; pair it
// with IsBool or Kind when the distinction matters.
func (s *ScalarNode) AsBool() bool {
	return s != nil && s.kind == BoolScalar && s.truth
}

// AsInt returns the integer value, and whether this scalar is an integer.
//
// A float scalar is not an integer even when it holds a whole number: Schema
// Salad distinguishes int/long from float/double, so the conversion is left to
// the validator.
//
// A [DecimalScalar] is not an integer either, for the arithmetic reason that it
// does not fit — every value an int64 can hold is parsed as an IntScalar, so a
// DecimalScalar is out of int's and long's range by construction. Use
// [ScalarNode.AsDecimal] to read an integer of any magnitude.
func (s *ScalarNode) AsInt() (int64, bool) {
	if s == nil || s.kind != IntScalar {
		return 0, false
	}

	return s.num, true
}

// AsDecimal returns the literal the document wrote, and whether this scalar
// carries one.
//
// It is the accessor for anything that renders a number back out, and for a
// range check that must be exact: a [Decimal] holds the value with no binary
// rounding at all, so it can tell 2^63 from 2^63-1 where a float64 cannot.
//
// It reports false for a number this package computed rather than read — a
// float64 handed to [NewFloatNode], say. Such a value has no literal, and the
// reference implementation renders it from its value instead, so the distinction
// is one callers must keep.
func (s *ScalarNode) AsDecimal() (Decimal, bool) {
	if s == nil || !s.written {
		return Decimal{digits: "", exp: 0, neg: false, floatForm: false}, false
	}

	return s.dec, true
}

// AsFloat returns the value as a float64, and whether this scalar is numeric.
// Integer scalars convert, so AsFloat succeeds for IntScalar, DecimalScalar and
// FloatScalar alike.
//
// A DecimalScalar too large for a float64 converts to the saturated infinity IEEE
// 754 rounding produces, matching how an over-large float literal is parsed. The
// conversion is lossy by nature, which is why the validator uses it only where
// the declared type is float or double.
func (s *ScalarNode) AsFloat() (float64, bool) {
	if s == nil {
		return 0, false
	}

	switch s.kind {
	case IntScalar:
		return float64(s.num), true
	case FloatScalar:
		return s.flt, true
	case DecimalScalar:
		return s.dec.Float64(), true
	default:
		return 0, false
	}
}

// Value returns the value boxed into a plain Go value: nil, bool, int64,
// [Decimal], float64, or string. It is the per-scalar half of ToAny.
//
// A number the document wrote is boxed as its Decimal, so that a value passing
// through an untyped position — the payload of an Any, or a corner of a mapping
// no schema describes — is rendered from the same literal a typed one is. Only a
// computed float, which has no literal, comes back as a float64.
func (s *ScalarNode) Value() any {
	switch s.Kind() {
	case BoolScalar:
		return s.truth
	case IntScalar:
		return s.num
	case DecimalScalar:
		return s.dec
	case FloatScalar:
		return s.floatValue()
	case StringScalar:
		return s.str
	case NullScalar:
		return nil
	}

	return nil
}

// String renders the scalar for human consumption. Strings render as their own
// text, without quoting.
//
// This is a diagnostic, not the rendering a CWL value goes out through. A float
// is spelled the compact way Go spells a float64 — "1e+300", not three hundred
// digits — because an error message quoting a magnitude in full helps nobody.
// The rendering that must reproduce the document is [Decimal.String], reached
// through [ScalarNode.AsDecimal] or [ScalarNode.Value].
func (s *ScalarNode) String() string {
	switch s.Kind() {
	case NullScalar:
		return nameNull
	case BoolScalar:
		return strconv.FormatBool(s.truth)
	case IntScalar:
		return strconv.FormatInt(s.num, 10)
	case DecimalScalar:
		return s.dec.String()
	case FloatScalar:
		return strconv.FormatFloat(s.flt, 'g', -1, 64)
	case StringScalar:
		return s.str
	}

	return ""
}

// floatValue boxes a float, preferring the literal the document wrote over the
// float64 it rounded to.
func (s *ScalarNode) floatValue() any {
	if s.written {
		return s.dec
	}

	return s.flt
}

func (s *ScalarNode) isNode() {}
