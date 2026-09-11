package cwlexec

import (
	"context"
	"errors"
	"fmt"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Materializes file/directory literals from ExpressionTool results using [PathMap].

// ErrLiteralTooLarge reports a file literal whose contents exceed the 64 KiB limit.
var ErrLiteralTooLarge = errors.New("file literal contents are over the 64 KiB limit")

// expressionOutPrefix is the temp directory prefix for ExpressionTool output.
const expressionOutPrefix = "cwl-expr-"

// materializeExpressionOutputs writes file/directory literals to disk and returns the typed result.
func materializeExpressionOutputs(
	ctx context.Context, call *StepCall, object map[string]any,
) (map[string]any, error) {
	typed, err := expressionTypedValues(object)
	if err != nil {
		return nil, err
	}

	scan := &literalScan{err: nil, roots: make([]cwlcore.FileOrDirectory, 0)}
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

// writeExpressionLiterals stages scanned literals to the output directory and rewrites paths.
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

	mapper := NewPathMap(outdir, outdir)

	for _, root := range roots {
		err = mapper.Materialize(root)
		if err != nil {
			return nil, err
		}
	}

	outFS := NewLocalDirFS(outdir)

	err = mapper.Apply(outFS, outFS)
	if err != nil {
		return nil, err
	}

	return mapper.RewriteInputs(typed), nil
}

// expressionTypedValues converts each output port value via [cwlcore.FromExpressionValue].
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

// literalScan collects the outermost file/directory literals in an output object.
type literalScan struct {
	err   error
	roots []cwlcore.FileOrDirectory
}

// value walks one output port's value, collecting outermost literals.
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

// root records a literal root and descends into its members.
func (s *literalScan) root(value cwlcore.FileOrDirectory) {
	if !isLiteral(value) {
		return
	}

	s.roots = append(s.roots, value)
	s.walk(value)
}

// walk descends into a literal's secondary files or directory listing.
func (s *literalScan) walk(value cwlcore.FileOrDirectory) {
	switch typed := value.(type) {
	case *cwlcore.File:
		s.walkFile(typed)
	case *cwlcore.Directory:
		s.walkDirectory(typed)
	default:
	}
}

// walkFile measures a file literal and descends into secondary files.
func (s *literalScan) walkFile(file *cwlcore.File) {
	if !isFileLiteral(file) {
		return
	}

	s.measure(file)

	for _, secondary := range file.SecondaryFiles {
		s.walk(secondary)
	}
}

// walkDirectory descends into a directory literal's listing.
func (s *literalScan) walkDirectory(dir *cwlcore.Directory) {
	if !isDirectoryLiteral(dir) {
		return
	}

	for _, entry := range dir.Listing {
		s.walk(entry)
	}
}

// measure validates the literal size limit and fills in size/checksum.
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

// isLiteral reports whether a File or Directory has no location and must be materialized.
func isLiteral(value cwlcore.FileOrDirectory) bool {
	if file, ok := value.(*cwlcore.File); ok {
		return isFileLiteral(file)
	}

	dir, ok := value.(*cwlcore.Directory)

	return ok && isDirectoryLiteral(dir)
}

// isFileLiteral reports whether a File has no location/path and has contents set.
func isFileLiteral(file *cwlcore.File) bool {
	return file != nil && file.Location == "" && file.Path == "" && file.Contents.IsSet()
}

// isDirectoryLiteral reports whether a Directory has no location/path and has a non-nil listing.
func isDirectoryLiteral(dir *cwlcore.Directory) bool {
	return dir != nil && dir.Location == "" && dir.Path == "" && dir.Listing != nil
}
