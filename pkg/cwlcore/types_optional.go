package cwlcore

import "strconv"

// The Opt wrappers: OptBool, OptInt and OptString.
//
// They are the smallest members of the union family described in
// types_value.go, and they exist for one reason, which is worth stating plainly
// because it is easy to get wrong: a CWL field gets an Opt wrapper only where
// the Go zero value is itself a legal value the document could have written, so
// that "absent" and "zero" would otherwise be indistinguishable.
//
// There are exactly three such places in CWL v1.2:
//
//   - CommandLineBinding.separate and CommandLineBinding.shellQuote, whose
//     schema default is true — a plain bool would read an absent field as
//     false and silently invert the default.
//   - File.size, where 0 is an empty file, not an uncomputed size.
//   - File.contents, where "" is an empty file literal, not an unread file.
//
// Everywhere else a plain bool, int64 or string is used and its zero value
// means absent: an optional boolean whose default is false, a location, a
// basename and a checksum all have no legal zero value to be confused with.
//
// Each wrapper has the same four-method shape: IsSet, a typed reader, Or to
// apply a default in one step, and String for diagnostics.

// OptBool is an optional boolean: absent, true, or false, with absent as the
// zero value. It carries CommandLineBinding's separate and shellQuote, whose
// schema default is true.
//
// Read it with Or, which supplies that default in one step:
//
//	if binding.Separate.Or(true) { /* ... */ }
type OptBool struct {
	value bool
	set   bool
}

// NewOptBool returns an OptBool holding v, marked as present.
func NewOptBool(v bool) OptBool {
	return OptBool{value: v, set: true}
}

// IsSet reports whether the document declared this field.
func (b OptBool) IsSet() bool {
	return b.set
}

// Bool returns the declared value, or false if the field was absent.
func (b OptBool) Bool() bool {
	return b.value
}

// Or returns the declared value, or def if the field was absent. def is the
// field's schema default.
func (b OptBool) Or(def bool) bool {
	if !b.set {
		return def
	}

	return b.value
}

// String renders the OptBool as "true", "false", or "unset".
func (b OptBool) String() string {
	if !b.set {
		return ValueUnset.String()
	}

	return strconv.FormatBool(b.value)
}

// OptInt is an optional 64-bit integer, with absent as the zero value. It
// carries File.Size, where 0 is a perfectly ordinary value — an empty file —
// and must not be confused with a size the implementation has not computed.
type OptInt struct {
	value int64
	set   bool
}

// NewOptInt returns an OptInt holding v, marked as present.
func NewOptInt(v int64) OptInt {
	return OptInt{value: v, set: true}
}

// IsSet reports whether the document supplied this field.
func (i OptInt) IsSet() bool {
	return i.set
}

// Int returns the value, or 0 if the field was absent.
func (i OptInt) Int() int64 {
	return i.value
}

// Or returns the value, or def if the field was absent.
func (i OptInt) Or(def int64) int64 {
	if !i.set {
		return def
	}

	return i.value
}

// String renders the OptInt as its decimal value, or "unset".
func (i OptInt) String() string {
	if !i.set {
		return ValueUnset.String()
	}

	return strconv.FormatInt(i.value, 10)
}

// OptString is an optional string, with absent as the zero value. It carries
// File.Contents, where the empty string is a legal value: a File whose contents
// are "" is a file literal that must be created on disk empty, which is a
// different thing from a File whose contents were never loaded.
type OptString struct {
	value string
	set   bool
}

// NewOptString returns an OptString holding v, marked as present.
func NewOptString(v string) OptString {
	return OptString{value: v, set: true}
}

// IsSet reports whether the document supplied this field.
func (s OptString) IsSet() bool {
	return s.set
}

// Value returns the string, or "" if the field was absent.
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

// String renders the OptString as its value, or "unset" when absent. Use Value
// to read the string itself, which does not disguise an absent field.
func (s OptString) String() string {
	if !s.set {
		return ValueUnset.String()
	}

	return s.value
}
