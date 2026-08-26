package cwlcore

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// Run-reference resolution.
//
// The fixtures live under testdata/decode/runs and are deliberately spread over
// a subdirectory: a relative reference must resolve against the document that
// wrote it, and the only way to tell that apart from resolving against the
// process's working directory is to make the two disagree.

// runsPath locates a fixture under testdata/decode/runs.
func runsPath(name string) string {
	return filepath.Join("testdata", "decode", "runs", name)
}

// loadRuns loads a run fixture and fails the test if it does not load.
func loadRuns(t *testing.T, name string) *Workflow {
	t.Helper()

	process, err := LoadFile(t.Context(), runsPath(name))
	if err != nil {
		t.Fatalf("LoadFile(%s): %v", name, err)
	}

	wf, ok := process.(*Workflow)
	if !ok {
		t.Fatalf("%s loaded as %T, want a *Workflow", name, process)
	}

	return wf
}

// stepNamed returns the step whose identifier ends in name.
func stepNamed(t *testing.T, wf *Workflow, name string) *WorkflowStep {
	t.Helper()

	for i := range wf.Steps {
		id := wf.Steps[i].ID
		if id == name || strings.HasSuffix(id, "/"+name) || idFragment(id) == name {
			return &wf.Steps[i]
		}
	}

	t.Fatalf("the workflow declares no step %q", name)

	return nil
}

// runProcess returns the process a named step runs, failing when the reference
// was never followed.
func runProcess(t *testing.T, wf *Workflow, name string) Process {
	t.Helper()

	step := stepNamed(t, wf, name)
	if step.Run.Process == nil {
		t.Fatalf("step %q still has an unresolved run reference %q", name, step.Run.Ref)
	}

	return step.Run.Process
}

// errorTree renders an error's whole tree, so that an assertion can read what
// the leaves say rather than only the top line.
func errorTree(t *testing.T, err error) string {
	t.Helper()

	var tree *salad.Error
	if !errors.As(err, &tree) {
		return err.Error()
	}

	return tree.Pretty()
}

func TestDecodeLinksSiblingRunReferences(t *testing.T) {
	t.Parallel()

	// A packed $graph resolves without any I/O at all, which is why Decode
	// does this and not only Load.
	workflow, ok := decodeFixture(t, "workflow.cwl").(*Workflow)
	if !ok {
		t.Fatal("decoded process is not a *Workflow")
	}

	step := stepNamed(t, workflow, "step_one")
	if step.Run.Process == nil {
		t.Fatal(`the step running "#tool" was not linked to the tool declared alongside it`)
	}

	assertEqual(t, "linked class", step.Run.Process.Class(), ClassCommandLineTool)
	assertEqual(t, "linked id", step.Run.Process.Base().ID, "#tool")

	// The reference stays, because what a step pointed at is worth knowing
	// once the pointer itself no longer says.
	assertEqual(t, "Run.Ref", step.Run.Ref, "#tool")
	assertEqual(t, "Run.IsRef()", step.Run.IsRef(), true)
}

