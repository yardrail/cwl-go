package cwlexec

import (
	"path/filepath"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// outSecondaryParam builds an output parameter that globs one pattern and declares secondaryFiles.
func outSecondaryParam(schemas ...cwlcore.SecondaryFileSchema) cwlcore.CommandOutputParameter {
	param := outTestParam("#tool/primary", outTypeFile, outGlobBinding(outPrimaryName))
	param.SecondaryFiles = schemas

	return param
}

// outSecondaryNames lists the outBasenames of a collected File's secondary files.
func outSecondaryNames(t *testing.T, outputs map[string]any, name string) []string {
	t.Helper()

	file, ok := outputs[name].(*cwlcore.File)
	if !ok {
		t.Fatalf("output %q = %#v, want a *cwlcore.File", name, outputs[name])
	}

	if file.SecondaryFiles == nil {
		t.Fatal("a parameter that declares secondaryFiles must leave the field set, even when empty")
	}

	return outEntryNames(t, file.SecondaryFiles)
}

func TestSubstitutePattern(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		basename string
		pattern  string
		want     string
	}{
		"plain suffix":               {basename: outPrimaryName, pattern: outExtBai, want: outPrimaryName + outExtBai},
		"one caret":                  {basename: outPrimaryName, pattern: outCaretBai, want: outBaiName},
		"two carets":                 {basename: "reads.tar.gz", pattern: "^^.idx", want: outIndexName},
		"caret with no extension":    {basename: "reads", pattern: outCaretBai, want: outBaiName},
		"carets outrunning the dots": {basename: outPrimaryName, pattern: "^^^.idx", want: outIndexName},
		"caret takes the last dot":   {basename: "a.b.c", pattern: "^.d", want: "a.b.d"},
		"no suffix at all":           {basename: outPrimaryName, pattern: "", want: outPrimaryName},
		"caret only":                 {basename: outPrimaryName, pattern: "^", want: "reads"},
		"leading dot is a boundary":  {basename: ".cshrc", pattern: "^.bak", want: ".bak"},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := outSubstitutePattern(testCase.basename, testCase.pattern)
			if got != testCase.want {
				t.Errorf("outSubstitutePattern(%q, %q) = %q, want %q",
					testCase.basename, testCase.pattern, got, testCase.want)
			}
		})
	}
}

func TestTrimOptionalMarker(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		outExtBai + "?":          outExtBai,
		outExtBai:                outExtBai,
		"$(self.nameroot + '?')": "$(self.nameroot + '?')",
	}

	for pattern, want := range cases {
		if got := outTrimOptionalMarker(pattern); got != want {
			t.Errorf("outTrimOptionalMarker(%q) = %q, want %q", pattern, got, want)
		}
	}
}

func TestCollectOutputsSecondaryFilesCaretRule(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outPrimaryName, outContentHello)
	outWriteFile(t, dir, outBaiName, "index-data\n")
	outWriteFile(t, dir, outPrimaryName+".md5", "md5")

	tool := outTestTool(outSecondaryParam(
		cwlcore.SecondaryFileSchema{Pattern: outCaretBai},
		cwlcore.SecondaryFileSchema{Pattern: ".md5"},
	))

	outputs := outCollect(t, tool, dir, 0)
	assertDeepEqual(t, "secondaryFiles", outSecondaryNames(t, outputs, "primary"),
		[]string{outBaiName, outPrimaryName + ".md5"})

	file, ok := outputs["primary"].(*cwlcore.File)
	if !ok {
		t.Fatalf("primary = %#v", outputs["primary"])
	}

	index, ok := file.SecondaryFiles[0].(*cwlcore.File)
	if !ok {
		t.Fatalf("first secondary = %#v, want a File", file.SecondaryFiles[0])
	}

	// A secondary file is measured like any other: the conformance harness checks its checksum
	// from disk too.
	assertDeepEqual(t, "secondary checksum", index.Checksum, outSumIndex)
	assertDeepEqual(t, "secondary path", index.Path, filepath.Join(dir, outBaiName))
}

func TestCollectOutputsMissingOptionalSecondaryFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outPrimaryName, outContentHello)

	tool := outTestTool(outSecondaryParam(cwlcore.SecondaryFileSchema{Pattern: outCaretBai}))
	outputs := outCollect(t, tool, dir, 0)

	// Process.yml: secondary files on outputs are optional unless the document says otherwise,
	// so a missing one is dropped rather than reported.
	assertDeepEqual(t, "secondaryFiles", outSecondaryNames(t, outputs, "primary"), make([]string, 0))
}

