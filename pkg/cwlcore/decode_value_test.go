package cwlcore

import (
	"strings"
	"testing"
)

// toolWithBinding builds a tool whose single input carries the given
// CommandLineBinding block, already indented to sit under inputBinding.
func toolWithBinding(t *testing.T, block string) *CommandLineBinding {
	t.Helper()

	src := "class: CommandLineTool\ncwlVersion: v1.2\noutputs: []\ninputs:\n" +
		"  - id: p\n    type: string\n    inputBinding:\n" + block

	return mustCommandLineTool(t, src).Inputs[0].InputBinding
}

// requirementOfClass decodes a tool carrying one requirement written as block,
// already indented to sit under requirements as a list entry.
func requirementOfClass(t *testing.T, block string) ProcessRequirement {
	t.Helper()

	src := "class: CommandLineTool\ncwlVersion: v1.2\ninputs: []\noutputs: []\nrequirements:\n" + block

	reqs := mustCommandLineTool(t, src).Requirements
	if len(reqs) != 1 {
		t.Fatalf("decoded %d requirements, want 1", len(reqs))
	}

	return reqs[0]
}

func TestDecodeSeparateAndShellQuoteTriState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		block     string
		wantSet   bool
		wantValue bool
	}{
		{
			name:      "absent keeps the schema default of true",
			block:     "      prefix: \"-p\"\n",
			wantSet:   false,
			wantValue: true,
		},
		{
			name:      "explicit false",
			block:     "      separate: false\n      shellQuote: false\n",
			wantSet:   true,
			wantValue: false,
		},
		{
			name:      "explicit true",
			block:     "      separate: true\n      shellQuote: true\n",
			wantSet:   true,
			wantValue: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			binding := toolWithBinding(t, tc.block)

			assertEqual(t, "Separate.IsSet()", binding.Separate.IsSet(), tc.wantSet)
			assertEqual(t, "ShellQuote.IsSet()", binding.ShellQuote.IsSet(), tc.wantSet)
			assertEqual(t, "Separate.Or(true)", binding.Separate.Or(true), tc.wantValue)
			assertEqual(t, "ShellQuote.Or(true)", binding.ShellQuote.Or(true), tc.wantValue)
		})
	}
}

func TestDecodePositionUnion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		block    string
		wantExpr string
		wantKind ValueKind
		wantInt  int64
	}{
		{name: "absent", block: "      prefix: \"-p\"\n", wantKind: ValueUnset},
		{name: "integer", block: "      position: 0\n", wantKind: ValueInt},
		{
			name:     "expression",
			block:    "      position: \"$(inputs.n)\"\n",
			wantKind: ValueExpression,
			wantExpr: "$(inputs.n)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			position := toolWithBinding(t, tc.block).Position

			assertEqual(t, "Position.Kind()", position.Kind(), tc.wantKind)
			assertEqual(t, "Position.Int()", position.Int(), tc.wantInt)
			assertEqual(t, "Position.Expression()", string(position.Expression()), tc.wantExpr)
		})
	}
}

func TestDecodeFileSizeAndContentsTriState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		entry        string
		wantSizeSet  bool
		wantSize     int64
		wantContSet  bool
		wantContents string
	}{
		{
			name:  "both absent",
			entry: "        location: reads.txt\n",
		},
		{
			name:        "present and zero",
			entry:       "        location: empty.txt\n        size: 0\n        contents: \"\"\n",
			wantSizeSet: true,
			wantContSet: true,
		},
		{
			name:         "present and non-zero",
			entry:        "        size: 12\n        contents: greetings\n",
			wantSizeSet:  true,
			wantSize:     12,
			wantContSet:  true,
			wantContents: "greetings",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			file := stagedFile(t, tc.entry)

			assertEqual(t, "Size.IsSet()", file.Size.IsSet(), tc.wantSizeSet)
			assertEqual(t, "Size.Int()", file.Size.Int(), tc.wantSize)
			assertEqual(t, "Contents.IsSet()", file.Contents.IsSet(), tc.wantContSet)
			assertEqual(t, "Contents.Value()", file.Contents.Value(), tc.wantContents)
		})
	}
}

