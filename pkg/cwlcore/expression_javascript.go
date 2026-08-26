package cwlcore

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// jsProgramName labels compiled programs in JavaScript stack traces.
const jsProgramName = "cwl-expression"

// jsonGlobal is the name of the built-in the parameter context is marshalled
// through.
const jsonGlobal = "JSON"

// programCache memoizes compiled JavaScript. A goja Program "is not linked to
// a runtime in any way and can be run in multiple runtimes (possibly at the
// same time)", so it outlives the per-evaluation sandbox and turns the
// repeated evaluation of one expression — a scatter over a thousand items —
// into a thousand runs of one compile.
//
// Compilation failures are not cached: they are permanent and rare, and
// recompiling to report one costs nothing.
type programCache struct {
	programs map[string]*goja.Program
	mu       sync.RWMutex
}

// compile returns the compiled form of src, in ECMAScript strict mode. Spec:
// "Expressions also must be evaluated in Javascript strict mode".
func (c *programCache) compile(src string) (*goja.Program, error) {
	c.mu.RLock()
	program, ok := c.programs[src]
	c.mu.RUnlock()

	if ok {
		return program, nil
	}

	program, err := goja.Compile(jsProgramName, src, true)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrExpressionSyntax, err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.programs == nil {
		c.programs = make(map[string]*goja.Program)
	}

	c.programs[src] = program

	return program, nil
}

// evalJSExpr evaluates a $(...) fragment. body already carries the enclosing
// parentheses, which is exactly what makes it an ECMAScript expression and
// lets $({a: 1}) read as an object literal rather than a block.
func (e *Evaluator) evalJSExpr(body string, ctx *EvalContext) (any, error) {
	return e.runJS(body, ctx)
}

// evalJSBody evaluates a ${...} fragment. Spec: it "must be evaluated as an
// ECMAScript function body for an anonymous, zero-argument function. This
// means the code will be evaluated as (function() { ... })()".
func (e *Evaluator) evalJSBody(body string, ctx *EvalContext) (any, error) {
	return e.runJS("(function()"+body+")()", ctx)
}

// runJS evaluates src in a sandbox of its own.
//
// A fresh goja Runtime per evaluation is what implements the spec's
// requirement that expressions run "in an isolated context (a 'sandbox') which
// permits no side effects to leak outside the context": there is no shared
// state for one expression to leave behind for the next. The parameter context
// is installed by decoding it from JSON inside the sandbox, so the expression
// sees plain JavaScript objects and cannot reach the Go values behind them.
func (e *Evaluator) runJS(src string, ctx *EvalContext) (any, error) {
	program, err := e.programs.compile(src)
	if err != nil {
		return nil, err
	}

	sandbox := goja.New()

	err = e.prepare(sandbox, ctx)
	if err != nil {
		return nil, err
	}

	value, err := e.run(sandbox, program)
	if err != nil {
		return nil, err
	}

	return jsResult(sandbox, value)
}

// prepare installs the parameter context and runs the expressionLib fragments,
// whose declarations the expression then sees as globals.
func (e *Evaluator) prepare(sandbox *goja.Runtime, ctx *EvalContext) error {
	err := setJSGlobals(sandbox, ctx)
	if err != nil {
		return err
	}

	if e.libSrc == "" {
		return nil
	}

	program, err := e.programs.compile(e.libSrc)
	if err != nil {
		return err
	}

	_, err = e.run(sandbox, program)

	return err
}

// run executes one program under the evaluator's time limit.
//
// The limit is enforced with goja's interrupt mechanism, the only thing that
// stops a `while (true) {}`; the spec allows it, since "implementations may
// apply other limits". The recover is a belt-and-braces guard: a malformed
// workflow must never take the process down, so an engine panic becomes an
// ordinary evaluation error.
func (e *Evaluator) run(sandbox *goja.Runtime, program *goja.Program) (goja.Value, error) {
	timeout := e.jsTimeout()

	timer := time.AfterFunc(timeout, func() { sandbox.Interrupt(ErrExpressionTimeout) })
	defer timer.Stop()

	value, err := runGuarded(sandbox, program)
	sandbox.ClearInterrupt()

	if err != nil {
		return nil, convertJSError(err, timeout)
	}

	return value, nil
}

// runGuarded runs one program, turning an engine panic into an ordinary error.
func runGuarded(sandbox *goja.Runtime, program *goja.Program) (goja.Value, error) {
	var value goja.Value

	err := func() (err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("%w: the JavaScript engine panicked: %v", ErrExpressionEval, recovered)
			}
		}()

		var runErr error

		value, runErr = sandbox.RunProgram(program)

		return runErr
	}()
	if err != nil {
		return nil, err
	}

	return value, nil
}

// jsTimeout is the configured limit, or the default when none was set.
func (e *Evaluator) jsTimeout() time.Duration {
	if e.timeout <= 0 {
		return DefaultEvalTimeout
	}

	return e.timeout
}

// convertJSError classifies a failure from the engine.
func convertJSError(err error, timeout time.Duration) error {
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) {
		return fmt.Errorf("%w after %s", ErrExpressionTimeout, timeout)
	}

	var exception *goja.Exception
	if errors.As(err, &exception) {
		return fmt.Errorf("%w: %s", ErrExpressionEval, exception.String())
	}

	return fmt.Errorf("%w: %w", ErrExpressionEval, err)
}

