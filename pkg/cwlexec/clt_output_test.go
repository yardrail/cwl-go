package cwlexec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

func TestCollectOutputsSingleFileGlob(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, "out.txt", outContentHello)

	tool := outTestTool(outTestParam(outToolID+"/result", outTypeFile, outGlobBinding("out.txt")))
	outputs := outCollect(t, tool, dir, 0)

	file := outWantFile(t, outputs, "result", "out.txt", outSumHello, int64(len(outContentHello)))

	assertDeepEqual(t, "nameroot", file.Nameroot, "out")
	assertDeepEqual(t, "nameext", file.Nameext, ".txt")
	assertDeepEqual(t, "path", file.Path, filepath.Join(dir, "out.txt"))
	assertDeepEqual(t, "dirname", file.Dirname, dir)
	assertDeepEqual(t, "location", file.Location, outFileURI(filepath.Join(dir, "out.txt")))

	if file.Contents.IsSet() {
		t.Errorf("contents = %s, want unset without loadContents", file.Contents)
	}
}

func TestCollectOutputsZeroByteFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, "empty.dat", "")

	tool := outTestTool(outTestParam("#tool/result", outTypeFile, outGlobBinding("empty.dat")))
	outputs := outCollect(t, tool, dir, 0)

	// Size 0 must be *present*, not absent: an empty file has a size, and OptInt exists so that
	// the two cannot be confused.
	outWantFile(t, outputs, "result", "empty.dat", outSumEmpty, 0)
}

func TestCollectOutputsMultiFileGlobIsSorted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range []string{outNameC, outNameA, outNameB} {
		outWriteFile(t, dir, name, name)
	}

	tool := outTestTool(outTestParam("#tool/all", outTypeFileArray, outGlobBinding("*.txt")))

	// Ten runs, because a single agreeing run would not distinguish a sorted result from a
	// lucky readdir order.
	for range 10 {
		outputs := outCollect(t, tool, dir, 0)
		assertDeepEqual(t, "basenames", outBasenames(t, outputs["all"]), []string{outNameA, outNameB, outNameC})
	}
}

func TestCollectOutputsKeepsPatternOrderBetweenPatterns(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, "z.log", "z")
	outWriteFile(t, dir, outNameA, "a")

	tool := outTestTool(outTestParam("#tool/all", outTypeFileArray, outGlobBinding("*.log", "*.txt")))
	outputs := outCollect(t, tool, dir, 0)

	// Sorting is within a pattern, not across them: the document asked for the logs first.
	assertDeepEqual(t, "basenames", outBasenames(t, outputs["all"]), []string{"z.log", outNameA})
}

func TestCollectOutputsOptionalGlobMatchesNothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tool := outTestTool(outTestParam("#tool/maybe", outTypeOptionalFile, outGlobBinding("absent.txt")))
	outputs := outCollect(t, tool, dir, 0)

	if _, present := outputs["maybe"]; !present {
		t.Fatal("an output that collected nothing must still appear in the object")
	}

	if outputs["maybe"] != nil {
		t.Errorf("maybe = %#v, want nil", outputs["maybe"])
	}
}

func TestCollectOutputsRequiredGlobMatchesNothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tool := outTestTool(outTestParam("#tool/needed", outTypeFile, outGlobBinding("absent.txt")))

	err := outCollectErr(t, tool, dir)
	assertErrorIs(t, "required glob with no match", err, ErrOutputMissing)
	assertNames(t, err, "needed", "absent.txt")
}

func TestCollectOutputsRequiredArrayGlobMatchesNothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tool := outTestTool(outTestParam("#tool/all", outTypeFileArray, outGlobBinding("*.txt")))
	outputs := outCollect(t, tool, dir, 0)

	// An array output that matched nothing is an empty array, not an error: nothing is a
	// perfectly good number of files for a list to hold.
	assertDeepEqual(t, "all", outputs["all"], make([]any, 0))
}

func TestCollectOutputsMultipleMatchesForASingleFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outNameA, "a")
	outWriteFile(t, dir, outNameB, "b")

	tool := outTestTool(outTestParam("#tool/one", outTypeFile, outGlobBinding("*.txt")))

	err := outCollectErr(t, tool, dir)
	assertErrorIs(t, "two matches for one file", err, ErrOutputMultiple)
}

