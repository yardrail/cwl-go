package cwlcli

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// jsonIndent is one level of indentation in rendered JSON.
const jsonIndent = "  "

// The initial buffer sizes for encoding. They only save early regrowth on the
// two shapes that dominate a dump — a whole document, and one small object —
// so being roughly right is the whole requirement.
const (
	documentBufferSize = 1024
	objectBufferSize   = 256
)

// JSON renders v as indented JSON.
//
// Every value that is not one of this package's own ordered shapes is rendered
// by [cwlcore.EncodeJSON], which is the project's single definition of how a
// CWL value becomes JSON text. That matters most for numbers. A number a
// document wrote travels as a [salad.Decimal] carrying its own literal, and
// cwlcore writes that literal back — which is what makes a forty-three-digit
// integer come out as forty-three digits and a float written 1.23e-05 come out
// as 0.0000123, both of which the standard encoder loses to a float64 long
// before it formats anything. For the computed numbers that have no literal the
// two still disagree: the standard encoder switches to exponent notation at
// 1e21, cwlcore at 1e16, and they differ below 1e-4 and on whether a whole float
// keeps its ".0". A second encoder holding a second copy of those rules is how
// they came to disagree in the first place, so there is only the one.
//
// Two things survive that delegation. An [Object] keeps its insertion order
// rather than being sorted, because a dump is written in the order a reader
// wants to see; a Go map reaching the encoder is still sorted by key, since
// there is no author's order to keep. And a value outside the JSON type system
// — a channel, a function, a NaN — is an error rather than something rendered
// approximately, which is what [json.Indent] alone would not catch.
func JSON(v any) ([]byte, error) {
	compact, err := appendJSON(make([]byte, 0, documentBufferSize), v)
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer

	// Indenting also normalizes whitespace, which is what lets cwlcore's
	// json.dumps layout — ", " between entries, ": " after a key — and this
	// package's indented layout be the same encoder.
	err = json.Indent(&out, compact, "", jsonIndent)
	if err != nil {
		return nil, err
	}

	return out.Bytes(), nil
}

// MarshalJSON renders the object in insertion order, satisfying
// [json.Marshaler] so that an Object nested inside a value some other encoder
// walks still comes out ordered.
func (o *Object) MarshalJSON() ([]byte, error) {
	return appendJSONObject(make([]byte, 0, objectBufferSize), o)
}

// appendJSON appends the compact JSON encoding of v to dst.
func appendJSON(dst []byte, v any) ([]byte, error) {
	switch t := v.(type) {
	case *Object:
		return appendJSONObject(dst, t)
	case []any:
		return appendJSONSlice(dst, t)
	default:
		return appendJSONLeaf(dst, v)
	}
}

// appendJSONObject appends an object's entries in insertion order.
func appendJSONObject(dst []byte, o *Object) ([]byte, error) {
	dst = append(dst, '{')

	for i, entry := range o.Entries() {
		if i > 0 {
			dst = append(dst, ',')
		}

		// A key is a string, so it is always renderable and needs no
		// check of its own.
		dst = append(dst, cwlcore.EncodeJSON(entry.Key)...)
		dst = append(dst, ':')

		var err error

		dst, err = appendJSON(dst, entry.Value)
		if err != nil {
			return nil, err
		}
	}

	return append(dst, '}'), nil
}

// appendJSONSlice appends a slice's items in order.
func appendJSONSlice(dst []byte, items []any) ([]byte, error) {
	dst = append(dst, '[')

	for i, item := range items {
		if i > 0 {
			dst = append(dst, ',')
		}

		var err error

		dst, err = appendJSON(dst, item)
		if err != nil {
			return nil, err
		}
	}

	return append(dst, ']'), nil
}

// appendJSONLeaf appends anything that is not one of this package's own
// ordered shapes: a scalar, a Go map, or a slice of some concrete type.
//
// The standard encoder is run first and its output thrown away. It is here as
// the oracle for one question only — is this value in the JSON type system —
// because it is the one with an error to report, and because [cwlcore.EncodeJSON]
// answers no by rendering the value's Go string form rather than by failing.
// Silently writing a channel's address into an output object that a
// conformance run compares would be far worse than refusing to write anything.
func appendJSONLeaf(dst []byte, v any) ([]byte, error) {
	err := json.NewEncoder(io.Discard).Encode(v)
	if err != nil {
		return nil, err
	}

	return append(dst, cwlcore.EncodeJSON(v)...), nil
}
