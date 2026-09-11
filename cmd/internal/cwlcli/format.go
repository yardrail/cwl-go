package cwlcli

import (
	"errors"
	"fmt"
	"io"
)

// Format is an output encoding. Implements [flag.Value].
type Format string

// The output encodings every cwl-go tool understands.
const (
	FormatJSON Format = "json"
	FormatText Format = "text"
)

// ErrFormat is the error [Format.Set] reports for an unknown encoding.
var ErrFormat = errors.New("unknown output format")

// String returns the format's flag spelling, satisfying [flag.Value].
func (f *Format) String() string {
	if f == nil || *f == "" {
		return string(FormatJSON)
	}

	return string(*f)
}

// Set parses the format's flag spelling, satisfying [flag.Value].
func (f *Format) Set(s string) error {
	switch Format(s) {
	case FormatJSON, FormatText:
		*f = Format(s)

		return nil
	default:
		return fmt.Errorf("%w %q: expected %s or %s", ErrFormat, s, FormatJSON, FormatText)
	}
}

// Render writes v to w in the format's encoding, followed by a newline.
func (f *Format) Render(w io.Writer, v any) error {
	if Format(f.String()) == FormatText {
		fmt.Fprintln(w, Text(v))

		return nil
	}

	encoded, err := JSON(v)
	if err != nil {
		return err
	}

	fmt.Fprintln(w, string(encoded))

	return nil
}

// Formats lists the accepted format spellings, for a usage message.
func Formats() string {
	return string(FormatJSON) + "|" + string(FormatText)
}
