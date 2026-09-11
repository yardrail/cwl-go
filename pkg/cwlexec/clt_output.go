package cwlexec

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Collecting a CommandLineTool's outputs from a finished process's output directory.

// Errors reported while collecting outputs. Use [errors.Is] to test.
var (
	// ErrOutputDir reports a non-absolute output directory.
	ErrOutputDir = errors.New("output directory must be an absolute path")
	// ErrOutputMissing reports a required output whose glob matched nothing.
	ErrOutputMissing = errors.New("output glob matched no files")
	// ErrOutputMultiple reports multiple matches for a single-valued output.
	ErrOutputMultiple = errors.New("output glob matched more than one path for a single value")
	// ErrStreamFile reports an invalid captured stream filename.
	ErrStreamFile = errors.New("captured stream filename is not a path inside the output directory")
)

// Stream identifies one of the two standard streams a CommandLineTool can capture to a file.
type Stream string

const (
	// StreamStdout captures the tool's standard output.
	StreamStdout Stream = "stdout"
	// StreamStderr captures the tool's standard error.
	StreamStderr Stream = "stderr"
)

// CollectOutputs gathers outputs from outdir, returning the output object keyed by short name.
// outdir must be absolute. Does not classify exit codes; see [ClassifyExit].
func CollectOutputs(tool *cwlcore.CommandLineTool, outdir string, outfs WriteFS, exitCode int,
	inputs map[string]any, eval *cwlcore.Evaluator, rt cwlcore.RuntimeContext,
) (map[string]any, error) {
	if !filepath.IsAbs(outdir) {
		return nil, fmt.Errorf("%w: %q", ErrOutputDir, outdir)
	}

	collector := newOutputCollector(tool, outdir, outfs, inputs,
		withEvaluator(eval), withRuntime(rt), withExitCode(exitCode))

	outputs := make(map[string]any, len(tool.Outputs))

	for index := range tool.Outputs {
		param := &tool.Outputs[index]
		name := ShortName(param.ID())

		value, err := collector.parameter(param)
		if err != nil {
			return nil, fmt.Errorf("collecting output %q: %w", name, err)
		}

		outputs[name] = value
	}

	return outputs, nil
}

// ClassifyExit maps an exit code to a Status per the tool's success/failure code lists.
// Unclassified non-zero codes are permanent failures.
func ClassifyExit(tool *cwlcore.CommandLineTool, exitCode int) Status {
	switch {
	case slices.Contains(tool.SuccessCodes, exitCode):
		return StatusSuccess
	case slices.Contains(tool.TemporaryFailCodes, exitCode):
		return StatusTemporaryFail
	case slices.Contains(tool.PermanentFailCodes, exitCode):
		return StatusPermanentFail
	case exitCode == 0:
		return StatusSuccess
	default:
		return StatusPermanentFail
	}
}

// StreamFile returns the filename for a captured stream, relative to the output directory.
// Undeclared streams get a deterministic derived name.
func StreamFile(tool *cwlcore.CommandLineTool, stream Stream, inputs map[string]any,
	eval *cwlcore.Evaluator, rt cwlcore.RuntimeContext,
) (string, error) {
	declared := tool.Stdout
	if stream == StreamStderr {
		declared = tool.Stderr
	}

	if declared == "" {
		return outGeneratedStreamFile(tool, stream), nil
	}

	name, err := eval.EvalString(string(declared),
		&cwlcore.EvalContext{Inputs: outExpressionObject(inputs), Self: nil, Runtime: rt})
	if err != nil {
		return "", err
	}

	err = outCheckStreamFile(stream, name)
	if err != nil {
		return "", err
	}

	return name, nil
}

// outCheckStreamFile validates a captured-stream filename.
func outCheckStreamFile(stream Stream, name string) error {
	if name == "" {
		return fmt.Errorf("%w: %s names nothing", ErrStreamFile, stream)
	}

	if !filepath.IsLocal(name) {
		return fmt.Errorf("%w: %s names %q", ErrStreamFile, stream, name)
	}

	return nil
}

