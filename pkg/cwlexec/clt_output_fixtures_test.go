package cwlexec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Known-good sha1 digests, produced by sha1sum rather than by this package, so that a checksum
// test cannot pass by agreeing with the implementation it is checking.
const (
	// outSumHello is sha1sum of the five bytes "hello" followed by a newline.
	outSumHello = "sha1$f572d396fae9206628714fb2ce00f72e94f2258f"

	// outSumEmpty is sha1sum of no bytes at all, the digest of a zero-byte file.
	outSumEmpty = "sha1$da39a3ee5e6b4b0d3255bfef95601890afd80709"

	// outSumAlpha is sha1sum of "alpha".
	outSumAlpha = "sha1$be76331b95dfc399cd776d2fc68021e0db03cc4f"

	// outSumBeta is sha1sum of "beta".
	outSumBeta = "sha1$a295e0bdde1938d1fbfd343e5a3e569e868e1465"

	// outSumIndex is sha1sum of "index-data" followed by a newline.
	outSumIndex = "sha1$684c9a09cebbc4609a1eb6ff4fbf8180bf9b11e4"
)

// outContentHello is the content whose digest is outSumHello.
const outContentHello = "hello\n"

// The literals the output tests repeat, named once so that a reader can tell at a glance which
// occurrences are meant to be the same string.
const (
	// outNameA, outNameB and outNameC are the files the glob-ordering fixtures write.
	outNameA = "a.txt"
	outNameB = "b.txt"
	outNameC = "c.txt"

	// outRefN reads a numeric input and outRefNs a numeric array, for the cases that feed a
	// glob or a pattern the wrong type.
	outRefN  = "$(inputs.n)"
	outRefNs = "$(inputs.ns)"

	// outBadRef is a parameter reference that parses and then fails to resolve.
	outBadRef = "$(inputs.absent.deeper)"

	// outEscapePath is an absolute path outside any output directory.
	outEscapePath = "/etc/passwd"

	// outTestOutdir is the notional output directory the pure path tests reason about, and
	// outTestAbs an absolute path outside it.
	outTestOutdir = "/outputs"
	outTestAbs    = "/elsewhere/a.txt"

	// outPrimaryName is the primary file the secondaryFiles fixtures glob for.
	outPrimaryName = "reads.bam"

	// outCaretBai is the caret pattern those fixtures apply to it, outExtBai the plain one.
	outCaretBai = "^.bai"
	outExtBai   = ".bai"

	// outIndexName is the file an expression-valued secondaryFiles pattern names, and
	// outAuxName the directory another one names.
	outIndexName = "reads.idx"
	outAuxName   = "aux"

	// outToolID is the resolved identifier the fixture tools carry.
	outToolID = "file:///w.cwl#collect"

	// outBaiName is the secondary file the caret fixtures expect beside the primary.
	outBaiName = "reads.bai"

	// outLogName is the file the stdout fixtures capture to.
	outLogName = "run.log"

	// outNameTree, outNameSub and outNameDeeper are the directories the tree fixtures build, and
	// outNameX the file an expression-named secondary directory holds.
	outNameTree   = "tree"
	outNameSub    = "sub"
	outNameDeeper = "deeper"
	outNameX      = "x.txt"

	// outKeyKept is the object key the field-rendering tests expect to survive.
	outKeyKept = "kept"

	// outNameSrc is the input port the fixtures read a value back out of, and outRefSrc the
	// parameter reference that names it.
	outNameSrc = "src"
	outRefSrc  = "$(inputs.src)"
)

// outTypeFile, outTypeDirectory and outTypeOptionalFile are the parameter types the output tests declare.
var (
	outTypeFile         = cwlcore.NewPrimitiveType(cwlcore.PrimitiveFile)
	outTypeDirectory    = cwlcore.NewPrimitiveType(cwlcore.PrimitiveDirectory)
	outTypeNull         = cwlcore.NewPrimitiveType(cwlcore.PrimitiveNull)
	outTypeString       = cwlcore.NewPrimitiveType(cwlcore.PrimitiveString)
	outTypeLong         = cwlcore.NewPrimitiveType(cwlcore.PrimitiveLong)
	outTypeOptionalFile = cwlcore.NewUnionType([]cwlcore.TypeRef{outTypeNull, outTypeFile})
	outTypeFileArray    = cwlcore.NewArrayType(&cwlcore.ArraySchema{Items: outTypeFile})
	outTypeDirArray     = cwlcore.NewArrayType(&cwlcore.ArraySchema{Items: outTypeDirectory})
	outTypeEntryArray   = cwlcore.NewArrayType(&cwlcore.ArraySchema{
		Items: cwlcore.NewUnionType([]cwlcore.TypeRef{outTypeFile, outTypeDirectory}),
	})
)

