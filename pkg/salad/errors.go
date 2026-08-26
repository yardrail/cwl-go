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

// SourceLine identifies the region of a source document that a node or an error
// originated from. It is the analogue of schema-salad's sourceline.SourceLine.
//
// The zero SourceLine means "location unknown"; every accessor tolerates it.
type SourceLine struct {
	// File is the normalized URL or path the document was loaded from; "" when unknown.
	File string
	// Start is the first position covered by this region.
	Start Position
	// End is the last position covered by this region. It equals Start when the
	// parser does not report a distinct end.
	End Position
}

// IsZero reports whether s carries no location information at all.
func (s SourceLine) IsZero() bool {
	return s.File == "" && s.Start.IsZero()
}

// String renders s as a "file:line:col" prefix suitable for error messages.
//
// It degrades gracefully: a SourceLine with no line information renders as just
// the file, one with no file renders as "line:col", and the zero value renders
// as the empty string.
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

// Error is one node in a tree of loading and validation errors, mirroring
// schema-salad's SchemaSaladException. Every error this package returns is a
// *Error.
//
// The tree shape is load-bearing: validating a union records one child per
// member so the reader can see why every alternative was rejected, and
// validating a record nests one child per offending field. Use Pretty to render
// the whole tree, Leaves to get just the tip errors, and [errors.As] to recover
// a *Error from an error value returned by this package.
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
	return &Error{Msg: fmt.Sprintf(format, a...), Loc: loc}
}

// Warnf builds a leaf Error at loc, flagged as a warning.
func Warnf(loc SourceLine, format string, a ...any) *Error {
	return &Error{Msg: fmt.Sprintf(format, a...), Loc: loc, Warning: true}
}

// Group builds an Error whose message provides context for a set of child
// errors. Nil children are dropped.
func Group(loc SourceLine, msg string, children ...*Error) *Error {
	kept := make([]*Error, 0, len(children))
	for _, c := range children {
		if c != nil {
			kept = append(kept, c)
		}
	}

	return &Error{Msg: msg, Loc: loc, Children: kept}
}

// Error returns a one-line summary of the error: the "file:line:col" prefix, if
// known, followed by the message.
//
// A node with no message of its own reports the first of its leaf errors, so
// that a grouping node still produces something useful when printed with %v.
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

// Unwrap returns the child errors, so that [errors.Is] and [errors.As] traverse
// the whole tree. It returns nil when this node has no children.
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

// Leaves returns the tip errors of the tree in depth-first order: the nodes that
// have no children and carry a message. It mirrors SchemaSaladException.leaves.
func (e *Error) Leaves() []*Error {
	if e == nil {
		return make([]*Error, 0)
	}

	return e.appendLeaves(make([]*Error, 0, len(e.Children)+1))
}

// Pretty renders the error tree as an indented, multi-line string, one error per
// line, two spaces of indent per level. Grouping nodes with no message of their
// own are elided and their children are rendered at the parent's level.
//
// It mirrors SchemaSaladException.pretty_str and is the rendering intended for
// end users. The result has no trailing newline.
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
