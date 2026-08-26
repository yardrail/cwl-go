package cwlcore

import (
	"errors"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// The malformed-document tests.
//
// Decoding runs over a tree pkg/salad has already validated, so none of these
// documents can reach Decode through Load. They can reach it through Decode and
// DecodeNode, which are public entry points a caller may hand any tree at all,
// and the contract there is that a shape the model cannot represent produces a
// located error rather than a silently wrong value.

func TestDecodeMalformedFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		src      string
		contains string
	}{
		{
			name:     "label is not a string",
			src:      operationHead + "    type: string\nlabel: 7\n",
			contains: "label",
		},
		{
			name:     "type name is not a string",
			src:      operationHead + "    type: [7]\n",
			contains: "type name",
		},
		{
			name:     "inputs is neither a sequence nor a mapping",
			src:      "class: Operation\ncwlVersion: v1.2\noutputs: []\ninputs: 7\n",
			contains: "inputs",
		},
		{
			name:     "arguments written as a mapping, which has no mapSubject",
			src:      "class: CommandLineTool\ncwlVersion: v1.2\ninputs: []\noutputs: []\narguments:\n  a: b\n",
			contains: "arguments",
		},
		{
			name:     "identifier map with a bare value and no mapPredicate",
			src:      "class: Operation\ncwlVersion: v1.2\ninputs: []\noutputs: []\nrequirements:\n  DockerRequirement: 7\n",
			contains: "requirements",
		},
		{
			name:     "successCodes holds something that is not an integer",
			src:      "class: CommandLineTool\ncwlVersion: v1.2\ninputs: []\noutputs: []\nsuccessCodes: [ok]\n",
			contains: "successCodes",
		},
		{
			name:     "a requirements entry is not a mapping",
			src:      "class: Operation\ncwlVersion: v1.2\ninputs: []\noutputs: []\nrequirements: [7]\n",
			contains: "requirement",
		},
		{
			name:     "an input binding is not a mapping",
			src:      toolHead + "    type: string\n    inputBinding: 7\n",
			contains: kindNameBinding,
		},
		{
			name:     "a secondary file pattern is neither a string nor a mapping",
			src:      operationHead + "    type: File\n    secondaryFiles: [[1]]\n",
			contains: "secondary file",
		},
		{
			name:     "a step output is neither a string nor a mapping",
			src:      workflowHead + "    out: [[1]]\n",
			contains: "step output",
		},
		{
			name:     "a workflow step is not a mapping",
			src:      "class: Workflow\ncwlVersion: v1.2\ninputs: []\noutputs: []\nsteps: [7]\n",
			contains: "workflow step",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assertDecodeFails(t, tc.src, tc.contains)
		})
	}
}

// workflowHead is the smallest workflow carrying one step, for the cases whose
// malformed field lives on a step. The step's remaining fields are appended.
const workflowHead = "class: Workflow\ncwlVersion: v1.2\ninputs: []\noutputs: []\n" +
	"steps:\n  - id: only\n    run: \"#tool\"\n    in: []\n"

func TestDecodeMalformedUnionValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		src      string
		contains string
	}{
		{
			name:     "a boolean-or-expression field holding a number",
			src:      operationHead + "    type: File\n    secondaryFiles:\n      pattern: .idx\n      required: 7\n",
			contains: "required",
		},
		{
			name:     "a boolean-or-expression field holding a sequence",
			src:      operationHead + "    type: File\n    secondaryFiles:\n      pattern: .idx\n      required: [true]\n",
			contains: "required",
		},
		{
			name:     "an integer-or-expression field holding a float",
			src:      toolHead + "    type: string\n    inputBinding:\n      position: 1.5\n",
			contains: "position",
		},
		{
			name:     "a resource field holding a boolean",
			src:      toolWithRequirement + "  - class: ResourceRequirement\n    coresMin: true\n",
			contains: "coresMin",
		},
		{
			name:     "a plain boolean field holding a string",
			src:      operationHead + "    type: File\n    streamable: \"yes\"\n",
			contains: "streamable",
		},
		{
			name:     "an OptBool field holding a string",
			src:      toolHead + "    type: string\n    inputBinding:\n      separate: \"yes\"\n",
			contains: "separate",
		},
		{
			name: "an OptInt field holding a string",
			src: toolWithRequirement + "  - class: InitialWorkDirRequirement\n    listing:\n" +
				"      - class: File\n        size: big\n",
			contains: keySize,
		},
		{
			name: "an OptInt field holding a sequence",
			src: toolWithRequirement + "  - class: InitialWorkDirRequirement\n    listing:\n" +
				"      - class: File\n        size: [1]\n",
			contains: keySize,
		},
		{
			name: "an OptString field holding a number",
			src: toolWithRequirement + "  - class: InitialWorkDirRequirement\n    listing:\n" +
				"      - class: File\n        contents: 7\n",
			contains: "contents",
		},
		{
			name:     "a listing that is neither an expression nor a sequence",
			src:      toolWithRequirement + "  - class: InitialWorkDirRequirement\n    listing: 7\n",
			contains: "listing",
		},
		{
			name: "a filesystem value with neither File nor Directory as its class",
			src: toolWithRequirement + "  - class: InitialWorkDirRequirement\n    listing:\n" +
				"      - - class: Socket\n          location: s\n",
			contains: ClassDirectory,
		},
		{
			name: "a listing entry that is none of the union's members",
			src: toolWithRequirement + "  - class: InitialWorkDirRequirement\n    listing:\n" +
				"      - 7\n",
			contains: "listing entry",
		},
		{
			name: "a workflow input binding is not a mapping",
			src: "class: Workflow\ncwlVersion: v1.2\noutputs: []\nsteps: []\n" +
				"inputs:\n  p:\n    type: File\n    inputBinding: 7\n",
			contains: kindNameBinding,
		},
		{
			name: "an output binding is not a mapping",
			src: "class: CommandLineTool\ncwlVersion: v1.2\ninputs: []\n" +
				"outputs:\n  p:\n    type: File\n    outputBinding: 7\n",
			contains: kindNameBinding,
		},
		{
			name: "a filesystem value that is not a mapping",
			src: toolWithRequirement + "  - class: InitialWorkDirRequirement\n    listing:\n" +
				"      - - 7\n",
			contains: "File or Directory",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assertDecodeFails(t, tc.src, tc.contains)
		})
	}
}

// toolHead is operationHead's CommandLineTool counterpart, for the cases whose
// malformed field only exists on a tool's input parameter.
const toolHead = "class: CommandLineTool\ncwlVersion: v1.2\noutputs: []\ninputs:\n  - id: p\n"

// toolWithRequirement is the smallest tool that opens a requirements list, for
// the cases whose malformed field lives inside a requirement.
const toolWithRequirement = "class: CommandLineTool\ncwlVersion: v1.2\ninputs: []\noutputs: []\nrequirements:\n"

// assertDecodeFails decodes src and checks that it fails with a located error
// mentioning contains.
func assertDecodeFails(t *testing.T, src, contains string) {
	t.Helper()

	_, err := Decode(parseDoc(t, "bad.cwl", src))
	if err == nil {
		t.Fatal("Decode succeeded, want an error")
	}

	var decoded *salad.Error
	if !errors.As(err, &decoded) {
		t.Fatalf("error %v is not a *salad.Error", err)
	}

	mentioned := false

	for _, leaf := range decoded.Leaves() {
		if leaf.Loc.IsZero() {
			t.Errorf("error leaf %q carries no source location", leaf.Msg)
		}

		mentioned = mentioned || strings.Contains(leaf.Msg, contains)
	}

	if !mentioned {
		t.Errorf("no error leaf mentions %q; got:\n%s", contains, decoded.Pretty())
	}
}

func TestDecodeIdentifierMapKeepsAnExplicitSubject(t *testing.T) {
	t.Parallel()

	// A parameter written under one key but declaring another id keeps the id
	// it declared, rather than having the key forced over it.
	const src = `class: Operation
cwlVersion: v1.2
outputs: []
inputs:
  under_this_key:
    id: but_called_this
    type: string
`

	operation, ok := decodeSource(t, src).(*Operation)
	if !ok {
		t.Fatal("decoded process is not a *Operation")
	}

	assertEqual(t, "Inputs[0].ID()", operation.Inputs[0].ID(), "but_called_this")
}

func TestDecodeArgumentThatIsNeitherStringNorBinding(t *testing.T) {
	t.Parallel()

	const src = "class: CommandLineTool\ncwlVersion: v1.2\ninputs: []\noutputs: []\narguments: [7]\n"

	_, err := Decode(parseDoc(t, "bad.cwl", src))
	if err == nil {
		t.Fatal("Decode succeeded, want an error")
	}
}