// outTestParam builds an output parameter with the given identifier, type and binding.
func outTestParam(
	id string,
	declared cwlcore.TypeRef,
	binding *cwlcore.CommandOutputBinding,
) cwlcore.CommandOutputParameter {
	return cwlcore.CommandOutputParameter{
		ParameterBase: cwlcore.ParameterBase{IDField: id, Type: declared},
		OutputBinding: binding,
	}
}

// outGlobBinding builds an output binding that globs the given patterns.
func outGlobBinding(patterns ...string) *cwlcore.CommandOutputBinding {
	globs := make([]cwlcore.Expression, 0, len(patterns))
	for _, pattern := range patterns {
		globs = append(globs, cwlcore.Expression(pattern))
	}

	return &cwlcore.CommandOutputBinding{Glob: globs}
}

// outTestTool builds a CommandLineTool declaring the given output parameters.
func outTestTool(params ...cwlcore.CommandOutputParameter) *cwlcore.CommandLineTool {
	return &cwlcore.CommandLineTool{
		ProcessBase: cwlcore.ProcessBase{ID: outToolID},
		Outputs:     params,
	}
}

// outStagingTool builds a CommandLineTool declaring no outputs and one InitialWorkDirRequirement
// holding the given listing.
func outStagingTool(listing cwlcore.InitialWorkDirListing) *cwlcore.CommandLineTool {
	tool := outTestTool()
	tool.Requirements = []cwlcore.ProcessRequirement{
		&cwlcore.InitialWorkDirRequirement{Listing: listing},
	}

	return tool
}

// outWriteFile creates a file under dir, making any parent directories it names, and returns its path.
func outWriteFile(t *testing.T, dir, name, content string) string {
	t.Helper()

	local := filepath.Join(dir, name)

	err := os.MkdirAll(filepath.Dir(local), 0o755)
	if err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(local), err)
	}

	err = os.WriteFile(local, []byte(content), 0o600)
	if err != nil {
		t.Fatalf("writing %s: %v", local, err)
	}

	return local
}

// collect runs CollectOutputs over a tool with a parameter-references-only evaluator.
func outCollect(t *testing.T, tool *cwlcore.CommandLineTool, outdir string, exitCode int) map[string]any {
	t.Helper()

	outputs, err := CollectOutputs(tool, outdir, exitCode, nil, cwlcore.NewEvaluator(),
		cwlcore.RuntimeContext{Outdir: outdir})
	if err != nil {
		t.Fatalf("CollectOutputs: %v", err)
	}

	return outputs
}

// outCollectErr runs CollectOutputs and requires it to fail.
func outCollectErr(t *testing.T, tool *cwlcore.CommandLineTool, outdir string) error {
	t.Helper()

	_, err := CollectOutputs(tool, outdir, 0, nil, cwlcore.NewEvaluator(),
		cwlcore.RuntimeContext{Outdir: outdir})
	if err == nil {
		t.Fatal("CollectOutputs succeeded, want an error")
	}

	return err
}

// outWantFile requires that outputs[name] is a File with the given basename, checksum and size.
func outWantFile(t *testing.T, outputs map[string]any, name, basename, checksum string, size int64) *cwlcore.File {
	t.Helper()

	file, ok := outputs[name].(*cwlcore.File)
	if !ok {
		t.Fatalf("output %q = %#v, want a *cwlcore.File", name, outputs[name])
	}

	if file.Basename != basename {
		t.Errorf("basename = %q, want %q", file.Basename, basename)
	}

	if file.Checksum != checksum {
		t.Errorf("checksum = %q, want %q", file.Checksum, checksum)
	}

	if !file.Size.IsSet() || file.Size.Int() != size {
		t.Errorf("size = %s, want %d", file.Size, size)
	}

	return file
}

// outBasenames lists the outBasenames of a collected list value, which is how the ordering tests read a
// multi-file glob's result.
func outBasenames(t *testing.T, value any) []string {
	t.Helper()

	items, ok := value.([]any)
	if !ok {
		t.Fatalf("value = %#v, want a list", value)
	}

	names := make([]string, 0, len(items))

	for _, item := range items {
		switch typed := item.(type) {
		case *cwlcore.File:
			names = append(names, typed.Basename)
		case *cwlcore.Directory:
			names = append(names, typed.Basename)
		default:
			t.Fatalf("list holds %#v, want a File or Directory", item)
		}
	}

	return names
}
