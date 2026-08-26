package cwlexec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// Top-level secondaryFiles discovery over a completed input object.
//
// The shared fixture tree carries files/data.bam beside files/data.bam.bai, which is the ordinary
// primary-and-index pair every one of these patterns is aimed at.

// Literals the discovery tests repeat.
const (
	jobBam     = "files/data.bam"
	jobBai     = "data.bam.bai"
	jobPattern = ".bai"
	jobMissing = ".missing"
	jobRenamed = "renamed"
	jobDirName = "dir"
	jobElseIdx = "/elsewhere/index.bai"
)

// jobSecondaryTool is a File input carrying the given secondaryFiles patterns.
func jobSecondaryTool(schemas ...cwlcore.SecondaryFileSchema) *cwlcore.CommandLineTool {
	param := jobParam("f", jobTypeFile)
	param.SecondaryFiles = schemas

	return jobTool(param)
}

// jobPatternOf builds a pattern with no explicit `required`.
func jobPatternOf(pattern string) cwlcore.SecondaryFileSchema {
	return cwlcore.SecondaryFileSchema{Pattern: cwlcore.Expression(pattern)}
}

// jobJSTool is jobSecondaryTool with an InlineJavascriptRequirement, which is what makes a `${...}`
// pattern legal.
func jobJSTool(schemas ...cwlcore.SecondaryFileSchema) *cwlcore.CommandLineTool {
	tool := jobSecondaryTool(schemas...)
	tool.Requirements = []cwlcore.ProcessRequirement{&cwlcore.InlineJavascriptRequirement{}}

	return tool
}

// jobSecondaryNames returns the basenames attached to the input "f", and nil when nothing looked.
func jobSecondaryNames(t *testing.T, values map[string]any) []string {
	t.Helper()

	file := jobFileValue(t, values, "f")
	if file.SecondaryFiles == nil {
		return nil
	}

	names := make([]string, 0, len(file.SecondaryFiles))
	for _, value := range file.SecondaryFiles {
		names = append(names, basenameOf(value))
	}

	return names
}

// jobBamJob is a job order supplying the .bam primary and nothing else.
func jobBamJob() string {
	return "f: {class: File, location: " + jobBam + "}"
}

func TestSecondaryFilesAreDiscoveredFromDisk(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	cases := map[string]struct {
		pattern string
		want    []string
	}{
		"a suffix": {
			pattern: jobPattern,
			want:    []string{jobBai},
		},
		"the caret rule strips an extension": {
			// files/data.bam and files/data.bam.bai: "^.bam.bai" strips ".bam" and
			// appends, which reaches the index from the primary the long way round.
			pattern: "^.bam.bai",
			want:    []string{jobBai},
		},
		"an expression naming the file": {
			pattern: "$(self.basename).bai",
			want:    []string{jobBai},
		},
		"an expression reading another input": {
			pattern: "$(inputs.suffix)",
			want:    []string{jobBai},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// A second parameter is declared throughout so that the "reads another
			// input" case has something to read, and so that every other case proves
			// the extra parameter changes nothing.
			tool := jobSecondaryTool(jobPatternOf(tc.pattern))
			tool.Inputs = append(tool.Inputs, jobParam("suffix", jobTypeString))

			src := jobBamJob() + "\nsuffix: " + jobBai + "\n"

			got := jobSecondaryNames(t, jobMustParse(t, fixtures, src, tool))
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("secondaryFiles = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestADiscoveredSecondaryFileIsMeasuredAndNamed(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	file := jobFileValue(t, jobMustParse(t, fixtures, jobBamJob(),
		jobSecondaryTool(jobPatternOf(jobPattern))), "f")

	if len(file.SecondaryFiles) != 1 {
		t.Fatalf("secondaryFiles = %v, want the one the pattern names", file.SecondaryFiles)
	}

	index, ok := file.SecondaryFiles[0].(*cwlcore.File)
	if !ok {
		t.Fatalf("secondaryFiles[0] is %T, want *cwlcore.File", file.SecondaryFiles[0])
	}

	if want := filepath.Join(fixtures, "files", jobBai); index.Path != want {
		t.Errorf("path = %q, want %q", index.Path, want)
	}

	// A discovered secondary goes through the same measurement a supplied one does, so an
	// expression reading self.secondaryFiles[0].size gets the same answer either way.
	if !index.Size.IsSet() || index.Checksum == "" {
		t.Errorf("size = %v, checksum = %q, want both read from disk", index.Size, index.Checksum)
	}

	if index.Nameroot != "data.bam" || index.Nameext != jobExtBai {
		t.Errorf("name fields = %q/%q", index.Nameroot, index.Nameext)
	}
}

func TestARequiredSecondaryFileThatIsAbsentIsAnError(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	cases := map[string]cwlcore.SecondaryFileSchema{
		// Process.yml: "When not explicitly specified, secondary files specified for
		// `inputs` are required and `outputs` are optional." So a bare pattern is required
		// here, which is the opposite of what the output side makes of the same field.
		"required by default": jobPatternOf(jobMissing),
		"required explicitly": {Pattern: jobMissing, Required: cwlcore.NewExprBool(true)},
		"required by an expression": {
			Pattern:  jobMissing,
			Required: cwlcore.NewExprBoolExpression("$(inputs.want)"),
		},
	}

	for name, schema := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tool := jobSecondaryTool(schema)
			tool.Inputs = append(tool.Inputs, jobParam("want", jobTypeBoolean))

			message := jobMustFail(t, fixtures, jobBamJob()+"\nwant: true\n", tool)

			jobWantMessage(t, message, "required secondary file does not exist")
			jobWantMessage(t, message, "data.bam.missing")
		})
	}
}