func TestDecodeStepOutputAsMapping(t *testing.T) {
	t.Parallel()

	workflow, ok := decodeSource(t, workflowHead+"    out:\n      - id: result\n").(*Workflow)
	if !ok {
		t.Fatal("decoded process is not a *Workflow")
	}

	assertEqual(t, "Steps[0].Out[0].ID", workflow.Steps[0].Out[0].ID, "result")
}

func TestDecodeNodeRejectsNothing(t *testing.T) {
	t.Parallel()

	_, err := DecodeNode(nil)
	if err == nil {
		t.Fatal("DecodeNode(nil) succeeded, want an error")
	}

	if !strings.Contains(err.Error(), "process") {
		t.Errorf("error %q does not say what was missing", err)
	}
}

func TestDecodeAllReportsABrokenGraphEntry(t *testing.T) {
	t.Parallel()

	const src = `$graph:
  - id: "#good"
    class: Operation
    inputs: []
    outputs: []
  - 7
`

	_, err := DecodeAll(parseDoc(t, "bad.cwl", src))
	if err == nil {
		t.Error("DecodeAll succeeded, want an error naming the broken entry")
	}
}

func TestDecodeGraphOfNothingUsable(t *testing.T) {
	t.Parallel()

	// A graph whose entries are not even mappings still reports which
	// identifiers it found, which in this case is none at all.
	_, err := Decode(parseDoc(t, "bad.cwl", "$graph: [7]\n"))
	if err == nil {
		t.Fatal("Decode succeeded, want an error")
	}

	if !strings.Contains(err.Error(), "none") {
		t.Errorf("error %q does not report the empty identifier list", err)
	}
}

func TestDecoderErrOr(t *testing.T) {
	t.Parallel()

	loc := salad.SourceLine{File: "x.cwl", Start: salad.Position{Line: 3, Column: 1}}

	// A decoder that produced no value must have said why. errOr says it on
	// the decoder's behalf if it somehow did not, which is what stops a nil
	// error from ever accompanying a nil Process.
	supplied := newDecoder().errOr(loc, "nothing came of it")
	if supplied == nil {
		t.Fatal("errOr returned nil for a decoder that recorded nothing")
	}

	for _, want := range []string{"nothing came of it", "x.cwl:3:1"} {
		if !strings.Contains(supplied.Error(), want) {
			t.Errorf("supplied error %q does not mention %q", supplied, want)
		}
	}

	// When the decoder did record something, that is what comes back.
	d := newDecoder()
	d.failf(loc, "the real problem")

	recorded := d.errOr(loc, "nothing came of it")
	if !strings.Contains(recorded.Error(), "the real problem") {
		t.Errorf("errOr returned %q, want the error the decoder recorded", recorded)
	}
}

func TestDecodeNeverReturnsANilProcessWithoutAnError(t *testing.T) {
	t.Parallel()

	// The guarantee every loading entry point rests on, asserted directly on
	// the seam that provides it.
	for _, src := range []string{
		"class: Operation\ncwlVersion: v1.2\ninputs: []\noutputs: []\n",
		"class: ex:Custom\ncwlVersion: v1.2\ninputs: []\noutputs: []\n",
	} {
		process, err := Decode(parseDoc(t, "in.cwl", src))
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}

		if process == nil {
			t.Error("Decode returned a nil Process alongside a nil error")
		}
	}
}

func TestBlankNodeIDOfNothing(t *testing.T) {
	t.Parallel()

	// blankNodeID is total: a node it cannot walk still yields a well-formed
	// blank node identifier rather than panicking.
	id := blankNodeID(nil)
	if !strings.HasPrefix(id, blankNodePrefix) {
		t.Errorf("blankNodeID(nil) = %q, want a %q identifier", id, blankNodePrefix)
	}
}

func TestLoadRejectsUnparseableSource(t *testing.T) {
	t.Parallel()

	_, err := Load(t.Context(), []byte("class: [\n"), "bad.cwl")
	if err == nil {
		t.Error("Load succeeded on unparseable YAML, want an error")
	}

	_, err = Load(t.Context(), []byte("class: NoSuchClass\n"), "bad.cwl")
	if err == nil {
		t.Error("Load succeeded on a schema-invalid document, want an error")
	}
}

func TestLoadFileRejectsAMissingDocument(t *testing.T) {
	t.Parallel()

	_, err := LoadFile(t.Context(), "testdata/decode/no-such-file.cwl")
	if err == nil {
		t.Error("LoadFile succeeded on a missing document, want an error")
	}
}
