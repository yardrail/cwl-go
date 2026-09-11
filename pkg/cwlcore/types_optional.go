package cwlcore

import "strconv"

// Opt wrappers for fields where Go's zero value collides with a legal CWL value.

// OptBool is an optional boolean where absent != false.
type OptBool struct {
	value bool
	set   bool
}

// NewOptBool wraps v as present.
func NewOptBool(v bool) OptBool {
	return OptBool{value: v, set: true}
}

// IsSet reports whether the field was declared.
func (b OptBool) IsSet() bool {
	return b.set
}

// Bool returns the value, or false if absent.
func (b OptBool) Bool() bool {
	return b.value
}

// Or returns the value, or def if absent.
func (b OptBool) Or(def bool) bool {
	if !b.set {
		return def
	}

	return b.value
}

// String renders for diagnostics.
func (b OptBool) String() string {
	if !b.set {
		return ValueUnset.String()
	}

	return strconv.FormatBool(b.value)
}

// OptInt is an optional int64 where absent != 0.
type OptInt struct {
	value int64
	set   bool
}

// NewOptInt wraps v as present.
func NewOptInt(v int64) OptInt {
	return OptInt{value: v, set: true}
}

// IsSet reports whether the field was declared.
func (i OptInt) IsSet() bool {
	return i.set
}

// Int returns the value, or 0 if absent.
func (i OptInt) Int() int64 {
	return i.value
}

// Or returns the value, or def if absent.
func (i OptInt) Or(def int64) int64 {
	if !i.set {
		return def
	}

	return i.value
}

// String renders for diagnostics.
func (i OptInt) String() string {
	if !i.set {
		return ValueUnset.String()
	}

	return strconv.FormatInt(i.value, 10)
}

// OptString is an optional string where absent != "".
type OptString struct {
	value string
	set   bool
}

// NewOptString wraps v as present.
func NewOptString(v string) OptString {
	return OptString{value: v, set: true}
}

// IsSet reports whether the field was declared.
func (s OptString) IsSet() bool {
	return s.set
}

// Value returns the string, or "" if absent.
func (s OptString) Value() string {
	return s.value
}

// Or returns the string, or def if the field was absent.
func (s OptString) Or(def string) string {
	if !s.set {
		return def
	}

	return s.value
}

// String renders for diagnostics.
func (s OptString) String() string {
	if !s.set {
		return ValueUnset.String()
	}

	return s.value
}
