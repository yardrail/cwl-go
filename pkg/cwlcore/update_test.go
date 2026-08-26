package cwlcore

import (
	"errors"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// testDocURI is the base URI the inline documents below are parsed against. They
// are never fetched, so it only has to be a name a diagnostic can print.
const testDocURI = "in.cwl"

// Documents used by the version-routing and upgrade tests. They are inline
// rather than fixtures because each one exists to carry a single field: what is
// under test is which schema judged it, not what it says.
const (
	// Written with inputs in list form rather than the identifier-map shorthand,
	// because the upgrade tests assert on a raw parse: turning the shorthand into
	// a list is resolution's job, and resolution is not what they are measuring.
	toolV10 = `cwlVersion: v1.0
class: CommandLineTool
inputs:
  - id: inp1
    type: File
    secondaryFiles:
      - ".2"
outputs: []
arguments: [echo, $(inputs.inp1)]
`

	// The record spelling of secondaryFiles arrived in v1.1, so a v1.0 document
	// using it is asking for syntax its declared version did not have.
	toolV10RecordSecondaryFiles = `cwlVersion: v1.0
class: CommandLineTool
inputs:
  inp1:
    type: File
    secondaryFiles:
      - pattern: ".2"
        required: true
outputs: []
arguments: [echo, $(inputs.inp1)]
`

	// Fractional cores arrived in v1.2, which widened coresMin from long to
	// accept a float. The discriminator here is a type widening rather than a
	// new field, which is precisely why the real v1.1 schema has to be vendored:
	// no hand-written list of forbidden field names would catch it.
	toolV11FractionalCores = `cwlVersion: v1.1
class: CommandLineTool
inputs: []
requirements:
  ResourceRequirement:
    coresMin: .5
outputs: []
arguments: [echo]
`

	// A step condition arrived in v1.2.
	workflowV10Conditional = `cwlVersion: v1.0
class: Workflow
requirements:
  InlineJavascriptRequirement: {}
inputs: []
outputs: []
steps:
  only:
    in: []
    out: []
    when: $(true)
    run:
      class: CommandLineTool
      inputs: []
      outputs: []
      arguments: [echo]
`
)

func TestDeclaredVersionReadsTheRawParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want string
	}{
		{name: "a plain version", src: "cwlVersion: v1.0\nclass: Workflow\n", want: CWLVersionV10},
		{name: "the cwl-prefixed spelling", src: "cwlVersion: cwl:v1.1\n", want: CWLVersionV11},
		{name: "the fully expanded spelling", src: "cwlVersion: https://w3id.org/cwl/cwl#v1.2\n", want: CWLVersionV12},
		{name: "a $graph document", src: "cwlVersion: v1.0\n$graph: []\n", want: CWLVersionV10},
		{name: "no version at all", src: "class: Workflow\n", want: ""},
		{name: "a version that is not a string", src: "cwlVersion: 1.0\n", want: ""},
		{name: "a root that is not a mapping", src: "[]\n", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root, err := salad.Parse(testDocURI, []byte(tc.src))
			if err != nil {
				t.Fatalf("parsing: %v", err)
			}

			if got := DeclaredVersion(root); got != tc.want {
				t.Errorf("DeclaredVersion = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDeclaredVersionOfNothing(t *testing.T) {
	t.Parallel()

	if got := DeclaredVersion(nil); got != "" {
		t.Errorf("DeclaredVersion(nil) = %q, want %q", got, "")
	}
}

func TestSchemaForRoutesByVersion(t *testing.T) {
	t.Parallel()

	seen := make(map[*salad.Schema]string, 3)

	for _, version := range []string{CWLVersionV10, CWLVersionV11, CWLVersionV12} {
		loaded, err := schemaFor(version)
		if err != nil {
			t.Fatalf("schemaFor(%q): %v", version, err)
		}

		if other, clash := seen[loaded.Schema]; clash {
			t.Fatalf("schemaFor(%q) returned the same schema as %q", version, other)
		}

		seen[loaded.Schema] = version
	}

	// A document declaring nothing is validated against v1.2, which is the
	// version this package's model describes.
	blank, err := schemaFor("")
	if err != nil {
		t.Fatalf(`schemaFor(""): %v`, err)
	}

	if seen[blank.Schema] != CWLVersionV12 {
		t.Errorf(`schemaFor("") returned the %s schema, want the %s one`, seen[blank.Schema], CWLVersionV12)
	}
}

func TestSchemaForRejectsAnUnknownVersion(t *testing.T) {
	t.Parallel()

	_, err := schemaFor("draft-3")
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("schemaFor(draft-3) = %v, want an unsupported-version failure", err)
	}

	if !strings.Contains(err.Error(), "draft-3") {
		t.Errorf("schemaFor(draft-3) = %q, want it to name the version", err)
	}
}

// TestSchemaMemoIsPerVersion guards the refactor from one nullary memo to three:
// each version is flattened at most once, and no two of them share a result.
func TestSchemaMemoIsPerVersion(t *testing.T) {
	t.Parallel()

	for _, version := range []string{CWLVersionV10, CWLVersionV11, CWLVersionV12} {
		first, err := schemaFor(version)
		if err != nil {
			t.Fatalf("schemaFor(%q): %v", version, err)
		}

		second, err := schemaFor(version)
		if err != nil {
			t.Fatalf("schemaFor(%q) a second time: %v", version, err)
		}

		if first != second {
			t.Errorf("schemaFor(%q) returned a different schema the second time", version)
		}
	}
}

func TestV10DocumentValidatesAsV10AndLoads(t *testing.T) {
	t.Parallel()

	process, err := Load(t.Context(), []byte(toolV10), "file:///tools/tool-v10.cwl", salad.Strict(true))
	if err != nil {
		t.Fatalf("loading a v1.0 document: %v", explain(err))
	}

	if process.Class() != ClassCommandLineTool {
		t.Fatalf("class = %q, want %q", process.Class(), ClassCommandLineTool)
	}

	// The upgrade stamps the version it produced, so nothing downstream has to
	// ask what the document originally said.
	if got := process.Base().CWLVersion; got != CWLVersionV12 {
		t.Errorf("CWLVersion after loading = %q, want %q", got, CWLVersionV12)
	}
}

// TestV12SyntaxInAnOlderDocumentIsRejected is the point of the whole exercise:
// each of these documents is structurally valid v1.2 and is refused anyway,
// because it declares a version that did not have the syntax it uses. Each case
// asserts the field the failure names, so that a document failing for some
// unrelated reason cannot be mistaken for the feature working.
func TestV12SyntaxInAnOlderDocumentIsRejected(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		src   string
		field string
	}{
		{
			name:  "a v1.0 tool using the v1.1 secondaryFiles record",
			src:   toolV10RecordSecondaryFiles,
			field: "secondaryFiles",
		},
		{
			name:  "a v1.1 tool using v1.2 fractional cores",
			src:   toolV11FractionalCores,
			field: "coresMin",
		},
		{
			name:  "a v1.0 workflow using a v1.2 step condition",
			src:   workflowV10Conditional,
			field: "when",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// The same source under the v1.2 schema is a control: if it did
			// not load there, the rejection below would prove nothing.
			v12 := strings.Replace(tc.src, "cwlVersion: v1.0", "cwlVersion: v1.2", 1)
			v12 = strings.Replace(v12, "cwlVersion: v1.1", "cwlVersion: v1.2", 1)

			_, err := Load(t.Context(), []byte(v12), "file:///tools/control.cwl", salad.Strict(true))
			if err != nil {
				t.Fatalf("the same document declaring v1.2 must be valid, got: %v", explain(err))
			}

			_, err = Load(t.Context(), []byte(tc.src), "file:///tools/subject.cwl", salad.Strict(true))
			if err == nil {
				t.Fatal("the document loaded, want it rejected against its declared version")
			}

			if !strings.Contains(explain(err), tc.field) {
				t.Errorf("failure = %s\nwant it to name %q", explain(err), tc.field)
			}
		})
	}
}