func TestAnOptionalSecondaryFileThatIsAbsentIsDropped(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	cases := map[string]cwlcore.SecondaryFileSchema{
		"declared not required": {Pattern: jobMissing, Required: cwlcore.NewExprBool(false)},
		// The loader normally rewrites a trailing "?" into required: false while the
		// document is resolved, so this is the hand-built spelling reaching the same place.
		"the ? marker": jobPatternOf(".missing?"),
		"an expression evaluating to false": {
			Pattern:  jobMissing,
			Required: cwlcore.NewExprBoolExpression("$(inputs.want)"),
		},
		// A `required` expression that evaluates to null is not required, which is not the
		// same as leaving the field unset. filesarray_secondaryfiles pins it: the suite
		// runs docker-array-secondaryfiles.cwl with `require_dat` unset, so
		// `required: $(inputs.require_dat)` is null against a file that does not exist, and
		// the run must still succeed.
		"an expression evaluating to null": {
			Pattern:  jobMissing,
			Required: cwlcore.NewExprBoolExpression("$(inputs.absent)"),
		},
	}

	for name, schema := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tool := jobSecondaryTool(schema)
			tool.Inputs = append(tool.Inputs,
				jobParam("want", jobTypeBoolean),
				jobParam("absent", jobOptionalOf(jobTypeString)))

			got := jobSecondaryNames(t, jobMustParse(t, fixtures, jobBamJob()+"\nwant: false\n", tool))
			if len(got) != 0 {
				t.Errorf("secondaryFiles = %v, want none: the pattern named nothing that exists", got)
			}
		})
	}
}

func TestAppliedPatternsLeaveAnEmptyListRatherThanNil(t *testing.T) {
	t.Parallel()

	// An empty list records that the patterns were applied and found nothing, which is a
	// different statement from the nil that means nobody looked.
	fixtures := jobFixtures(t)
	optional := cwlcore.SecondaryFileSchema{Pattern: jobMissing, Required: cwlcore.NewExprBool(false)}

	applied := jobFileValue(t, jobMustParse(t, fixtures, jobBamJob(), jobSecondaryTool(optional)), "f")
	if applied.SecondaryFiles == nil {
		t.Error("secondaryFiles = nil after a pattern was applied, want an empty list")
	}

	none := jobFileValue(t, jobMustParse(t, fixtures, jobBamJob(), jobFileTool()), "f")
	if none.SecondaryFiles != nil {
		t.Errorf("secondaryFiles = %v with no pattern declared, want nil", none.SecondaryFiles)
	}
}