func TestDecodeLeavesExternalRunReferencesAlone(t *testing.T) {
	t.Parallel()

	// Decode does no I/O, so a reference to another document is left for Load
	// to follow rather than reported as an error.
	doc := parseDoc(t, runsPath("main.cwl"), string(fixtureSource(t, filepath.Join("runs", "main.cwl"))))

	process, err := Decode(doc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	workflow, ok := process.(*Workflow)
	if !ok {
		t.Fatal("decoded process is not a *Workflow")
	}

	step := stepNamed(t, workflow, "one")
	if step.Run.Process != nil {
		t.Error("Decode followed a reference into another document")
	}

	assertEqual(t, "Run.Ref", step.Run.Ref, "tool.cwl")
}

func TestLoadFollowsExternalRunReferences(t *testing.T) {
	t.Parallel()

	workflow := loadRuns(t, "main.cwl")

	direct := runProcess(t, workflow, "one")
	assertEqual(t, "one runs", direct.Class(), ClassCommandLineTool)

	// The second step runs a workflow in a subdirectory, which in turn runs
	// "../tool.cwl". That reference only resolves if it is taken relative to
	// the document that wrote it — relative to anything else it names a file
	// that does not exist.
	nested := runProcess(t, workflow, "two")
	assertEqual(t, "two runs", nested.Class(), ClassWorkflow)

	sub, ok := nested.(*Workflow)
	if !ok {
		t.Fatal("the nested step does not run a *Workflow")
	}

	leaf := runProcess(t, sub, "inner")
	assertEqual(t, "the nested workflow's step runs", leaf.Class(), ClassCommandLineTool)
}

func TestLoadFollowsRunReferencesFromAnyWorkingDirectory(t *testing.T) {
	t.Parallel()

	// Loading by absolute path is the same assertion as above without relying
	// on the test process's directory: every reference inside the document is
	// relative, so nothing resolves unless it resolves against the document.
	// (t.Chdir is deliberately not used — it mutates process state that the
	// rest of this package's parallel tests depend on.)
	absolute, err := filepath.Abs(runsPath("main.cwl"))
	if err != nil {
		t.Fatalf("resolving the fixture path: %v", err)
	}

	process, err := LoadFile(t.Context(), absolute)
	if err != nil {
		t.Fatalf("LoadFile(%s): %v", absolute, err)
	}

	workflow, ok := process.(*Workflow)
	if !ok {
		t.Fatalf("loaded %T, want a *Workflow", process)
	}

	sub, ok := runProcess(t, workflow, "two").(*Workflow)
	if !ok {
		t.Fatal("the nested step does not run a *Workflow")
	}

	assertEqual(t, "the nested workflow's step runs",
		runProcess(t, sub, "inner").Class(), ClassCommandLineTool)
}

func TestLoadFollowsARunReferenceWithAFragment(t *testing.T) {
	t.Parallel()

	workflow := loadRuns(t, "fragment.cwl")

	helper := runProcess(t, workflow, "helper")
	assertEqual(t, "helper runs", helper.Class(), ClassCommandLineTool)

	if got := idFragment(helper.Base().ID); got != "helper" {
		t.Errorf("the step runs %q, want the packed document's #helper", helper.Base().ID)
	}
}

func TestLoadLinksAPackedGraphWithoutTouchingTheFilesystemTwice(t *testing.T) {
	t.Parallel()

	workflow := loadRuns(t, "packed.cwl")

	helper := runProcess(t, workflow, "run_helper")
	assertEqual(t, "run_helper runs", helper.Class(), ClassCommandLineTool)
}

// TestLoadLinksSiblingsWhenTheGraphIsAddressedByFragment covers the shape a
// packed conformance document arrives in: "pack.cwl#main", where the member the
// fragment names runs a tool packed alongside it.
//
// Selecting one member used to decode that member alone, so nothing was ever
// indexed for its "#tool" reference to resolve against and the reference was
// reported as naming an object the document does not declare. The reference and
// the sibling's identifier were spelled identically all along — the index simply
// was not built.
func TestLoadLinksSiblingsWhenTheGraphIsAddressedByFragment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		fixture  string
		step     string
		fragment string
	}{
		// The entry point is not called "main", so a fragment is the only way
		// to reach it at all.
		{
			name:     "a member that is not the entry point",
			fixture:  "graph_fragment_runs.cwl",
			fragment: "#wf",
			step:     "speak",
		},
		// "#main" addressed explicitly rather than by the entry-point rule,
		// which is how cwltest spells every packed test.
		{
			name:     "the entry point named explicitly",
			fixture:  "packed.cwl",
			fragment: graphMainFragment,
			step:     "run_helper",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			uri := runsPath(tc.fixture) + tc.fragment

			process, err := LoadFile(t.Context(), uri)
			if err != nil {
				t.Fatalf("LoadFile(%s): %v", uri, err)
			}

			workflow, ok := process.(*Workflow)
			if !ok {
				t.Fatalf("%s loaded as %T, want a *Workflow", uri, process)
			}

			assertEqual(t, "the step runs", runProcess(t, workflow, tc.step).Class(), ClassCommandLineTool)
		})
	}
}

// TestLoadStillReportsAMissingSiblingInAFragmentedGraph pins the other side of
// the fix: linking more does not mean forgiving more. A reference into the same
// document that names nothing is still fatal, and still names the step.
func TestLoadStillReportsAMissingSiblingInAFragmentedGraph(t *testing.T) {
	t.Parallel()

	const src = `cwlVersion: v1.2
$graph:
  - id: "#wf"
    class: Workflow
    inputs: []
    outputs: []
    steps:
      - id: orphan
        run: "#absent"
        in: []
        out: []
`

	doc := parseDoc(t, "graph.cwl", src)

	_, err := decodeAndResolve(t.Context(), doc, "wf", nil)
	if err == nil {
		t.Fatal("a step running an undeclared sibling was accepted, want an error")
	}

	for _, want := range []string{"orphan", "#absent", "declares no object"} {
		if !strings.Contains(errorTree(t, err), want) {
			t.Errorf("error does not mention %q:\n%s", want, errorTree(t, err))
		}
	}
}

// TestDecodeRejectsACycleReachedThroughAFragment checks that selecting a member
// by fragment still runs the cycle check over what that member reaches, rather
// than only over a document's entry point.
func TestDecodeRejectsACycleReachedThroughAFragment(t *testing.T) {
	t.Parallel()

	const src = `cwlVersion: v1.2
$graph:
  - id: "#wf"
    class: Workflow
    inputs: []
    outputs: []
    steps:
      - id: recurse
        run: "#wf"
        in: []
        out: []
`

	_, err := decodeFragment(parseDoc(t, "graph.cwl", src), "wf")
	if err == nil {
		t.Fatal("a workflow reached by fragment that runs itself was accepted, want an error")
	}

	for _, want := range []string{"recurse", "runs itself"} {
		if !strings.Contains(errorTree(t, err), want) {
			t.Errorf("error does not mention %q:\n%s", want, errorTree(t, err))
		}
	}
}

