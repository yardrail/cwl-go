package cwlcore

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Expression-evaluation failure sentinels. Each error wraps exactly one; classify with [errors.Is].
var (
	// ErrJavaScript reports a JavaScript expression encountered without InlineJavascriptRequirement.
	ErrJavaScript = errors.New(
		"cwlcore: expression requires JavaScript but InlineJavascriptRequirement is not in scope",
	)

	// ErrNotParameterReference reports a $(...) naming a symbol outside the parameter context.
	ErrNotParameterReference = errors.New("cwlcore: not a parameter reference")

	// ErrExpressionSyntax reports an unterminated or unparseable expression.
	ErrExpressionSyntax = errors.New("cwlcore: malformed expression")

	// ErrExpressionEval reports an expression that parsed but failed to evaluate.
	ErrExpressionEval = errors.New("cwlcore: expression evaluation failed")

	// ErrExpressionTimeout reports a JavaScript expression that exceeded the time limit.
	ErrExpressionTimeout = errors.New("cwlcore: expression evaluation timed out")

	// ErrNotBoolean reports an expression that returned a non-boolean where a boolean was required.
	ErrNotBoolean = errors.New("cwlcore: expression did not return a boolean")
)

// initialParts is the initial capacity for interpolation parts.
const initialParts = 4

// DefaultEvalTimeout is the wall-clock limit for a single JavaScript evaluation.
const DefaultEvalTimeout = 20 * time.Second

// Leading symbols valid in a parameter reference.
const (
	rootInputs  = "inputs"
	rootSelf    = "self"
	rootRuntime = "runtime"
)

// Runtime parameter context field names.
const (
	runtimeOutdir     = "outdir"
	runtimeTmpdir     = "tmpdir"
	runtimeCores      = "cores"
	runtimeRAM        = "ram"
	runtimeOutdirSize = "outdirSize"
	runtimeTmpdirSize = "tmpdirSize"
	runtimeExitCode   = "exitCode"
	runtimeFieldCount = 7
)

// EvalContext is the inputs/self/runtime environment for expression evaluation.
// A nil *EvalContext is valid and behaves as empty.
type EvalContext struct {
	// Inputs is the job's input object, reachable as inputs.
	Inputs map[string]any

	// Self is the contextual value, visible as self. Nil means null.
	Self any

	// Runtime is the runtime.* parameter context.
	Runtime RuntimeContext
}

// RuntimeContext holds the runtime.* fields. Nil pointer fields are undefined, not null.
type RuntimeContext struct {
	// Cores is the reserved number of CPU cores, runtime.cores.
	Cores *int64

	// RAM is the reserved RAM in mebibytes, runtime.ram.
	RAM *int64

	// OutdirSize is the reserved output-directory size in mebibytes,
	// runtime.outdirSize.
	OutdirSize *int64

	// TmpdirSize is the reserved temporary-directory size in mebibytes,
	// runtime.tmpdirSize.
	TmpdirSize *int64

	// ExitCode is runtime.exitCode, only set during outputEval.
	ExitCode *int

	// Outdir is the designated output directory, runtime.outdir.
	Outdir string

	// Tmpdir is the designated temporary directory, runtime.tmpdir.
	Tmpdir string
}

// asMap renders the runtime context as the object an expression sees.
func (r RuntimeContext) asMap() map[string]any {
	runtime := make(map[string]any, runtimeFieldCount)
	runtime[runtimeOutdir] = r.Outdir
	runtime[runtimeTmpdir] = r.Tmpdir

	optional := map[string]*int64{
		runtimeCores:      r.Cores,
		runtimeRAM:        r.RAM,
		runtimeOutdirSize: r.OutdirSize,
		runtimeTmpdirSize: r.TmpdirSize,
	}

	for name, value := range optional {
		if value != nil {
			runtime[name] = *value
		}
	}

	if r.ExitCode != nil {
		runtime[runtimeExitCode] = int64(*r.ExitCode)
	}

	return runtime
}

// NeedsParsing reports whether s contains "$(" or "${" expression syntax.
func NeedsParsing(s string) bool {
	return strings.Contains(s, "$(") || strings.Contains(s, "${")
}

// Evaluator evaluates CWL expressions. Construct with [NewEvaluator].
// Nil/zero value evaluates parameter references only. Immutable and concurrent-safe.
type Evaluator struct {
	// libSrc is the joined expressionLib, run before every expression.
	libSrc string

	// programs caches compiled JavaScript programs.
	programs programCache

	// timeout bounds one JavaScript evaluation.
	timeout time.Duration

	// jsEnabled mirrors InlineJavascriptRequirement being in scope.
	jsEnabled bool
}

// referencesOnly is the shared config for nil/zero Evaluators.
var referencesOnly = &Evaluator{
	libSrc:    "",
	programs:  programCache{programs: nil, mu: sync.RWMutex{}},
	timeout:   DefaultEvalTimeout,
	jsEnabled: false,
}

// EvalOption configures an Evaluator at construction time.
type EvalOption func(*Evaluator)

// NewEvaluator returns an Evaluator configured by opts.
func NewEvaluator(opts ...EvalOption) *Evaluator {
	evaluator := &Evaluator{
		libSrc:    "",
		programs:  programCache{programs: nil, mu: sync.RWMutex{}},
		timeout:   DefaultEvalTimeout,
		jsEnabled: false,
	}

	for _, opt := range opts {
		opt(evaluator)
	}

	return evaluator
}