func TestCollectOutputsRejectsGlobEscapes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, filepath.Dir(dir), "outside.txt", "secret")

	cases := map[string]string{
		"parent traversal":      "../outside.txt",
		"deep traversal":        "sub/../../outside.txt",
		"absolute outside":      filepath.Join(filepath.Dir(dir), "outside.txt"),
		"absolute unrelated":    outEscapePath,
		"absolute parent":       filepath.Dir(dir),
		"traversal with a star": "../*.txt",
	}

	for name, pattern := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tool := outTestTool(outTestParam("#tool/x", outTypeOptionalFile, outGlobBinding(pattern)))

			err := outCollectErr(t, tool, dir)
			assertErrorIs(t, name, err, ErrGlobEscape)
		})
	}
}

func TestCollectOutputsAcceptsAnAbsoluteGlobInsideTheOutputDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, "out.txt", outContentHello)

	// CommandLineTool.yml: an absolute glob "must refer to a path within the output directory",
	// which is a constraint on where it points, not a ban on the spelling.
	tool := outTestTool(outTestParam("#tool/result", outTypeFile, outGlobBinding(filepath.Join(dir, "out.txt"))))
	outputs := outCollect(t, tool, dir, 0)

	outWantFile(t, outputs, "result", "out.txt", outSumHello, int64(len(outContentHello)))
}

func TestCollectOutputsGlobDotIsTheOutputDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, "inner.txt", "x")

	tool := outTestTool(outTestParam("#tool/whole", outTypeDirectory, outGlobBinding(".")))
	outputs := outCollect(t, tool, dir, 0)

	value, ok := outputs["whole"].(*cwlcore.Directory)
	if !ok {
		t.Fatalf("whole = %#v, want a *cwlcore.Directory", outputs["whole"])
	}

	assertDeepEqual(t, "path", value.Path, dir)
}

func TestCollectOutputsEmptyGlobPatternMatchesNothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, "out.txt", "x")

	tool := outTestTool(outTestParam("#tool/none", outTypeFileArray, outGlobBinding("")))
	outputs := outCollect(t, tool, dir, 0)

	assertDeepEqual(t, "none", outputs["none"], make([]any, 0))
}

func TestCollectOutputsMalformedGlobPattern(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tool := outTestTool(outTestParam("#tool/x", outTypeOptionalFile, outGlobBinding("[")))

	err := outCollectErr(t, tool, dir)
	assertErrorIs(t, "malformed pattern", err, ErrGlobPattern)
}

func TestCollectOutputsWithoutABinding(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tool := outTestTool(outTestParam("#tool/unbound", outTypeString, nil))
	outputs := outCollect(t, tool, dir, 0)

	if _, present := outputs["unbound"]; !present {
		t.Fatal("a parameter with no binding must still appear in the object")
	}

	if outputs["unbound"] != nil {
		t.Errorf("unbound = %#v, want nil", outputs["unbound"])
	}
}

func TestCollectOutputsRejectsARelativeOutputDirectory(t *testing.T) {
	t.Parallel()

	_, err := CollectOutputs(outTestTool(), "relative/dir", 0, nil, nil, cwlcore.RuntimeContext{})
	assertErrorIs(t, "relative outdir", err, ErrOutputDir)
}

func TestCollectOutputsNamesTheFailingPort(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tool := outTestTool(
		outTestParam("#tool/fine", outTypeFileArray, outGlobBinding("*.none")),
		outTestParam("#tool/broken", outTypeFile, outGlobBinding("missing.txt")),
	)

	err := outCollectErr(t, tool, dir)
	assertNames(t, err, `collecting output "broken"`)
}

func TestCollectOutputsGlobExpression(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, "picked.txt", outContentHello)

	tool := outTestTool(outTestParam("#tool/result", outTypeFile, outGlobBinding("$(inputs.name)")))

	outputs, err := CollectOutputs(tool, dir, 0, map[string]any{"name": "picked.txt"},
		cwlcore.NewEvaluator(), cwlcore.RuntimeContext{Outdir: dir})
	if err != nil {
		t.Fatalf("CollectOutputs: %v", err)
	}

	outWantFile(t, outputs, "result", "picked.txt", outSumHello, int64(len(outContentHello)))
}

func TestCollectOutputsGlobExpressionSeesTypedInputFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, "reads.txt", outContentHello)

	tool := outTestTool(outTestParam("#tool/result", outTypeFile, outGlobBinding("$(inputs.src.basename)")))
	inputs := map[string]any{outNameSrc: &cwlcore.File{Basename: "reads.txt"}}

	outputs, err := CollectOutputs(tool, dir, 0, inputs, cwlcore.NewEvaluator(),
		cwlcore.RuntimeContext{Outdir: dir})
	if err != nil {
		t.Fatalf("CollectOutputs: %v", err)
	}

	outWantFile(t, outputs, "result", "reads.txt", outSumHello, int64(len(outContentHello)))
}

