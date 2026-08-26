package cwlcore

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// allCoreRequirements is every core class, as a fresh value per class, so the
// inheritance-validity table can push the whole vocabulary through a scope at
// once.
func allCoreRequirements() []ProcessRequirement {
	return reqs(
		&InlineJavascriptRequirement{},
		&SchemaDefRequirement{},
		&LoadListingRequirement{},
		&DockerRequirement{},
		&SoftwareRequirement{},
		&InitialWorkDirRequirement{},
		&EnvVarRequirement{},
		&ShellCommandRequirement{},
		&ResourceRequirement{},
		&WorkReuse{},
		&NetworkAccess{},
		&InplaceUpdateRequirement{},
		&ToolTimeLimit{},
		&SubworkflowFeatureRequirement{},
		&ScatterFeatureRequirement{},
		&MultipleInputFeatureRequirement{},
		&StepInputExpressionRequirement{},
	)
}

// workflowOnly is the four classes concepts.md leaves out of the list a
// CommandLineTool inherits.
var workflowOnly = []string{
	ClassSubworkflowFeatureRequirement,
	ClassScatterFeatureRequirement,
	ClassMultipleInputFeatureRequirement,
	ClassStepInputExpressionRequirement,
}

// TestInheritanceValidityFilterCommandLineTool checks the spec's list from both
// sides: all thirteen valid classes reach a tool from an enclosing workflow,
// and none of the four workflow-only ones do.
func TestInheritanceValidityFilterCommandLineTool(t *testing.T) {
	t.Parallel()

	scope := NewScope(workflow(allCoreRequirements(), nil)).PushProcess(tool(nil, nil))

	for _, req := range allCoreRequirements() {
		class := req.Class()

		t.Run(class, func(t *testing.T) {
			t.Parallel()

			_, found, _ := scope.GetRequirement(class)
			if want := !slices.Contains(workflowOnly, class); found != want {
				t.Errorf("GetRequirement(%q) found = %v, want %v", class, found, want)
			}
		})
	}

	if got, want := len(scope.EffectiveRequirements()), len(commandLineToolRequirements); got != want {
		t.Errorf("EffectiveRequirements() length = %d, want %d", got, want)
	}
}

func TestInheritanceValidityFilterAppliesToHints(t *testing.T) {
	t.Parallel()

	scope := NewScope(workflow(nil, hints(&ScatterFeatureRequirement{}, &DockerRequirement{}))).
		PushProcess(tool(nil, nil))

	if _, found, _ := scope.GetRequirement(ClassScatterFeatureRequirement); found {
		t.Error("GetRequirement(ScatterFeatureRequirement) found = true, want false")
	}

	if _, found, _ := scope.GetRequirement(ClassDockerRequirement); !found {
		t.Error("GetRequirement(DockerRequirement) found = false, want true")
	}

	if got, want := classesOf(scope.EffectiveHints()), ClassDockerRequirement; got != want {
		t.Errorf("EffectiveHints() = %v, want %v", got, want)
	}
}

// TestInheritanceValidityFilterOnlyRestrictsCommandLineTool pins the deliberate
// narrowness of the rule: concepts.md restricts CommandLineTool substeps and
// nothing else, and neither does this package.
func TestInheritanceValidityFilterOnlyRestrictsCommandLineTool(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		inner Process
		want  bool
	}{
		{"CommandLineTool filters", tool(nil, nil), false},
		{"ExpressionTool inherits", &ExpressionTool{}, true},
		{"Operation inherits", operation(nil), true},
		{"Workflow inherits", workflow(nil, nil), true},
		{"extension class inherits", &RawProcess{ClassIRI: "http://example.com/ns#Agent"}, true},
		{"classless step frame inherits", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			scope := NewScope(workflow(reqs(&ScatterFeatureRequirement{}), nil))
			if tt.inner == nil {
				scope = scope.Push(nil, nil)
			} else {
				scope = scope.PushProcess(tt.inner)
			}

			if _, found, _ := scope.GetRequirement(ClassScatterFeatureRequirement); found != tt.want {
				t.Errorf("GetRequirement() found = %v, want %v", found, tt.want)
			}
		})
	}
}

// TestInheritanceValidityFilterSparesOwnDeclarations checks that the rule
// reaches only inherited entries. A tool that declares a workflow-only class on
// itself is not inheriting it from anywhere, so the scope still reports it and
// CheckKnown-style callers can diagnose it themselves.
func TestInheritanceValidityFilterSparesOwnDeclarations(t *testing.T) {
	t.Parallel()

	scope := NewScope(tool(reqs(&ScatterFeatureRequirement{}), nil))
	if _, found, _ := scope.GetRequirement(ClassScatterFeatureRequirement); !found {
		t.Error("GetRequirement() found = false, want true for the tool's own declaration")
	}
}