// stagedFile decodes a File value out of an InitialWorkDirRequirement listing.
func stagedFile(t *testing.T, entry string) *File {
	t.Helper()

	block := "  - class: InitialWorkDirRequirement\n    listing:\n      - class: File\n" + entry

	requirement, ok := requirementOfClass(t, block).(*InitialWorkDirRequirement)
	if !ok {
		t.Fatal("decoded requirement is not a *InitialWorkDirRequirement")
	}

	file := requirement.Listing.Entries()[0].File()
	if file == nil {
		t.Fatal("listing entry does not hold a File")
	}

	return file
}

func TestDecodeInitialWorkDirListingMembers(t *testing.T) {
	t.Parallel()

	const block = `  - class: InitialWorkDirRequirement
    listing:
      - null
      - "$(inputs.staged)"
      - entryname: script.sh
        entry: "echo hi"
        writable: true
      - class: File
        location: reads.txt
        basename: reads.txt
        secondaryFiles:
          - class: File
            location: reads.txt.idx
      - class: Directory
        location: refs
        listing:
          - class: File
            location: refs/one.txt
      - - class: File
          location: a.txt
        - class: Directory
          location: b
`

	requirement, ok := requirementOfClass(t, block).(*InitialWorkDirRequirement)
	if !ok {
		t.Fatal("decoded requirement is not a *InitialWorkDirRequirement")
	}

	entries := requirement.Listing.Entries()

	assertEqual(t, "Listing.Kind()", requirement.Listing.Kind(), ValueList)
	assertEqual(t, "len(Entries())", len(entries), 6)
	assertEqual(t, "Entries()[0].Kind()", entries[0].Kind(), ValueNull)
	assertEqual(t, "Entries()[1].Kind()", entries[1].Kind(), ValueExpression)
	assertEqual(t, "Entries()[1].Expression()", string(entries[1].Expression()), "$(inputs.staged)")
	assertEqual(t, "Entries()[2].Kind()", entries[2].Kind(), ValueDirent)
	assertEqual(t, "Entries()[2].Dirent().Entryname", string(entries[2].Dirent().Entryname), "script.sh")
	assertEqual(t, "Entries()[2].Dirent().Writable", entries[2].Dirent().Writable, true)
	assertEqual(t, "Entries()[3].Kind()", entries[3].Kind(), ValueFile)
	assertEqual(t, "Entries()[3].File().Basename", entries[3].File().Basename, "reads.txt")
	assertEqual(t, "len(Entries()[3].File().SecondaryFiles)", len(entries[3].File().SecondaryFiles), 1)
	assertEqual(t, "Entries()[4].Kind()", entries[4].Kind(), ValueDirectory)
	assertEqual(t, "len(Entries()[4].Directory().Listing)", len(entries[4].Directory().Listing), 1)
	assertEqual(t, "Entries()[5].Kind()", entries[5].Kind(), ValueList)
	assertEqual(t, "len(Entries()[5].Objects())", len(entries[5].Objects()), 2)
	assertEqual(t, "Entries()[5].Objects()[1].Class()", entries[5].Objects()[1].Class(), ClassDirectory)
}

func TestDecodeDirectoryListingAbsentIsNotEmpty(t *testing.T) {
	t.Parallel()

	const block = `  - class: InitialWorkDirRequirement
    listing:
      - class: Directory
        location: refs
      - class: Directory
        location: empty
        listing: []
`

	requirement, ok := requirementOfClass(t, block).(*InitialWorkDirRequirement)
	if !ok {
		t.Fatal("decoded requirement is not a *InitialWorkDirRequirement")
	}

	entries := requirement.Listing.Entries()

	// An absent listing means "fetch it at runtime"; an empty one means the
	// directory has no entries, so the two must not decode alike.
	if entries[0].Directory().Listing != nil {
		t.Error("an absent listing decoded to a non-nil slice")
	}

	if got := entries[1].Directory().Listing; got == nil || len(got) != 0 {
		t.Errorf("an empty listing decoded to %v, want a non-nil empty slice", got)
	}
}