// outGeneratedStreamFile derives a deterministic filename for an unnamed captured stream.
func outGeneratedStreamFile(tool *cwlcore.CommandLineTool, stream Stream) string {
	return strings.TrimPrefix(outChecksumOf([]byte(string(stream)+"\x00"+tool.ID)), outChecksumPrefix)
}

// outputCollector carries the fixed context of one [CollectOutputs] call.
type outputCollector struct {
	tool     *cwlcore.CommandLineTool
	eval     *cwlcore.Evaluator
	scope    *cwlcore.RequirementScope
	inputs   map[string]any         // expression-ready input object
	roots    []string               // allowed paths for containment checks
	runtime  cwlcore.RuntimeContext // runtime.* context (exitCode undefined here)
	outdir   string                 // cleaned absolute output directory
	outfs    WriteFS                // filesystem backing outdir
	outroot  string                 // symlink-resolved outdir
	exitCode int                    // process exit code, for outputEval only
}

// newOutputCollector builds the shared context for output collection.
type outputCollectorOption func(*outputCollector)

func withEvaluator(eval *cwlcore.Evaluator) outputCollectorOption {
	return func(c *outputCollector) {
		c.eval = eval
	}
}

func withRuntime(rt cwlcore.RuntimeContext) outputCollectorOption {
	return func(c *outputCollector) {
		c.runtime = rt
	}
}

func withExitCode(code int) outputCollectorOption {
	return func(c *outputCollector) {
		c.exitCode = code
	}
}

func newOutputCollector(
	tool *cwlcore.CommandLineTool, outdir string, outfs WriteFS, inputs map[string]any,
	opts ...outputCollectorOption,
) *outputCollector {
	dir := filepath.Clean(outdir)
	rendered := outExpressionObject(inputs)
	scope := cwlcore.NewScope(tool)

	if outfs == nil {
		outfs = NewLocalDirFS(dir)
	}

	collector := &outputCollector{
		tool:   tool,
		eval:   nil,
		scope:  scope,
		inputs: rendered,
		roots:  outAllowedRoots(rendered, scope),
		runtime: cwlcore.RuntimeContext{
			Cores:      nil,
			RAM:        nil,
			OutdirSize: nil,
			TmpdirSize: nil,
			ExitCode:   nil,
			Outdir:     "",
			Tmpdir:     "",
		},
		outdir:   dir,
		outfs:    outfs,
		outroot:  outResolvePath(dir),
		exitCode: 0,
	}

	for _, opt := range opts {
		opt(collector)
	}

	return collector
}

// relOutPath converts an absolute path to one relative to the output directory.
func (c *outputCollector) relOutPath(local string) string {
	rel, err := filepath.Rel(c.outdir, local)
	if err != nil {
		return local
	}

	return filepath.ToSlash(rel)
}

// context builds the evaluation context with self bound to the given value. exitCode is undefined.
func (c *outputCollector) context(self any) *cwlcore.EvalContext {
	return &cwlcore.EvalContext{Inputs: c.inputs, Self: self, Runtime: c.runtime}
}

// outputEvalContext builds the evaluation context for outputEval, with runtime.exitCode set.
func (c *outputCollector) outputEvalContext(self any) *cwlcore.EvalContext {
	runtime := c.runtime
	runtime.ExitCode = &c.exitCode

	return &cwlcore.EvalContext{Inputs: c.inputs, Self: self, Runtime: runtime}
}

// parameter collects one output parameter. Records are collected field-by-field unless outputEval is present.
func (c *outputCollector) parameter(param *cwlcore.CommandOutputParameter) (any, error) {
	target := c.parameterTarget(param)

	shape := outRecordType(target.typ)
	if shape.schema != nil && !outEvaluatesWhole(target.binding) {
		return c.record(shape)
	}

	return c.bound(target)
}

// bound collects a value through its output binding: glob, loadContents, outputEval, secondaryFiles, format.
func (c *outputCollector) bound(target *outTarget) (any, error) {
	binding, patterns, err := c.collectionPlan(target)
	if err != nil || binding == nil {
		return nil, err
	}

	globbed, err := c.globValues(patterns, binding)
	if err != nil {
		return nil, err
	}

	value, err := c.bindValue(target, binding, globbed, patterns)
	if err != nil {
		return nil, err
	}

	return value, c.publish(target, value)
}

