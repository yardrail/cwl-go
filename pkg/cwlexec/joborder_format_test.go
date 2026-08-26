package cwlexec

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// Literals the format tests repeat.
const (
	jobEDAM       = "http://edamontology.org/"
	jobFormat1    = "http://example.com/format1"
	jobFormat2    = "http://example.com/format2"
	jobPrefixed   = "edam:format_1929"
	jobExpanded   = jobEDAM + "format_1929"
	jobSpacesDoc  = "$namespaces: {edam: '" + jobEDAM + "'}\n"
	jobFormatExpr = "$(inputs.f.format)"
)

// jobFormatParam builds a File input parameter constrained to the given format IRIs.
func jobFormatParam(name string, formats ...string) cwlcore.CommandInputParameter {
	param := jobParam(name, jobTypeFile)
	for _, iri := range formats {
		param.Format = append(param.Format, cwlcore.Expression(iri))
	}

	return param
}

// jobProcessAt writes a process document into a fresh directory and returns a tool whose
// identifier names it, which is what job-order loading recovers the document from.
func jobProcessAt(t *testing.T, doc string, params ...cwlcore.CommandInputParameter) *cwlcore.CommandLineTool {
	t.Helper()

	dir := t.TempDir()
	local := filepath.Join(dir, "tool.cwl")

	err := os.WriteFile(local, []byte(doc), 0o600)
	if err != nil {
		t.Fatalf("writing the process document: %v", err)
	}

	tool := jobTool(params...)
	tool.ID = "file://" + local

	return tool
}

func TestExpandFormatAppliesOnlyPrefixResolution(t *testing.T) {
	t.Parallel()

	vocab := joVocabulary{namespaces: map[string]string{"edam": jobEDAM}}

	cases := map[string]struct {
		in   string
		want string
	}{
		"a declared prefix":       {in: jobPrefixed, want: jobExpanded},
		"an undeclared prefix":    {in: "gx:fasta", want: "gx:fasta"},
		"an absolute IRI":         {in: jobFormat1, want: jobFormat1},
		"no colon at all":         {in: "fasta", want: "fasta"},
		"a leading colon":         {in: ":fasta", want: ":fasta"},
		"an empty value":          {in: "", want: ""},
		"only the first colon":    {in: "edam:a:b", want: jobEDAM + "a:b"},
		"against no table at all": {in: jobPrefixed, want: jobPrefixed},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			table := vocab
			if name == "against no table at all" {
				table = joVocabulary{}
			}

			if got := table.expandFormat(tc.in); got != tc.want {
				t.Errorf("expandFormat(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestReadVocabularyFromDocuments(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		doc          string
		job          string
		wantEDAM     string
		wantOntology bool
	}{
		"the process document declares the prefix": {
			doc:      jobSpacesDoc + "class: CommandLineTool\n",
			job:      "{}",
			wantEDAM: jobEDAM,
		},
		"the job order declares its own": {
			doc:      "class: CommandLineTool\n",
			job:      jobSpacesDoc,
			wantEDAM: jobEDAM,
		},
		"the job order wins a conflict": {
			doc:      "$namespaces: {edam: 'http://stale/'}\n",
			job:      jobSpacesDoc,
			wantEDAM: jobEDAM,
		},
		"$schemas as a single IRI": {
			doc:          "$schemas: EDAM.owl\n",
			job:          "{}",
			wantOntology: true,
		},
		"$schemas as a list": {
			doc:          "$schemas: [EDAM.owl, gx_edam.ttl]\n",
			job:          "{}",
			wantOntology: true,
		},
		"$schemas as an empty list": {
			doc: "$schemas: []\n",
			job: "{}",
		},
		"a $namespaces entry that is not a string": {
			doc: "$namespaces: {edam: [1, 2]}\n",
			job: "{}",
		},
		"no directives at all": {
			doc: "class: CommandLineTool\n",
			job: "{}",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tool := jobProcessAt(t, tc.doc)

			root, err := salad.Parse("job.yml", []byte(tc.job))
			if err != nil {
				t.Fatalf("parsing the job order: %v", err)
			}

			vocab := joReadVocabulary(tool, root)

			if got := vocab.namespaces["edam"]; got != tc.wantEDAM {
				t.Errorf("namespaces[edam] = %q, want %q", got, tc.wantEDAM)
			}

			if vocab.hasOntology != tc.wantOntology {
				t.Errorf("hasOntology = %v, want %v", vocab.hasOntology, tc.wantOntology)
			}
		})
	}
}

func TestReadVocabularyToleratesDocumentsItCannotRead(t *testing.T) {
	t.Parallel()

	empty, err := salad.Parse("job.yml", []byte("{}"))
	if err != nil {
		t.Fatalf("parsing the job order: %v", err)
	}

	missing := jobProcessAt(t, jobSpacesDoc)

	err = os.Remove(joProcessFile(missing))
	if err != nil {
		t.Fatalf("removing the process document: %v", err)
	}

	cases := map[string]cwlcore.Process{
		"a process with no identifier":     jobTool(),
		"a process at a remote URL":        jobRemoteTool(),
		"a document that does not exist":   missing,
		"a document that does not parse":   jobProcessAt(t, "a: [1\n"),
		"a document that is not a mapping": jobProcessAt(t, "- one\n- two\n"),
	}

	for name, process := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			vocab := joReadVocabulary(process, empty)
			if len(vocab.namespaces) != 0 || vocab.hasOntology {
				t.Errorf("vocabulary = %+v, want an empty one", vocab)
			}
		})
	}
}