// WithJS enables JavaScript evaluation with the given expressionLib fragments.
func WithJS(expressionLib []string) EvalOption {
	libSrc := strings.Join(expressionLib, "\n")

	return func(e *Evaluator) {
		e.jsEnabled = true
		e.libSrc = libSrc
	}
}

// WithTimeout sets the JavaScript evaluation time limit.
func WithTimeout(d time.Duration) EvalOption {
	return func(e *Evaluator) {
		if d <= 0 {
			d = DefaultEvalTimeout
		}

		e.timeout = d
	}
}

// Eval evaluates a CWL expression string and returns the resulting value.
// A sole fragment preserves its type; multiple fragments interpolate to string.
// Strips surrounding whitespace; use [Evaluator.EvalContent] for file content.
func (e *Evaluator) Eval(expr string, ctx *EvalContext) (any, error) {
	if !NeedsParsing(expr) {
		return expr, nil
	}

	return e.usable().evalScanned(strings.TrimSpace(expr), ctx)
}

// EvalContent is [Eval] without whitespace stripping, for file content.
func (e *Evaluator) EvalContent(expr string, ctx *EvalContext) (any, error) {
	if !NeedsParsing(expr) {
		return expr, nil
	}

	return e.usable().evalScanned(expr, ctx)
}

// EvalString is [Eval] with the result rendered as text. Null yields "".
func (e *Evaluator) EvalString(expr string, ctx *EvalContext) (string, error) {
	value, err := e.Eval(expr, ctx)
	if err != nil {
		return "", err
	}

	if value == nil {
		return "", nil
	}

	return interpolatedText(value), nil
}

// EvalBool is [Eval] constrained to a boolean result. Returns [ErrNotBoolean] on type mismatch.
func (e *Evaluator) EvalBool(expr string, ctx *EvalContext) (bool, error) {
	value, err := e.Eval(expr, ctx)
	if err != nil {
		return false, err
	}

	result, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%w: %s evaluated to %s, want %s",
			ErrNotBoolean, expr, TypeName(value), typeNameBoolean)
	}

	return result, nil
}

// evalScanned interpolates a value already prepared for scanning.
func (e *Evaluator) evalScanned(src string, ctx *EvalContext) (any, error) {
	if ctx == nil {
		ctx = &EvalContext{
			Inputs: nil,
			Self:   nil,
			Runtime: RuntimeContext{
				Cores:      nil,
				RAM:        nil,
				OutdirSize: nil,
				TmpdirSize: nil,
				ExitCode:   nil,
				Outdir:     "",
				Tmpdir:     "",
			},
		}
	}

	return e.interpolate(src, ctx)
}

// usable resolves a possibly-nil receiver to a configuration that can be read.
func (e *Evaluator) usable() *Evaluator {
	if e == nil {
		return referencesOnly
	}

	return e
}

// interpolate walks src fragment by fragment, evaluating and concatenating.
func (e *Evaluator) interpolate(src string, ctx *EvalContext) (any, error) {
	parts := make([]string, 0, initialParts)
	rest := src

	for {
		window, found, err := scanFragment(rest)
		if err != nil {
			return nil, err
		}

		if !found {
			break
		}

		parts = append(parts, rest[:window.start])

		if window.escape {
			parts = append(parts, unescape(rest, window))
			rest = rest[window.end:]

			continue
		}

		value, err := e.evalFragment(rest[window.start+1:window.end], ctx)
		if err != nil {
			return nil, err
		}

		if isWholeString(parts, window, rest) {
			return value, nil
		}

		parts = append(parts, interpolatedText(value))
		rest = rest[window.end:]
	}

	parts = append(parts, rest)

	return strings.Join(parts, ""), nil
}

// evalFragment evaluates one scanned fragment (body has leading "$" removed).
func (e *Evaluator) evalFragment(body string, ctx *EvalContext) (any, error) {
	if strings.HasPrefix(body, "{") {
		if !e.jsEnabled {
			return nil, fmt.Errorf("%w: cannot evaluate function body $%s", ErrJavaScript, body)
		}

		return e.evalJSBody(body, ctx)
	}

	value, err := evalParamRef(body, ctx)
	if err == nil {
		return value, nil
	}

	if e.jsEnabled {
		// Parameter reference failed; fall through to JavaScript engine.
		return e.evalJSExpr(body, ctx)
	}

	return nil, withoutJavaScript(body, err)
}

// withoutJavaScript classifies a parameter-reference failure when JS is disabled.
func withoutJavaScript(body string, err error) error {
	if hasParamRefSyntax(body) {
		return err
	}

	return fmt.Errorf("%w: cannot evaluate $%s", ErrJavaScript, body)
}

// isWholeString reports whether window is the entire value (type-preserving case).
func isWholeString(parts []string, window scanWindow, rest string) bool {
	return len(parts) == 1 && window.start == 0 && window.end == len(rest)
}

// unescape applies the spec's escaping rules: \\→\, \$(→$(, \${→${.
func unescape(src string, w scanWindow) string {
	escape := src[w.start:w.end]

	switch {
	case escape == `\\`:
		return `\`
	case strings.HasPrefix(escape, `\$`) && len(escape) > len(`\$`):
		return escape[len(`\`):]
	default:
		return escape
	}
}

// interpolatedText renders a value for string interpolation. Strings pass through; others become JSON.
func interpolatedText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}

	return EncodeJSON(value)
}