// setJSGlobals defines inputs, self and runtime. Spec: the runtime "must
// initialize as global variables the fields of the parameter context".
func setJSGlobals(sandbox *goja.Runtime, ctx *EvalContext) error {
	context := map[string]any{
		rootInputs:  ctx.Inputs,
		rootSelf:    ctx.Self,
		rootRuntime: ctx.Runtime.asMap(),
	}

	parsed, err := jsonParse(sandbox, EncodeJSON(context))
	if err != nil {
		return err
	}

	object, ok := parsed.(*goja.Object)
	if !ok {
		return fmt.Errorf("%w: the parameter context did not decode to an object", ErrExpressionEval)
	}

	for _, name := range []string{rootInputs, rootSelf, rootRuntime} {
		err := sandbox.Set(name, object.Get(name))
		if err != nil {
			return fmt.Errorf("%w: cannot define %s: %w", ErrExpressionEval, name, err)
		}
	}

	return nil
}

// jsResult converts a finished evaluation to a Go value, enforcing the spec's
// rule that expressions "must return a valid JSON data type: one of null,
// string, number, boolean, array, object. Other return values must result in a
// permanentFailure."
//
// undefined and anything JSON.stringify cannot represent — a function, for
// instance — are rejected outright. NaN and the infinities are rejected too:
// they are numbers in JavaScript but not in JSON, and JSON.stringify quietly
// turns them into null, which would hide a failed computation behind a
// plausible-looking result.
//
// Everything else is round-tripped through the sandbox's own JSON.stringify,
// which is what makes the result a JSON type by construction: a Date becomes
// its ISO string through toJSON, exactly as the reference implementation sees
// it, rather than leaking a Go [time.Time] to the caller.
func jsResult(sandbox *goja.Runtime, value goja.Value) (any, error) {
	if value == nil || goja.IsUndefined(value) {
		return nil, fmt.Errorf("%w: the expression returned undefined", ErrExpressionEval)
	}

	if goja.IsNull(value) {
		// Returned through a variable because a bare "return nil, nil" reads
		// as a missing result rather than the JSON null it is.
		var null any

		return null, nil
	}

	if goja.IsNaN(value) || goja.IsInfinity(value) {
		return nil, fmt.Errorf("%w: the expression returned %s, which is not a JSON number",
			ErrExpressionEval, value.String())
	}

	text, err := jsonText(sandbox, value)
	if err != nil {
		return nil, err
	}

	return decodeJSONText(text)
}

// jsonText encodes a result value with the sandbox's JSON.stringify.
func jsonText(sandbox *goja.Runtime, value goja.Value) (string, error) {
	stringify, err := jsonMethod(sandbox, "stringify")
	if err != nil {
		return "", err
	}

	encoded, err := stringify(goja.Undefined(), value)
	if err != nil {
		return "", fmt.Errorf("%w: the expression result could not be encoded as JSON: %w",
			ErrExpressionEval, err)
	}

	if goja.IsUndefined(encoded) {
		return "", fmt.Errorf("%w: the expression returned a %s, which is not a valid JSON data type",
			ErrExpressionEval, jsKind(value))
	}

	return encoded.String(), nil
}

// jsKind names a value for the error reporting an invalid return type.
func jsKind(value goja.Value) string {
	if _, ok := goja.AssertFunction(value); ok {
		return "function"
	}

	return "result"
}

// jsonMethod looks up one method of the sandbox's JSON global.
func jsonMethod(sandbox *goja.Runtime, name string) (goja.Callable, error) {
	holder, ok := sandbox.GlobalObject().Get(jsonGlobal).(*goja.Object)
	if !ok {
		return nil, fmt.Errorf("%w: the %s global is unavailable", ErrExpressionEval, jsonGlobal)
	}

	method, ok := goja.AssertFunction(holder.Get(name))
	if !ok {
		return nil, fmt.Errorf("%w: %s.%s is unavailable", ErrExpressionEval, jsonGlobal, name)
	}

	return method, nil
}

// jsonParse decodes text with the sandbox's JSON.parse, producing native
// JavaScript objects rather than Go values wrapped by the engine. That is what
// keeps the sandbox sealed in both directions: the expression gets the full
// object protocol, and mutating what it gets cannot reach the caller's data.
func jsonParse(sandbox *goja.Runtime, text string) (goja.Value, error) {
	parse, err := jsonMethod(sandbox, "parse")
	if err != nil {
		return nil, err
	}

	value, err := parse(goja.Undefined(), sandbox.ToValue(text))
	if err != nil {
		return nil, fmt.Errorf("%w: the parameter context is not encodable as JSON: %w",
			ErrExpressionEval, err)
	}

	return value, nil
}

// decodeJSONText parses an evaluation result back into Go.
func decodeJSONText(text string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()

	var decoded any

	err := decoder.Decode(&decoded)
	if err != nil {
		return nil, fmt.Errorf("%w: the expression result is not valid JSON: %w", ErrExpressionEval, err)
	}

	return canonicalNumbers(decoded), nil
}

// canonicalNumbers replaces the [json.Number] placeholders left by UseNumber
// with int64 or float64, so that a JavaScript integer arrives as an integer.
// Decoding straight into any would make every number a float64 and turn
// $(inputs.n + 1) into "4.0" when interpolated.
func canonicalNumbers(value any) any {
	switch typed := value.(type) {
	case json.Number:
		return numberValue(typed)
	case []any:
		for i, item := range typed {
			typed[i] = canonicalNumbers(item)
		}

		return typed
	case map[string]any:
		for key, item := range typed {
			typed[key] = canonicalNumbers(item)
		}

		return typed
	default:
		return value
	}
}

// numberValue narrows a JSON number to the tightest Go type that holds it.
func numberValue(number json.Number) any {
	integer, err := number.Int64()
	if err == nil {
		return integer
	}

	float, floatErr := number.Float64()
	if floatErr != nil {
		return number.String()
	}

	return float
}
