package cwlcli

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// jsonIndent is one level of indentation in rendered JSON.
const jsonIndent = "  "

// Initial buffer sizes for encoding.
const (
	documentBufferSize = 1024
	objectBufferSize   = 256
)

// JSON renders v as indented JSON, using [cwlcore.EncodeJSON] for leaf values.
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

// MarshalJSON renders the object in insertion order.
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

// appendJSONLeaf appends a scalar or Go map/slice via [cwlcore.EncodeJSON], after validating with the standard encoder.
func appendJSONLeaf(dst []byte, v any) ([]byte, error) {
	err := json.NewEncoder(io.Discard).Encode(v)
	if err != nil {
		return nil, err
	}

	return append(dst, cwlcore.EncodeJSON(v)...), nil
}
