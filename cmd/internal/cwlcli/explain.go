package cwlcli

import (
	"errors"
	"strings"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// Explain renders err the way a person reading a bug report needs to see it.
//
// Every error pkg/salad and pkg/cwlcore return is a [salad.Error]: a tree whose
// nodes carry accurate source lines, with one child per union member that was
// tried and one per offending field. Flattening that to a single line is what
// makes validators unusable, so Explain keeps the tree — [salad.Error.Pretty]
// renders it indented, innermost detail deepest — and falls back to the plain
// message only for an error that is not one.
//
// A nil error renders as the empty string.
func Explain(err error) string {
	if err == nil {
		return ""
	}

	var serr *salad.Error
	if errors.As(err, &serr) {
		return serr.Pretty()
	}

	return err.Error()
}

// LimitLines returns at most limit lines of s, and how many lines it left out.
//
// It exists because a [salad.Error] tree is not bounded in size: validating a
// mapping against an abstract type with twenty concrete subtypes records why
// each of the twenty was rejected, and the honest full rendering of that runs
// to hundreds of lines. The first candidate tried is almost always the one the
// author meant, and it comes first, so keeping the head of the tree and saying
// how much was dropped beats both truncating silently and burying the answer.
//
// A limit of zero or less keeps everything.
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

// Indent prefixes every line of s with prefix, leaving blank lines alone.
//
// It is what nests a rendered error tree under the heading naming the document
// it came from, so that a run over several documents reads as several blocks
// rather than one wall of lines.
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