func TestAnExpressionPatternMayProduceNullsObjectsAndLists(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)
	index := filepath.Join(fixtures, "files", jobBai)

	cases := map[string]struct {
		pattern string
		want    []string
	}{
		// Process.yml: "The expression may return 'null' in which case there is no
		// secondaryFile from that expression." Not a missing file — none was named — so a
		// required pattern is not violated by one.
		"a null result": {pattern: "${ return null; }", want: nil},
		"empty list":    {pattern: "${ return []; }", want: nil},
		"a list of names": {
			pattern: "${ return ['" + jobBai + "', null]; }",
			want:    []string{jobBai},
		},
		"a File object": {
			pattern: "${ return {'class': 'File', 'path': '" + index + "'}; }",
			want:    []string{jobBai},
		},
		// "If an expression returns a File object with the same `location` but a different
		// `basename` ... the expression result takes precedence."
		"a File object choosing its own basename": {
			pattern: "${ return {'class': 'File', 'location': 'file://" + index + "', 'basename': 'renamed.bai'}; }",
			want:    []string{"renamed.bai"},
		},
		"a Directory object": {
			pattern: "${ return {'class': 'Directory', 'path': '" + filepath.Join(fixtures, jobDirName) + "'}; }",
			want:    []string{jobDirName},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := jobSecondaryNames(t, jobMustParse(t, fixtures, jobBamJob(),
				jobJSTool(jobPatternOf(tc.pattern))))

			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("secondaryFiles = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSecondaryFileDiscoveryErrors(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	cases := map[string]struct {
		schema cwlcore.SecondaryFileSchema
		want   string
	}{
		"a pattern that does not evaluate": {
			schema: jobPatternOf("${ throw new Error('boom'); }"),
			want:   "boom",
		},
		"a pattern producing a number": {
			schema: jobPatternOf("${ return 7; }"),
			want:   "did not produce a name or a File",
		},
		"a required expression that does not evaluate": {
			schema: cwlcore.SecondaryFileSchema{
				Pattern:  jobPattern,
				Required: cwlcore.NewExprBoolExpression("${ throw new Error('bang'); }"),
			},
			want: "bang",
		},
		"a required expression producing a string": {
			schema: cwlcore.SecondaryFileSchema{
				Pattern:  jobPattern,
				Required: cwlcore.NewExprBoolExpression("${ return 'yes'; }"),
			},
			want: "must evaluate to a boolean or null",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			jobWantMessage(t, jobMustFail(t, fixtures, jobBamJob(), jobJSTool(tc.schema)), tc.want)
		})
	}
}

func TestASuppliedSecondaryFileIsNotRediscovered(t *testing.T) {
	t.Parallel()

	// The pattern names exactly what the job order already supplied. Attaching it again would
	// both duplicate the entry and throw away the basename the job order chose for it.
	src := "f:\n" +
		"  class: File\n" +
		"  location: " + jobBam + "\n" +
		"  secondaryFiles:\n" +
		"    - {class: File, location: files/data.bam.bai, basename: " + jobBai + "}\n"

	got := jobSecondaryNames(t, jobMustParse(t, jobFixtures(t), src,
		jobSecondaryTool(jobPatternOf(jobPattern))))

	if len(got) != 1 || got[0] != jobBai {
		t.Errorf("secondaryFiles = %v, want the single entry the job order supplied", got)
	}
}

func TestAFileLiteralIsNotSearchedForCompanions(t *testing.T) {
	t.Parallel()

	// A literal has no directory on any filesystem for a companion to sit in. Reporting the
	// pattern as violated would blame it for the primary's own placement.
	values := jobMustParse(t, jobFixtures(t), "f: {class: File, contents: hi, basename: note.txt}",
		jobSecondaryTool(jobPatternOf(jobMissing)))

	if got := jobSecondaryNames(t, values); got != nil {
		t.Errorf("secondaryFiles = %v, want nil for a literal", got)
	}
}