// publish type-checks, attaches secondaryFiles and format, and relocates the collected value.
func (c *outputCollector) publish(target *outTarget, value any) error {
	err := checkValueType(cwlcore.ToExpressionValue(value), target.typ)
	if err != nil {
		return err
	}

	err = c.attachSecondaryFiles(target.secondaryFiles, value)
	if err != nil {
		return err
	}

	err = c.applyFormat(target.format, value)
	if err != nil {
		return err
	}

	return c.relocate(value)
}

// collectionPlan resolves the binding and glob patterns for an output. Nil binding means null value.
// stdout/stderr type shortcuts synthesize a binding if none exists.
func (c *outputCollector) collectionPlan(
	target *outTarget,
) (*cwlcore.CommandOutputBinding, []string, error) {
	stream, shortcut := outShortcutStream(target.typ)
	if !shortcut {
		if target.binding == nil {
			return nil, nil, nil
		}

		patterns, err := c.globPatterns(target.binding)

		return target.binding, patterns, err
	}

	name, err := StreamFile(c.tool, stream, c.inputs, c.eval, c.runtime)
	if err != nil {
		return nil, nil, err
	}

	binding := target.binding
	if binding == nil {
		binding = &cwlcore.CommandOutputBinding{OutputEval: "", LoadListing: "", Glob: nil, LoadContents: false}
	}

	return binding, []string{name}, nil
}

// outShortcutStream reports whether a type uses the stdout/stderr shortcut.
func outShortcutStream(declared cwlcore.TypeRef) (Stream, bool) {
	switch declared.Kind() {
	case cwlcore.TypeKindStdout:
		return StreamStdout, true
	case cwlcore.TypeKindStderr:
		return StreamStderr, true
	default:
		return "", false
	}
}

// bindValue produces the output value from globbed matches, applying outputEval if present.
func (c *outputCollector) bindValue(target *outTarget,
	binding *cwlcore.CommandOutputBinding, globbed []cwlcore.FileOrDirectory, patterns []string,
) (any, error) {
	value := any(outWiden(globbed))

	if binding.OutputEval != "" {
		evaluated, err := c.evalOutput(binding.OutputEval, globbed)
		if err != nil {
			return nil, err
		}

		value = evaluated
	}

	return outReduceValue(target.typ, value, patterns)
}

// evalOutput evaluates an outputEval expression and re-types File/Directory objects in the result.
func (c *outputCollector) evalOutput(
	expr cwlcore.Expression, globbed []cwlcore.FileOrDirectory,
) (any, error) {
	self := cwlcore.ToExpressionValue(globbed)

	evaluated, err := c.eval.Eval(string(expr), c.outputEvalContext(self))
	if err != nil {
		return nil, err
	}

	return c.retypeValue(evaluated)
}

// outReduceValue reduces a list to a single value for non-array output types.
func outReduceValue(declared cwlcore.TypeRef, value any, patterns []string) (any, error) {
	items, ok := value.([]any)
	if !ok || !outSingleFileType(declared) {
		return value, nil
	}

	switch len(items) {
	case 0:
		return nil, outNoMatch(declared, patterns)
	case 1:
		return items[0], nil
	default:
		return nil, fmt.Errorf("%w: glob %s matched %d paths",
			ErrOutputMultiple, outQuoted(patterns), len(items))
	}
}

// outNoMatch reports an error for a required output with no matches. Optional types return nil.
func outNoMatch(declared cwlcore.TypeRef, patterns []string) error {
	if declared.IsOptional() {
		return nil
	}

	return fmt.Errorf("%w: glob %s", ErrOutputMissing, outQuoted(patterns))
}

// outSingleFileType reports whether the type expects a single File or Directory (not an array).
func outSingleFileType(declared cwlcore.TypeRef) bool {
	switch declared.Kind() {
	case cwlcore.TypeKindStdout, cwlcore.TypeKindStderr:
		return true
	case cwlcore.TypeKindPrimitive:
		return declared.Name() == cwlcore.PrimitiveFile || declared.Name() == cwlcore.PrimitiveDirectory
	case cwlcore.TypeKindUnion:
		return slices.ContainsFunc(declared.Options(), outSingleFileType)
	default:
		return false
	}
}