func TestCollectOutputsMissingRequiredSecondaryFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outPrimaryName, outContentHello)

	tool := outTestTool(outSecondaryParam(cwlcore.SecondaryFileSchema{
		Pattern:  outCaretBai,
		Required: cwlcore.NewExprBool(true),
	}))

	err := outCollectErr(t, tool, dir)
	assertErrorIs(t, "missing required secondary", err, ErrSecondaryMissing)
	assertNames(t, err, outBaiName)
}

func TestCollectOutputsSecondaryFileRequiredByExpression(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outPrimaryName, outContentHello)

	tool := outTestTool(outSecondaryParam(cwlcore.SecondaryFileSchema{
		Pattern:  outCaretBai,
		Required: cwlcore.NewExprBoolExpression("$(self.nameext == '.bam')"),
	}))

	_, err := CollectOutputs(tool, dir, 0, nil, cwlcore.NewEvaluator(cwlcore.WithJS(nil)),
		cwlcore.RuntimeContext{Outdir: dir})
	assertErrorIs(t, "required by expression", err, ErrSecondaryMissing)
}

func TestCollectOutputsSecondaryFileRequiredExpressionFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outPrimaryName, outContentHello)

	tool := outTestTool(outSecondaryParam(cwlcore.SecondaryFileSchema{
		Pattern:  outCaretBai,
		Required: cwlcore.NewExprBoolExpression("$(self.basename)"),
	}))

	err := outCollectErr(t, tool, dir)
	assertErrorIs(t, "non-boolean required", err, cwlcore.ErrNotBoolean)
}

func TestCollectOutputsSecondaryFileOptionalMarker(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outPrimaryName, outContentHello)
	outWriteFile(t, dir, outBaiName, "index-data\n")

	// The trailing "?" is part of the shorthand, not part of the filename: without trimming it
	// this would look for "reads.bai?" and find nothing.
	tool := outTestTool(outSecondaryParam(cwlcore.SecondaryFileSchema{Pattern: "^.bai?"}))
	outputs := outCollect(t, tool, dir, 0)

	assertDeepEqual(t, "secondaryFiles", outSecondaryNames(t, outputs, "primary"), []string{outBaiName})
}

func TestCollectOutputsSecondaryFilesFromAnExpression(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outPrimaryName, outContentHello)
	outWriteFile(t, dir, outIndexName, "index-data\n")

	tool := outTestTool(outSecondaryParam(
		cwlcore.SecondaryFileSchema{Pattern: "$(self.nameroot + '.idx')"}))

	outputs, err := CollectOutputs(tool, dir, 0, nil, cwlcore.NewEvaluator(cwlcore.WithJS(nil)),
		cwlcore.RuntimeContext{Outdir: dir})
	if err != nil {
		t.Fatalf("CollectOutputs: %v", err)
	}

	assertDeepEqual(t, "secondaryFiles", outSecondaryNames(t, outputs, "primary"), []string{outIndexName})
}

func TestCollectOutputsSecondaryFilesExpressionReturningAnArrayAndNull(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outPrimaryName, outContentHello)
	outWriteFile(t, dir, "one.idx", "alpha")
	outWriteFile(t, dir, "two.idx", "beta")

	tool := outTestTool(outSecondaryParam(
		cwlcore.SecondaryFileSchema{Pattern: "${ return ['one.idx', null, 'two.idx']; }"}))

	outputs, err := CollectOutputs(tool, dir, 0, nil, cwlcore.NewEvaluator(cwlcore.WithJS(nil)),
		cwlcore.RuntimeContext{Outdir: dir})
	if err != nil {
		t.Fatalf("CollectOutputs: %v", err)
	}

	// Process.yml: "The expression may return 'null' in which case there is no secondaryFile
	// from that expression." Not a missing file — no file was named.
	assertDeepEqual(t, "secondaryFiles", outSecondaryNames(t, outputs, "primary"),
		[]string{"one.idx", "two.idx"})
}

func TestCollectOutputsSecondaryFilesExpressionReturningAFileObject(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outPrimaryName, outContentHello)
	outWriteFile(t, dir, outIndexName, "index-data\n")

	tool := outTestTool(outSecondaryParam(
		cwlcore.SecondaryFileSchema{Pattern: "${ return {class: 'File', location: '" + outIndexName + "'}; }"}))

	outputs, err := CollectOutputs(tool, dir, 0, nil, cwlcore.NewEvaluator(cwlcore.WithJS(nil)),
		cwlcore.RuntimeContext{Outdir: dir})
	if err != nil {
		t.Fatalf("CollectOutputs: %v", err)
	}

	file, ok := outputs["primary"].(*cwlcore.File)
	if !ok {
		t.Fatalf("primary = %#v", outputs["primary"])
	}

	index, ok := file.SecondaryFiles[0].(*cwlcore.File)
	if !ok {
		t.Fatalf("secondary = %#v, want a File", file.SecondaryFiles[0])
	}

	assertDeepEqual(t, "basename", index.Basename, "reads.idx")
	assertDeepEqual(t, "checksum", index.Checksum, outSumIndex)
}

