package cwlexec

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// Literals the job-order tests repeat often enough that goconst — which counts package-wide —
// asks for a name.
const (
	jobHello          = "hello"
	jobGreeting       = "greeting"
	jobHelloText      = "Hello, CWL!\n"
	jobFallback       = "fallback"
	jobExtTxt         = ".txt"
	jobSrcSeven       = "v: 7"
	jobSrcOneAndAHalf = "v: 1.5"
	jobSrcThree       = "v: 3"

	jobCaseUnknownField = "unknown field"
	jobCaseNotAMapping  = "not a mapping"
	jobCaseAbsent       = "absent"

	jobNameCshrc  = ".cshrc"
	jobNameReadme = "README"
	jobNameEmpty  = "empty"
	jobExtBai     = ".bai"
)

// Shorthand for the CWLType symbols the job-order tests declare parameters with.
var (
	jobTypeString    = cwlcore.NewPrimitiveType(cwlcore.PrimitiveString)
	jobTypeInt       = cwlcore.NewPrimitiveType(cwlcore.PrimitiveInt)
	jobTypeLong      = cwlcore.NewPrimitiveType(cwlcore.PrimitiveLong)
	jobTypeFloat     = cwlcore.NewPrimitiveType(cwlcore.PrimitiveFloat)
	jobTypeDouble    = cwlcore.NewPrimitiveType(cwlcore.PrimitiveDouble)
	jobTypeBoolean   = cwlcore.NewPrimitiveType(cwlcore.PrimitiveBoolean)
	jobTypeNull      = cwlcore.NewPrimitiveType(cwlcore.PrimitiveNull)
	jobTypeAny       = cwlcore.NewPrimitiveType(cwlcore.PrimitiveAny)
	jobTypeFile      = cwlcore.NewPrimitiveType(cwlcore.PrimitiveFile)
	jobTypeDirectory = cwlcore.NewPrimitiveType(cwlcore.PrimitiveDirectory)
)

// jobOptionalOf wraps typ in the [null, typ] union that `T?` expands to.
func jobOptionalOf(typ cwlcore.TypeRef) cwlcore.TypeRef {
	return cwlcore.NewUnionType([]cwlcore.TypeRef{jobTypeNull, typ})
}

// jobParam builds a CommandLineTool input parameter.
func jobParam(id string, typ cwlcore.TypeRef) cwlcore.CommandInputParameter {
	return cwlcore.CommandInputParameter{
		ParameterBase: cwlcore.ParameterBase{IDField: id, Type: typ},
	}
}

// jobTool builds a CommandLineTool with the given inputs and no identifier.
func jobTool(params ...cwlcore.CommandInputParameter) *cwlcore.CommandLineTool {
	return &cwlcore.CommandLineTool{Inputs: params}
}

// jobFixtures returns the absolute path of the shared fixture tree.
func jobFixtures(t *testing.T) string {
	t.Helper()

	dir, err := filepath.Abs(filepath.Join("testdata", "joborder"))
	if err != nil {
		t.Fatalf("resolving fixture directory: %v", err)
	}

	return dir
}

// jobParse loads src as a job order located in dir, which need not exist on disk.
func jobParse(t *testing.T, dir, src string, p cwlcore.Process) (map[string]any, error) {
	t.Helper()

	return ParseJobOrder(t.Context(), filepath.Join(dir, "job.yml"), []byte(src), p)
}

// jobMustParse is jobParse for a job order that must load cleanly.
func jobMustParse(t *testing.T, dir, src string, p cwlcore.Process) map[string]any {
	t.Helper()

	values, err := jobParse(t, dir, src, p)
	if err != nil {
		t.Fatalf("loading job order: %v", err)
	}

	return values
}

// jobMustFail is jobParse for a job order that must be rejected, returning the message.
func jobMustFail(t *testing.T, dir, src string, p cwlcore.Process) string {
	t.Helper()

	values, err := jobParse(t, dir, src, p)
	if err == nil {
		t.Fatalf("expected an error, got %v", values)
	}

	var structured *salad.Error
	if !jobAsSaladError(err, &structured) {
		t.Fatalf("expected a *salad.Error, got %T", err)
	}

	return structured.Pretty()
}

// jobAsSaladError is [errors.As] specialised to *salad.Error, kept as a helper so that the tests
// read cleanly.
func jobAsSaladError(err error, target **salad.Error) bool {
	return errors.As(err, target)
}

