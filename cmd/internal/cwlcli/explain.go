package cwlcli

import (
	"errors"
	"strings"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// Explain renders err as a human-readable tree via [salad.Error.Pretty], or as a plain message.
func Explain(err error) string {
	if err == nil {
		return ""
	}

	if serr, ok := errors.AsType[*salad.Error](err); ok {
		return serr.Pretty()
	}

	return err.Error()
}

// LimitLines returns at most limit lines of s, and the number omitted. Zero limit keeps all.
func LimitLines(s string, limit int) (_ string, _ int) {
	if limit <= 0 || s == "" {
		return s, 0
	}

	lines := strings.Split(s, "\n")
	if len(lines) <= limit {
		return s, 0
	}

	return strings.Join(lines[:limit], "\n"), len(lines) - limit
}

// Indent prefixes every non-blank line of s with prefix.
func Indent(s, prefix string) string {
	if s == "" {
		return ""
	}

	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}

		lines[i] = prefix + line
	}

	return strings.Join(lines, "\n")
}