func TestUpgradeRenamesCwltoolRequirements(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		class string
		want  string
	}{
		{name: "work reuse", class: cwltoolNamespace + "WorkReuse", want: ClassWorkReuse},
		{name: "the arvados spelling of work reuse", class: arvadosReuseClass, want: ClassWorkReuse},
		{name: "the time limit, which was renamed", class: cwltoolTimeLimit, want: ClassToolTimeLimit},
		{name: "network access", class: cwltoolNamespace + "NetworkAccess", want: ClassNetworkAccess},
		{
			name:  "in-place update",
			class: cwltoolNamespace + "InplaceUpdateRequirement",
			want:  ClassInplaceUpdateRequirement,
		},
		{name: "load listing", class: cwltoolNamespace + "LoadListingRequirement", want: ClassLoadListingRequirement},
		{name: "a class that is already core", class: ClassShellCommandRequirement, want: ClassShellCommandRequirement},
		{
			name:  "an extension we know nothing about",
			class: "http://example.org/x#Thing",
			want:  "http://example.org/x#Thing",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := parseNode(t, "cwlVersion: v1.0\nclass: CommandLineTool\nrequirements:\n  - class: "+tc.class+"\n")

			upgraded := Upgrade(&salad.Document{Root: root, BaseURI: testDocURI}, CWLVersionV10)

			if got := firstRequirementClass(t, upgraded.Root); got != tc.want {
				t.Errorf("requirement class after upgrading = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestUpgradeRenamesStepRequirements pins the descent a class-dispatched walk
// would miss: a workflow step has no class of its own, so its requirements are
// only reached by a rule that knows to look at steps.
func TestUpgradeRenamesStepRequirements(t *testing.T) {
	t.Parallel()

	root := parseNode(t, `cwlVersion: v1.0
class: Workflow
inputs: []
outputs: []
steps:
  - id: one
    in: []
    out: []
    run: tool.cwl
    hints:
      - class: `+cwltoolTimeLimit+`
        timelimit: 5
`)

	upgraded := Upgrade(&salad.Document{Root: root, BaseURI: testDocURI}, CWLVersionV10)

	steps := seqField(t, upgraded.Root, keySteps)
	if got := firstHintClass(t, steps[0]); got != ClassToolTimeLimit {
		t.Errorf("step hint class after upgrading = %q, want %q", got, ClassToolTimeLimit)
	}
}

func TestUpgradeChainsThroughV11(t *testing.T) {
	t.Parallel()

	root := parseNode(t, toolV10)

	upgraded := Upgrade(&salad.Document{Root: root, BaseURI: testDocURI}, CWLVersionV10)

	m, ok := salad.AsMap(upgraded.Root)
	if !ok {
		t.Fatal("the upgraded root is not a mapping")
	}

	if got := lenientText(m, keyCWLVersion); got != CWLVersionV12 {
		t.Errorf("cwlVersion after upgrading = %q, want %q", got, CWLVersionV12)
	}

	// v1.0's implicit defaults, made explicit: without them a Directory's
	// listing would silently stop being loaded and the network would silently
	// be taken away.
	hints := seqField(t, upgraded.Root, keyHints)
	if len(hints) != 2 {
		t.Fatalf("hints after upgrading = %d, want the two v1.0 defaults", len(hints))
	}

	want := []string{ClassLoadListingRequirement, ClassNetworkAccess}
	for i, hint := range hints {
		if got := classOf(t, hint); got != want[i] {
			t.Errorf("hint %d = %q, want %q", i, got, want[i])
		}
	}

	// The string pattern becomes the record form v1.1 introduced.
	pattern := firstSecondaryFilePattern(t, upgraded.Root)
	if pattern != ".2" {
		t.Errorf("secondaryFiles pattern after upgrading = %q, want %q", pattern, ".2")
	}
}

// TestUpgradeKeepsAQuestionMarkInAV10Pattern pins the one place this deliberately
// differs from pkg/salad's secondaryFilesDSL expansion. Reading a trailing "?" as
// "this file is optional" is a v1.1 rule; in a v1.0 document the character is part
// of the pattern, and stripping it would silently change which file is looked for.
func TestUpgradeKeepsAQuestionMarkInAV10Pattern(t *testing.T) {
	t.Parallel()

	root := parseNode(t, strings.Replace(toolV10, `".2"`, `".2?"`, 1))

	upgraded := Upgrade(&salad.Document{Root: root, BaseURI: testDocURI}, CWLVersionV10)

	if got := firstSecondaryFilePattern(t, upgraded.Root); got != ".2?" {
		t.Errorf("pattern after upgrading = %q, want %q", got, ".2?")
	}
}

// TestUpgradeTrimsWorkflowInputBindings covers cwltool's fix_inputBinding: v1.0 let a
// Workflow input carry a full CommandLineBinding, which describes a command line the
// process does not have. Everything but loadContents goes.
func TestUpgradeTrimsWorkflowInputBindings(t *testing.T) {
	t.Parallel()

	root := parseNode(t, `cwlVersion: v1.0
class: Workflow
inputs:
  - id: one
    type: File
    inputBinding:
      position: 1
      prefix: "-x"
      loadContents: true
outputs: []
steps: []
`)

	upgraded := Upgrade(&salad.Document{Root: root, BaseURI: testDocURI}, CWLVersionV10)

	inputs := seqField(t, upgraded.Root, keyInputs)

	input, ok := salad.AsMap(inputs[0])
	if !ok {
		t.Fatal("the upgraded input is not a mapping")
	}

	binding, ok := salad.AsMap(fieldNode(input, keyInputBinding))
	if !ok {
		t.Fatal("the upgraded input has no inputBinding")
	}

	if got := binding.Keys(); len(got) != 1 || got[0] != keyLoadContents {
		t.Errorf("inputBinding keys after upgrading = %v, want only %q", got, keyLoadContents)
	}
}

func TestUpgradeLeavesAV12DocumentAlone(t *testing.T) {
	t.Parallel()

	doc := &salad.Document{Root: parseNode(t, toolV10), BaseURI: testDocURI}

	if got := Upgrade(doc, CWLVersionV12); got != doc {
		t.Error("Upgrade rewrote a document that declares the version it already targets")
	}

	if got := Upgrade(nil, CWLVersionV10); got != nil {
		t.Errorf("Upgrade(nil) = %v, want nil", got)
	}
}

// parseNode parses a document body without resolving or validating it.
func parseNode(t *testing.T, src string) salad.Node {
	t.Helper()

	root, err := salad.Parse(testDocURI, []byte(src))
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}

	return root
}

// seqField returns the items of one sequence-valued field of a mapping node.
func seqField(t *testing.T, n salad.Node, key string) []salad.Node {
	t.Helper()

	m, ok := salad.AsMap(n)
	if !ok {
		t.Fatalf("the node is not a mapping, so it has no %q", key)
	}

	seq, ok := salad.AsSeq(fieldNode(m, key))
	if !ok {
		t.Fatalf("the %q field is not a sequence", key)
	}

	return seq.Items()
}

// classOf returns the class of a mapping node.
func classOf(t *testing.T, n salad.Node) string {
	t.Helper()

	m, ok := salad.AsMap(n)
	if !ok {
		t.Fatal("the node is not a mapping, so it has no class")
	}

	return lenientText(m, keyClass)
}

// firstRequirementClass returns the class of a process's first requirement.
func firstRequirementClass(t *testing.T, root salad.Node) string {
	t.Helper()

	return classOf(t, seqField(t, root, keyRequirements)[0])
}

// firstHintClass returns the class of a mapping's first hint.
func firstHintClass(t *testing.T, n salad.Node) string {
	t.Helper()

	return classOf(t, seqField(t, n, keyHints)[0])
}

// firstSecondaryFilePattern returns the pattern of the first secondary file of a
// tool's first input.
func firstSecondaryFilePattern(t *testing.T, root salad.Node) string {
	t.Helper()

	inputs := seqField(t, root, keyInputs)

	input, ok := salad.AsMap(inputs[0])
	if !ok {
		t.Fatal("the first input is not a mapping")
	}

	record, ok := salad.AsMap(seqField(t, input, keySecondaryFiles)[0])
	if !ok {
		t.Fatal("the first secondary file is not a mapping")
	}

	return lenientText(record, keyPattern)
}

// explain renders an error tree the way a command-line tool would, so that a
// failed assertion shows the leaf that explains it rather than a one-line
// summary of a union rejection.
func explain(err error) string {
	var tree *salad.Error
	if errors.As(err, &tree) {
		return tree.Pretty()
	}

	return err.Error()
}

// TestUpgradeStampsEveryProcessOfAGraph covers the two shapes a document root can
// take. Resolution normally replaces a top-level $graph with the sequence it held,
// so the sequence is the shape production sees; the mapping is what a tree built by
// some other means still looks like, and both have to reach every process.
func TestUpgradeStampsEveryProcessOfAGraph(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		read func(*testing.T, salad.Node) []salad.Node
	}{
		{
			name: "a resolved graph, which is a bare sequence",
			src: `- class: CommandLineTool
  cwlVersion: v1.0
  inputs: []
  outputs: []
- class: Workflow
  cwlVersion: v1.0
  inputs: []
  outputs: []
  steps: []
`,
			read: func(t *testing.T, root salad.Node) []salad.Node {
				t.Helper()

				seq, ok := salad.AsSeq(root)
				if !ok {
					t.Fatal("the upgraded root is not a sequence")
				}

				return seq.Items()
			},
		},
		{
			name: "an unresolved graph, which still carries the directive",
			src: `$graph:
  - class: CommandLineTool
    cwlVersion: v1.0
    inputs: []
    outputs: []
  - "not a process at all"
`,
			read: func(t *testing.T, root salad.Node) []salad.Node {
				t.Helper()

				return seqField(t, root, keyGraph)[:1]
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			doc := &salad.Document{Root: parseNode(t, tc.src), BaseURI: testDocURI}

			for i, member := range tc.read(t, Upgrade(doc, CWLVersionV10).Root) {
				if got := firstHintClass(t, member); got != ClassLoadListingRequirement {
					t.Errorf("member %d hint = %q, want %q", i, got, ClassLoadListingRequirement)
				}
			}
		})
	}
}

// TestUpgradeToleratesShapesTheSchemaWouldHaveRejected pins the rewrites' behaviour
// on trees no valid document produces. They run over a validated document in
// production, but they also run over whatever a caller of Upgrade hands them, and a
// rewrite that panicked on a surprise would turn a diagnosable document into a
// crash.
func TestUpgradeToleratesShapesTheSchemaWouldHaveRejected(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
	}{
		{name: "a root that is neither mapping nor sequence", src: "just a string\n"},
		{name: "requirements that are not a sequence", src: "class: Workflow\nrequirements: {}\n"},
		{name: "a requirement that is not a mapping", src: "class: Workflow\nrequirements: [1]\n"},
		{name: "a requirement with no class", src: "class: Workflow\nrequirements:\n  - {}\n"},
		{name: "steps that are not mappings", src: "class: Workflow\nsteps: [1]\n"},
		{name: "inputs that are not mappings", src: "class: Workflow\ninputs: [1]\n"},
		{name: "an input with no inputBinding", src: "class: Workflow\ninputs:\n  - id: one\n"},
		{name: "a lone secondaryFiles pattern", src: "class: CommandLineTool\nsecondaryFiles: \".bai\"\n"},
		{
			name: "secondaryFiles that are already records",
			src:  "class: CommandLineTool\nsecondaryFiles:\n  pattern: \".bai\"\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			doc := &salad.Document{Root: parseNode(t, tc.src), BaseURI: testDocURI}
			if got := Upgrade(doc, CWLVersionV10); got == nil {
				t.Fatal("Upgrade returned nothing")
			}
		})
	}
}