func TestLoadLoadsAReferencedDocumentOnce(t *testing.T) {
	t.Parallel()

	// Two steps naming the same document must end up pointing at the same
	// process, which is the observable half of loading it once.
	workflow := loadRuns(t, "shared.cwl")

	first := runProcess(t, workflow, "first")
	second := runProcess(t, workflow, "second")

	if first != second {
		t.Error("two steps running the same document were given two different processes")
	}
}

func TestDecodeRejectsALocalRunCycle(t *testing.T) {
	t.Parallel()

	// A cycle inside one document needs no I/O to find, so Decode rejects it
	// too — and names the workflow, because here it has an identifier worth
	// printing rather than the blank node one decoding would have invented.
	const src = `cwlVersion: v1.2
$graph:
  - id: "#main"
    class: Workflow
    inputs: []
    outputs: []
    steps:
      - id: recurse
        run: "#main"
        in: []
        out: []
`

	_, err := Decode(parseDoc(t, "cycle.cwl", src))
	if err == nil {
		t.Fatal("Decode accepted a workflow that runs itself, want an error")
	}

	for _, want := range []string{"recurse", graphMainFragment, "runs itself"} {
		if !strings.Contains(errorTree(t, err), want) {
			t.Errorf("error does not mention %q:\n%s", want, errorTree(t, err))
		}
	}

	// DecodeAll checks every process it returns, not only an entry point.
	_, err = DecodeAll(parseDoc(t, "cycle.cwl", src))
	if err == nil {
		t.Error("DecodeAll accepted a workflow that runs itself, want an error")
	}
}

func TestLoadRejectsRunCycles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		fixture string
		steps   []string
	}{
		{name: "direct self reference", fixture: "self.cwl", steps: []string{"again"}},
		{name: "indirect cycle", fixture: "cycle_a.cwl", steps: []string{"to_b", "to_a"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assertRunCycle(t, tc.fixture, tc.steps)
		})
	}
}

// assertRunCycle checks that a fixture is rejected as a cycle, and that the
// error names every step on the path back to it — which is what makes an
// indirect cycle findable.
func assertRunCycle(t *testing.T, fixture string, steps []string) {
	t.Helper()

	_, err := LoadFile(t.Context(), runsPath(fixture))
	if err == nil {
		t.Fatal("LoadFile accepted a workflow that runs itself, want an error")
	}

	tree := errorTree(t, err)
	if !strings.Contains(tree, "runs itself") {
		t.Errorf("error does not say the workflow runs itself:\n%s", tree)
	}

	for _, step := range steps {
		if !strings.Contains(tree, step) {
			t.Errorf("error does not name the step %q:\n%s", step, tree)
		}
	}
}

func TestLoadReportsAnUnresolvableRunReference(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		fixture  string
		mentions []string
	}{
		{
			// The target exists but is not a CWL document, so link checking
			// passes it and this layer is what reports it.
			name:     "a target that cannot be decoded",
			fixture:  "broken.cwl",
			mentions: []string{"wrong", "notes.txt"},
		},
		{
			// A target that does not exist at all is caught earlier, by link
			// resolution, which names the field and the line as well.
			name:     "a target that does not exist",
			fixture:  "missing.cwl",
			mentions: []string{"absent-tool.cwl", keyRun},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assertUnresolvableRun(t, tc.fixture, tc.mentions)
		})
	}
}

// assertUnresolvableRun checks that a fixture is rejected and that the error
// mentions everything a reader would need to find the offending reference.
func assertUnresolvableRun(t *testing.T, fixture string, mentions []string) {
	t.Helper()

	_, err := LoadFile(t.Context(), runsPath(fixture))
	if err == nil {
		t.Fatal("LoadFile accepted an unresolvable run reference, want an error")
	}

	tree := errorTree(t, err)
	for _, want := range mentions {
		if !strings.Contains(tree, want) {
			t.Errorf("error does not mention %q:\n%s", want, tree)
		}
	}
}

func TestLoadReportsARunReferenceToAnUndeclaredObject(t *testing.T) {
	t.Parallel()

	// A reference that resolves to something the document declares but that
	// is not a process — here, another step — cannot be anywhere else either,
	// so it is reported without a second fetch being attempted.
	const src = `class: Workflow
cwlVersion: v1.2
id: "#main"
inputs: []
outputs: []
steps:
  - id: real
    run:
      class: Operation
      inputs: []
      outputs: []
    in: []
    out: []
  - id: orphan
    run: "#main/real"
    in: []
    out: []
`

	_, err := Load(t.Context(), []byte(src), "orphan.cwl")
	if err == nil {
		t.Fatal("Load accepted a step running an object the document does not declare")
	}

	if !strings.Contains(errorTree(t, err), "orphan") {
		t.Errorf("error does not name the offending step:\n%s", errorTree(t, err))
	}
}
