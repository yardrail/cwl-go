package salad

import (
	"fmt"
	"strings"
)

// prettyIndent is the per-level indent used by Error.Pretty.
const prettyIndent = "  "

// warningPrefix marks an Error that is advisory rather than fatal.
const warningPrefix = "warning: "

// emptyErrorMsg is what a completely empty error tree renders as.
const emptyErrorMsg = "salad: error with no message"

// Position is a single point inside a source document.
//
// The zero Position means "unknown".
type Position struct {
	// Line is the 1-based line number, or 0 when unknown.
	Line int
	// Column is the 1-based column number, or 0 when unknown.
	Column int
	// Offset is the 0-based byte offset from the start of the document, or 0 when unknown.
	Offset int
}

// IsZero reports whether p carries no line/column information.
func (p Position) IsZero() bool {
	return p.Line == 0 && p.Column == 0
}

// SourceLine identifies a region in a source document. The zero value means unknown.
type SourceLine struct {
	// File is the normalized URL or path the document was loaded from; "" when unknown.
	File string
	// Start is the first position covered by this region.
	Start Position
	// End is the last position covered by this region.
	End Position
}

// IsZero reports whether s carries no location information at all.
func (s SourceLine) IsZero() bool {
	return s.File == "" && s.Start.IsZero()
}

// String renders s as a "file:line:col" prefix for error messages.
func (s SourceLine) String() string {
	switch {
	case s.IsZero():
		return ""
	case s.Start.IsZero():
		return s.File
	case s.File == "":
		return fmt.Sprintf("%d:%d", s.Start.Line, s.Start.Column)
	default:
		return fmt.Sprintf("%s:%d:%d", s.File, s.Start.Line, s.Start.Column)
	}
}

// Error is a tree node of loading/validation errors. Every error from this package is a *Error.
type Error struct {
	// Msg is this node's message. It may be empty, in which case the node exists
	// only to group Children and is elided from rendered output.
	Msg string
	// Children are the nested contextual errors that explain Msg.
	Children []*Error
	// Loc is where the offending value came from; the zero value if unknown.
	Loc SourceLine
	// Warning marks an advisory error that does not by itself make a document invalid.
	Warning bool
}

// Errorf builds a leaf Error at loc with a printf-formatted message.
func Errorf(loc SourceLine, format string, a ...any) *Error {
	return &Error{Msg: fmt.Sprintf(format, a...), Children: nil, Loc: loc, Warning: false}
}

// Warnf builds a leaf Error at loc, flagged as a warning.
func Warnf(loc SourceLine, format string, a ...any) *Error {
	return &Error{Msg: fmt.Sprintf(format, a...), Children: nil, Loc: loc, Warning: true}
}

// Group builds an Error grouping child errors under a context message.
func Group(loc SourceLine, msg string, children ...*Error) *Error {
	kept := make([]*Error, 0, len(children))
	for _, c := range children {
		if c != nil {
			kept = append(kept, c)
		}
	}

	return &Error{Msg: msg, Children: kept, Loc: loc, Warning: false}
}

// Error returns a one-line summary. Grouping nodes report their first leaf.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}

	if e.Msg != "" {
		return e.summary()
	}

	leaves := e.Leaves()
	if len(leaves) == 0 {
		return emptyErrorMsg
	}

	return leaves[0].Error()
}

// Unwrap returns child errors for [errors.Is]/[errors.As] traversal.
func (e *Error) Unwrap() []error {
	if e == nil || len(e.Children) == 0 {
		return nil
	}

	out := make([]error, 0, len(e.Children))
	for _, c := range e.Children {
		out = append(out, c)
	}

	return out
}

// Leaves returns the leaf errors of the tree in depth-first order.
func (e *Error) Leaves() []*Error {
	if e == nil {
		return make([]*Error, 0)
	}

	return e.appendLeaves(make([]*Error, 0, len(e.Children)+1))
}

// Pretty renders the error tree as an indented multi-line string for end users.
func (e *Error) Pretty() string {
	if e == nil {
		return ""
	}

	return strings.Join(e.appendLines(make([]string, 0, len(e.Children)+1), 0), "\n")
}

// summary renders this node alone, without its children.
func (e *Error) summary() string {
	msg := e.Msg
	if e.Warning {
		msg = warningPrefix + msg
	}

	if prefix := e.Loc.String(); prefix != "" {
		return prefix + ": " + msg
	}

	return msg
}

// appendLeaves appends this subtree's tip errors to dst and returns the result.
func (e *Error) appendLeaves(dst []*Error) []*Error {
	if len(e.Children) == 0 {
		if e.Msg == "" {
			return dst
		}

		return append(dst, e)
	}

	for _, c := range e.Children {
		if c != nil {
			dst = c.appendLeaves(dst)
		}
	}

	return dst
}

// appendLines appends this subtree's rendered lines to dst, indented by level.
func (e *Error) appendLines(dst []string, level int) []string {
	next := level
	if e.Msg != "" {
		dst = append(dst, strings.Repeat(prettyIndent, level)+e.summary())
		next = level + 1
	}

	for _, c := range e.Children {
		if c != nil {
			dst = c.appendLines(dst, next)
		}
	}

	return dst
}
