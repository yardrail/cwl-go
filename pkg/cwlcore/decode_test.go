package cwlcore

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// Decoding tests build their documents with salad.Parse rather than by loading
// the CWL schema, because Decode is defined over an already validated tree and
// the schema loader is a separate concern (see decode_schema_test.go). Parsing
// alone reproduces everything Decode actually reads, with two documented
// exceptions the decoders handle themselves: the identifier-map form of an array
// field, and the short spelling of a class discriminator.

// fixtureDoc parses a fixture under testdata/decode into a document.
func fixtureDoc(t *testing.T, name string) *salad.Document {
	t.Helper()

	return parseDoc(t, fixturePath(name), string(fixtureSource(t, name)))
}

// parseDoc parses src into a document rooted at name.
func parseDoc(t *testing.T, name, src string) *salad.Document {
	t.Helper()

	root, err := salad.Parse(name, []byte(src))
	if err != nil {
		t.Fatalf("parsing %s: %v", name, err)
	}

	return &salad.Document{Root: root, BaseURI: name}
}

// decodeFixture decodes a fixture and fails the test if decoding does not
// succeed.
func decodeFixture(t *testing.T, name string) Process {
	t.Helper()

	process, err := Decode(fixtureDoc(t, name))
	if err != nil {
		t.Fatalf("Decode(%s): %v", name, err)
	}

	return process
}

// decodeSource decodes an inline document and fails the test if decoding does
// not succeed.
func decodeSource(t *testing.T, src string) Process {
	t.Helper()

	process, err := Decode(parseDoc(t, "inline.cwl", src))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	return process
}

// mustCommandLineTool decodes an inline document expected to be a tool.
func mustCommandLineTool(t *testing.T, src string) *CommandLineTool {
	t.Helper()

	tool, ok := decodeSource(t, src).(*CommandLineTool)
	if !ok {
		t.Fatal("decoded process is not a *CommandLineTool")
	}

	return tool
}

func TestDecodeDispatchesOnClass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want    Process
		fixture string
		class   string
	}{
		{fixture: "command_line_tool.cwl", want: &CommandLineTool{}, class: ClassCommandLineTool},
		{fixture: "workflow.cwl", want: &Workflow{}, class: ClassWorkflow},
		{fixture: "expression_tool.cwl", want: &ExpressionTool{}, class: ClassExpressionTool},
		{fixture: "operation.cwl", want: &Operation{}, class: ClassOperation},
	}

	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			process := decodeFixture(t, tc.fixture)

			if got, want := typeName(process), typeName(tc.want); got != want {
				t.Fatalf("decoded %s, want %s", got, want)
			}

			if got := process.Class(); got != tc.class {
				t.Errorf("Class() = %q, want %q", got, tc.class)
			}

			if got := process.Base().CWLVersion; got != CWLVersionV12 {
				t.Errorf("CWLVersion = %q, want %q", got, CWLVersionV12)
			}
		})
	}
}

// typeName renders a process's concrete Go type for a test failure message.
func typeName(p Process) string {
	switch p.(type) {
	case *CommandLineTool:
		return "*CommandLineTool"
	case *Workflow:
		return "*Workflow"
	case *ExpressionTool:
		return "*ExpressionTool"
	case *Operation:
		return "*Operation"
	case *RawProcess:
		return "*RawProcess"
	default:
		return "unknown"
	}
}

