package cwlcore

import (
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// The dockerPull values the scoping tables use to identify which declaration
// won. Named constants because the tables repeat them.
const (
	pullWorkflow = "wf"
	pullStep     = "step"
	pullRun      = "run"
	pullReq      = "from-requirements"
	pullHint     = "from-hints"
)

// extClass stands in for an extension requirement class a downstream package
// would own, and scopeDocFile for the document the fixtures pretend to come
// from.
const (
	extClass     = "http://example.com/ns#TriggerRequirement"
	scopeDocFile = "wf.cwl"
)

// reqs and hints keep the scope literals below readable; enforce-slice-style
// forbids the bare composite literals that would otherwise appear inline.
func reqs(in ...ProcessRequirement) []ProcessRequirement {
	out := make([]ProcessRequirement, 0, len(in))

	return append(out, in...)
}

func hints(in ...Hint) []Hint {
	out := make([]Hint, 0, len(in))

	return append(out, in...)
}

func tool(r []ProcessRequirement, h []Hint) *CommandLineTool {
	return &CommandLineTool{ProcessBase: ProcessBase{ID: "#tool", Requirements: r, Hints: h}}
}

func workflow(r []ProcessRequirement, h []Hint) *Workflow {
	return &Workflow{ProcessBase: ProcessBase{ID: "#wf", Requirements: r, Hints: h}}
}

func operation(r []ProcessRequirement) *Operation {
	return &Operation{ProcessBase: ProcessBase{ID: "#op", Requirements: r}}
}

// classesOf renders a resolved list as its class names, which is what every
// ordering assertion below actually cares about.
func classesOf[T Hint](entries []T) string {
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Class())
	}

	return strings.Join(names, ",")
}

// assertDockerPull resolves DockerRequirement and checks which declaration won.
func assertDockerPull(t *testing.T, scope *RequirementScope, wantPull string, wantOrigin RequirementOrigin) {
	t.Helper()

	got, found, origin := scope.GetRequirement(ClassDockerRequirement)
	if !found {
		t.Fatal("GetRequirement() found = false, want true")
	}

	docker, ok := got.(*DockerRequirement)
	if !ok {
		t.Fatalf("GetRequirement() = %T, want *DockerRequirement", got)
	}

	if docker.DockerPull != wantPull {
		t.Errorf("dockerPull = %q, want %q", docker.DockerPull, wantPull)
	}

	if wantOrigin != OriginNone && origin != wantOrigin {
		t.Errorf("origin = %q, want %q", origin, wantOrigin)
	}
}

func TestGetRequirementRequirementsBeatHints(t *testing.T) {
	t.Parallel()

	asHint := &DockerRequirement{DockerPull: pullHint}
	asReq := &DockerRequirement{DockerPull: pullReq}

	tests := []struct {
		name   string
		scope  *RequirementScope
		want   string
		origin RequirementOrigin
	}{
		{"hint only", NewScope(tool(nil, hints(asHint))), pullHint, OriginHints},
		{"requirement only", NewScope(tool(reqs(asReq), nil)), pullReq, OriginRequirements},
		{"both on one process", NewScope(tool(reqs(asReq), hints(asHint))), pullReq, OriginRequirements},
		{
			"outer requirement beats inner hint",
			NewScope(workflow(reqs(asReq), nil)).PushProcess(tool(nil, hints(asHint))),
			pullReq,
			OriginRequirements,
		},
		{
			"inner requirement beats outer hint",
			NewScope(workflow(nil, hints(asHint))).PushProcess(tool(reqs(asReq), nil)),
			pullReq,
			OriginRequirements,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assertDockerPull(t, tt.scope, tt.want, tt.origin)
		})
	}
}

func TestGetRequirementInnerMostWins(t *testing.T) {
	t.Parallel()

	wfScope := NewScope(workflow(reqs(&DockerRequirement{DockerPull: pullWorkflow}), nil))
	stepScope := wfScope.Push(reqs(&DockerRequirement{DockerPull: pullStep}), nil)
	runScope := stepScope.PushProcess(tool(reqs(&DockerRequirement{DockerPull: pullRun}), nil))

	tests := []struct {
		name  string
		scope *RequirementScope
		want  string
	}{
		{"workflow only", wfScope, pullWorkflow},
		{"step beats workflow", stepScope, pullStep},
		{"embedded run beats step", runScope, pullRun},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assertDockerPull(t, tt.scope, tt.want, OriginRequirements)
		})
	}
}

func TestGetRequirementLastDeclarationInAFrameWins(t *testing.T) {
	t.Parallel()

	scope := NewScope(tool(reqs(
		&DockerRequirement{DockerPull: pullWorkflow},
		&DockerRequirement{DockerPull: pullRun},
	), nil))

	assertDockerPull(t, scope, pullRun, OriginRequirements)
}