// TestDecodeInitialWorkDirListingAbsentIsUnset covers initialWorkDirListing's
// guard for a listing field that was never written at all — decoding must
// leave the zero value rather than treat a missing field as an empty listing.
func TestDecodeInitialWorkDirListingAbsentIsUnset(t *testing.T) {
	t.Parallel()

	const block = "  - class: InitialWorkDirRequirement\n"

	requirement, ok := requirementOfClass(t, block).(*InitialWorkDirRequirement)
	if !ok {
		t.Fatal("decoded requirement is not a *InitialWorkDirRequirement")
	}

	if requirement.Listing.Kind() != ValueUnset {
		t.Errorf("Listing.Kind() = %s, want %s for an absent field", requirement.Listing.Kind(), ValueUnset)
	}
}

func TestDecodeInitialWorkDirListingAsExpression(t *testing.T) {
	t.Parallel()

	const block = "  - class: InitialWorkDirRequirement\n    listing: \"$(inputs.everything)\"\n"

	requirement, ok := requirementOfClass(t, block).(*InitialWorkDirRequirement)
	if !ok {
		t.Fatal("decoded requirement is not a *InitialWorkDirRequirement")
	}

	assertEqual(t, "Listing.Kind()", requirement.Listing.Kind(), ValueExpression)
	assertEqual(t, "Listing.Expression()", string(requirement.Listing.Expression()), "$(inputs.everything)")
}

func TestDecodeResourceRequirementLeavesAbsentFieldsUnset(t *testing.T) {
	t.Parallel()

	const block = `  - class: ResourceRequirement
    coresMin: 2
    ramMin: 1.5
    outdirMin: "$(inputs.n)"
`

	requirement, ok := requirementOfClass(t, block).(*ResourceRequirement)
	if !ok {
		t.Fatal("decoded requirement is not a *ResourceRequirement")
	}

	assertEqual(t, "CoresMin.Kind()", requirement.CoresMin.Kind(), ValueInt)
	assertEqual(t, "CoresMin.Int()", requirement.CoresMin.Int(), int64(2))
	assertEqual(t, "RAMMin.Kind()", requirement.RAMMin.Kind(), ValueFloat)
	assertEqual(t, "RAMMin.Float()", requirement.RAMMin.Float(), 1.5)
	assertEqual(t, "OutdirMin.Kind()", requirement.OutdirMin.Kind(), ValueExpression)
	assertEqual(t, "CoresMax.IsSet()", requirement.CoresMax.IsSet(), false)
	assertEqual(t, "RAMMax.IsSet()", requirement.RAMMax.IsSet(), false)
	assertEqual(t, "TmpdirMin.IsSet()", requirement.TmpdirMin.IsSet(), false)
	assertEqual(t, "OutdirMax.IsSet()", requirement.OutdirMax.IsSet(), false)
}