// TestInheritanceValidityFilterKeepsExtensions checks the carve-out: the spec's
// list enumerates core classes, so an extension requirement still propagates
// into a tool.
func TestInheritanceValidityFilterKeepsExtensions(t *testing.T) {
	t.Parallel()

	scope := NewScope(workflow(reqs(&RawRequirement{ClassIRI: extClass}), nil)).PushProcess(tool(nil, nil))
	if _, found, _ := scope.GetRequirement(extClass); !found {
		t.Errorf("GetRequirement(%q) found = false, want true", extClass)
	}
}

func TestIsCoreRequirement(t *testing.T) {
	t.Parallel()

	for _, r := range allCoreRequirements() {
		if !IsCoreRequirement(r.Class()) {
			t.Errorf("IsCoreRequirement(%q) = false, want true", r.Class())
		}
	}

	if got := len(coreRequirements); got != len(allCoreRequirements()) {
		t.Errorf("coreRequirements has %d entries, want %d", got, len(allCoreRequirements()))
	}

	negatives := []string{
		"",
		"CommandLineTool",
		"DockerRequirements",
		"dockerrequirement",
		"http://commonwl.org/cwltool#TimeLimit",
	}
	for _, class := range negatives {
		if IsCoreRequirement(class) {
			t.Errorf("IsCoreRequirement(%q) = true, want false", class)
		}
	}
}

func TestCheckKnown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		scope   *RequirementScope
		allow   map[string]bool
		wantErr bool
	}{
		{"empty scope", &RequirementScope{}, nil, false},
		{"core requirement", NewScope(tool(reqs(&DockerRequirement{}), nil)), nil, false},
		{"unknown requirement", NewScope(tool(reqs(&RawRequirement{ClassIRI: extClass}), nil)), nil, true},
		{
			"unknown requirement vouched for",
			NewScope(tool(reqs(&RawRequirement{ClassIRI: extClass}), nil)),
			map[string]bool{extClass: true},
			false,
		},
		{
			"unknown requirement vouched false",
			NewScope(tool(reqs(&RawRequirement{ClassIRI: extClass}), nil)),
			map[string]bool{extClass: false},
			true,
		},
		{"unknown hint", NewScope(tool(nil, hints(&RawHint{ClassIRI: extClass}))), nil, false},
		{
			"unknown requirement on an outer frame",
			NewScope(workflow(reqs(&RawRequirement{ClassIRI: extClass}), nil)).PushProcess(tool(nil, nil)),
			nil,
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.scope.CheckKnown(tt.allow)
			if (err != nil) != tt.wantErr {
				t.Fatalf("CheckKnown() error = %v, wantErr %v", err, tt.wantErr)
			}

			if err != nil && !strings.Contains(err.Error(), extClass) {
				t.Errorf("CheckKnown() error = %q, want it to name %q", err, extClass)
			}
		})
	}
}

// TestCheckKnownNamesFirstOffenderWithLocation checks that the fatal error
// points at the offending declaration, in scope order.
func TestCheckKnownNamesFirstOffenderWithLocation(t *testing.T) {
	t.Parallel()

	loc := salad.SourceLine{File: scopeDocFile, Start: salad.Position{Line: 7, Column: 3}}
	outer := &RawRequirement{ClassIRI: "ex:First", Node: salad.NewStringNode(loc, "ex:First")}
	inner := &RawRequirement{ClassIRI: "ex:Second"}

	scope := NewScope(workflow(reqs(outer), nil)).PushProcess(operation(reqs(inner)))

	err := scope.CheckKnown(nil)
	if err == nil {
		t.Fatal("CheckKnown() error = nil, want an error")
	}

	var serr *salad.Error
	if !errors.As(err, &serr) {
		t.Fatalf("CheckKnown() error is %T, want *salad.Error", err)
	}

	if serr.Warning {
		t.Error("CheckKnown() returned a warning, want a fatal error by default")
	}

	if !strings.Contains(err.Error(), "ex:First") || strings.Contains(err.Error(), "ex:Second") {
		t.Errorf("CheckKnown() error = %q, want only the first offender named", err)
	}

	if serr.Loc != loc {
		t.Errorf("CheckKnown() error loc = %v, want %v", serr.Loc, loc)
	}
}