// TestEffectiveRequirementsNoFieldMerge pins the rule most likely to be
// "helpfully" implemented wrong: an inner declaration replaces the outer one
// outright, so a field the outer set and the inner did not is gone, not
// inherited.
func TestEffectiveRequirementsNoFieldMerge(t *testing.T) {
	t.Parallel()

	outer := &ResourceRequirement{RAMMin: NewResourceInt(2048)}
	inner := &ResourceRequirement{CoresMin: NewResourceInt(4)}

	scope := NewScope(workflow(reqs(outer), nil)).PushProcess(tool(reqs(inner), nil))

	got, found, _ := scope.GetRequirement(ClassResourceRequirement)
	if !found {
		t.Fatal("GetRequirement() found = false, want true")
	}

	resource, ok := got.(*ResourceRequirement)
	if !ok {
		t.Fatalf("GetRequirement() = %T, want *ResourceRequirement", got)
	}

	if resource != inner {
		t.Error("GetRequirement() returned a merged value, want the inner declaration itself")
	}

	if resource.RAMMin.IsSet() {
		t.Errorf("ramMin = %v, want unset: the outer declaration must not survive", resource.RAMMin)
	}

	if want := int64(4); resource.CoresMin.Int() != want {
		t.Errorf("coresMin = %d, want %d", resource.CoresMin.Int(), want)
	}

	if effective := scope.EffectiveRequirements(); len(effective) != 1 || effective[0] != inner {
		t.Errorf("EffectiveRequirements() = %v, want exactly the inner declaration", effective)
	}
}

func TestEffectiveRequirementsOneEntryPerClass(t *testing.T) {
	t.Parallel()

	winner := &DockerRequirement{DockerPull: pullRun}
	scope := NewScope(workflow(reqs(
		&DockerRequirement{DockerPull: pullWorkflow},
		&ShellCommandRequirement{},
	), nil)).
		Push(reqs(&EnvVarRequirement{}), nil).
		PushProcess(tool(reqs(winner), nil))

	got := scope.EffectiveRequirements()

	// The winning DockerRequirement sits at its own position, inner-most, so a
	// backwards scan of this list agrees with GetRequirement.
	want := strings.Join([]string{
		ClassShellCommandRequirement,
		ClassEnvVarRequirement,
		ClassDockerRequirement,
	}, ",")
	if classesOf(got) != want {
		t.Errorf("EffectiveRequirements() = %v, want %v", classesOf(got), want)
	}

	if got[2] != ProcessRequirement(winner) {
		t.Errorf("EffectiveRequirements()[2] = %v, want the inner-most declaration", got[2])
	}
}

func TestEffectiveHintsKeepsDuplicatesInStableOrder(t *testing.T) {
	t.Parallel()

	scope := NewScope(workflow(nil, hints(
		&DockerRequirement{DockerPull: pullWorkflow},
		&DockerRequirement{DockerPull: pullStep},
	))).
		Push(nil, hints(&EnvVarRequirement{})).
		PushProcess(tool(nil, hints(&DockerRequirement{DockerPull: pullRun})))

	got := scope.EffectiveHints()

	want := strings.Join([]string{
		ClassDockerRequirement,
		ClassDockerRequirement,
		ClassEnvVarRequirement,
		ClassDockerRequirement,
	}, ",")
	if classesOf(got) != want {
		t.Fatalf("EffectiveHints() = %v, want %v", classesOf(got), want)
	}

	last, ok := got[3].(*DockerRequirement)
	if !ok || last.DockerPull != pullRun {
		t.Errorf("EffectiveHints() last entry = %v, want the inner-most declaration", got[3])
	}
}

// TestRawEntriesFlowThroughScoping checks that an extension class is scoped by
// exactly the same rules as a core one: no special case, no early rejection.
func TestRawEntriesFlowThroughScoping(t *testing.T) {
	t.Parallel()

	outer := &RawRequirement{ClassIRI: extClass, Node: salad.NewStringNode(salad.SourceLine{}, "outer")}
	inner := &RawRequirement{ClassIRI: extClass, Node: salad.NewStringNode(salad.SourceLine{}, "inner")}

	scope := NewScope(workflow(reqs(outer), hints(&RawHint{ClassIRI: extClass}))).
		PushProcess(operation(reqs(inner)))

	got, found, origin := scope.GetRequirement(extClass)
	if !found || origin != OriginRequirements {
		t.Fatalf("GetRequirement() found = %v, origin = %q, want true, %q", found, origin, OriginRequirements)
	}

	if got != ProcessRequirement(inner) {
		t.Errorf("GetRequirement() = %v, want the inner declaration", got)
	}

	if effective := scope.EffectiveRequirements(); len(effective) != 1 {
		t.Errorf("EffectiveRequirements() = %v, want one entry for the extension class", classesOf(effective))
	}
}

