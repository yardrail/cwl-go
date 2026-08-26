package cwlcore

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Expression-evaluation failure sentinels. Every error returned from this
// file's evaluation entry points wraps exactly one of them, so callers can
// classify a failure with [errors.Is] without matching on message text.
//
// Exactly one is the contract, not a coincidence: no error wraps two, so a
// caller may dispatch on them as a closed set and a scheduler may act on the
// classification alone. In particular ErrNotBoolean never travels with an
// evaluation sentinel, because "your gate returned 1" and "your gate threw"
// call for different handling.
var (
	// ErrJavaScript reports a fragment that could only ever have been
	// JavaScript — a ${...} function body, or a $(...) whose contents the
	// parameter-reference grammar cannot even parse, such as
	// $(inputs.n + 1) — encountered while InlineJavascriptRequirement is not
	// in scope. Per the CWL v1.2 spec, absent that requirement "the workflow
	// platform must not perform expression interpolation".
	//
	// It means precisely "this needed JavaScript and JavaScript was not
	// enabled", so a caller may safely act on it by advising that
	// InlineJavascriptRequirement be declared. A fragment that parses as a
	// parameter reference but does not resolve never produces it; see
	// ErrNotParameterReference.
	ErrJavaScript = errors.New(
		"cwlcore: expression requires JavaScript but InlineJavascriptRequirement is not in scope",
	)

	// ErrNotParameterReference reports a $(...) fragment that parses as a
	// parameter reference but names a leading symbol outside the parameter
	// context — anything that is not inputs, self or runtime.
	//
	// It is deliberately distinct from ErrJavaScript because the two have
	// opposite remedies and the shapes are indistinguishable: $(input.flag)
	// is a misspelling of $(inputs.flag), while $(Math.PI) and $(true) are
	// JavaScript that wants InlineJavascriptRequirement. Reporting either as
	// "declare InlineJavascriptRequirement" would give actively wrong advice
	// for what is one of the most common CWL authoring mistakes, so the
	// sentinel stays neutral and the message names the offending symbol.
	ErrNotParameterReference = errors.New("cwlcore: not a parameter reference")

	// ErrExpressionSyntax reports an expression string the evaluator cannot
	// even delimit or compile: an unterminated $(...)/${...} block, or
	// JavaScript the engine refuses to parse.
	ErrExpressionSyntax = errors.New("cwlcore: malformed expression")

	// ErrExpressionEval reports an expression that was well formed but could
	// not be evaluated: a missing key, an out-of-range index, a thrown
	// JavaScript exception, or a result that is not a valid JSON data type.
	// Per the spec these are permanent failures, never retried.
	ErrExpressionEval = errors.New("cwlcore: expression evaluation failed")

	// ErrExpressionTimeout reports a JavaScript expression that ran past the
	// evaluator's wall-clock limit and was interrupted.
	ErrExpressionTimeout = errors.New("cwlcore: expression evaluation timed out")

	// ErrNotBoolean reports an expression that evaluated successfully but
	// produced something other than a boolean, where the field's declared
	// type requires one — a step's when, or a requirement field such as
	// WorkReuse or NetworkAccess.
	//
	// It is separate from ErrExpressionEval precisely because the two mean
	// opposite things about the document. A gate that returns 1 is wrong in a
	// specific, explainable way and the message can say so; a gate that threw
	// is an expression that blew up. Both are permanent failures, but a
	// caller that fused them could not tell an author which mistake they
	// made. An error carrying ErrNotBoolean never also carries an evaluation
	// sentinel.
	ErrNotBoolean = errors.New("cwlcore: expression did not return a boolean")
)

// initialParts is the literal-and-fragment capacity most interpolated field
// values need: a prefix, one fragment and a suffix, with room to spare.
const initialParts = 4

// DefaultEvalTimeout bounds a single JavaScript evaluation unless WithTimeout
// overrides it. The spec permits such a limit: "implementations may apply
// other limits". The value matches the reference implementation's default.
const DefaultEvalTimeout = 20 * time.Second

// The only leading symbols a parameter reference may name. Spec: the runtime
// "must initialize as global variables the fields of the parameter context",
// which is exactly inputs, self and runtime.
const (
	rootInputs  = "inputs"
	rootSelf    = "self"
	rootRuntime = "runtime"
)