// twoOffenderScope declares one unrecognized requirement on a workflow and
// another on the process beneath it, the second carrying a source line.
func twoOffenderScope() (*RequirementScope, salad.SourceLine) {
	loc := salad.SourceLine{File: scopeDocFile, Start: salad.Position{Line: 12, Column: 5}}
	inner := &RawRequirement{ClassIRI: "ex:Second", Node: salad.NewStringNode(loc, "ex:Second")}

	scope := NewScope(workflow(reqs(&RawRequirement{ClassIRI: "ex:First"}), nil)).
		PushProcess(operation(reqs(inner)))

	return scope, loc
}

// TestCheckKnownWithLenientReturnsNil pins the contract that makes WithLenient
// an override at all: it must not come back as an error, or the ordinary
// err != nil call site fails the run the user asked to proceed with.
func TestCheckKnownWithLenientReturnsNil(t *testing.T) {
	t.Parallel()

	scope, _ := twoOffenderScope()

	err := scope.CheckKnown(nil, WithLenient())
	if err != nil {
		t.Errorf("CheckKnown(WithLenient()) error = %v, want nil", err)
	}
}

// runIfPermitted is the call site cwlexec will write: refuse the run when
// CheckKnown objects, proceed otherwise. Nothing here inspects what kind of
// error came back, because no caller should have to.
func runIfPermitted(scope *RequirementScope, opts ...CheckOption) string {
	err := scope.CheckKnown(nil, opts...)
	if err != nil {
		return "permanentFail"
	}

	return "success"
}

// TestCheckKnownAtTheCallSite drives that call site both ways over a document
// declaring a requirement this implementation has never heard of. The default
// must stop the run; WithLenient must let it through.
func TestCheckKnownAtTheCallSite(t *testing.T) {
	t.Parallel()

	scope, _ := twoOffenderScope()

	if got := runIfPermitted(scope); got != "permanentFail" {
		t.Errorf("default check = %q, want %q", got, "permanentFail")
	}

	if got := runIfPermitted(scope, WithLenient()); got != "success" {
		t.Errorf("lenient check = %q, want %q", got, "success")
	}
}

// TestCheckKnownWarnFunc checks that leniency loses nothing when a sink is
// supplied: every offender arrives, flagged as a warning, in scope order, with
// its source line intact.
func TestCheckKnownWarnFunc(t *testing.T) {
	t.Parallel()

	scope, loc := twoOffenderScope()

	warned := make([]*salad.Error, 0, 2)
	sink := func(e *salad.Error) { warned = append(warned, e) }

	err := scope.CheckKnown(nil, WithLenient(), WithWarnFunc(sink))
	if err != nil {
		t.Fatalf("CheckKnown() error = %v, want nil", err)
	}

	if len(warned) != 2 {
		t.Fatalf("sink received %d findings, want 2", len(warned))
	}

	for _, e := range warned {
		if !e.Warning {
			t.Errorf("finding %q Warning = false, want true", e.Msg)
		}
	}

	if !strings.Contains(warned[0].Msg, "ex:First") || !strings.Contains(warned[1].Msg, "ex:Second") {
		t.Errorf("sink received %q then %q, want ex:First then ex:Second", warned[0].Msg, warned[1].Msg)
	}

	if warned[1].Loc != loc {
		t.Errorf("second finding loc = %v, want %v", warned[1].Loc, loc)
	}
}

// TestCheckKnownWarnFuncWithoutLenient pins the documented rule that the sink
// observes downgraded findings and nothing else: a fatal check reports through
// its returned error and leaves the sink alone.
func TestCheckKnownWarnFuncWithoutLenient(t *testing.T) {
	t.Parallel()

	scope, _ := twoOffenderScope()

	called := 0
	sink := func(_ *salad.Error) { called++ }

	err := scope.CheckKnown(nil, WithWarnFunc(sink))
	if err == nil {
		t.Fatal("CheckKnown() error = nil, want the fatal error")
	}

	if !strings.Contains(err.Error(), "ex:First") {
		t.Errorf("CheckKnown() error = %q, want the first offender named", err)
	}

	if called != 0 {
		t.Errorf("sink called %d times, want 0 without WithLenient", called)
	}
}

// TestCheckKnownLenientClean checks that leniency does not invent a finding
// when there is nothing to warn about.
func TestCheckKnownLenientClean(t *testing.T) {
	t.Parallel()

	scope := NewScope(tool(reqs(&DockerRequirement{}), nil))

	called := 0
	sink := func(_ *salad.Error) { called++ }

	err := scope.CheckKnown(nil, WithLenient(), WithWarnFunc(sink))
	if err != nil || called != 0 {
		t.Errorf("CheckKnown() error = %v, sink calls = %d, want nil, 0", err, called)
	}
}