func TestCollectOutputsSecondaryFilesExpressionReturningADirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outPrimaryName, outContentHello)
	outWriteFile(t, dir, outAuxName+"/"+outNameX, "x")

	tool := outTestTool(outSecondaryParam(cwlcore.SecondaryFileSchema{Pattern: "$(inputs.aux)"}))

	outputs, err := CollectOutputs(tool, dir, 0, map[string]any{outAuxName: outAuxName},
		cwlcore.NewEvaluator(), cwlcore.RuntimeContext{Outdir: dir})
	if err != nil {
		t.Fatalf("CollectOutputs: %v", err)
	}

	assertDeepEqual(t, "secondaryFiles", outSecondaryNames(t, outputs, "primary"), []string{outAuxName})

	primary, ok := outputs["primary"].(*cwlcore.File)
	if !ok {
		t.Fatalf("primary = %#v, want a *cwlcore.File", outputs["primary"])
	}

	aux, ok := primary.SecondaryFiles[0].(*cwlcore.Directory)
	if !ok {
		t.Fatalf("secondary = %#v, want a *cwlcore.Directory", primary.SecondaryFiles[0])
	}

	// A Directory hanging off a File is published like any other, so the completion pass has to
	// reach it too.
	outFillListings(primary)
	assertDeepEqual(t, "listing", outEntryNames(t, aux.Listing), []string{outNameX})
}

func TestCollectOutputsSecondaryFilesExpressionReturningANumber(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outPrimaryName, outContentHello)

	tool := outTestTool(outSecondaryParam(cwlcore.SecondaryFileSchema{Pattern: outRefN}))

	_, err := CollectOutputs(tool, dir, 0, map[string]any{"n": int64(3)},
		cwlcore.NewEvaluator(), cwlcore.RuntimeContext{Outdir: dir})
	assertErrorIs(t, "numeric secondaryFiles", err, ErrSecondaryValue)
}

func TestCollectOutputsSecondaryFilesExpressionFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outPrimaryName, outContentHello)

	tool := outTestTool(outSecondaryParam(
		cwlcore.SecondaryFileSchema{Pattern: outBadRef}))

	err := outCollectErr(t, tool, dir)
	assertErrorIs(t, "failing secondaryFiles expression", err, cwlcore.ErrExpressionEval)
}

func TestCollectOutputsSecondaryFilesSkipDirectoryPrimaries(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, "out/x.txt", "x")

	param := outTestParam("#tool/d", outTypeDirectory, outGlobBinding("out"))
	param.SecondaryFiles = []cwlcore.SecondaryFileSchema{{
		Pattern:  ".idx",
		Required: cwlcore.NewExprBool(true),
	}}

	// A Directory has no secondaryFiles field in the schema, so there is nowhere to put one and
	// nothing to look for. A required pattern must not fire against a value that cannot carry it.
	outputs := outCollect(t, outTestTool(param), dir, 0)
	outWantDirectory(t, outputs)
}

func TestCollectOutputsSecondaryFilesAcrossAnArray(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, "a.bam", "alpha")
	outWriteFile(t, dir, "a.bai", "index-data\n")
	outWriteFile(t, dir, "b.bam", "beta")
	outWriteFile(t, dir, "b.bai", "index-data\n")

	param := outTestParam("#tool/all", outTypeFileArray, outGlobBinding("*.bam"))
	param.SecondaryFiles = []cwlcore.SecondaryFileSchema{{Pattern: outCaretBai}}

	outputs := outCollect(t, outTestTool(param), dir, 0)

	items, ok := outputs["all"].([]any)
	if !ok {
		t.Fatalf("all = %#v, want a list", outputs["all"])
	}

	assertInt(t, "len(all)", len(items), 2)

	for _, item := range items {
		file, isFile := item.(*cwlcore.File)
		if !isFile {
			t.Fatalf("item = %#v, want a File", item)
		}

		assertDeepEqual(t, file.Basename+" secondaryFiles",
			outEntryNames(t, file.SecondaryFiles), []string{file.Nameroot + ".bai"})
	}
}

