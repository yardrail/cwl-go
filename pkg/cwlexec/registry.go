package cwlexec

import "github.com/yardrail/cwl-go/pkg/cwlcore"

// registration is one class's entry in a [Registry].
type registration struct {
	handler    StepHandler
	unbudgeted bool
}

// RegisterOption configures one class registration. See [Unbudgeted].
type RegisterOption func(*registration)

// Unbudgeted exempts a class from resource selection.
func Unbudgeted() RegisterOption {
	return func(r *registration) {
		r.unbudgeted = true
	}
}

// Registry maps process classes to handlers. Not safe for concurrent registration.
type Registry struct {
	entries map[Class]registration
}

// NewRegistry returns a Registry with built-in handlers for the four core CWL classes.
func NewRegistry() *Registry {
	registry := &Registry{entries: nil}

	registry.Register(Class(cwlcore.ClassExpressionTool), expressionToolHandler{})
	registry.Register(Class(cwlcore.ClassOperation), operationHandler{})
	registry.Register(Class(cwlcore.ClassCommandLineTool), commandLineToolPlaceholder())
	registry.Register(Class(cwlcore.ClassWorkflow), workflowPlaceholder())

	return registry
}

// Register binds a handler to a class, replacing any existing binding. Panics on empty class or nil handler.
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

// Handler returns the handler for class, or false if unregistered.
func (r *Registry) Handler(class Class) (StepHandler, bool) {
	entry, found := r.entries[class]

	return entry.handler, found
}

// IsUnbudgeted reports whether class was registered with [Unbudgeted].
func (r *Registry) IsUnbudgeted(class Class) bool {
	return r.entries[class].unbudgeted
}