func TestDecodeExpressionBearingRequirements(t *testing.T) {
	t.Parallel()

	tests := []struct {
		check    func(*testing.T, ProcessRequirement)
		name     string
		block    string
		wantKind ValueKind
	}{
		{
			name:  "WorkReuse with a literal",
			block: "  - class: WorkReuse\n    enableReuse: false\n",
			check: func(t *testing.T, r ProcessRequirement) {
				t.Helper()

				reuse, ok := r.(*WorkReuse)
				if !ok {
					t.Fatalf("decoded %T, want *WorkReuse", r)
				}

				assertEqual(t, "EnableReuse.Kind()", reuse.EnableReuse.Kind(), ValueBool)
				assertEqual(t, "EnableReuse.Bool()", reuse.EnableReuse.Bool(), false)
			},
		},
		{
			name:  "NetworkAccess with an expression",
			block: "  - class: NetworkAccess\n    networkAccess: \"$(inputs.online)\"\n",
			check: func(t *testing.T, r ProcessRequirement) {
				t.Helper()

				access, ok := r.(*NetworkAccess)
				if !ok {
					t.Fatalf("decoded %T, want *NetworkAccess", r)
				}

				assertEqual(t, "NetworkAccess.Kind()", access.NetworkAccess.Kind(), ValueExpression)
			},
		},
		{
			name:  "ToolTimeLimit with an integer",
			block: "  - class: ToolTimeLimit\n    timelimit: 30\n",
			check: func(t *testing.T, r ProcessRequirement) {
				t.Helper()

				limit, ok := r.(*ToolTimeLimit)
				if !ok {
					t.Fatalf("decoded %T, want *ToolTimeLimit", r)
				}

				assertEqual(t, "Timelimit.Int()", limit.Timelimit.Int(), int64(30))
			},
		},
		{
			name:  "InplaceUpdateRequirement",
			block: "  - class: InplaceUpdateRequirement\n    inplaceUpdate: true\n",
			check: func(t *testing.T, r ProcessRequirement) {
				t.Helper()

				update, ok := r.(*InplaceUpdateRequirement)
				if !ok {
					t.Fatalf("decoded %T, want *InplaceUpdateRequirement", r)
				}

				assertEqual(t, "InplaceUpdate", update.InplaceUpdate, true)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.check(t, requirementOfClass(t, tc.block))
		})
	}
}

func TestDecodeEveryCoreRequirementClass(t *testing.T) {
	t.Parallel()

	// One minimal declaration per class, so that a class missing from the
	// decoder table shows up here as a RawRequirement rather than in production.
	blocks := map[string]string{
		ClassInlineJavascriptRequirement:     "  - class: InlineJavascriptRequirement\n",
		ClassSchemaDefRequirement:            "  - class: SchemaDefRequirement\n    types: []\n",
		ClassLoadListingRequirement:          "  - class: LoadListingRequirement\n    loadListing: no_listing\n",
		ClassDockerRequirement:               "  - class: DockerRequirement\n    dockerPull: alpine\n",
		ClassSoftwareRequirement:             "  - class: SoftwareRequirement\n    packages: []\n",
		ClassInitialWorkDirRequirement:       "  - class: InitialWorkDirRequirement\n    listing: []\n",
		ClassEnvVarRequirement:               "  - class: EnvVarRequirement\n    envDef: []\n",
		ClassShellCommandRequirement:         "  - class: ShellCommandRequirement\n",
		ClassResourceRequirement:             "  - class: ResourceRequirement\n",
		ClassWorkReuse:                       "  - class: WorkReuse\n    enableReuse: true\n",
		ClassNetworkAccess:                   "  - class: NetworkAccess\n    networkAccess: true\n",
		ClassInplaceUpdateRequirement:        "  - class: InplaceUpdateRequirement\n    inplaceUpdate: false\n",
		ClassToolTimeLimit:                   "  - class: ToolTimeLimit\n    timelimit: 1\n",
		ClassSubworkflowFeatureRequirement:   "  - class: SubworkflowFeatureRequirement\n",
		ClassScatterFeatureRequirement:       "  - class: ScatterFeatureRequirement\n",
		ClassMultipleInputFeatureRequirement: "  - class: MultipleInputFeatureRequirement\n",
		ClassStepInputExpressionRequirement:  "  - class: StepInputExpressionRequirement\n",
	}

	if len(blocks) != len(requirementDecoders) {
		t.Fatalf("covering %d requirement classes, but the decoder table has %d", len(blocks), len(requirementDecoders))
	}

	for class, block := range blocks {
		t.Run(class, func(t *testing.T) {
			t.Parallel()

			requirement := requirementOfClass(t, block)

			if _, isRaw := requirement.(*RawRequirement); isRaw {
				t.Fatalf("%s decoded as a *RawRequirement", class)
			}

			assertEqual(t, "Class()", requirement.Class(), class)
		})
	}
}