// TestUpgradeWrapsALoneSecondaryFilePattern pins the "top" half of cwltool's
// update_secondaryFiles: a bare pattern becomes a one-element list, because v1.1
// types the field as a record or an array of them and a lone record would be the
// wrong shape for the array case a v1.0 document could equally have written.
func TestUpgradeWrapsALoneSecondaryFilePattern(t *testing.T) {
	t.Parallel()

	src := `class: CommandLineTool
inputs:
  - id: one
    type: File
    secondaryFiles: ".bai"
outputs: []
`

	upgraded := Upgrade(&salad.Document{Root: parseNode(t, src), BaseURI: testDocURI}, CWLVersionV10)

	if got := firstSecondaryFilePattern(t, upgraded.Root); got != ".bai" {
		t.Errorf("pattern after upgrading = %q, want %q", got, ".bai")
	}
}

// TestUpgradeLeavesAFileObjectsSecondaryFilesAlone guards the one collision the
// field name creates: a File value's secondaryFiles holds File and Directory
// objects, not patterns, and wrapping those in a pattern record would corrupt a
// default that was perfectly good.
func TestUpgradeLeavesAFileObjectsSecondaryFilesAlone(t *testing.T) {
	t.Parallel()

	src := `class: CommandLineTool
inputs:
  - id: one
    type: File
    default:
      class: File
      location: primary.bam
      secondaryFiles:
        - class: File
          location: primary.bam.bai
outputs: []
`

	upgraded := Upgrade(&salad.Document{Root: parseNode(t, src), BaseURI: testDocURI}, CWLVersionV10)

	input, ok := salad.AsMap(seqField(t, upgraded.Root, keyInputs)[0])
	if !ok {
		t.Fatal("the first input is not a mapping")
	}

	secondary, ok := salad.AsMap(seqField(t, fieldNode(input, keyDefault), keySecondaryFiles)[0])
	if !ok {
		t.Fatal("the secondary file is not a mapping")
	}

	if got := lenientText(secondary, keyClass); got != "File" {
		t.Errorf("secondary file class after upgrading = %q, want it left as a File", got)
	}
}