func TestDecodeCommandLineTool(t *testing.T) {
	t.Parallel()

	tool, ok := decodeFixture(t, "command_line_tool.cwl").(*CommandLineTool)
	if !ok {
		t.Fatal("decoded process is not a *CommandLineTool")
	}

	assertEqual(t, "ID", tool.ID, "#echo")
	assertEqual(t, "Label", tool.Label, "echo a message")
	assertEqual(t, "Stdin", string(tool.Stdin), "in.txt")
	assertEqual(t, "Stdout", string(tool.Stdout), "out.txt")
	assertEqual(t, "Stderr", string(tool.Stderr), "err.txt")
	assertEqual(t, "len(Doc)", len(tool.Doc), 2)
	assertEqual(t, "Intent[0]", tool.Intent[0], "http://edamontology.org/operation_0004")
	assertEqual(t, "BaseCommand", strings.Join(tool.BaseCommand, " "), "echo -n")
	assertEqual(t, "SuccessCodes[1]", tool.SuccessCodes[1], 7)
	assertEqual(t, "TemporaryFailCodes[0]", tool.TemporaryFailCodes[0], 3)
	assertEqual(t, "PermanentFailCodes[0]", tool.PermanentFailCodes[0], 1)
	assertEqual(t, "len(Inputs)", len(tool.Inputs), 2)
	assertEqual(t, "len(Outputs)", len(tool.Outputs), 2)
	assertEqual(t, "len(Requirements)", len(tool.Requirements), 3)
	assertEqual(t, "len(Hints)", len(tool.Hints), 1)
}

func TestDecodeCommandLineToolParameters(t *testing.T) {
	t.Parallel()

	tool, ok := decodeFixture(t, "command_line_tool.cwl").(*CommandLineTool)
	if !ok {
		t.Fatal("decoded process is not a *CommandLineTool")
	}

	message := tool.Inputs[0]
	assertEqual(t, "Inputs[0].ID()", message.ID(), "message")
	assertEqual(t, "Inputs[0].Type", message.Type.Name(), PrimitiveString)
	assertEqual(t, "Inputs[0].InputBinding.Prefix", message.InputBinding.Prefix, "--msg")
	assertEqual(t, "Inputs[0].InputBinding.Position", message.InputBinding.Position.Int(), int64(1))

	reference := tool.Inputs[1]
	assertEqual(t, "Inputs[1].Type.IsOptional()", reference.Type.IsOptional(), true)
	assertEqual(t, "Inputs[1].LoadContents", reference.LoadContents, true)
	assertEqual(t, "Inputs[1].LoadListing", reference.LoadListing, LoadListingDeep)
	assertEqual(t, "Inputs[1].Streamable", reference.Streamable, true)
	assertEqual(t, "len(Inputs[1].Format)", len(reference.Format), 1)
	assertEqual(t, "Inputs[1].SecondaryFiles[0].Pattern", string(reference.SecondaryFiles[0].Pattern), ".fai")
	assertEqual(t, "Inputs[1].SecondaryFiles[0].Required", reference.SecondaryFiles[0].Required.Bool(), true)

	if reference.Node == nil {
		t.Error("Inputs[1].Node is nil, want the node the parameter was decoded from")
	}

	assertEqual(t, "Outputs[0].Type.Kind()", tool.Outputs[0].Type.Kind(), TypeKindStdout)

	binding := tool.Outputs[1].OutputBinding
	assertEqual(t, "Outputs[1].OutputBinding.Glob[0]", string(binding.Glob[0]), "*.log")
	assertEqual(t, "Outputs[1].OutputBinding.OutputEval", string(binding.OutputEval), "$(self[0])")
	assertEqual(t, "Outputs[1].OutputBinding.LoadContents", binding.LoadContents, true)
	assertEqual(t, "Outputs[1].OutputBinding.LoadListing", binding.LoadListing, LoadListingShallow)
}