// jobRemoteTool is a tool whose identifier names a document on another host, which no local read
// can reach.
func jobRemoteTool() *cwlcore.CommandLineTool {
	tool := jobTool()
	tool.ID = "https://example.com/tool.cwl#main"

	return tool
}

func TestTheProcessDocumentIsFoundThroughAnInputWhenTheProcessIsAnonymous(t *testing.T) {
	t.Parallel()

	// The case that matters most: the schema makes `id` optional, so almost every tool in the
	// conformance suite decodes to a blank-node identifier that names no document at all. The
	// document has to be reached through a parameter's source location instead, or a job order
	// written against such a tool would never see its $namespaces.
	tool := jobProcessAt(t, jobSpacesDoc)
	local := joProcessFile(tool)

	src, err := os.ReadFile(filepath.Clean(local))
	if err != nil {
		t.Fatalf("reading the process document back: %v", err)
	}

	node, err := salad.Parse("file://"+local, src)
	if err != nil {
		t.Fatalf("parsing the process document: %v", err)
	}

	// A parameter with no node at all comes first, so that the search has to walk past it.
	anonymous := jobTool(jobParam("nodeless", jobTypeFile), jobParam("f", jobTypeFile))
	anonymous.ID = "_:6f1c2d7e"
	anonymous.Inputs[1].Node = node

	if got := joProcessFile(anonymous); got != local {
		t.Fatalf("joProcessFile = %q, want %q", got, local)
	}

	empty, err := salad.Parse("job.yml", []byte("{}"))
	if err != nil {
		t.Fatalf("parsing the job order: %v", err)
	}

	if got := joReadVocabulary(anonymous, empty).namespaces["edam"]; got != jobEDAM {
		t.Errorf("namespaces[edam] = %q, want %q", got, jobEDAM)
	}
}

func TestJobOrderFormatIsExpandedAgainstTheProcessNamespaces(t *testing.T) {
	t.Parallel()

	// The shape of conformance's format_checking_subclass: the prefix lives in the process
	// document and the prefixed name lives in the job order, so a runner that never brings the
	// two together carries "edam:format_1929" through where the full IRI is expected.
	tool := jobProcessAt(t, jobSpacesDoc, jobParam("f", jobTypeFile))

	values := jobMustParse(t, jobFixtures(t),
		"f: {class: File, location: files/hello.txt, format: "+jobPrefixed+"}", tool)

	if got := jobFileValue(t, values, "f").Format; got != jobExpanded {
		t.Errorf("format = %q, want %q", got, jobExpanded)
	}
}

func TestAllowedFormatsDropsAnExpressionConstraint(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		declared []cwlcore.Expression
		want     []string
	}{
		"nothing declared": {declared: nil, want: make([]string, 0)},
		"one IRI": {
			declared: []cwlcore.Expression{jobFormat1},
			want:     []string{jobFormat1},
		},
		"two IRIs": {
			declared: []cwlcore.Expression{jobFormat1, jobFormat2},
			want:     []string{jobFormat1, jobFormat2},
		},
		"an expression": {declared: []cwlcore.Expression{jobFormatExpr}, want: nil},
		"an expression among IRIs": {
			declared: []cwlcore.Expression{jobFormat1, jobFormatExpr},
			want:     nil,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := joAllowedFormats(tc.declared); !slices.Equal(got, tc.want) {
				t.Errorf("joAllowedFormats = %v, want %v", got, tc.want)
			}
		})
	}
}