func TestCollectOutputsGlobExpressionFailures(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		glob string
		want error
	}{
		"unresolvable reference": {glob: outBadRef, want: cwlcore.ErrExpressionEval},
		"number":                 {glob: outRefN, want: ErrGlobValue},
		"array of numbers":       {glob: outRefNs, want: ErrGlobValue},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			tool := outTestTool(outTestParam("#tool/x", outTypeOptionalFile, outGlobBinding(testCase.glob)))
			inputs := map[string]any{"n": int64(3), "ns": []any{int64(3)}}

			_, err := CollectOutputs(tool, dir, 0, inputs, cwlcore.NewEvaluator(),
				cwlcore.RuntimeContext{Outdir: dir})
			assertErrorIs(t, name, err, testCase.want)
		})
	}
}

func TestGlobStringsAcceptsAnArrayAndNull(t *testing.T) {
	t.Parallel()

	patterns, err := outGlobStrings([]any{"a", "b"})
	if err != nil {
		t.Fatalf("outGlobStrings: %v", err)
	}

	assertDeepEqual(t, "array", patterns, []string{"a", "b"})

	patterns, err = outGlobStrings(nil)
	if err != nil {
		t.Fatalf("outGlobStrings(nil): %v", err)
	}

	if patterns != nil {
		t.Errorf("outGlobStrings(nil) = %#v, want nil", patterns)
	}
}

func TestClassifyExit(t *testing.T) {
	t.Parallel()

	declared := &cwlcore.CommandLineTool{
		SuccessCodes:       []int{0, 4},
		TemporaryFailCodes: []int{5},
		PermanentFailCodes: []int{6, 4},
	}
	bare := &cwlcore.CommandLineTool{}

	cases := map[string]struct {
		tool *cwlcore.CommandLineTool
		code int
		want Status
	}{
		"bare zero succeeds":          {tool: bare, code: 0, want: StatusSuccess},
		"bare non-zero fails":         {tool: bare, code: 1, want: StatusPermanentFail},
		"declared success code":       {tool: declared, code: 4, want: StatusSuccess},
		"declared temporary code":     {tool: declared, code: 5, want: StatusTemporaryFail},
		"declared permanent code":     {tool: declared, code: 6, want: StatusPermanentFail},
		"undeclared zero still wins":  {tool: declared, code: 0, want: StatusSuccess},
		"undeclared code is terminal": {tool: declared, code: 9, want: StatusPermanentFail},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := ClassifyExit(testCase.tool, testCase.code)
			if got != testCase.want {
				t.Errorf("ClassifyExit(%d) = %q, want %q", testCase.code, got, testCase.want)
			}
		})
	}
}

func TestSingleFileType(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		declared cwlcore.TypeRef
		want     bool
	}{
		"a File":              {declared: outTypeFile, want: true},
		"a Directory":         {declared: outTypeDirectory, want: true},
		"an optional File":    {declared: outTypeOptionalFile, want: true},
		"the stdout shortcut": {declared: cwlcore.NewShortcutType(cwlcore.TypeKindStdout), want: true},
		"the stderr shortcut": {declared: cwlcore.NewShortcutType(cwlcore.TypeKindStderr), want: true},
		"a plain string":      {declared: outTypeString, want: false},
		"a File array":        {declared: outTypeFileArray, want: false},
		"no type at all":      {declared: cwlcore.TypeRef{}, want: false},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := outSingleFileType(testCase.declared); got != testCase.want {
				t.Errorf("outSingleFileType(%s) = %t, want %t", testCase.declared, got, testCase.want)
			}
		})
	}
}

func TestWithinDir(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		local string
		want  bool
	}{
		"the directory itself": {local: outTestOutdir, want: true},
		"a child":              {local: outTestOutdir + "/a.txt", want: true},
		"a grandchild":         {local: outTestOutdir + "/sub/a.txt", want: true},
		"a sibling":            {local: outTestOutdir + "x", want: false},
		"a parent":             {local: "/", want: false},
		"an unrelated path":    {local: outEscapePath, want: false},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := outWithinDir(outTestOutdir, testCase.local); got != testCase.want {
				t.Errorf("outWithinDir(%s, %q) = %t, want %t", outTestOutdir, testCase.local, got, testCase.want)
			}
		})
	}
}