func TestDecodeCommandLineToolArguments(t *testing.T) {
	t.Parallel()

	tool, ok := decodeFixture(t, "command_line_tool.cwl").(*CommandLineTool)
	if !ok {
		t.Fatal("decoded process is not a *CommandLineTool")
	}

	assertEqual(t, "len(Arguments)", len(tool.Arguments), 3)
	assertEqual(t, "Arguments[0].Kind()", tool.Arguments[0].Kind(), ValueString)
	assertEqual(t, "Arguments[0].Literal()", tool.Arguments[0].Literal(), "--literal")
	assertEqual(t, "Arguments[1].Kind()", tool.Arguments[1].Kind(), ValueExpression)
	assertEqual(t, "Arguments[1].Expression()", string(tool.Arguments[1].Expression()), "$(inputs.message)")
	assertEqual(t, "Arguments[2].Kind()", tool.Arguments[2].Kind(), ValueBinding)

	binding := tool.Arguments[2].Binding()
	assertEqual(t, "Arguments[2].Binding().Prefix", binding.Prefix, "--tagged")
	assertEqual(t, "Arguments[2].Binding().Position", binding.Position.Int(), int64(3))
	assertEqual(t, "Arguments[2].Binding().Separate.Or(true)", binding.Separate.Or(true), false)
	assertEqual(t, "Arguments[2].Binding().ShellQuote.Or(true)", binding.ShellQuote.Or(true), false)
}

func TestDecodeWorkflow(t *testing.T) {
	t.Parallel()

	workflow, ok := decodeFixture(t, "workflow.cwl").(*Workflow)
	if !ok {
		t.Fatal("decoded process is not a *Workflow")
	}

	assertEqual(t, "ID", workflow.ID, graphMainFragment)
	assertEqual(t, "len(Inputs)", len(workflow.Inputs), 2)
	assertEqual(t, "Inputs[0].ID()", workflow.Inputs[0].ID(), "files")
	assertEqual(t, "Inputs[0].Type.Kind()", workflow.Inputs[0].Type.Kind(), TypeKindArray)
	assertEqual(t, "Inputs[1].ID()", workflow.Inputs[1].ID(), "threads")
	assertEqual(t, "Inputs[1].Type.Name()", workflow.Inputs[1].Type.Name(), PrimitiveInt)

	merged := workflow.Outputs[0]
	assertEqual(t, "Outputs[0].ID()", merged.ID(), "merged")
	assertEqual(t, "Outputs[0].OutputSource", strings.Join(merged.OutputSource, ","), "step_two/out,step_one/out")
	assertEqual(t, "Outputs[0].LinkMerge", merged.LinkMerge, LinkMergeFlattened)
	assertEqual(t, "Outputs[0].PickValue", merged.PickValue, PickAllNonNull)

	// The identifier-map form of requirements is normalized into the slice,
	// with the map key becoming each entry's class.
	assertEqual(t, "len(Requirements)", len(workflow.Requirements), 2)
	assertEqual(t, "Requirements[0].Class()", workflow.Requirements[0].Class(), ClassMultipleInputFeatureRequirement)
	assertEqual(t, "Requirements[1].Class()", workflow.Requirements[1].Class(), ClassScatterFeatureRequirement)
}