func TestDiscoveryReachesRecordFieldsAndArrayItems(t *testing.T) {
	t.Parallel()

	// The shape of conformance's record-in-secondaryFiles.cwl: a record whose File field and
	// array-of-File field each declare a pattern of their own. A record declares none — it is
	// not a File — so a runner that only looks at parameters finds nothing here.
	record := cwlcore.NewRecordType(&cwlcore.RecordSchema{
		Fields: []cwlcore.RecordField{
			{
				Name:           "f1",
				Type:           jobTypeFile,
				SecondaryFiles: []cwlcore.SecondaryFileSchema{jobPatternOf(jobPattern)},
			},
			{
				Name: "f2",
				Type: cwlcore.NewArrayType(&cwlcore.ArraySchema{Items: jobTypeFile}),
				SecondaryFiles: []cwlcore.SecondaryFileSchema{
					{Pattern: ".bai", Required: cwlcore.NewExprBool(false)},
				},
			},
		},
	})

	src := "r:\n" +
		"  f1: {class: File, location: " + jobBam + "}\n" +
		"  f2:\n" +
		"    - {class: File, location: " + jobBam + "}\n" +
		"    - {class: File, location: files/hello.txt}\n"

	values := jobMustParse(t, jobFixtures(t), src, jobTool(jobParam("r", record)))

	fields, ok := values["r"].(map[string]any)
	if !ok {
		t.Fatalf(`input "r" is %T, want a record`, values["r"])
	}

	jobWantSecondary(t, fields["f1"], []string{jobBai})

	items, ok := fields["f2"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("f2 is %T, want a two-element array", fields["f2"])
	}

	jobWantSecondary(t, items[0], []string{jobBai})
	jobWantSecondary(t, items[1], nil)
}

// jobWantSecondary fails unless value is a File carrying exactly the named secondary files.
func jobWantSecondary(t *testing.T, value any, want []string) {
	t.Helper()

	file, ok := value.(*cwlcore.File)
	if !ok {
		t.Fatalf("value is %T, want *cwlcore.File", value)
	}

	got := make([]string, 0, len(file.SecondaryFiles))
	for _, secondary := range file.SecondaryFiles {
		got = append(got, basenameOf(secondary))
	}

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("secondaryFiles = %v, want %v", got, want)
	}
}

func TestDiscoveryLooksThroughAUnion(t *testing.T) {
	t.Parallel()

	// A `File[]?` is a union whose array member holds the item declaration, so the descent has
	// to look past the union to find it.
	files := jobOptionalOf(cwlcore.NewArrayType(&cwlcore.ArraySchema{Items: jobTypeFile}))

	list := jobParam("list", files)
	list.SecondaryFiles = []cwlcore.SecondaryFileSchema{jobPatternOf(jobPattern)}

	values := jobMustParse(t, jobFixtures(t), "list: [{class: File, location: "+jobBam+"}]", jobTool(list))

	items, ok := values["list"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("list is %T, want a one-element array", values["list"])
	}

	jobWantSecondary(t, items[0], []string{jobBai})
}

// jobPairDef declares, as a SchemaDefRequirement writes it, a record whose File field carries a
// secondaryFiles pattern of its own.
const jobPairDef = `
- name: Pair
  type: record
  fields:
    - name: f1
      type: File
      secondaryFiles: .bai
`

func TestDiscoveryLooksThroughANamedType(t *testing.T) {
	t.Parallel()

	// A record named by a SchemaDefRequirement carries its field declarations only once the
	// name is resolved. Resolving is safe here, and only here: a job order is loaded against
	// the top-level process, so its own requirements are the whole scope.
	node, err := salad.Parse("schemadef.yml", []byte(jobPairDef))
	if err != nil {
		t.Fatalf("parsing the schemadef fixture: %v", err)
	}

	declarations, ok := salad.AsSeq(node)
	if !ok {
		t.Fatalf("schemadef fixture parsed as %s, want a sequence", salad.NodeKind(node))
	}

	tool := jobTool(jobParam("r", cwlcore.NewNamedType("Pair")))
	tool.Requirements = []cwlcore.ProcessRequirement{
		&cwlcore.SchemaDefRequirement{Types: declarations.Items()},
	}

	values := jobMustParse(t, jobFixtures(t), "r: {f1: {class: File, location: "+jobBam+"}}", tool)

	fields, ok := values["r"].(map[string]any)
	if !ok {
		t.Fatalf(`input "r" is %T, want a record`, values["r"])
	}

	jobWantSecondary(t, fields["f1"], []string{jobBai})
}

func TestDiscoveryStopsWhenTheContextIsCancelled(t *testing.T) {
	t.Parallel()

	// Discovery reads and hashes every companion it finds, so a cancelled run must stop here
	// too and not only in the conversion pass. It is exercised directly because the conversion
	// pass observes the same context first and would never let a cancelled one reach this far.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	tool := jobSecondaryTool(jobPatternOf(jobPattern))
	inputs := map[string]any{"f": &cwlcore.File{Path: filepath.Join(jobFixtures(t), "files", "data.bam")}}

	err := joDiscoverSecondaryFiles(ctx, inputs, tool)
	if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Errorf("err = %v, want the cancellation to reach discovery", err)
	}
}