// jobRecordFormatTool mirrors conformance's tests/record-in-format.cwl: a File parameter with a
// format, and a record whose File field and array-of-File field each carry one of their own.
func jobRecordFormatTool(t *testing.T) *cwlcore.CommandLineTool {
	t.Helper()

	record := cwlcore.NewRecordType(&cwlcore.RecordSchema{
		Fields: []cwlcore.RecordField{
			{Name: "f1", Type: jobTypeFile, Format: []cwlcore.Expression{jobFormat1}},
			{
				Name:   "f2",
				Type:   cwlcore.NewArrayType(&cwlcore.ArraySchema{Items: jobTypeFile}),
				Format: []cwlcore.Expression{jobFormat2},
			},
		},
	})

	return jobProcessAt(t, "class: CommandLineTool\n",
		jobFormatParam("regular_input", jobFormat1),
		jobParam("record_input", record))
}

// jobRecordFormatJob renders a job order for [jobRecordFormatTool], with each of the three
// format-bearing positions settable so that one at a time can be made wrong.
func jobRecordFormatJob(regular, f1, f2 string) string {
	return "regular_input: {class: File, location: files/hello.txt, format: " + regular + "}\n" +
		"record_input:\n" +
		"  f1: {class: File, location: files/README, format: " + f1 + "}\n" +
		"  f2:\n" +
		"    - {class: File, location: files/empty.txt, format: " + jobFormat2 + "}\n" +
		"    - {class: File, location: files/data.bam, format: " + f2 + "}\n"
}

func TestFormatIsCheckedInsideRecordsAndArraysToo(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	t.Run("a job order every position agrees with", func(t *testing.T) {
		t.Parallel()

		jobMustParse(t, fixtures, jobRecordFormatJob(jobFormat1, jobFormat1, jobFormat2),
			jobRecordFormatTool(t))
	})

	cases := map[string]struct {
		src  string
		want string
	}{
		"a bad regular input": {
			src:  jobRecordFormatJob("http://example.com/formatZ", jobFormat1, jobFormat2),
			want: "regular_input:",
		},
		"a bad record field": {
			src:  jobRecordFormatJob(jobFormat1, "http://example.com/formatZ", jobFormat2),
			want: "record_input.f1:",
		},
		"a bad array item": {
			src:  jobRecordFormatJob(jobFormat1, jobFormat1, "http://example.com/formatZ"),
			want: "record_input.f2[1]:",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			message := jobMustFail(t, fixtures, tc.src, jobRecordFormatTool(t))
			jobWantMessage(t, message, tc.want)
			jobWantMessage(t, message, "formatZ")
		})
	}
}

func TestAFileWithNoFormatFailsAParameterThatRequiresOne(t *testing.T) {
	t.Parallel()

	tool := jobProcessAt(t, "class: CommandLineTool\n", jobFormatParam("f", jobFormat1))

	message := jobMustFail(t, jobFixtures(t), "f: {class: File, location: files/hello.txt}", tool)
	jobWantMessage(t, message, "f:")
}

func TestFormatIsNotCheckedWhenTheDocumentNamesAnOntology(t *testing.T) {
	t.Parallel()

	// conformance's format_checking_subclass and format_checking_equivalentclass both bind a
	// file whose format is a subtype of the declared one. Exact matching would reject both, so
	// the specification's "if no ontology is available, file formats may be tested by exact
	// match" is applied strictly: only where there is no $schemas at all.
	tool := jobProcessAt(t, jobSpacesDoc+"$schemas: [EDAM.owl]\n",
		jobFormatParam("f", jobEDAM+"format_2330"))

	values := jobMustParse(t, jobFixtures(t),
		"f: {class: File, location: files/hello.txt, format: "+jobPrefixed+"}", tool)

	if got := jobFileValue(t, values, "f").Format; got != jobExpanded {
		t.Errorf("format = %q, want the expansion to happen even though the check does not", got)
	}
}

func TestAParameterFormatIsNotAppliedToSecondaryFiles(t *testing.T) {
	t.Parallel()

	// CheckFormat's own contract: "Secondary files are not examined against the parameter's
	// format". The descent has to clear the constraint for that to hold.
	tool := jobProcessAt(t, "class: CommandLineTool\n", jobFormatParam("f", jobFormat1))

	src := "f:\n" +
		"  class: File\n" +
		"  location: files/data.bam\n" +
		"  format: " + jobFormat1 + "\n" +
		"  secondaryFiles:\n" +
		"    - {class: File, location: files/data.bam.bai}\n"

	file := jobFileValue(t, jobMustParse(t, jobFixtures(t), src, tool), "f")
	if len(file.SecondaryFiles) != 1 {
		t.Fatalf("secondaryFiles = %v, want the one the job order supplied", file.SecondaryFiles)
	}
}