func TestDecodeWorkflowSteps(t *testing.T) {
	t.Parallel()

	workflow, ok := decodeFixture(t, "workflow.cwl").(*Workflow)
	if !ok {
		t.Fatal("decoded process is not a *Workflow")
	}

	assertEqual(t, "len(Steps)", len(workflow.Steps), 2)

	first := workflow.Steps[0]
	assertEqual(t, "Steps[0].ID", first.ID, "step_one")
	assertEqual(t, "Steps[0].Run.IsRef()", first.Run.IsRef(), true)
	assertEqual(t, "Steps[0].Run.Ref", first.Run.Ref, "#tool")
	assertEqual(t, "Steps[0].Scatter", strings.Join(first.Scatter, ","), "files")
	assertEqual(t, "Steps[0].ScatterMethod", first.ScatterMethod, ScatterDotProduct)
	assertEqual(t, "Steps[0].When", string(first.When), "$(inputs.threads > 0)")
	assertEqual(t, "Steps[0].Out[0].ID", first.Out[0].ID, "out")

	// The step's `in` map is keyed by id, and a bare value becomes the
	// mapPredicate field, which for a step input is `source`.
	assertEqual(t, "len(Steps[0].In)", len(first.In), 2)
	assertEqual(t, "Steps[0].In[0].ID", first.In[0].ID, "files")
	assertEqual(t, "Steps[0].In[0].Source", strings.Join(first.In[0].Source, ","), "files")

	threads := first.In[1]
	assertEqual(t, "Steps[0].In[1].ValueFrom", string(threads.ValueFrom), "$(self)")
	assertEqual(t, "Steps[0].In[1].LinkMerge", threads.LinkMerge, LinkMergeNested)
	assertEqual(t, "Steps[0].In[1].PickValue", threads.PickValue, PickFirstNonNull)
	assertEqual(t, "Steps[0].In[1].LoadContents", threads.LoadContents, true)

	if threads.Default == nil {
		t.Error("Steps[0].In[1].Default is nil, want the salad node the document wrote")
	}

	second := workflow.Steps[1]
	assertEqual(t, "Steps[1].Run.IsRef()", second.Run.IsRef(), false)
	assertEqual(t, "Steps[1].Out[0].ID", second.Out[0].ID, "out")

	inline, ok := second.Run.Process.(*ExpressionTool)
	if !ok {
		t.Fatal("Steps[1].Run.Process is not a *ExpressionTool")
	}

	assertEqual(t, "Steps[1] inline Expression", string(inline.Expression), "${return {out: inputs.x};}")
}

func TestDecodeExpressionToolAndOperation(t *testing.T) {
	t.Parallel()

	tool, ok := decodeFixture(t, "expression_tool.cwl").(*ExpressionTool)
	if !ok {
		t.Fatal("decoded process is not a *ExpressionTool")
	}

	assertEqual(t, "Expression", string(tool.Expression), "${return {picked: inputs.candidates[0]};}")
	assertEqual(t, "Inputs[0].Type.Kind()", tool.Inputs[0].Type.Kind(), TypeKindArray)
	assertEqual(t, "Outputs[0].ID()", tool.Outputs[0].ID(), "picked")

	if tool.Inputs[0].Default == nil {
		t.Error("Inputs[0].Default is nil, want the salad node the document wrote")
	}

	operation, ok := decodeFixture(t, "operation.cwl").(*Operation)
	if !ok {
		t.Fatal("decoded process is not a *Operation")
	}

	assertEqual(t, "Operation ID", operation.ID, "#align")
	assertEqual(t, "Operation Intent[0]", operation.Intent[0], "http://example.org/ontology#Align")
	assertEqual(t, "Operation Inputs[0].ID()", operation.Inputs[0].ID(), "reads")
	assertEqual(t, "Operation Outputs[0].ID()", operation.Outputs[0].ID(), "alignment")
}

func TestDecodeExtensionClassBecomesRawProcess(t *testing.T) {
	t.Parallel()

	raw, ok := decodeFixture(t, "extension.cwl").(*RawProcess)
	if !ok {
		t.Fatal("decoded process is not a *RawProcess")
	}

	// Decode carries the class discriminator exactly as resolution left it, so
	// a prefixed name in an unresolved tree stays prefixed. The point of the
	// assertion is that the class reaches the caller unchanged and unjudged.
	assertEqual(t, "ClassIRI", raw.ClassIRI, "ex:CustomTool")
	assertEqual(t, "Class()", raw.Class(), raw.ClassIRI)
	assertEqual(t, "ID", raw.ID, "#custom")
	assertEqual(t, "Label", raw.Label, "an extension process")
	assertEqual(t, "Inputs[0].ID()", raw.Inputs[0].ID(), "prompt")
	assertEqual(t, "Inputs[0].Type.Name()", raw.Inputs[0].Type.Name(), PrimitiveString)
	assertEqual(t, "Outputs[0].ID()", raw.Outputs[0].ID(), "answer")

	if raw.Node == nil {
		t.Fatal("Node is nil, want the complete validated node for the process")
	}

	// The class-specific fields are reachable through Node, which is the whole
	// contract the extension point offers.
	node, isMap := salad.AsMap(raw.Node)
	if !isMap {
		t.Fatal("Node is not a mapping")
	}

	if !node.Has("customField") {
		t.Error("Node does not carry the extension's own fields")
	}
}