// The field names of the runtime parameter context, and how many there are.
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

// EvalContext is the symbol environment one expression is evaluated against.
// Its three fields become the inputs, self and runtime globals, and nothing
// else is in scope.
//
// Values reachable through Inputs and Self are expected to be JSON-shaped —
// nil, bool, string, a numeric type, []any or map[string]any — because that is
// what a decoded CWL document and a JavaScript expression can both represent.
// Other slice and string-keyed map types are accepted too and read
// reflectively.
//
// A nil *EvalContext is valid and behaves as an empty context.
type EvalContext struct {
	// Inputs is the job's input object, reachable as inputs.
	Inputs map[string]any

	// Self is the value of self for this evaluation, which the spec defines
	// per field: an output binding's file list, a step input's value, and so
	// on. It may be nil, and nil is visible to the expression as null.
	Self any

	// Runtime is the runtime.* parameter context.
	Runtime RuntimeContext
}

// RuntimeContext holds the runtime.* fields of the parameter context. Nil
// pointer fields are not defined for the expression at all, so referencing
// them is an error rather than a silent null — an unresolved resource
// requirement should not read as "zero cores".
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

	// ExitCode is the exit code of the finished process. Spec: it "is
	// available to expressions in outputEval as runtime.exitCode", and
	// nowhere else, so it is nil for every other evaluation.
	ExitCode *int

	// Outdir is the designated output directory, runtime.outdir.
	Outdir string

	// Tmpdir is the designated temporary directory, runtime.tmpdir.
	Tmpdir string
}

// asMap renders the runtime context as the object an expression sees. Fields
// that are not set are omitted rather than nulled, so a reference to them
// fails loudly.
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

// NeedsParsing reports whether s contains an expression at all. A field value
// that does not is used verbatim, which is why every caller should gate on
// this before evaluating: it is both the fast path and the reason a literal
// string containing neither "$(" nor "${" can never fail to evaluate.
func NeedsParsing(s string) bool {
	return strings.Contains(s, "$(") || strings.Contains(s, "${")
}

// Evaluator evaluates CWL expressions under a fixed configuration derived from
// a process's requirement scope. Construct one with NewEvaluator.
//
// Without WithJS only parameter references are legal and no JavaScript engine
// is ever constructed; with it, $(...) is a full ECMAScript expression and
// ${...} a function body.
//
// The zero value is usable, and so is a nil *Evaluator: both evaluate
// parameter references only, which is the behaviour the spec requires of every
// conforming implementation whether or not any requirement is declared. That
// mirrors a nil *EvalContext, which Eval also accepts, so neither half of the
// call needs a defensive construction.
//
// An Evaluator is immutable once built and safe for concurrent use by multiple
// goroutines. It must not be copied after first use.
type Evaluator struct {
	// libSrc is the requirement's expressionLib fragments joined into one
	// program, run before every expression.
	libSrc string

	// programs memoizes compiled JavaScript, which is runtime-independent
	// and so survives the per-evaluation sandbox.
	programs programCache

	// timeout bounds one JavaScript evaluation; zero means
	// DefaultEvalTimeout.
	timeout time.Duration

	// jsEnabled mirrors InlineJavascriptRequirement being in scope.
	jsEnabled bool
}

// referencesOnly is the configuration a nil or zero-valued *Evaluator behaves
// as. It is shared and never mutated: with JavaScript disabled its program
// cache is never touched.
var referencesOnly = &Evaluator{timeout: DefaultEvalTimeout}

// EvalOption configures an Evaluator at construction time.
type EvalOption func(*Evaluator)

// NewEvaluator returns an Evaluator configured by opts. With no options it
// supports parameter references only, which is what the spec requires of every
// conforming implementation regardless of any requirement being declared.
func NewEvaluator(opts ...EvalOption) *Evaluator {
	evaluator := &Evaluator{timeout: DefaultEvalTimeout}

	for _, opt := range opts {
		opt(evaluator)
	}

	return evaluator
}

