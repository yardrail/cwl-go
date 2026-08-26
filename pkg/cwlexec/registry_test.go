package cwlexec

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// extensionClass is a class this package has never heard of, standing in for the process classes a
// downstream engine layers on through the RawProcess seam.
const extensionClass Class = "https://example.com/ext#Approval"

// noopHandler is a handler that succeeds with no outputs, used where only the registration matters.
func noopHandler() StepHandler {
	return HandlerFunc(func(_ context.Context, _ *StepCall) (Result, error) {
		return Success(nil)
	})
}

func TestNewRegistryBuiltIns(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()

	for _, class := range []Class{
		Class(cwlcore.ClassCommandLineTool),
		Class(cwlcore.ClassWorkflow),
		Class(cwlcore.ClassExpressionTool),
		Class(cwlcore.ClassOperation),
	} {
		handler, found := registry.Handler(class)
		if !found || handler == nil {
			t.Fatalf("built-in handler for %q is missing", class)
		}

		if registry.IsUnbudgeted(class) {
			t.Fatalf("built-in %q must take part in resource selection", class)
		}
	}
}

func TestRegistryHandlerMissIsFailClosed(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()

	for _, class := range []Class{extensionClass, "", "commandlinetool"} {
		if handler, found := registry.Handler(class); found || handler != nil {
			t.Fatalf("Handler(%q) = %v, %t; an unregistered class must miss", class, handler, found)
		}
	}
}

func TestRegistryRegisterOverridesBuiltIn(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	mine := &recordingHandler{result: Result{Status: StatusSuccess}}

	registry.Register(Class(cwlcore.ClassOperation), mine)

	handler, found := registry.Handler(Class(cwlcore.ClassOperation))
	if !found || handler != StepHandler(mine) {
		t.Fatalf("Handler = %v, %t; want the registered override", handler, found)
	}

	// And the built-in it replaced no longer runs.
	_, err := handler.Execute(t.Context(), &StepCall{})
	if err != nil {
		t.Fatalf("override returned %v, want the override's own result", err)
	}
}

func TestRegistryUnbudgeted(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()

	registry.Register(extensionClass, noopHandler(), Unbudgeted())

	if !registry.IsUnbudgeted(extensionClass) {
		t.Fatal("Unbudgeted must be observable through the registration")
	}

	if registry.IsUnbudgeted("never-registered") {
		t.Fatal("an unregistered class must not report as unbudgeted")
	}

	// Options are replaced wholesale along with the handler.
	registry.Register(extensionClass, noopHandler())

	if registry.IsUnbudgeted(extensionClass) {
		t.Fatal("re-registering without Unbudgeted must clear it")
	}
}

func TestZeroRegistry(t *testing.T) {
	t.Parallel()

	registry := &Registry{}

	if _, found := registry.Handler(Class(cwlcore.ClassCommandLineTool)); found {
		t.Fatal("the zero Registry carries no built-ins")
	}

	registry.Register(extensionClass, noopHandler())

	if _, found := registry.Handler(extensionClass); !found {
		t.Fatal("the zero Registry must accept a registration")
	}
}

func TestRegistryRegisterPanics(t *testing.T) {
	t.Parallel()

	cases := []struct {
		handler StepHandler
		name    string
		class   Class
	}{
		{name: "empty class", class: "", handler: noopHandler()},
		{name: "nil handler", class: extensionClass, handler: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if recover() == nil {
					t.Fatal("Register must panic on a registration that can never dispatch")
				}
			}()

			NewRegistry().Register(tc.class, tc.handler)
		})
	}
}

func TestBuiltInOperationFailsClosed(t *testing.T) {
	t.Parallel()

	operation := &cwlcore.Operation{}
	operation.ID = "file:///w.cwl#approve"

	handler, _ := NewRegistry().Handler(Class(cwlcore.ClassOperation))

	result, err := Outcome(handler.Execute(t.Context(), &StepCall{StepID: stepID, Process: operation}))

	if !errors.Is(err, ErrOperationNotExecutable) {
		t.Fatalf("error = %v, want ErrOperationNotExecutable", err)
	}

	if result.Status != StatusPermanentFail {
		t.Fatalf("status = %q, want a permanent failure", result.Status)
	}

	// An Operation must never look like it produced data.
	if result.Outputs != nil {
		t.Fatalf("outputs = %v, want none", result.Outputs)
	}
}

func TestDescribe(t *testing.T) {
	t.Parallel()

	tool := newExpressionTool("$(inputs.x)")

	cases := []struct {
		call *StepCall
		name string
		want string
	}{
		{name: "nil call", call: nil, want: "no step"},
		{name: "no process", call: &StepCall{StepID: stepID}, want: `step "compute"`},
		{
			name: "step and process",
			call: &StepCall{StepID: stepID, Process: tool},
			want: `step "compute" running "` + toolID + `"`,
		},
		{
			name: "process run as its own step",
			call: &StepCall{StepID: toolID, Process: tool},
			want: `step "` + toolID + `"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := describe(tc.call); got != tc.want {
				t.Fatalf("describe = %q, want %q", got, tc.want)
			}
		})
	}
}

// assertNames fails unless err's message contains every one of wants.
func assertNames(t *testing.T, err error, wants ...string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err, want)
		}
	}
}