func TestDecodeRequirementDetails(t *testing.T) {
	t.Parallel()

	const block = `  - class: InlineJavascriptRequirement
    expressionLib: "function one() { return 1; }"
`

	javascript, ok := requirementOfClass(t, block).(*InlineJavascriptRequirement)
	if !ok {
		t.Fatal("decoded requirement is not a *InlineJavascriptRequirement")
	}

	// A single string normalizes into the slice the model declares.
	assertEqual(t, "len(ExpressionLib)", len(javascript.ExpressionLib), 1)

	const docker = `  - class: DockerRequirement
    dockerPull: alpine
    dockerLoad: http://example.org/img.tar
    dockerFile: "FROM alpine"
    dockerImport: http://example.org/fs.tar
    dockerImageId: sha256:abc
    dockerOutputDirectory: /out
`

	image, ok := requirementOfClass(t, docker).(*DockerRequirement)
	if !ok {
		t.Fatal("decoded requirement is not a *DockerRequirement")
	}

	assertEqual(t, "DockerImageID", image.DockerImageID, "sha256:abc")
	assertEqual(t, "DockerOutputDirectory", image.DockerOutputDirectory, "/out")
	assertEqual(t, "DockerFile", image.DockerFile, "FROM alpine")
}

func TestDecodeEnvVarAndSoftwareIdentifierMaps(t *testing.T) {
	t.Parallel()

	const env = "  - class: EnvVarRequirement\n    envDef:\n      PATH: /usr/bin\n      HOME: \"$(runtime.outdir)\"\n"

	vars, ok := requirementOfClass(t, env).(*EnvVarRequirement)
	if !ok {
		t.Fatal("decoded requirement is not a *EnvVarRequirement")
	}

	assertEqual(t, "len(EnvDef)", len(vars.EnvDef), 2)
	assertEqual(t, "EnvDef[0].EnvName", vars.EnvDef[0].EnvName, "HOME")
	assertEqual(t, "EnvDef[0].EnvValue", string(vars.EnvDef[0].EnvValue), "$(runtime.outdir)")
	assertEqual(t, "EnvDef[1].EnvName", vars.EnvDef[1].EnvName, "PATH")

	const software = `  - class: SoftwareRequirement
    packages:
      - package: samtools
        version: ["1.9", "1.10"]
        specs: [https://identifiers.org/rrid/RRID:SCR_002105]
`

	packages, ok := requirementOfClass(t, software).(*SoftwareRequirement)
	if !ok {
		t.Fatal("decoded requirement is not a *SoftwareRequirement")
	}

	assertEqual(t, "Packages[0].Package", packages.Packages[0].Package, "samtools")
	assertEqual(t, "Packages[0].Version", strings.Join(packages.Packages[0].Version, ","), "1.9,1.10")
	assertEqual(t, "len(Packages[0].Specs)", len(packages.Packages[0].Specs), 1)
}

func TestDecodeSchemaDefRequirementKeepsNodes(t *testing.T) {
	t.Parallel()

	const block = `  - class: SchemaDefRequirement
    types:
      - name: rec
        type: record
        fields: []
`

	defs, ok := requirementOfClass(t, block).(*SchemaDefRequirement)
	if !ok {
		t.Fatal("decoded requirement is not a *SchemaDefRequirement")
	}

	assertEqual(t, "len(Types)", len(defs.Types), 1)

	if defs.Types[0] == nil {
		t.Error("Types[0] is nil, want the salad node the document wrote")
	}
}