// TestRawHintSurfacesAsRawRequirement checks the one conversion GetRequirement
// performs: a hints entry of an unmodelled class has no ProcessRequirement type
// of its own, so it is handed back as an equivalent RawRequirement.
func TestRawHintSurfacesAsRawRequirement(t *testing.T) {
	t.Parallel()

	node := salad.NewStringNode(salad.SourceLine{File: scopeDocFile}, "hint")
	scope := NewScope(tool(nil, hints(&RawHint{ClassIRI: extClass, Node: node})))

	got, found, origin := scope.GetRequirement(extClass)
	if !found || origin != OriginHints {
		t.Fatalf("GetRequirement() found = %v, origin = %q, want true, %q", found, origin, OriginHints)
	}

	raw, ok := got.(*RawRequirement)
	if !ok {
		t.Fatalf("GetRequirement() = %T, want *RawRequirement", got)
	}

	if raw.ClassIRI != extClass || raw.Node != salad.Node(node) {
		t.Errorf("GetRequirement() = %+v, want the hint's class and node", raw)
	}
}

// otherHint is a Hint implementation from outside this package, standing in for
// one a downstream runtime supplies.
type otherHint struct {
	class string
}

func (h otherHint) Class() string { return h.class }

func TestForeignHintSurfacesAsRawRequirement(t *testing.T) {
	t.Parallel()

	scope := NewScope(tool(nil, hints(otherHint{class: extClass})))

	got, found, origin := scope.GetRequirement(extClass)
	if !found || origin != OriginHints {
		t.Fatalf("GetRequirement() found = %v, origin = %q, want true, %q", found, origin, OriginHints)
	}

	if got.Class() != extClass {
		t.Errorf("GetRequirement().Class() = %q, want %q", got.Class(), extClass)
	}
}

// assertEmptyScope checks that a scope resolves nothing and objects to nothing.
func assertEmptyScope(t *testing.T, scope *RequirementScope) {
	t.Helper()

	if _, found, origin := scope.GetRequirement(ClassDockerRequirement); found || origin != OriginNone {
		t.Errorf("GetRequirement() found = %v, origin = %q, want false, %q", found, origin, OriginNone)
	}

	if got := scope.EffectiveRequirements(); len(got) != 0 {
		t.Errorf("EffectiveRequirements() = %v, want empty", classesOf(got))
	}

	if got := scope.EffectiveHints(); len(got) != 0 {
		t.Errorf("EffectiveHints() = %v, want empty", classesOf(got))
	}

	err := scope.CheckKnown(nil)
	if err != nil {
		t.Errorf("CheckKnown() error = %v, want nil", err)
	}
}

func TestScopeEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		scope *RequirementScope
	}{
		{"zero value", &RequirementScope{}},
		{"nil process", NewScope(nil)},
		{"process with no requirements", NewScope(tool(nil, nil))},
		{"push onto an empty scope", (&RequirementScope{}).Push(nil, nil)},
		{"push a nil process", NewScope(tool(nil, nil)).PushProcess(nil)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assertEmptyScope(t, tt.scope)
		})
	}
}

// TestPushDoesNotMutateParent checks the immutability the API promises: one
// workflow scope is the parent of every step's scope, so a push must never be
// visible on the scope it was pushed from.
func TestPushDoesNotMutateParent(t *testing.T) {
	t.Parallel()

	parent := NewScope(workflow(reqs(&DockerRequirement{DockerPull: pullWorkflow}), nil))

	first := parent.Push(reqs(&EnvVarRequirement{}), nil)
	second := parent.Push(reqs(&ShellCommandRequirement{}), nil)

	if got := classesOf(parent.EffectiveRequirements()); got != ClassDockerRequirement {
		t.Errorf("parent EffectiveRequirements() = %v, want %v", got, ClassDockerRequirement)
	}

	wantFirst := ClassDockerRequirement + "," + ClassEnvVarRequirement
	if got := classesOf(first.EffectiveRequirements()); got != wantFirst {
		t.Errorf("first EffectiveRequirements() = %v, want %v", got, wantFirst)
	}

	wantSecond := ClassDockerRequirement + "," + ClassShellCommandRequirement
	if got := classesOf(second.EffectiveRequirements()); got != wantSecond {
		t.Errorf("second EffectiveRequirements() = %v, want %v", got, wantSecond)
	}
}

// TestEffectiveRequirementsBackwardsScanAgreesWithGetRequirement pins the
// ordering contract EffectiveRequirements documents.
func TestEffectiveRequirementsBackwardsScanAgreesWithGetRequirement(t *testing.T) {
	t.Parallel()

	scope := NewScope(workflow(reqs(
		&DockerRequirement{DockerPull: pullWorkflow},
		&ResourceRequirement{RAMMin: NewResourceInt(1)},
	), nil)).
		PushProcess(tool(reqs(&DockerRequirement{DockerPull: pullRun}), nil))

	for _, class := range []string{ClassDockerRequirement, ClassResourceRequirement} {
		want, found, _ := scope.GetRequirement(class)
		if !found {
			t.Fatalf("GetRequirement(%q) found = false, want true", class)
		}

		got, ok := lastOfClass(scope.EffectiveRequirements(), class)
		if !ok || got != want {
			t.Errorf("EffectiveRequirements() winner for %q = %v, want %v", class, got, want)
		}
	}
}
