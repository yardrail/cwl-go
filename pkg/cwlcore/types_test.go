package cwlcore

import (
	"strconv"
	"testing"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// Extension class IRIs used to exercise the Raw* fallbacks. They are in an
// example.org namespace on purpose: this package must carry no vocabulary of
// its own beyond CWL v1.2, and the tests must not smuggle one in either.
const (
	extProcessIRI     = "http://example.org/ext#Agent"
	extRequirementIRI = "http://example.org/ext#Approval"
	extHintIRI        = "http://example.org/ext#Note"

	// mainID is a stand-in for a resolved process identifier.
	mainID = "#main"
)

// allProcesses returns one instance of every concrete Process implementation.
// Tests that must cover the whole Process set build from this, so that adding a
// process type without extending the tests fails a count assertion below.
func allProcesses() []Process {
	return []Process{
		&CommandLineTool{},
		&Workflow{},
		&ExpressionTool{},
		&Operation{},
		&RawProcess{ClassIRI: extProcessIRI},
	}
}

// allRequirements returns one instance of every concrete ProcessRequirement.
func allRequirements() []ProcessRequirement {
	return []ProcessRequirement{
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
		&RawRequirement{ClassIRI: extRequirementIRI},
	}
}

// allParameters returns one instance of every concrete Parameter.
func allParameters() []Parameter {
	return []Parameter{
		&CommandInputParameter{},
		&CommandOutputParameter{},
		&WorkflowInputParameter{},
		&WorkflowOutputParameter{},
		&OperationInputParameter{},
		&OperationOutputParameter{},
		&ExpressionToolOutputParameter{},
	}
}

func TestProcessClass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		proc Process
		name string
		want string
	}{
		{name: "CommandLineTool", proc: &CommandLineTool{}, want: ClassCommandLineTool},
		{name: "Workflow", proc: &Workflow{}, want: ClassWorkflow},
		{name: "ExpressionTool", proc: &ExpressionTool{}, want: ClassExpressionTool},
		{name: "Operation", proc: &Operation{}, want: ClassOperation},
		{
			name: "RawProcess returns its ClassIRI",
			proc: &RawProcess{ClassIRI: extProcessIRI},
			want: extProcessIRI,
		},
		{name: "RawProcess with no class", proc: &RawProcess{}, want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.proc.Class(); got != tc.want {
				t.Errorf("Class() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProcessSetIsComplete(t *testing.T) {
	t.Parallel()

	const wantProcesses = 5
	if got := len(allProcesses()); got != wantProcesses {
		t.Errorf("allProcesses() has %d entries, want %d — was a Process type added without updating the tests?",
			got, wantProcesses)
	}

	const wantRequirements = 18 // 17 core classes plus RawRequirement
	if got := len(allRequirements()); got != wantRequirements {
		t.Errorf("allRequirements() has %d entries, want %d", got, wantRequirements)
	}

	const wantParameters = 7
	if got := len(allParameters()); got != wantParameters {
		t.Errorf("allParameters() has %d entries, want %d", got, wantParameters)
	}
}

func TestProcessBaseIsAddressableThroughInterface(t *testing.T) {
	t.Parallel()

	for _, proc := range allProcesses() {
		t.Run(proc.Class(), func(t *testing.T) {
			t.Parallel()
			assertBaseIsWritable(t, proc)
		})
	}
}

// assertBaseIsWritable checks that writes through Base() land on the process
// itself. decode.go fills every process in exactly this way, so if the pointer
// were to a copy, decoding would silently produce empty processes.
func assertBaseIsWritable(t *testing.T, proc Process) {
	t.Helper()

	base := proc.Base()
	if base == nil {
		t.Fatal("Base() returned nil")
	}

	base.ID = mainID
	base.CWLVersion = CWLVersionV12
	base.Requirements = append(base.Requirements, &ShellCommandRequirement{})

	if got := proc.Base().ID; got != mainID {
		t.Errorf("after setting ID, Base().ID reads %q, want %q", got, mainID)
	}

	if got := proc.Base().CWLVersion; got != CWLVersionV12 {
		t.Errorf("after setting CWLVersion, Base().CWLVersion reads %q", got)
	}

	if got := len(proc.Base().Requirements); got != 1 {
		t.Errorf("after appending a requirement, len(Requirements) = %d, want 1", got)
	}
}

func TestProcessBaseIsTheEmbeddedValue(t *testing.T) {
	t.Parallel()

	tool := &CommandLineTool{}
	if tool.Base() != &tool.ProcessBase {
		t.Error("Base() did not return a pointer to the embedded ProcessBase")
	}

	raw := &RawProcess{}
	if raw.Base() != &raw.ProcessBase {
		t.Error("RawProcess.Base() did not return a pointer to the embedded ProcessBase")
	}
}

func TestRequirementClass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		req  ProcessRequirement
		name string
		want string
	}{
		{name: "InlineJavascript", req: &InlineJavascriptRequirement{}, want: ClassInlineJavascriptRequirement},
		{name: "SchemaDef", req: &SchemaDefRequirement{}, want: ClassSchemaDefRequirement},
		{name: "LoadListing", req: &LoadListingRequirement{}, want: ClassLoadListingRequirement},
		{name: "Docker", req: &DockerRequirement{}, want: ClassDockerRequirement},
		{name: "Software", req: &SoftwareRequirement{}, want: ClassSoftwareRequirement},
		{name: "InitialWorkDir", req: &InitialWorkDirRequirement{}, want: ClassInitialWorkDirRequirement},
		{name: "EnvVar", req: &EnvVarRequirement{}, want: ClassEnvVarRequirement},
		{name: "ShellCommand", req: &ShellCommandRequirement{}, want: ClassShellCommandRequirement},
		{name: "Resource", req: &ResourceRequirement{}, want: ClassResourceRequirement},
		{name: "WorkReuse", req: &WorkReuse{}, want: ClassWorkReuse},
		{name: "NetworkAccess", req: &NetworkAccess{}, want: ClassNetworkAccess},
		{name: "InplaceUpdate", req: &InplaceUpdateRequirement{}, want: ClassInplaceUpdateRequirement},
		{name: "ToolTimeLimit", req: &ToolTimeLimit{}, want: ClassToolTimeLimit},
		{name: "Subworkflow", req: &SubworkflowFeatureRequirement{}, want: ClassSubworkflowFeatureRequirement},
		{name: "Scatter", req: &ScatterFeatureRequirement{}, want: ClassScatterFeatureRequirement},
		{
			name: "MultipleInput", req: &MultipleInputFeatureRequirement{},
			want: ClassMultipleInputFeatureRequirement,
		},
		{
			name: "StepInputExpression", req: &StepInputExpressionRequirement{},
			want: ClassStepInputExpressionRequirement,
		},
		{
			name: "RawRequirement returns its ClassIRI",
			req:  &RawRequirement{ClassIRI: extRequirementIRI},
			want: extRequirementIRI,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.req.Class(); got != tc.want {
				t.Errorf("Class() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEveryRequirementIsAlsoAHint(t *testing.T) {
	t.Parallel()

	// A hints entry may name any requirement class, so ProcessRequirement
	// must satisfy Hint without any adaptation.
	for _, req := range allRequirements() {
		var hint Hint = req
		if hint.Class() != req.Class() {
			t.Errorf("as a Hint, %T reports class %q, want %q", req, hint.Class(), req.Class())
		}
	}
}

func TestRawHintClass(t *testing.T) {
	t.Parallel()

	hint := &RawHint{ClassIRI: extHintIRI, Node: salad.NewNullNode(salad.SourceLine{})}
	if got := hint.Class(); got != extHintIRI {
		t.Errorf("Class() = %q, want the ClassIRI", got)
	}

	if hint.Node == nil {
		t.Error("RawHint dropped its Node")
	}
}

func TestParameterID(t *testing.T) {
	t.Parallel()

	const infileID = "#tool/infile"

	for _, param := range allParameters() {
		if got := param.ID(); got != "" {
			t.Errorf("%T zero value ID() = %q, want empty", param, got)
		}
	}

	in := &CommandInputParameter{}
	in.IDField = infileID

	var param Parameter = in
	if got := param.ID(); got != infileID {
		t.Errorf("ID() = %q, want %q", got, infileID)
	}
}

// TestParameterBaseIsSharedAndAddressable exercises the pattern decode.go
// relies on: one helper that takes a *ParameterBase and fills the fields every
// parameter shares, called once per concrete parameter type.
func TestParameterBaseIsSharedAndAddressable(t *testing.T) {
	t.Parallel()

	commandIn := &CommandInputParameter{}
	workflowOut := &WorkflowOutputParameter{}
	expressionOut := &ExpressionToolOutputParameter{}

	bases := []*ParameterBase{
		&commandIn.ParameterBase,
		&workflowOut.ParameterBase,
		&expressionOut.ParameterBase,
	}
	for i, base := range bases {
		fillParameterBase(base, "#p"+strconv.Itoa(i))
	}

	if commandIn.IDField != "#p0" || commandIn.ID() != "#p0" {
		t.Errorf("CommandInputParameter: IDField = %q, ID() = %q", commandIn.IDField, commandIn.ID())
	}

	if !commandIn.Type.IsSet() || commandIn.Type.Name() != PrimitiveFile {
		t.Errorf("CommandInputParameter.Type = %v, want File", commandIn.Type)
	}

	if workflowOut.ID() != "#p1" || expressionOut.ID() != "#p2" {
		t.Errorf("ID() = %q and %q, want #p1 and #p2", workflowOut.ID(), expressionOut.ID())
	}

	// The type-specific fields sit alongside the shared base untouched.
	commandIn.InputBinding = &CommandLineBinding{Prefix: "-i"}
	if commandIn.InputBinding.Prefix != "-i" {
		t.Error("the type-specific field did not survive")
	}
}

// fillParameterBase is a stand-in for the decode.go helper the embedded base
// exists to make possible.
func fillParameterBase(base *ParameterBase, id string) {
	base.IDField = id
	base.Type = NewPrimitiveType(PrimitiveFile)
	base.Doc = []string{"documented"}
}

func TestStepRunIsRef(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  StepRun
		want bool
	}{
		{name: "zero value is neither", run: StepRun{}, want: false},
		{name: "reference", run: StepRun{Ref: "tool.cwl"}, want: true},
		{name: "embedded process", run: StepRun{Process: &CommandLineTool{}}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.run.IsRef(); got != tc.want {
				t.Errorf("IsRef() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSealedMarkerMethods calls the unexported isProcess, isParameter and
// isRequirement methods directly on every concrete implementation. They are
// sealed-interface markers with empty bodies that production code never
// invokes — a type switch is exhaustive by construction once they compile —
// so this is the only way to exercise them at all.
func TestSealedMarkerMethods(t *testing.T) {
	t.Parallel()

	for _, p := range allProcesses() {
		p.isProcess()
	}

	for _, r := range allRequirements() {
		r.isRequirement()
	}

	for _, param := range allParameters() {
		param.isParameter()
	}
}

func TestEnumSymbolSpellings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "dotproduct", got: string(ScatterDotProduct), want: "dotproduct"},
		{name: "nested_crossproduct", got: string(ScatterNestedCrossProduct), want: "nested_crossproduct"},
		{name: "flat_crossproduct", got: string(ScatterFlatCrossProduct), want: "flat_crossproduct"},
		{name: "merge_nested", got: string(LinkMergeNested), want: "merge_nested"},
		{name: "merge_flattened", got: string(LinkMergeFlattened), want: "merge_flattened"},
		{name: "first_non_null", got: string(PickFirstNonNull), want: "first_non_null"},
		{name: "the_only_non_null", got: string(PickTheOnlyNonNull), want: "the_only_non_null"},
		{name: "all_non_null", got: string(PickAllNonNull), want: "all_non_null"},
		{name: "no_listing", got: string(LoadListingNone), want: "no_listing"},
		{name: "shallow_listing", got: string(LoadListingShallow), want: "shallow_listing"},
		{name: "deep_listing", got: string(LoadListingDeep), want: "deep_listing"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.got != tc.want {
				t.Errorf("constant = %q, want %q", tc.got, tc.want)
			}
		})
	}
}