func TestLoadFileDocumentReportsAnUnfetchableReference(t *testing.T) {
	t.Parallel()

	_, err := LoadFileDocument(t.Context(), "no-such-document.cwl")
	if err == nil {
		t.Fatal("LoadFileDocument succeeded on a document that does not exist")
	}

	if !strings.Contains(err.Error(), "no-such-document.cwl") {
		t.Errorf("failure = %q, want it to name the document", err)
	}
}

// TestUpgradeKeepsHintsTheDocumentDeclared pins that the two v1.0 defaults are
// prepended rather than substituted: a document's own hints survive, after them.
func TestUpgradeKeepsHintsTheDocumentDeclared(t *testing.T) {
	t.Parallel()

	src := `class: CommandLineTool
cwlVersion: v1.0
inputs: []
outputs: []
hints:
  - class: ` + ClassShellCommandRequirement + `
`

	upgraded := Upgrade(&salad.Document{Root: parseNode(t, src), BaseURI: testDocURI}, CWLVersionV10)

	hints := seqField(t, upgraded.Root, keyHints)

	want := []string{ClassLoadListingRequirement, ClassNetworkAccess, ClassShellCommandRequirement}
	if len(hints) != len(want) {
		t.Fatalf("hints after upgrading = %d, want %d", len(hints), len(want))
	}

	for i, hint := range hints {
		if got := classOf(t, hint); got != want[i] {
			t.Errorf("hint %d = %q, want %q", i, got, want[i])
		}
	}
}