func TestCollectPathOnAMissingPath(t *testing.T) {
	t.Parallel()

	_, err := outCollectPath(filepath.Join(t.TempDir(), "gone"), &cwlcore.CommandOutputBinding{})
	if err == nil {
		t.Fatal("outCollectPath on a missing path succeeded, want an error")
	}
}

func TestCollectFileOnAMissingPath(t *testing.T) {
	t.Parallel()

	_, err := outCollectFile(filepath.Join(t.TempDir(), "gone"), &cwlcore.CommandOutputBinding{LoadContents: true})
	if err == nil {
		t.Fatal("outCollectFile on a missing path succeeded, want an error")
	}
}

func TestStreamFile(t *testing.T) {
	t.Parallel()

	named := &cwlcore.CommandLineTool{
		ProcessBase: cwlcore.ProcessBase{ID: outToolID},
		Stdout:      "out.log",
		Stderr:      "err.log",
	}

	name, err := StreamFile(named, StreamStdout, nil, nil, cwlcore.RuntimeContext{})
	if err != nil {
		t.Fatalf("StreamFile(stdout): %v", err)
	}

	assertDeepEqual(t, "stdout", name, "out.log")

	name, err = StreamFile(named, StreamStderr, nil, nil, cwlcore.RuntimeContext{})
	if err != nil {
		t.Fatalf("StreamFile(stderr): %v", err)
	}

	assertDeepEqual(t, "stderr", name, "err.log")
}

func TestStreamFileEvaluatesAnExpression(t *testing.T) {
	t.Parallel()

	tool := &cwlcore.CommandLineTool{Stdout: "$(inputs.base).log"}

	name, err := StreamFile(tool, StreamStdout, map[string]any{"base": "run"},
		cwlcore.NewEvaluator(), cwlcore.RuntimeContext{})
	if err != nil {
		t.Fatalf("StreamFile: %v", err)
	}

	assertDeepEqual(t, "stdout", name, "run.log")
}

func TestStreamFileGeneratesAStableNamePerStream(t *testing.T) {
	t.Parallel()

	tool := &cwlcore.CommandLineTool{ProcessBase: cwlcore.ProcessBase{ID: outToolID}}
	other := &cwlcore.CommandLineTool{ProcessBase: cwlcore.ProcessBase{ID: outToolID + "-other"}}

	out := outGeneratedStreamFile(tool, StreamStdout)
	err := outGeneratedStreamFile(tool, StreamStderr)

	if out != outGeneratedStreamFile(tool, StreamStdout) {
		t.Error("the generated name must be the same on every call, or the two streams cannot agree on it")
	}

	if out == err {
		t.Error("stdout and stderr must not be redirected to the same generated file")
	}

	if out == outGeneratedStreamFile(other, StreamStdout) {
		t.Error("two tools must not share a generated stdout file")
	}

	if len(out) != 40 || strings.ContainsAny(out, "/.") {
		t.Errorf("generated name %q is not a bare hex digest", out)
	}
}

func TestStreamFileRejectsUnusableNames(t *testing.T) {
	t.Parallel()

	cases := map[string]cwlcore.Expression{
		"no name at all": "$(inputs.unnamed)",
		"absolute":       outEscapePath,
		"traversal":      "../escape.log",
	}

	for name, declared := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tool := &cwlcore.CommandLineTool{Stdout: declared}

			_, err := StreamFile(tool, StreamStdout, map[string]any{"unnamed": ""},
				cwlcore.NewEvaluator(), cwlcore.RuntimeContext{})
			assertErrorIs(t, name, err, ErrStreamFile)
		})
	}
}

func TestStreamFilePropagatesAnEvaluationFailure(t *testing.T) {
	t.Parallel()

	tool := &cwlcore.CommandLineTool{Stdout: "$(inputs.absent.deeper)"}

	_, err := StreamFile(tool, StreamStdout, nil, cwlcore.NewEvaluator(), cwlcore.RuntimeContext{})
	assertErrorIs(t, "bad stdout expression", err, cwlcore.ErrExpressionEval)
}

func TestCollectOutputsStdoutShortcut(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	tool := outTestTool(outTestParam("#tool/log", cwlcore.NewShortcutType(cwlcore.TypeKindStdout), nil))
	tool.Stdout = outLogName

	outWriteFile(t, dir, outLogName, outContentHello)

	outputs := outCollect(t, tool, dir, 0)
	outWantFile(t, outputs, "log", outLogName, outSumHello, int64(len(outContentHello)))
}