func TestDecodeExtensionClassSpelledAsIRI(t *testing.T) {
	t.Parallel()

	// The same process after identifier resolution has expanded the prefix.
	const src = `class: http://example.org/ext#CustomTool
cwlVersion: v1.2
inputs: []
outputs: []
`

	raw, ok := decodeSource(t, src).(*RawProcess)
	if !ok {
		t.Fatal("decoded process is not a *RawProcess")
	}

	assertEqual(t, "ClassIRI", raw.ClassIRI, "http://example.org/ext#CustomTool")
}

func TestDecodeUnknownRequirementsAndHints(t *testing.T) {
	t.Parallel()

	raw, ok := decodeFixture(t, "extension.cwl").(*RawProcess)
	if !ok {
		t.Fatal("decoded process is not a *RawProcess")
	}

	requirement, ok := raw.Requirements[0].(*RawRequirement)
	if !ok {
		t.Fatalf("Requirements[0] is %T, want *RawRequirement", raw.Requirements[0])
	}

	assertEqual(t, "RawRequirement.Class()", requirement.Class(), "ex:CustomRequirement")

	if requirement.Node == nil {
		t.Error("RawRequirement.Node is nil, want the complete node")
	}

	hint, ok := raw.Hints[0].(*RawHint)
	if !ok {
		t.Fatalf("Hints[0] is %T, want *RawHint", raw.Hints[0])
	}

	assertEqual(t, "RawHint.Class()", hint.Class(), "ex:CustomHint")

	if hint.Node == nil {
		t.Error("RawHint.Node is nil, want the complete node")
	}
}

func TestDecodeHintsNeverFail(t *testing.T) {
	t.Parallel()

	// WorkflowStep.hints is typed Any[] by the schema, so an entry that is not
	// even a mapping is schema-valid and must not stop decoding.
	const src = `class: Workflow
cwlVersion: v1.2
inputs: []
outputs: []
steps:
  - id: only
    run: "#tool"
    in: []
    out: []
    hints:
      - just a string
      - class: DockerRequirement
        dockerPull: "alpine:3"
`

	workflow, ok := decodeSource(t, src).(*Workflow)
	if !ok {
		t.Fatal("decoded process is not a *Workflow")
	}

	hints := workflow.Steps[0].Hints
	assertEqual(t, "len(Hints)", len(hints), 2)

	bare, ok := hints[0].(*RawHint)
	if !ok {
		t.Fatalf("Hints[0] is %T, want *RawHint", hints[0])
	}

	assertEqual(t, "Hints[0].Class()", bare.Class(), "")

	// A hint naming a core requirement class decodes into that class's struct.
	docker, ok := hints[1].(*DockerRequirement)
	if !ok {
		t.Fatalf("Hints[1] is %T, want *DockerRequirement", hints[1])
	}

	assertEqual(t, "Hints[1].DockerPull", docker.DockerPull, "alpine:3")
}

func TestDecodeGraphSelectsMain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fixture string
		wantID  string
	}{
		{fixture: "graph_fragment_main.cwl", wantID: graphMainFragment},
		{fixture: "graph_bare_main.cwl", wantID: graphMainName},
	}

	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			process := decodeFixture(t, tc.fixture)
			if got := process.Base().ID; got != tc.wantID {
				t.Errorf("selected process id = %q, want %q", got, tc.wantID)
			}
		})
	}
}

