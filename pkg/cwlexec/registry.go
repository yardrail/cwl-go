package cwlexec

import "github.com/yardrail/cwl-go/pkg/cwlcore"

// registration is one class's entry in a [Registry]: its handler plus the options that were set
// when it was registered. Options are static per class, so they are resolved once here rather than
// per call.
type registration struct {
	handler StepHandler

	// unbudgeted records that the class opted out of resource selection.
	unbudgeted bool
}

// RegisterOption configures one class registration. See [Unbudgeted].
type RegisterOption func(*registration)

// Unbudgeted marks a class as exempt from resource selection: the scheduler performs no cores or
// RAM accounting for its calls, skips Config.SelectResources, and hands the handler the zero
// [Resources].
//
// It exists for classes whose execution consumes no local machine resources — a call out to a
// remote service, a durable wait on a human decision. Counting those against a cores budget would
// stall a run behind work that is not using a core at all.
func Unbudgeted() RegisterOption {
	return func(r *registration) {
		r.unbudgeted = true
	}
}

// Registry maps process classes to the handlers that execute them.
//
// A Registry is *not* safe for concurrent registration: build it fully before starting a run, then
// treat it as read-only. Lookups on a completed registry are safe from many goroutines, which is
// what the scheduler does.
//
// The zero Registry is valid and empty — every lookup misses. Use [NewRegistry] to get one carrying
// the built-in handlers for the four core CWL process classes.
type Registry struct {
	entries map[Class]registration
}

// NewRegistry returns a Registry preloaded with the built-in handler for each of the four core CWL
// process classes:
//
//   - ExpressionTool — evaluates the tool's expression and binds the result to its output ports.
//     Fully implemented here; see [Registry.Register] to replace it.
//   - Operation — fails, permanently. An Operation "does not provide enough information to be
//     executed": it is an abstract placeholder for a step an engine is expected to recognize by
//     its identifier and requirements. Succeeding with null outputs would fabricate data, so the
//     built-in refuses, and an engine that knows what its operations mean registers its own
//     handler over the top.
//   - CommandLineTool and Workflow — placeholders that fail with [ErrNotImplemented], naming the
//     stream that owns the real implementation.
//
// Every other class must be registered by the caller or the run fails closed with [ErrNoHandler].
func NewRegistry() *Registry {
	registry := &Registry{entries: nil}

	registry.Register(Class(cwlcore.ClassExpressionTool), expressionToolHandler{})
	registry.Register(Class(cwlcore.ClassOperation), operationHandler{})
	registry.Register(Class(cwlcore.ClassCommandLineTool), commandLineToolPlaceholder())
	registry.Register(Class(cwlcore.ClassWorkflow), workflowPlaceholder())

	return registry
}

// Register binds handler to class, replacing any existing binding — including a built-in one, which
// is how a caller supplies its own Operation semantics or swaps in a different CommandLineTool
// execution strategy. It is also how a class this package has never heard of becomes executable.
//
// Options apply to the class, not to a call, and are replaced wholesale along with the handler: a
// re-registration that omits [Unbudgeted] clears it.
//
// Register panics on an empty class or a nil handler. Both are programming errors in code that runs
// once, before any step does, and the alternative — discovering a nil handler when the first step
// of a long run dispatches to it — is strictly worse.
func (r *Registry) Register(class Class, handler StepHandler, opts ...RegisterOption) {
	if class == "" {
		panic("cwlexec: Register called with an empty process class")
	}

	if handler == nil {
		panic("cwlexec: Register called with a nil handler for class " + string(class))
	}

	entry := registration{handler: handler, unbudgeted: false}
	for _, opt := range opts {
		opt(&entry)
	}

	if r.entries == nil {
		r.entries = make(map[Class]registration)
	}

	r.entries[class] = entry
}

// Handler returns the handler registered for class, and whether there is one.
//
// A miss is fail-closed: a class present in the document with no handler is an error at run time,
// not a step to skip. Executing an unrecognized class by guessing is how an engine silently
// produces wrong results, so the scheduler reports [ErrNoHandler] instead.
func (r *Registry) Handler(class Class) (StepHandler, bool) {
	entry, found := r.entries[class]

	return entry.handler, found
}

// IsUnbudgeted reports whether class was registered with [Unbudgeted]. An unregistered class
// reports false; it has no calls to budget, because dispatching it fails first.
func (r *Registry) IsUnbudgeted(class Class) bool {
	return r.entries[class].unbudgeted
}