// WithJS enables the ECMAScript 5.1 evaluation path. Pass it if and only if
// InlineJavascriptRequirement is in the requirement scope, supplying that
// requirement's expressionLib fragments; they are concatenated and run ahead
// of every expression, so the functions they define are in scope for it.
//
// expressionLib is not retained: it is joined into a single program up front.
func WithJS(expressionLib []string) EvalOption {
	libSrc := strings.Join(expressionLib, "\n")

	return func(e *Evaluator) {
		e.jsEnabled = true
		e.libSrc = libSrc
	}
}

// WithTimeout bounds how long one JavaScript evaluation may run before it is
// interrupted and fails with ErrExpressionTimeout. A non-positive duration
// restores DefaultEvalTimeout. It has no effect on parameter references, which
// cannot loop.
func WithTimeout(d time.Duration) EvalOption {
	return func(e *Evaluator) {
		if d <= 0 {
			d = DefaultEvalTimeout
		}

		e.timeout = d
	}
}

// Eval evaluates one whole field value and returns the value it denotes.
//
// A string that [NeedsParsing] reports false for — one containing neither
// "$(" nor "${" — is returned verbatim, as the string it is. That is what
// field interpolation requires, but it means a document that writes
// when: true rather than when: $(true) reaches a typed caller as the string
// "true", not as a boolean: an unparsed literal is data, not a failed
// expression. Callers that need to tell the two apart should gate on
// [NeedsParsing] themselves.
//
// Otherwise the string is stripped of surrounding whitespace and scanned for
// $(...) and ${...} fragments:
//
//   - If the stripped string is exactly one fragment, its value is returned
//     with its type intact. Spec: "If the value of a field has no leading or
//     trailing non-whitespace characters around a parameter reference, the
//     effective value of the field becomes the value of the referenced
//     parameter, preserving the return type." So $(inputs.n) with n = 3 is the
//     number 3, not "3".
//   - Otherwise every fragment is rendered as text and concatenated with the
//     literal characters around it, yielding a string.
//
// The strip is what makes the first rule work on a field written as a YAML
// block scalar, which always ends in a newline. It also means the surrounding
// whitespace is gone from an interpolated result — harmless for a field, whose
// whitespace is only punctuation around a reference, and wrong for a value that
// is file content. Use [Evaluator.EvalContent] for those.
//
// A nil ctx is treated as an empty context.
func (e *Evaluator) Eval(expr string, ctx *EvalContext) (any, error) {
	if !NeedsParsing(expr) {
		return expr, nil
	}

	return e.usable().evalScanned(strings.TrimSpace(expr), ctx)
}

// EvalContent is Eval for a value that is file content rather than a document
// field: the entry of an InitialWorkDirRequirement Dirent, and anything else
// whose result is written out as bytes.
//
// It differs in one thing — the input is not stripped of surrounding
// whitespace — and that is the whole of why it exists. In a field, whitespace
// around a reference is punctuation, so discarding it costs nothing and lets a
// block scalar's trailing newline be ignored when deciding whether the field
// keeps the referenced value's type. In file content a trailing newline is
// data, and a Dirent that stages one byte short of what the document asked for
// is simply wrong.
//
// The type-preservation rule still applies, now measured against the whole
// untouched string: "$(inputs.rec)" yields the record, while "$(inputs.rec)\n"
// is one fragment plus a literal newline and so interpolates to text. That is
// the distinction the spec's "no leading or trailing non-whitespace characters"
// draws once nothing has been stripped away first.
//
// A nil ctx is treated as an empty context.
func (e *Evaluator) EvalContent(expr string, ctx *EvalContext) (any, error) {
	if !NeedsParsing(expr) {
		return expr, nil
	}

	return e.usable().evalScanned(expr, ctx)
}

// EvalString is Eval rendered as text, for the fields that must end up a
// string — a command-line token from valueFrom, a stdout filename, an
// environment variable value.
//
// A string result is returned as is and any other value as its JSON
// representation, exactly as an embedded fragment would be interpolated. A
// null result yields the empty string; callers that must tell null apart from
// an empty string should use Eval.
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