func TestDecodeGraphWithoutMainFails(t *testing.T) {
	t.Parallel()

	_, err := Decode(fixtureDoc(t, "graph_no_main.cwl"))
	if err == nil {
		t.Fatal("Decode succeeded, want an error naming the missing entry point")
	}

	for _, want := range []string{graphMainFragment, "#first", "#second"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestDecodeAllKeysEveryProcess(t *testing.T) {
	t.Parallel()

	all, err := DecodeAll(fixtureDoc(t, "graph_fragment_main.cwl"))
	if err != nil {
		t.Fatalf("DecodeAll: %v", err)
	}

	assertEqual(t, "len(all)", len(all), 2)

	tool, ok := all["#tool"]
	if !ok {
		t.Fatal(`DecodeAll result has no "#tool" entry`)
	}

	assertEqual(t, `all["#tool"].Class()`, tool.Class(), ClassCommandLineTool)

	main, ok := all[graphMainFragment]
	if !ok {
		t.Fatal(`DecodeAll result has no "#main" entry`)
	}

	assertEqual(t, `all["#main"].Class()`, main.Class(), ClassWorkflow)
}

func TestDecodeAllOnSingleProcessDocument(t *testing.T) {
	t.Parallel()

	all, err := DecodeAll(fixtureDoc(t, "operation.cwl"))
	if err != nil {
		t.Fatalf("DecodeAll: %v", err)
	}

	assertEqual(t, "len(all)", len(all), 1)

	if _, ok := all["#align"]; !ok {
		t.Errorf("DecodeAll result keys are %v, want a single #align entry", keysOf(all))
	}
}

// keysOf renders a decoded graph's keys for a failure message.
func keysOf(all map[string]Process) []string {
	keys := make([]string, 0, len(all))
	for id := range all {
		keys = append(keys, id)
	}

	return keys
}

func TestDecodeNilDocument(t *testing.T) {
	t.Parallel()

	_, err := Decode(nil)
	if err == nil {
		t.Error("Decode(nil) succeeded, want an error")
	}

	_, err = DecodeAll(nil)
	if err == nil {
		t.Error("DecodeAll(nil) succeeded, want an error")
	}
}

func TestDecodeBlankNodeIDIsDeterministic(t *testing.T) {
	t.Parallel()

	const src = `class: Operation
cwlVersion: v1.2
inputs: []
outputs: []
`

	first := decodeSource(t, src).Base().ID
	second := decodeSource(t, src).Base().ID

	if !strings.HasPrefix(first, blankNodePrefix) {
		t.Errorf("generated id %q does not start with %q", first, blankNodePrefix)
	}

	if first != second {
		t.Errorf("decoding the same document twice produced %q and %q", first, second)
	}

	// A different document gets a different identifier.
	other := decodeSource(t, strings.Replace(src, "Operation", "ExpressionTool\nexpression: \"$(1)\"", 1))
	if other.Base().ID == first {
		t.Errorf("two different processes share the generated id %q", first)
	}
}

func TestDecodeBlankNodeIDsSeparateInlineSteps(t *testing.T) {
	t.Parallel()

	// Two structurally identical inline processes are told apart by the source
	// location that seeds the identifier.
	const src = `class: Workflow
cwlVersion: v1.2
inputs: []
outputs: []
steps:
  - id: one
    in: []
    out: []
    run:
      class: Operation
      inputs: []
      outputs: []
  - id: two
    in: []
    out: []
    run:
      class: Operation
      inputs: []
      outputs: []
`

	workflow, ok := decodeSource(t, src).(*Workflow)
	if !ok {
		t.Fatal("decoded process is not a *Workflow")
	}

	first := workflow.Steps[0].Run.Process.Base().ID
	second := workflow.Steps[1].Run.Process.Base().ID

	if first == second {
		t.Errorf("both inline processes were given the id %q", first)
	}
}

func TestDecodeErrorsCarrySourceLines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		src      string
		contains string
		wantLine int
	}{
		{
			name: "wrong field type",
			src: "class: CommandLineTool\n" +
				"cwlVersion: v1.2\n" +
				"inputs: []\n" +
				"outputs: []\n" +
				"baseCommand: 3\n",
			contains: "baseCommand",
			wantLine: 5,
		},
		{
			name:     "process without a class",
			src:      "cwlVersion: v1.2\ninputs: []\noutputs: []\n",
			contains: "class",
			wantLine: 1,
		},
		{
			name: "step without a run",
			src: "class: Workflow\n" +
				"cwlVersion: v1.2\n" +
				"inputs: []\n" +
				"outputs: []\n" +
				"steps:\n" +
				"  - id: only\n" +
				"    in: []\n" +
				"    out: []\n",
			contains: "run",
			wantLine: 6,
		},
		{
			name: "inline schema with an unknown kind",
			src: "class: Operation\n" +
				"cwlVersion: v1.2\n" +
				"outputs: []\n" +
				"inputs:\n" +
				"  - id: bad\n" +
				"    type:\n" +
				"      type: tuple\n",
			contains: "record",
			wantLine: 7,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := Decode(parseDoc(t, "bad.cwl", tc.src))
			if err == nil {
				t.Fatal("Decode succeeded, want an error")
			}

			assertErrorLine(t, err, tc.contains, tc.wantLine)
		})
	}
}

