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
	// DecimalScalar is an integer too large for int64, held as an exact [Decimal].
	DecimalScalar
)

// scalarKindNames maps each ScalarKind to its error-message name.
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

// ScalarNode is an immutable leaf value: null, bool, int, float, or string.
// Nil receivers are treated as null. Numeric nodes may carry their original literal.
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

// NewNumberNode builds a scalar from a numeric literal, preserving the original text.
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

// NewFloatNode builds a computed float scalar (no literal). Use [NewNumberNode] for document literals.
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

// AsBool returns the boolean value, or false if not a boolean.
func (s *ScalarNode) AsBool() bool {
	return s != nil && s.kind == BoolScalar && s.truth
}

// AsInt returns the int64 value, and whether this scalar is an IntScalar.
func (s *ScalarNode) AsInt() (int64, bool) {
	if s == nil || s.kind != IntScalar {
		return 0, false
	}

	return s.num, true
}

// AsDecimal returns the original [Decimal] literal, or false for computed values.
func (s *ScalarNode) AsDecimal() (Decimal, bool) {
	if s == nil || !s.written {
		return Decimal{digits: "", exp: 0, neg: false, floatForm: false}, false
	}

	return s.dec, true
}

// AsFloat returns the float64 value for any numeric scalar.
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

// Value returns the value as a plain Go value (nil, bool, int64, Decimal, float64, or string).
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

// String renders the scalar for diagnostics. Not for document reproduction; use [Decimal.String].
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

// floatValue boxes a float, preferring the original literal.
func (s *ScalarNode) floatValue() any {
	if s.written {
		return s.dec
	}

	return s.flt
}

func (s *ScalarNode) isNode() {}
