package cwlexec

import (
	"context"
	"errors"
	"fmt"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Writing an ExpressionTool's literals down.
//
// An ExpressionTool may return a File carrying `contents` and no location, or a Directory carrying a
// `listing` and no location. Process.yml calls the first "a file literal" and says of the second that
// "If the `listing` field is not provided, the `location` field must be provided"; both describe
// bytes that do not exist anywhere yet. Every other producer of a File value in this engine names one
// that a filesystem already holds — a job order names an input, an output binding globs a tool's
// working directory — so this is the one place where the engine has to create the file it is talking
// about.
//
// It happens here, in the handler, rather than in staging or at output time, for two reasons. Staging
// is too late for a top-level output: an ExpressionTool's result may be the run's own output object,
// which nothing stages, and a caller handed a File with no location cannot open it. Output time is
// too late for a downstream step: the literal has to exist before a CommandLineTool's argv can name
// it, and by then the value has been through valueFrom, scatter and requirement scoping, any of which
// may have copied it. The handler is the one moment when the value is new, is owned by nobody else,
// and has an output directory of its own to be written into.
//
// The writing itself is [PathMap]'s, not this file's. A file literal reaching a CommandLineTool's
// input parameter is the same problem with the same answer, so the plan-then-apply machinery in
// pathmap.go and clt_staging.go is reused whole; what is here is only the walk that finds the
// literals, and the conversion from the object shape an expression produces into the typed values
// that machinery — and every other output object in this engine — is written in terms of.

// ErrLiteralTooLarge reports a file literal whose `contents` exceed the size the specification
// permits, which Process.yml puts at 64 kilobytes for File.contents.
var ErrLiteralTooLarge = errors.New("file literal contents are over the 64 KiB limit")

// expressionOutPrefix names the temporary directory an ExpressionTool falls back to when the
// scheduler allocated it no output directory of its own.
const expressionOutPrefix = "cwl-expr-"

// materializeExpressionOutputs writes down every file and directory literal an ExpressionTool's
// result carries, and returns the result — typed, with those values naming what was written.
//
// The typing is not incidental to the writing, it is the other half of the same job. An expression
// produces the object shape the specification defines for a File, while every output object this
// engine passes to a scheduler, a downstream step or a caller holds a [*cwlcore.File]; see
// [CollectOutputs], which a CommandLineTool's outputs reach the same way. Converting here is what
// puts an ExpressionTool's outputs on the same footing as every other process's.
func materializeExpressionOutputs(
	ctx context.Context, call *StepCall, object map[string]any,
) (map[string]any, error) {
	typed, err := expressionTypedValues(object)
	if err != nil {
		return nil, err
	}

	scan := &literalScan{roots: make([]cwlcore.FileOrDirectory, 0)}
	for _, value := range typed {
		scan.value(value)
	}

	if scan.err != nil {
		return nil, scan.err
	}

	if len(scan.roots) == 0 {
		return typed, nil
	}

	return writeExpressionLiterals(ctx, call, typed, scan.roots)
}

// writeExpressionLiterals creates the scanned literals under the invocation's output directory and
// relocates the output object onto them.
func writeExpressionLiterals(
	ctx context.Context, call *StepCall, typed map[string]any, roots []cwlcore.FileOrDirectory,
) (map[string]any, error) {
	err := ctx.Err()
	if err != nil {
		return nil, err
	}

	outdir, err := ensureDir(call.OutDir, expressionOutPrefix)
	if err != nil {
		return nil, err
	}

	// Both directories are the output directory. A literal an ExpressionTool returned is one of
	// its outputs, so unlike a literal staged for a tool to read it must survive the invocation
	// and must be somewhere an enclosing workflow will look for it.
	mapper := NewPathMap(outdir, outdir)

	for _, root := range roots {
		err = mapper.Materialize(root)
		if err != nil {
			return nil, err
		}
	}

	err = mapper.Apply()
	if err != nil {
		return nil, err
	}

	return mapper.RewriteInputs(typed), nil
}

// expressionTypedValues converts each port of an expression's result into the typed filesystem
// values the staging machinery works on.
//
// The conversion is per port rather than over the object as a whole because [cwlcore.FromExpressionValue]
// reads a `class` field as a discriminator, and an output port may perfectly well be named `class`.
func expressionTypedValues(object map[string]any) (map[string]any, error) {
	typed := make(map[string]any, len(object))

	for key, value := range object {
		converted, err := cwlcore.FromExpressionValue(value)
		if err != nil {
			return nil, fmt.Errorf("output %q: %w", key, err)
		}

		typed[key] = converted
	}

	return typed, nil
}

// literalScan collects the literals in an output object: the outermost ones, which are what gets
// materialized, while measuring every literal File it passes on the way down.
//
// Only the outermost are collected because [PathMap.Materialize] plans a whole subtree — a File's
// secondary files beside it, a Directory literal's listing inside it — and collecting a nested one as
// well would plan it twice, the second time somewhere else.
type literalScan struct {
	// err is the first literal that could not be used, or nil.
	err error

	// roots are the outermost literals, in the order they were found.
	roots []cwlcore.FileOrDirectory
}

// value walks one output port's value, descending through the record and array shapes an expression
// can nest a filesystem value inside. Nothing it reaches this way is held by a literal, so every
// literal it finds is a root.
func (s *literalScan) value(value any) {
	switch typed := value.(type) {
	case *cwlcore.File:
		s.root(typed)
	case *cwlcore.Directory:
		s.root(typed)
	case []any:
		for _, item := range typed {
			s.value(item)
		}
	case map[string]any:
		for _, item := range typed {
			s.value(item)
		}
	default:
	}
}

// root records an outermost literal and walks what it holds.
func (s *literalScan) root(value cwlcore.FileOrDirectory) {
	if !isLiteral(value) {
		return
	}

	s.roots = append(s.roots, value)
	s.walk(value)
}

// walk descends into a literal's members: a File's secondary files, a Directory's listing.
//
// A member that is not itself a literal is left entirely alone, its own members included. Its bytes
// are on a filesystem already, and so is everything its fields name.
func (s *literalScan) walk(value cwlcore.FileOrDirectory) {
	switch typed := value.(type) {
	case *cwlcore.File:
		s.walkFile(typed)
	case *cwlcore.Directory:
		s.walkDirectory(typed)
	default:
	}
}

// walkFile measures a File literal and descends into its secondary files.
func (s *literalScan) walkFile(file *cwlcore.File) {
	if !isFileLiteral(file) {
		return
	}

	s.measure(file)

	for _, secondary := range file.SecondaryFiles {
		s.walk(secondary)
	}
}

// walkDirectory descends into a Directory literal's listing.
func (s *literalScan) walkDirectory(dir *cwlcore.Directory) {
	if !isDirectoryLiteral(dir) {
		return
	}

	for _, entry := range dir.Listing {
		s.walk(entry)
	}
}

// measure fills in a literal's size and checksum from the bytes it carries, and rejects one the
// specification does not allow to exist.
//
// The measurement happens before the file is written rather than after, because it is the same
// answer either way and doing it here keeps the file's size and checksum agreeing with `contents`
// even when the value is one this engine never gets to write down.
func (s *literalScan) measure(file *cwlcore.File) {
	if s.err != nil {
		return
	}

	size := len(file.Contents.Value())
	if size > joMaxContentsBytes {
		s.err = fmt.Errorf("%w: %q carries %d bytes, over the %d byte limit",
			ErrLiteralTooLarge, file.Basename, size, joMaxContentsBytes)

		return
	}

	outMeasureLiteral(file)
}

// isLiteral reports whether a filesystem value is one this engine must create.
func isLiteral(value cwlcore.FileOrDirectory) bool {
	if file, ok := value.(*cwlcore.File); ok {
		return isFileLiteral(file)
	}

	dir, ok := value.(*cwlcore.Directory)

	return ok && isDirectoryLiteral(dir)
}

// isFileLiteral reports whether a File is one this engine must create.
//
// Process.yml, File: "If no `location` or `path` is specified, a file object must specify `contents`
// with the UTF-8 text content of the file. This is a 'file literal'." The empty string is a literal
// like any other — an empty file — which is why File.contents is an OptString and why the test is
// whether it was set rather than whether it holds anything.
func isFileLiteral(file *cwlcore.File) bool {
	return file != nil && file.Location == "" && file.Path == "" && file.Contents.IsSet()
}

// isDirectoryLiteral reports whether a Directory is one this engine must create.
//
// Process.yml, Directory.listing: "If the `listing` field is not provided, the `location` field must
// be provided", so a Directory naming nowhere and carrying a listing is the directory equivalent of a
// file literal. An empty listing is a directory literal with nothing in it, and a nil one is a
// directory whose listing nobody has read; the distinction is why the test is against nil rather than
// against length.
func isDirectoryLiteral(dir *cwlcore.Directory) bool {
	return dir != nil && dir.Location == "" && dir.Path == "" && dir.Listing != nil
}