func TestCollectOutputsSecondaryFilesAfterOutputEval(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, "a.bam", "alpha")
	outWriteFile(t, dir, "b.bam", "beta")
	outWriteFile(t, dir, "b.bai", "index-data\n")

	// CommandOutputBinding applies its operations in the order glob, loadContents, outputEval,
	// secondaryFiles — so the patterns decorate whatever outputEval left behind, not the
	// globbed list it replaced.
	binding := outGlobBinding("*.bam")
	binding.OutputEval = "$(self[1])"

	param := outTestParam("#tool/picked", outTypeFile, binding)
	param.SecondaryFiles = []cwlcore.SecondaryFileSchema{{Pattern: outCaretBai}}

	outputs := outCollect(t, outTestTool(param), dir, 0)

	outWantFile(t, outputs, "picked", "b.bam", outSumBeta, int64(len("beta")))
	assertDeepEqual(t, "secondaryFiles", outSecondaryNames(t, outputs, "picked"), []string{"b.bai"})
}

func TestCollectOutputsFormatFromAnExpression(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, "out.txt", outContentHello)

	param := outTestParam("#tool/result", outTypeFile, outGlobBinding("out.txt"))
	param.Format = []cwlcore.Expression{"$(inputs.fmt)"}

	outputs, err := CollectOutputs(outTestTool(param), dir, 0,
		map[string]any{"fmt": "http://edamontology.org/format_2330"},
		cwlcore.NewEvaluator(), cwlcore.RuntimeContext{Outdir: dir})
	if err != nil {
		t.Fatalf("CollectOutputs: %v", err)
	}

	file, ok := outputs["result"].(*cwlcore.File)
	if !ok {
		t.Fatalf("result = %#v", outputs["result"])
	}

	assertDeepEqual(t, "format", file.Format, "http://edamontology.org/format_2330")
}

func TestCollectOutputsFormatUsesEachFilesOwnSelf(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, "a.txt", "alpha")
	outWriteFile(t, dir, "b.bin", "beta")

	param := outTestParam("#tool/all", outTypeFileArray, outGlobBinding("*.*"))
	param.Format = []cwlcore.Expression{"urn:ext$(self.nameext)"}

	outputs := outCollect(t, outTestTool(param), dir, 0)

	items, ok := outputs["all"].([]any)
	if !ok {
		t.Fatalf("all = %#v", outputs["all"])
	}

	formats := make([]string, 0, len(items))

	for _, item := range items {
		file, isFile := item.(*cwlcore.File)
		if !isFile {
			t.Fatalf("item = %#v", item)
		}

		formats = append(formats, file.Format)
	}

	assertDeepEqual(t, "formats", formats, []string{"urn:ext.txt", "urn:ext.bin"})
}

func TestCollectOutputsFormatOnAnEmptyOptionalOutput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	param := outTestParam("#tool/maybe", outTypeOptionalFile, outGlobBinding("absent.txt"))
	param.Format = []cwlcore.Expression{"urn:x"}

	outputs := outCollect(t, outTestTool(param), dir, 0)

	// No file, so nothing to carry a format, and a null value is not a format violation.
	if outputs["maybe"] != nil {
		t.Errorf("maybe = %#v, want nil", outputs["maybe"])
	}
}

func TestCollectOutputsFormatOnAValueThatIsNotAFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	binding := &cwlcore.CommandOutputBinding{OutputEval: "a string"}
	param := outTestParam("#tool/text", outTypeString, binding)
	param.Format = []cwlcore.Expression{"urn:x"}

	err := outCollectErr(t, outTestTool(param), dir)
	if err == nil {
		t.Fatal("a format declared on a non-File output must be reported")
	}
}

func TestCollectOutputsFormatExpressionFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, "out.txt", outContentHello)

	param := outTestParam("#tool/result", outTypeFile, outGlobBinding("out.txt"))
	param.Format = []cwlcore.Expression{outBadRef}

	err := outCollectErr(t, outTestTool(param), dir)
	assertErrorIs(t, "failing format expression", err, cwlcore.ErrExpressionEval)
}

func TestCollectOutputsFormatlessExpressionFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	param := outTestParam("#tool/maybe", outTypeOptionalFile, outGlobBinding("absent.txt"))
	param.Format = []cwlcore.Expression{outBadRef}

	err := outCollectErr(t, outTestTool(param), dir)
	assertErrorIs(t, "failing format expression", err, cwlcore.ErrExpressionEval)
}

func TestPolicyOf(t *testing.T) {
	t.Parallel()

	if outPolicies[true] != outSecondaryRequired {
		t.Error("required: true must select the required policy")
	}

	if outPolicies[false] != outSecondaryOptional {
		t.Error("required: false must select the optional policy")
	}
}