// jobPretty renders an error tree in full, so that an assertion can look at the leaves rather
// than only at the grouping node's summary.
func jobPretty(t *testing.T, err error) string {
	t.Helper()

	var structured *salad.Error
	if !jobAsSaladError(err, &structured) {
		return err.Error()
	}

	return structured.Pretty()
}

// jobFileValue asserts that name holds a *cwlcore.File and returns it.
func jobFileValue(t *testing.T, values map[string]any, name string) *cwlcore.File {
	t.Helper()

	file, ok := values[name].(*cwlcore.File)
	if !ok {
		t.Fatalf("input %q: expected a *cwlcore.File, got %T", name, values[name])
	}

	return file
}

// jobDirValue asserts that the input "d" holds a *cwlcore.Directory and returns it.
func jobDirValue(t *testing.T, values map[string]any) *cwlcore.Directory {
	t.Helper()

	dir, ok := values["d"].(*cwlcore.Directory)
	if !ok {
		t.Fatalf(`input "d": expected a *cwlcore.Directory, got %T`, values["d"])
	}

	return dir
}

// jobWantMessage fails unless message contains want.
func jobWantMessage(t *testing.T, message, want string) {
	t.Helper()

	if !strings.Contains(message, want) {
		t.Fatalf("error does not mention %q:\n%s", want, message)
	}
}

// jobForeignProcess is a process class this package does not model, built by embedding the
// shared base so that it satisfies the sealed cwlcore.Process interface.
type jobForeignProcess struct {
	cwlcore.ProcessBase
}

// Class names the extension class.
func (*jobForeignProcess) Class() string {
	return "https://example.com/ext#Thing"
}

func TestLoadJobOrderResolvesRelativeLocationAgainstTheJobFile(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)
	tool := jobTool(
		jobParam("greeting", jobTypeString),
		jobParam("message", jobTypeFile),
	)

	for _, name := range []string{"relative.yml", "relative.json"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			values, err := LoadJobOrder(t.Context(), filepath.Join(fixtures, "jobs", name), tool)
			if err != nil {
				t.Fatalf("loading job order: %v", err)
			}

			if values["greeting"] != jobHello {
				t.Errorf("greeting = %v, want %q", values["greeting"], jobHello)
			}

			// The job file says "../files/hello.txt". Against the process working
			// directory that names nothing at all; against the job file's own
			// directory it names the fixture, which is what must happen.
			jobAssertLocatedAt(t, jobFileValue(t, values, "message"),
				filepath.Join(fixtures, "files", jobHello+jobExtTxt))
		})
	}
}

func TestTheSameJobBytesResolveDifferentlyFromDifferentDirectories(t *testing.T) {
	t.Parallel()

	// The direct statement of the rule: nothing about the process changes between these two
	// loads, only the directory the job order is said to live in, and the same relative
	// location lands on a different file each time. (This is asserted by moving the job
	// rather than by moving the process, because t.Chdir cannot be used in a parallel test.)
	src := []byte("f: {class: File, location: data.txt}")

	for _, want := range []string{"first", "second"} {
		t.Run(want, func(t *testing.T) {
			t.Parallel()

			dir := jobWriteFile(t, "data.txt", want)

			values, err := ParseJobOrder(t.Context(), filepath.Join(dir, "job.yml"), src, jobFileTool())
			if err != nil {
				t.Fatalf("loading job order: %v", err)
			}

			file := jobFileValue(t, values, "f")
			if file.Path != filepath.Join(dir, "data.txt") {
				t.Errorf("path = %q, want it under %q", file.Path, dir)
			}

			if file.Size.Int() != int64(len(want)) {
				t.Errorf("size = %d, want %d", file.Size.Int(), len(want))
			}
		})
	}
}

// jobWriteFile writes content to a fresh temporary directory and returns the directory.
func jobWriteFile(t *testing.T, name, content string) string {
	t.Helper()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600)
	if err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	return dir
}

// jobAssertLocatedAt checks that a File's path and location both name want.
func jobAssertLocatedAt(t *testing.T, file *cwlcore.File, want string) {
	t.Helper()

	if file.Path != want {
		t.Errorf("path = %q, want %q", file.Path, want)
	}

	if file.Location != "file://"+want {
		t.Errorf("location = %q, want %q", file.Location, "file://"+want)
	}
}

