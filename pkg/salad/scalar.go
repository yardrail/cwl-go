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
)

// scalarKindNames maps each ScalarKind to the name used in error messages.
var scalarKindNames = [...]string{
	NullScalar:   nameNull,
	BoolScalar:   nameBoolean,
	IntScalar:    nameInt,
	FloatScalar:  nameFloat,
	StringScalar: nameString,
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
type ScalarNode struct {
	str   string
	loc   SourceLine
	kind  ScalarKind
	num   int64
	flt   float64
	truth bool
}

var _ Node = (*ScalarNode)(nil)

// NewNullNode builds the null scalar located at loc.
func NewNullNode(loc SourceLine) *ScalarNode {
	return &ScalarNode{loc: loc, kind: NullScalar}
}

// NewBoolNode builds a boolean scalar located at loc.
func NewBoolNode(loc SourceLine, v bool) *ScalarNode {
	return &ScalarNode{loc: loc, kind: BoolScalar, truth: v}
}

// NewIntNode builds an integer scalar located at loc.
func NewIntNode(loc SourceLine, v int64) *ScalarNode {
	return &ScalarNode{loc: loc, kind: IntScalar, num: v}
}

// NewFloatNode builds a floating-point scalar located at loc.
func NewFloatNode(loc SourceLine, v float64) *ScalarNode {
	return &ScalarNode{loc: loc, kind: FloatScalar, flt: v}
}

// NewStringNode builds a string scalar located at loc.
func NewStringNode(loc SourceLine, v string) *ScalarNode {
	return &ScalarNode{loc: loc, kind: StringScalar, str: v}
}

// Loc reports where in the source document this scalar came from.
func (s *ScalarNode) Loc() SourceLine {
	if s == nil {
		return SourceLine{}
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
func (s *ScalarNode) AsInt() (int64, bool) {
	if s == nil || s.kind != IntScalar {
		return 0, false
	}

	return s.num, true
}

// AsFloat returns the value as a float64, and whether this scalar is numeric.
// Integer scalars convert, so AsFloat succeeds for both IntScalar and FloatScalar.
func (s *ScalarNode) AsFloat() (float64, bool) {
	if s == nil {
		return 0, false
	}

	if s.kind == IntScalar {
		return float64(s.num), true
	}

	if s.kind == FloatScalar {
		return s.flt, true
	}

	return 0, false
}

// Value returns the value boxed into a plain Go value: nil, bool, int64,
// float64, or string. It is the per-scalar half of ToAny.
func (s *ScalarNode) Value() any {
	switch s.Kind() {
	case BoolScalar:
		return s.truth
	case IntScalar:
		return s.num
	case FloatScalar:
		return s.flt
	case StringScalar:
		return s.str
	case NullScalar:
		return nil
	}

	return nil
}

// String renders the scalar for human consumption. Strings render as their own
// text, without quoting.
func (s *ScalarNode) String() string {
	switch s.Kind() {
	case NullScalar:
		return nameNull
	case BoolScalar:
		return strconv.FormatBool(s.truth)
	case IntScalar:
		return strconv.FormatInt(s.num, 10)
	case FloatScalar:
		return strconv.FormatFloat(s.flt, 'g', -1, 64)
	case StringScalar:
		return s.str
	}

	return ""
}

func (s *ScalarNode) isNode() {}