// EvalBool is Eval constrained to a boolean, for the fields the spec defines
// as one: a step's when, and the requirement fields that accept a boolean or
// an expression.
//
// The result must already be a boolean. Spec, on when: "The expression must
// evaluate to a boolean value. It is an error if the expression evaluates to
// any other value." So 1, 0, "true" and an empty list are rejected rather
// than coerced the way JavaScript's own truthiness rules would.
//
// A wrong type gives ErrNotBoolean, naming the type that turned up as
// TypeName renders it. A failure to evaluate at all propagates unchanged,
// still carrying whichever of ErrExpressionEval, ErrJavaScript,
// ErrNotParameterReference, ErrExpressionSyntax or ErrExpressionTimeout
// describes it. The two never overlap, so a caller can branch on
// "the document is wrong here" separately from "the expression blew up".
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

// evalScanned interpolates a value already in the form its fragments are to be
// read out of, which is the only thing Eval and EvalContent disagree about.
func (e *Evaluator) evalScanned(src string, ctx *EvalContext) (any, error) {
	if ctx == nil {
		ctx = &EvalContext{}
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

// interpolate walks src fragment by fragment, following the reference
// implementation's structure: scan the remaining text for the next fragment,
// emit the literal run before it, emit the fragment, repeat.
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

// evalFragment evaluates one scanned fragment. body is the fragment with its
// leading "$" removed, so "(inputs.x)" for a parameter reference or
// "{ return 1; }" for a function body.
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
		// With InlineJavascriptRequirement in scope every $(...) is an
		// ECMAScript expression, so a fragment the parameter-reference
		// grammar cannot handle — or one it parses but cannot resolve, such
		// as $(inputs.s.length) on a string — falls through to the engine.
		// The spec's guarantee that the two modes agree makes trying the
		// native path first a pure optimisation.
		return e.evalJSExpr(body, ctx)
	}

	return nil, withoutJavaScript(body, err)
}

// withoutJavaScript classifies a native failure that cannot fall through to
// the engine, and is the whole of the ErrJavaScript / ErrNotParameterReference
// split.
//
// A fragment the grammar cannot parse at all is JavaScript by elimination, so
// the missing capability really is the engine. A fragment that does parse is
// left with the error evalParamRef produced — ErrExpressionEval for a
// reference that did not resolve, ErrNotParameterReference for one naming a
// symbol outside the parameter context. The second case is deliberately not
// upgraded: $(input.flag) and $(Math.PI) are lexically identical, and calling
// a misspelling a missing requirement is worse than leaving the sentinel
// neutral and letting the message name the symbol.
func withoutJavaScript(body string, err error) error {
	if hasParamRefSyntax(body) {
		return err
	}

	return fmt.Errorf("%w: cannot evaluate $%s", ErrJavaScript, body)
}

// isWholeString reports whether window covers the entire value being
// interpolated: nothing literal precedes it — this is the first fragment, and
// it starts at offset zero — and nothing follows. That is the spec's test for
// returning the referenced value with its type intact instead of stringifying
// it.
//
// The test is exact, on bytes, and the whitespace the spec permits around the
// reference is accounted for by [Evaluator.Eval] stripping it from the input
// before scanning. Widening the test to tolerate whitespace here instead would
// look equivalent and is not: it would also make "$(x)\n" a whole string, which
// [Evaluator.EvalContent] needs to interpolate so that the newline survives.
func isWholeString(parts []string, window scanWindow, rest string) bool {
	return len(parts) == 1 && window.start == 0 && window.end == len(rest)
}

// unescape applies the spec's three escaping rules to the escape window w.
// Spec: "The substrings \$( and \${ are replaced by $( and ${ respectively. No
// parameter or expression evaluation interpolation occurs. A double backslash
// \\ is replaced by a single backslash \. A substring starting with a backslash
// that does not match one of the previous rules is left unchanged."
//
// That third rule is where this differs from cwl-utils, whose interpolate
// strips the backslash from every \$ regardless of what follows; the spec keeps
// \$x verbatim.
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

// interpolatedText renders a fragment's value for embedding in a larger
// string. Spec: strings contribute "the literal text of the string ... and
// there are no leading or trailing quotes"; everything else contributes its
// textual JSON representation.
func interpolatedText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}

	return EncodeJSON(value)
}