func TestDecodeStringOrListNormalization(t *testing.T) {
	t.Parallel()

	// Every field the schema spells as `T | T[]`, written in the single-value
	// form, must reach the model as a one-element slice.
	const src = `class: CommandLineTool
cwlVersion: v1.2
doc: one paragraph
intent: http://example.org/ontology#Thing
baseCommand: echo
inputs:
  - id: p
    type: File
    format: http://edamontology.org/format_1929
    secondaryFiles: .idx
outputs:
  - id: out
    type: File
    outputBinding:
      glob: "*.txt"
`

	tool := mustCommandLineTool(t, src)

	assertEqual(t, "len(Doc)", len(tool.Doc), 1)
	assertEqual(t, "Doc[0]", tool.Doc[0], "one paragraph")
	assertEqual(t, "len(Intent)", len(tool.Intent), 1)
	assertEqual(t, "len(BaseCommand)", len(tool.BaseCommand), 1)
	assertEqual(t, "BaseCommand[0]", tool.BaseCommand[0], "echo")
	assertEqual(t, "len(Inputs[0].Format)", len(tool.Inputs[0].Format), 1)
	assertEqual(t, "len(Inputs[0].SecondaryFiles)", len(tool.Inputs[0].SecondaryFiles), 1)
	assertEqual(t, "len(Outputs[0].OutputBinding.Glob)", len(tool.Outputs[0].OutputBinding.Glob), 1)
	assertEqual(t, "Outputs[0].OutputBinding.Glob[0]", string(tool.Outputs[0].OutputBinding.Glob[0]), "*.txt")
}

func TestDecodeStringOrListNormalizationOnWorkflowFields(t *testing.T) {
	t.Parallel()

	const src = `class: Workflow
cwlVersion: v1.2
inputs: []
outputs:
  - id: out
    type: File
    outputSource: step/result
steps:
  - id: step
    run: "#tool"
    scatter: files
    in:
      - id: files
        source: outer
    out: [result]
`

	workflow, ok := decodeSource(t, src).(*Workflow)
	if !ok {
		t.Fatal("decoded process is not a *Workflow")
	}

	assertEqual(t, "len(Outputs[0].OutputSource)", len(workflow.Outputs[0].OutputSource), 1)
	assertEqual(t, "Outputs[0].OutputSource[0]", workflow.Outputs[0].OutputSource[0], "step/result")
	assertEqual(t, "len(Steps[0].Scatter)", len(workflow.Steps[0].Scatter), 1)
	assertEqual(t, "Steps[0].Scatter[0]", workflow.Steps[0].Scatter[0], "files")
	assertEqual(t, "len(Steps[0].In[0].Source)", len(workflow.Steps[0].In[0].Source), 1)
	assertEqual(t, "Steps[0].In[0].Source[0]", workflow.Steps[0].In[0].Source[0], "outer")
}

func TestDecodeAbsentListsStayNil(t *testing.T) {
	t.Parallel()

	const src = "class: Operation\ncwlVersion: v1.2\ninputs: []\noutputs: []\n"

	operation, ok := decodeSource(t, src).(*Operation)
	if !ok {
		t.Fatal("decoded process is not a *Operation")
	}

	if operation.Doc != nil {
		t.Errorf("Doc = %v, want nil for an absent field", operation.Doc)
	}

	if operation.Requirements != nil {
		t.Errorf("Requirements = %v, want nil for an absent field", operation.Requirements)
	}

	if operation.Inputs == nil || len(operation.Inputs) != 0 {
		t.Errorf("Inputs = %v, want a non-nil empty slice for an empty list", operation.Inputs)
	}
}

func TestDecodeWorkflowInputBindingCarriesOnlyLoadContents(t *testing.T) {
	t.Parallel()

	const src = `class: Workflow
cwlVersion: v1.2
outputs: []
steps: []
inputs:
  - id: p
    type: File
    inputBinding:
      loadContents: true
`

	workflow, ok := decodeSource(t, src).(*Workflow)
	if !ok {
		t.Fatal("decoded process is not a *Workflow")
	}

	binding := workflow.Inputs[0].InputBinding
	if binding == nil {
		t.Fatal("InputBinding is nil")
	}

	assertEqual(t, "InputBinding.LoadContents", binding.LoadContents, true)
}