func TestLoadJobOrderReportsAnUnreadableJobFile(t *testing.T) {
	t.Parallel()

	_, err := LoadJobOrder(t.Context(), filepath.Join(t.TempDir(), "absent.yml"), jobTool())
	if err == nil {
		t.Fatal("expected an error for a job file that does not exist")
	}

	jobWantMessage(t, err.Error(), "reading job order")
}

func TestLoadJobOrderRejectsMalformedYAML(t *testing.T) {
	t.Parallel()

	_, err := LoadJobOrder(t.Context(), filepath.Join(jobFixtures(t), "jobs", "broken.yml"), jobTool())
	if err == nil {
		t.Fatal("expected a parse error")
	}
}

func TestParseJobOrderRejectsAJobThatIsNotAMapping(t *testing.T) {
	t.Parallel()

	_, err := LoadJobOrder(t.Context(), filepath.Join(jobFixtures(t), "jobs", "not-a-mapping.yml"), jobTool())
	if err == nil {
		t.Fatal("expected an error for a sequence at the top level")
	}

	jobWantMessage(t, err.Error(), "must be a mapping")
}

func TestParseJobOrderRejectsANilProcess(t *testing.T) {
	t.Parallel()

	_, err := ParseJobOrder(t.Context(), "job.yml", []byte("{}"), nil)
	if err == nil {
		t.Fatal("expected an error for a nil process")
	}

	jobWantMessage(t, err.Error(), "none was given")
}

func TestParseJobOrderRequiresAnAbsolutePath(t *testing.T) {
	t.Parallel()

	_, err := ParseJobOrder(t.Context(), "job.yml", []byte("{}"), jobTool())
	if err == nil {
		t.Fatal("expected an error for a relative job order path")
	}

	jobWantMessage(t, err.Error(), "must be absolute")
}

func TestDefaultsFillInForAbsentAndExplicitlyNullInputs(t *testing.T) {
	t.Parallel()

	def := salad.NewStringNode(salad.SourceLine{}, jobFallback)

	tool := jobTool(cwlcore.CommandInputParameter{
		ParameterBase: cwlcore.ParameterBase{IDField: jobGreeting, Type: jobTypeString},
		Default:       def,
	})

	for name, src := range map[string]string{
		jobCaseAbsent:   "{}",
		"explicit null": "greeting: null",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			values := jobMustParse(t, t.TempDir(), src, tool)
			if values["greeting"] != jobFallback {
				t.Errorf("greeting = %v, want the default", values["greeting"])
			}
		})
	}

	values := jobMustParse(t, t.TempDir(), "greeting: supplied", tool)
	if values["greeting"] != "supplied" {
		t.Errorf("greeting = %v, want the supplied value", values["greeting"])
	}
}

func TestMissingRequiredInputIsAnErrorNamingTheParameter(t *testing.T) {
	t.Parallel()

	tool := jobTool(jobParam("file:///t.cwl#tool/threshold", jobTypeInt))

	message := jobMustFail(t, t.TempDir(), "{}", tool)
	jobWantMessage(t, message, `input "threshold"`)
	jobWantMessage(t, message, "does not accept null")
}

func TestOptionalInputAbsentIsPresentAndNull(t *testing.T) {
	t.Parallel()

	tool := jobTool(jobParam("maybe", jobOptionalOf(jobTypeString)))

	values := jobMustParse(t, t.TempDir(), "{}", tool)

	value, ok := values["maybe"]
	if !ok {
		t.Fatal("an optional input must still be a key, so that inputs.maybe reads as null")
	}

	if value != nil {
		t.Errorf("maybe = %v, want nil", value)
	}
}

// TestUndeclaredJobKeysAreIgnoredNotRejected pins the reversal the conformance suite forced.
//
// nested_prefixes_arrays runs tests/binding-test.cwl against tests/bwa-mem-job.json, whose
// min_std_max_min and minimum_seed_length name no declared input. Rejecting them — which is what
// this loader used to do, to keep a misspelling from being silent — fails a required test.
func TestUndeclaredJobKeysAreIgnoredNotRejected(t *testing.T) {
	t.Parallel()

	tool := jobTool(jobParam("greeting", jobTypeString))

	values := jobMustParse(t, t.TempDir(), "greetnig: hello\ngreeting: hello", tool)
	if values["greeting"] != jobHello {
		t.Errorf("greeting = %v, want %q", values["greeting"], jobHello)
	}

	if _, present := values["greetnig"]; present {
		t.Error("an undeclared key must be ignored, not carried into the input object")
	}

	reserved := "id: main\n$namespaces: {ex: 'http://example.com/'}\ncwl:tool: t.cwl\ngreeting: hello"

	values = jobMustParse(t, t.TempDir(), reserved, tool)
	if values["greeting"] != jobHello {
		t.Errorf("greeting = %v, want %q", values["greeting"], jobHello)
	}
}