func TestSecondaryLocalResolvesOnlyLocalReferences(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		ref  string
		want string
	}{
		"a relative name":       {ref: "index.bai", want: "/base/index.bai"},
		"an absolute path":      {ref: jobElseIdx, want: jobElseIdx},
		"a file IRI":            {ref: "file://" + jobElseIdx, want: jobElseIdx},
		"an empty reference":    {ref: "", want: ""},
		"another scheme":        {ref: "https://example.com/index.bai", want: ""},
		"a scheme with no path": {ref: "mailto:someone@example.com", want: ""},
		"not a URI at all":      {ref: "http://[::1", want: ""},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := joSecondaryLocal(tc.ref, "/base"); got != tc.want {
				t.Errorf("joSecondaryLocal(%q) = %q, want %q", tc.ref, got, tc.want)
			}
		})
	}
}

func TestARenamedDirectoryKeepsTheNameTheExpressionChose(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)
	pattern := "${ return {'class': 'Directory', 'path': '" + filepath.Join(fixtures, jobDirName) +
		"', 'basename': '" + jobRenamed + "'}; }"

	got := jobSecondaryNames(t, jobMustParse(t, fixtures, jobBamJob(), jobJSTool(jobPatternOf(pattern))))
	if len(got) != 1 || got[0] != jobRenamed {
		t.Errorf("secondaryFiles = %v, want the basename the expression chose", got)
	}
}

func TestDiscoveryReportsAnUnreadableCompanion(t *testing.T) {
	t.Parallel()
	stgSkipIfRoot(t)

	// The companion exists — so the pattern is satisfied — but its bytes cannot be read, which
	// is the one way measuring it can fail after everything before it has succeeded.
	dir := jobWriteFile(t, "primary.bam", jobHelloText)
	companion := filepath.Join(dir, "primary.bam.bai")

	err := os.WriteFile(companion, []byte("index"), 0o600)
	if err != nil {
		t.Fatalf("writing the companion: %v", err)
	}

	err = os.Chmod(companion, 0o000)
	if err != nil {
		t.Fatalf("making the companion unreadable: %v", err)
	}

	t.Cleanup(func() { stgRestore(t, companion, 0o600) })

	message := jobMustFail(t, dir, "f: {class: File, location: primary.bam}",
		jobSecondaryTool(jobPatternOf(jobPattern)))

	jobWantMessage(t, message, "primary.bam.bai")
}

func TestDiscoveryErrorsPropagateOutOfArraysAndRecords(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	// An optional record wrapping an array, so that the failure has to travel back up through
	// both descents — and so that joRecordFields has a union to look through on the way down.
	record := cwlcore.NewRecordType(&cwlcore.RecordSchema{
		Fields: []cwlcore.RecordField{{
			Name:           "f2",
			Type:           cwlcore.NewArrayType(&cwlcore.ArraySchema{Items: jobTypeFile}),
			SecondaryFiles: []cwlcore.SecondaryFileSchema{jobPatternOf(jobMissing)},
		}},
	})

	src := "r:\n" +
		"  f2:\n" +
		"    - {class: File, location: " + jobBam + "}\n"

	message := jobMustFail(t, fixtures, src, jobTool(jobParam("r", jobOptionalOf(record))))

	jobWantMessage(t, message, "r.f2[0]")
	jobWantMessage(t, message, "required secondary file does not exist")
}

func TestDiscoverySkipsATypedNilFile(t *testing.T) {
	t.Parallel()

	// Not reachable from a job order this package loaded — an absent optional input is an
	// untyped nil — but the input object is public, and a typed nil in it must not panic.
	inputs := map[string]any{"f": (*cwlcore.File)(nil)}

	err := joDiscoverSecondaryFiles(t.Context(), inputs, jobSecondaryTool(jobPatternOf(jobPattern)))
	if err != nil {
		t.Errorf("joDiscoverSecondaryFiles = %v, want a typed nil to be skipped", err)
	}
}