// assertErrorLine checks that some leaf of the error tree mentions contains and
// points at line.
func assertErrorLine(t *testing.T, err error, contains string, line int) {
	t.Helper()

	var decoded *salad.Error
	if !errors.As(err, &decoded) {
		t.Fatalf("error %v is not a *salad.Error", err)
	}

	for _, leaf := range decoded.Leaves() {
		if strings.Contains(leaf.Msg, contains) && leaf.Loc.Start.Line == line {
			return
		}
	}

	t.Errorf("no error leaf mentions %q at line %d; got:\n%s", contains, line, decoded.Pretty())
}

func TestDecodeCollectsSeveralErrors(t *testing.T) {
	t.Parallel()

	const src = `class: CommandLineTool
cwlVersion: v1.2
baseCommand: 3
inputs: []
outputs:
  - id: bad
    type: File
    streamable: "yes"
`

	_, err := Decode(parseDoc(t, "bad.cwl", src))
	if err == nil {
		t.Fatal("Decode succeeded, want an error")
	}

	var decoded *salad.Error
	if !errors.As(err, &decoded) {
		t.Fatalf("error %v is not a *salad.Error", err)
	}

	if got := len(decoded.Leaves()); got < 2 {
		t.Errorf("collected %d errors, want both of them:\n%s", got, decoded.Pretty())
	}
}

// TestDecodeNodeDecodesOneProcess covers the exported seam a downstream package
// holding a RawProcess uses to run core decoding again over a sub-node of it.
// Nothing inside this package calls it: selecting one member of a document goes
// through decodeFragment instead, so that the member's run references still
// resolve against the siblings it was packed with.
func TestDecodeNodeDecodesOneProcess(t *testing.T) {
	t.Parallel()

	node := parseDoc(t, "tool.cwl", "class: Operation\ncwlVersion: v1.2\ninputs: []\noutputs: []\n").Root

	process, err := DecodeNode(node)
	if err != nil {
		t.Fatalf("DecodeNode: %v", err)
	}

	assertEqual(t, "class", process.Class(), ClassOperation)
}

func TestDecodeNodeRejectsNonMapping(t *testing.T) {
	t.Parallel()

	_, err := DecodeNode(salad.NewStringNode(salad.SourceLine{}, "not a process"))
	if err == nil {
		t.Error("DecodeNode succeeded on a scalar, want an error")
	}
}

func TestLoadRejectsCancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := Load(ctx, []byte("class: Operation\n"), "in.cwl")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Load with a cancelled context returned %v, want context.Canceled", err)
	}

	_, err = LoadFile(ctx, "in.cwl")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("LoadFile with a cancelled context returned %v, want context.Canceled", err)
	}
}

// assertEqual reports a mismatch between a decoded value and what the fixture
// declared. It is generic so that one helper serves every field type.
func assertEqual[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}