func TestUndeclaredJobKeysAreReported(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		src      string
		declared []string
		want     []string
	}{
		"a misspelling": {
			src:      "greetnig: hello\ngreeting: hello",
			declared: []string{jobGreeting},
			want:     []string{"greetnig", "ignored", "declared=" + jobGreeting},
		},
		"a process with no inputs at all": {
			src:      "greeting: hello",
			declared: nil,
			want:     []string{jobGreeting},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			jobWantLogged(t, jobWarnFor(t, tc.src, tc.declared), tc.want)
		})
	}

	t.Run("only reserved keys say nothing", func(t *testing.T) {
		t.Parallel()

		logged := jobWarnFor(t, "id: main\n$namespaces: {}\ncwl:tool: t.cwl", nil)
		if logged != "" {
			t.Errorf("logged %q, want nothing: a reserved key is not a misspelling", logged)
		}
	})
}

// jobWantLogged fails unless logged mentions every one of want.
func jobWantLogged(t *testing.T, logged string, want []string) {
	t.Helper()

	for _, phrase := range want {
		if !strings.Contains(logged, phrase) {
			t.Errorf("logged %q, want it to mention %q", logged, phrase)
		}
	}
}

// jobWarnFor runs the undeclared-key report over src and returns everything it logged.
func jobWarnFor(t *testing.T, src string, declared []string) string {
	t.Helper()

	root, err := salad.Parse("job.yml", []byte(src))
	if err != nil {
		t.Fatalf("parsing the job order: %v", err)
	}

	supplied, ok := salad.AsMap(root)
	if !ok {
		t.Fatalf("job order parsed as %s, want a mapping", salad.NodeKind(root))
	}

	sink := &strings.Builder{}
	loader := &joLoader{log: slog.New(slog.NewTextHandler(sink, &slog.HandlerOptions{Level: slog.LevelWarn}))}

	loader.warnUndeclared(supplied, declared)

	return sink.String()
}

func TestTheJobLoaderFallsBackToTheDefaultLogger(t *testing.T) {
	t.Parallel()

	loader := &joLoader{}
	if loader.logger() != slog.Default() {
		t.Error("a loader with no logger must report through the default one rather than dropping the diagnostic")
	}
}

func TestEveryFailingInputIsReported(t *testing.T) {
	t.Parallel()

	tool := jobTool(
		jobParam("a", jobTypeInt),
		jobParam("b", jobTypeInt),
	)

	message := jobMustFail(t, t.TempDir(), "a: one\nb: two", tool)
	jobWantMessage(t, message, "a: expected int")
	jobWantMessage(t, message, "b: expected int")
}

func TestDefaultsResolveAgainstTheProcessDocument(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	def, err := salad.Parse("tool.cwl", []byte("{class: File, location: files/hello.txt}"))
	if err != nil {
		t.Fatalf("parsing the default: %v", err)
	}

	param := cwlcore.CommandInputParameter{
		ParameterBase: cwlcore.ParameterBase{IDField: "message", Type: jobTypeFile},
		Default:       def,
	}

	// The job order lives somewhere else entirely; the default is written in the process
	// document, so it is that document's directory the relative location resolves against.
	tool := &cwlcore.CommandLineTool{
		ProcessBase: cwlcore.ProcessBase{ID: "file://" + filepath.Join(fixtures, "tool.cwl")},
		Inputs:      []cwlcore.CommandInputParameter{param},
	}

	values := jobMustParse(t, t.TempDir(), "{}", tool)

	file := jobFileValue(t, values, "message")
	if want := filepath.Join(fixtures, "files", "hello.txt"); file.Path != want {
		t.Errorf("path = %q, want %q", file.Path, want)
	}
}