func TestCollectOutputsStderrShortcutWithAGeneratedName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	tool := outTestTool(outTestParam("#tool/errs", cwlcore.NewShortcutType(cwlcore.TypeKindStderr), nil))

	// This is the coupling with the stream that runs the process: it captures stderr to the
	// name StreamFile hands it, and collection globs for the same name.
	name, err := StreamFile(tool, StreamStderr, nil, nil, cwlcore.RuntimeContext{})
	if err != nil {
		t.Fatalf("StreamFile: %v", err)
	}

	outWriteFile(t, dir, name, outContentHello)

	outputs := outCollect(t, tool, dir, 0)
	outWantFile(t, outputs, "errs", name, outSumHello, int64(len(outContentHello)))
}

func TestCollectOutputsStdoutShortcutKeepsAnExplicitBindingsLoadContents(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	tool := outTestTool(outTestParam("#tool/log", cwlcore.NewShortcutType(cwlcore.TypeKindStdout),
		&cwlcore.CommandOutputBinding{Glob: []cwlcore.Expression{"ignored.txt"}, LoadContents: true}))
	tool.Stdout = outLogName

	outWriteFile(t, dir, outLogName, outContentHello)
	outWriteFile(t, dir, "ignored.txt", "other")

	outputs := outCollect(t, tool, dir, 0)
	file := outWantFile(t, outputs, "log", outLogName, outSumHello, int64(len(outContentHello)))

	assertDeepEqual(t, "contents", file.Contents.Value(), outContentHello)
}

func TestCollectOutputsStdoutShortcutReportsABadStreamName(t *testing.T) {
	t.Parallel()

	tool := outTestTool(outTestParam("#tool/log", cwlcore.NewShortcutType(cwlcore.TypeKindStdout), nil))
	tool.Stdout = "/absolute.log"

	err := outCollectErr(t, tool, t.TempDir())
	assertErrorIs(t, "absolute stdout", err, ErrStreamFile)
}

func TestShortcutStream(t *testing.T) {
	t.Parallel()

	stream, ok := outShortcutStream(cwlcore.NewShortcutType(cwlcore.TypeKindStdout))
	if stream != StreamStdout || !ok {
		t.Errorf("stdout shortcut = (%q, %t)", stream, ok)
	}

	stream, ok = outShortcutStream(cwlcore.NewShortcutType(cwlcore.TypeKindStderr))
	if stream != StreamStderr || !ok {
		t.Errorf("stderr shortcut = (%q, %t)", stream, ok)
	}

	stream, ok = outShortcutStream(outTypeFile)
	if stream != "" || ok {
		t.Errorf("File = (%q, %t), want no shortcut", stream, ok)
	}
}

// outMixedDir builds an output directory holding one file and one subdirectory, which is what a
// glob of "*" has to choose between.
func outMixedDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	outWriteFile(t, dir, outNameA, "alpha")

	err := os.Mkdir(filepath.Join(dir, outNameSub), 0o755)
	if err != nil {
		t.Fatalf("creating the directory: %v", err)
	}

	return dir
}

func TestCollectOutputsRejectsAGlobbedValueOfTheWrongClass(t *testing.T) {
	t.Parallel()

	// CommandLineTool.yml constrains a glob to "match and return files/directories which
	// actually exist" and says nothing about which of the two, so "*" will happily match a
	// subdirectory for an output declared File[]. Publishing it would put a Directory into a port
	// every consumer reads as a File.
	cases := map[string]cwlcore.TypeRef{
		"directory for File[]": outTypeFileArray,
		"file for Directory[]": outTypeDirArray,
	}

	for name, declared := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tool := outTestTool(outTestParam("#tool/result", declared, outGlobBinding("*")))

			assertErrorIs(t, name, outCollectErr(t, tool, outMixedDir(t)), ErrOutputType)
		})
	}
}

func TestCollectOutputsAcceptsBothClassesWhenTheTypeDoes(t *testing.T) {
	t.Parallel()

	dir := outMixedDir(t)
	tool := outTestTool(outTestParam("#tool/result", outTypeEntryArray, outGlobBinding("*")))

	// `items: [File, Directory]` is how a document says it meant both.
	assertDeepEqual(t, "result", outBasenames(t, outCollect(t, tool, dir, 0)["result"]),
		[]string{outNameA, outNameSub})
}