func TestProcessDirectory(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		id   string
		want string
	}{
		"file IRI":     {id: "file:///docs/tool.cwl#main", want: "/docs"},
		"bare path":    {id: "/docs/tool.cwl", want: "/docs"},
		"blank node":   {id: "_:0a1b", want: jobFallback},
		"http IRI":     {id: "http://example.com/tool.cwl", want: jobFallback},
		"unparseable":  {id: "http://[::1", want: jobFallback},
		"empty schema": {id: "file:", want: jobFallback},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tool := &cwlcore.CommandLineTool{ProcessBase: cwlcore.ProcessBase{ID: tc.id}}
			if got := joProcessDir(tool, jobFallback); got != tc.want {
				t.Errorf("joProcessDir(%q) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}

func TestJobInputsCoversEveryProcessKind(t *testing.T) {
	t.Parallel()

	base := cwlcore.ParameterBase{IDField: "in", Type: jobTypeString}

	processes := map[string]cwlcore.Process{
		"CommandLineTool": &cwlcore.CommandLineTool{
			Inputs: []cwlcore.CommandInputParameter{{ParameterBase: base}},
		},
		"Workflow": &cwlcore.Workflow{
			Inputs: []cwlcore.WorkflowInputParameter{{ParameterBase: base}},
		},
		"ExpressionTool": &cwlcore.ExpressionTool{
			Inputs: []cwlcore.WorkflowInputParameter{{ParameterBase: base}},
		},
		"Operation": &cwlcore.Operation{
			Inputs: []cwlcore.OperationInputParameter{{ParameterBase: base}},
		},
		"RawProcess": &cwlcore.RawProcess{
			Inputs: []cwlcore.OperationInputParameter{{ParameterBase: base}},
		},
	}

	for name, process := range processes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			values := jobMustParse(t, t.TempDir(), "in: value", process)
			if values["in"] != "value" {
				t.Errorf("in = %v, want %q", values["in"], "value")
			}
		})
	}

	t.Run("extension class", func(t *testing.T) {
		t.Parallel()

		// A process class outside this package declares no inputs it can read, so an
		// empty job order is the only valid one.
		values := jobMustParse(t, t.TempDir(), "{}", &jobForeignProcess{})
		if len(values) != 0 {
			t.Errorf("values = %v, want an empty input object", values)
		}
	})
}

func TestLoadContentsOnARecordFieldIsHonoured(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	schema := &cwlcore.RecordSchema{
		Fields: []cwlcore.RecordField{
			{Name: "file:///t.cwl#rec/doc", Type: jobTypeFile, LoadContents: true},
			{Name: "file:///t.cwl#rec/note", Type: jobOptionalOf(jobTypeString)},
		},
	}

	tool := jobTool(jobParam("rec", cwlcore.NewRecordType(schema)))

	values := jobMustParse(t, fixtures, "rec:\n  doc: {class: File, location: files/hello.txt}", tool)

	record, ok := values["rec"].(map[string]any)
	if !ok {
		t.Fatalf("rec: expected a record, got %T", values["rec"])
	}

	file, ok := record["doc"].(*cwlcore.File)
	if !ok {
		t.Fatalf("rec.doc: expected a *cwlcore.File, got %T", record["doc"])
	}

	if file.Contents.Value() != jobHelloText {
		t.Errorf("contents = %q, want the file's text", file.Contents.Value())
	}

	if record["note"] != nil {
		t.Errorf("rec.note = %v, want nil", record["note"])
	}
}

func TestNodeLocToleratesANilNode(t *testing.T) {
	t.Parallel()

	if !joNodeLoc(nil).IsZero() {
		t.Error("joNodeLoc(nil) must be the zero SourceLine")
	}

	node := salad.NewStringNode(salad.SourceLine{File: "x.yml"}, "v")
	if joNodeLoc(node).File != "x.yml" {
		t.Error("joNodeLoc must return the node's own location")
	}
}

func TestJobOrderHonoursContextCancellation(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	tool := jobTool(
		jobParam("message", jobTypeFile),
		jobParam("workdir", jobTypeDirectory),
	)

	src := "message: {class: File, location: files/hello.txt}\nworkdir: {class: Directory, location: dir}"

	_, err := ParseJobOrder(ctx, filepath.Join(fixtures, "job.yml"), []byte(src), tool)
	if err == nil {
		t.Fatal("expected the cancelled context to stop the load")
	}

	jobWantMessage(t, jobPretty(t, err), "context canceled")
}
