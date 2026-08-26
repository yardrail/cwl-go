package cwlexec

import (
	"cmp"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// outListingBinding builds an output binding that globs one pattern with the given loadListing mode.
func outListingBinding(pattern string, mode cwlcore.LoadListingEnum) *cwlcore.CommandOutputBinding {
	binding := outGlobBinding(pattern)
	binding.LoadListing = mode

	return binding
}

// outTreeDir builds a small directory tree under a fresh temporary directory:
//
//	tree/a.txt
//	tree/sub/b.txt
//	tree/sub/deeper/c.txt
func outTreeDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	outWriteFile(t, dir, outNameTree+"/"+outNameA, "alpha")
	outWriteFile(t, dir, outNameTree+"/"+outNameSub+"/"+outNameB, "beta")
	outWriteFile(t, dir, outNameTree+"/"+outNameSub+"/"+outNameDeeper+"/"+outNameC, "gamma")

	return dir
}

// outDirPort is the port name every Directory fixture declares.
const outDirPort = "d"

// outWantDirectory requires that the fixture Directory port holds a Directory, and returns it.
func outWantDirectory(t *testing.T, outputs map[string]any) *cwlcore.Directory {
	t.Helper()

	dir, ok := outputs[outDirPort].(*cwlcore.Directory)
	if !ok {
		t.Fatalf("output %q = %#v, want a *cwlcore.Directory", outDirPort, outputs[outDirPort])
	}

	return dir
}

// outEntryNames lists a listing's outBasenames in order.
func outEntryNames(t *testing.T, listing []cwlcore.FileOrDirectory) []string {
	t.Helper()

	names := make([]string, 0, len(listing))

	for _, entry := range listing {
		switch typed := entry.(type) {
		case *cwlcore.File:
			names = append(names, typed.Basename)
		case *cwlcore.Directory:
			names = append(names, typed.Basename)
		default:
			t.Fatalf("listing holds %#v", entry)
		}
	}

	return names
}

// outSubdirectory finds the named Directory in a listing.
func outSubdirectory(t *testing.T, listing []cwlcore.FileOrDirectory, name string) *cwlcore.Directory {
	t.Helper()

	for _, entry := range listing {
		if dir, ok := entry.(*cwlcore.Directory); ok && dir.Basename == name {
			return dir
		}
	}

	t.Fatalf("listing %v has no directory %q", outEntryNames(t, listing), name)

	return nil
}

// TestCollectOutputsLeavesAListingUnreadWhenNothingAsks pins the half of the loadListing rule that
// belongs to a tool: an output binding that asked for no listing gets none, whether it said so
// outright or left the field at Process.yml's default of no_listing. A collected value is still in
// flight — a later step may write into the very directory it names — so nil, meaning nobody read it,
// is the only thing that stays true about it.
func TestCollectOutputsLeavesAListingUnreadWhenNothingAsks(t *testing.T) {
	t.Parallel()

	modes := []cwlcore.LoadListingEnum{"", cwlcore.LoadListingNone}
	for _, mode := range modes {
		t.Run("mode "+string(cmp.Or(mode, "unset")), func(t *testing.T) {
			t.Parallel()

			dir := outTreeDir(t)
			tool := outTestTool(outTestParam("#tool/d", outTypeDirectory, outListingBinding(outNameTree, mode)))
			value := outWantDirectory(t, outCollect(t, tool, dir, 0))

			assertDeepEqual(t, "basename", value.Basename, outNameTree)
			assertDeepEqual(t, "path", value.Path, filepath.Join(dir, outNameTree))

			if value.Listing != nil {
				t.Errorf("a binding that asked for nothing produced %v", outEntryNames(t, value.Listing))
			}
		})
	}
}

// TestFillListingsCompletesADirectoryNothingRead covers the publication pass that runs where the
// run's output object is assembled: a Directory nobody read is read there, to the bottom, because
// that is the point past which the location alone may no longer be enough to recover its contents.
func TestFillListingsCompletesADirectoryNothingRead(t *testing.T) {
	t.Parallel()

	dir := outTreeDir(t)
	tool := outTestTool(outTestParam("#tool/d", outTypeDirectory, outListingBinding(outNameTree, "")))
	value := outWantDirectory(t, outCollect(t, tool, dir, 0))

	outFillListings(value)

	assertDeepEqual(t, "listing", outEntryNames(t, value.Listing), []string{outNameA, outNameSub})

	sub := outSubdirectory(t, value.Listing, outNameSub)
	assertDeepEqual(t, "sub listing", outEntryNames(t, sub.Listing), []string{outNameB, outNameDeeper})
}

func TestCollectOutputsShallowListing(t *testing.T) {
	t.Parallel()

	dir := outTreeDir(t)
	tool := outTestTool(
		outTestParam("#tool/d", outTypeDirectory, outListingBinding(outNameTree, cwlcore.LoadListingShallow)),
	)
	value := outWantDirectory(t, outCollect(t, tool, dir, 0))

	assertDeepEqual(t, "listing", outEntryNames(t, value.Listing), []string{outNameA, outNameSub})

	// shallow_listing stops at the top level, which is exactly the distinction it exists to draw
	// against deep_listing: the subdirectory is named but not opened.
	sub := outSubdirectory(t, value.Listing, outNameSub)
	if sub.Listing != nil {
		t.Errorf("shallow_listing descended into %q: %v", outNameSub, outEntryNames(t, sub.Listing))
	}

	// The publication pass then descends into what it left: a listing already set is kept and
	// completed, never replaced.
	outFillListings(value)
	assertDeepEqual(t, "listing", outEntryNames(t, value.Listing), []string{outNameA, outNameSub})
	assertDeepEqual(t, "sub listing", outEntryNames(t, sub.Listing), []string{outNameB, outNameDeeper})

	// Files in the listing are measured, so an expression reading self.listing[0].checksum sees
	// the same value the output object publishes.
	file, ok := value.Listing[0].(*cwlcore.File)
	if !ok {
		t.Fatalf("first entry = %#v, want a File", value.Listing[0])
	}

	assertDeepEqual(t, "checksum", file.Checksum, outSumAlpha)
	assertDeepEqual(t, "size", file.Size.Int(), int64(len("alpha")))
}

func TestOutputEvalSeesOnlyTheListingLoadListingAsked(t *testing.T) {
	t.Parallel()

	dir := outTreeDir(t)

	// The completion pass runs after outputEval, so what an expression sees is still exactly
	// what Process.yml's loadListing asked to be loaded "for use by expressions".
	binding := outListingBinding(outNameTree, cwlcore.LoadListingShallow)
	binding.OutputEval = "$(self[0].listing[1].listing)"

	tool := outTestTool(outTestParam("#tool/d", outTypeNull, binding))

	// The subdirectory the shallow read named but did not open has no listing field at all, so
	// the reference does not resolve. That is the distinction no_listing and shallow_listing
	// exist to draw, and it survives the completion pass because the pass runs afterwards.
	err := outCollectErr(t, tool, dir)
	if !strings.Contains(err.Error(), `has no field "listing"`) {
		t.Errorf("error = %v, want a report of the absent listing field", err)
	}
}

func TestCollectOutputsDeepListing(t *testing.T) {
	t.Parallel()

	dir := outTreeDir(t)
	tool := outTestTool(
		outTestParam("#tool/d", outTypeDirectory, outListingBinding(outNameTree, cwlcore.LoadListingDeep)),
	)
	value := outWantDirectory(t, outCollect(t, tool, dir, 0))

	assertDeepEqual(t, "listing", outEntryNames(t, value.Listing), []string{outNameA, outNameSub})

	sub := outSubdirectory(t, value.Listing, outNameSub)
	assertDeepEqual(t, "sub listing", outEntryNames(t, sub.Listing), []string{outNameB, outNameDeeper})

	deeper := outSubdirectory(t, sub.Listing, outNameDeeper)
	assertDeepEqual(t, "deeper listing", outEntryNames(t, deeper.Listing), []string{outNameC})
}

func TestCollectOutputsListingOfAnEmptyDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.Mkdir(filepath.Join(dir, "empty"), 0o755)
	if err != nil {
		t.Fatalf("creating the directory: %v", err)
	}

	tool := outTestTool(outTestParam("#tool/d", outTypeDirectory, outListingBinding("empty", cwlcore.LoadListingDeep)))
	value := outWantDirectory(t, outCollect(t, tool, dir, 0))

	// Read and empty, which is a *different* value from nil. Both render as "no entries" to a
	// careless reader, and the whole point of keeping them apart is that only one of them is a
	// statement about the directory.
	if value.Listing == nil {
		t.Fatal("a directory that was read and holds nothing must have an empty listing, not a nil one")
	}

	assertInt(t, "len(listing)", len(value.Listing), 0)
}

func TestDeepListingStopsAtASymlinkLoop(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outNameTree+"/"+outNameA, "alpha")

	err := os.Symlink(filepath.Join(dir, outNameTree), filepath.Join(dir, outNameTree, "loop"))
	if err != nil {
		t.Skipf("this filesystem does not support symlinks: %v", err)
	}

	tool := outTestTool(
		outTestParam("#tool/d", outTypeDirectory, outListingBinding(outNameTree, cwlcore.LoadListingDeep)),
	)
	value := outWantDirectory(t, outCollect(t, tool, dir, 0))

	assertDeepEqual(t, "listing", outEntryNames(t, value.Listing), []string{outNameA, "loop"})

	// The deep walk stops where the cycle closes, leaving the link's own listing unread.
	loop := outSubdirectory(t, value.Listing, "loop")
	if loop.Listing != nil {
		t.Errorf("followed a symlink loop during collection: %v", outEntryNames(t, loop.Listing))
	}

	// The publication pass reads the link once, because it starts a fresh walk there and the loop
	// has not been seen yet on that branch. What it must not do is keep going: the copy of the
	// link one level down closes the cycle, and its listing stays unread.
	outFillListings(value)
	assertDeepEqual(t, "loop listing", outEntryNames(t, loop.Listing), []string{outNameA, "loop"})

	if inner := outSubdirectory(t, loop.Listing, "loop"); inner.Listing != nil {
		t.Errorf("followed a symlink loop: %v", outEntryNames(t, inner.Listing))
	}
}

func TestGlobbedSymlinkKeepsItsOwnName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, "real.txt", outContentHello)

	err := os.Symlink(filepath.Join(dir, "real.txt"), filepath.Join(dir, "link.dat"))
	if err != nil {
		t.Skipf("this filesystem does not support symlinks: %v", err)
	}

	tool := outTestTool(outTestParam("#tool/l", outTypeFile, outGlobBinding("link.dat")))
	outputs := outCollect(t, tool, dir, 0)

	// CommandLineTool.yml: the value takes "the `basename` (and corresponding `nameroot` and
	// `nameext`) of the symlink", with the content of its target.
	file := outWantFile(t, outputs, "l", "link.dat", outSumHello, int64(len(outContentHello)))
	assertDeepEqual(t, "nameext", file.Nameext, ".dat")
}

func TestListDirectoryOnAMissingDirectory(t *testing.T) {
	t.Parallel()

	_, err := outListDirectory(filepath.Join(t.TempDir(), "gone"), outShallowWalk, nil)
	if err == nil {
		t.Fatal("outListDirectory on a missing directory succeeded, want an error")
	}
}

func TestListingEntryOnAMissingPath(t *testing.T) {
	t.Parallel()

	_, err := outListingEntry(filepath.Join(t.TempDir(), "gone"), outShallowWalk, nil)
	if err == nil {
		t.Fatal("outListingEntry on a missing path succeeded, want an error")
	}
}

func TestAlreadyWalkedOnAnUnrelatedDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if outAlreadyWalked(info, nil) {
		t.Error("an empty walk cannot contain anything")
	}

	if !outAlreadyWalked(info, []fs.FileInfo{info}) {
		t.Error("a directory must be recognised as itself")
	}
}

func TestCollectOutputsLoadContents(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, "text.txt", outContentHello)
	outWriteFile(t, dir, "empty.txt", "")

	binding := outGlobBinding("*.txt")
	binding.LoadContents = true

	tool := outTestTool(outTestParam("#tool/all", outTypeFileArray, binding))
	outputs := outCollect(t, tool, dir, 0)

	items, ok := outputs["all"].([]any)
	if !ok {
		t.Fatalf("all = %#v, want a list", outputs["all"])
	}

	empty, ok := items[0].(*cwlcore.File)
	if !ok {
		t.Fatalf("first = %#v, want a File", items[0])
	}

	// A zero-byte file has contents, and they are "". OptString is what keeps that apart from a
	// file whose contents were never read.
	if !empty.Contents.IsSet() {
		t.Error("an empty file read with loadContents must have contents set to the empty string")
	}

	assertDeepEqual(t, "empty contents", empty.Contents.Value(), "")

	text, ok := items[1].(*cwlcore.File)
	if !ok {
		t.Fatalf("second = %#v, want a File", items[1])
	}

	assertDeepEqual(t, "text contents", text.Contents.Value(), outContentHello)
}

func TestCollectOutputsLoadContentsOverTheLimit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// One byte over 64 KiB. CWL v1.2 changed this from silent truncation to a fatal error, so
	// 65537 bytes must fail rather than yield 65536 of them.
	outWriteFile(t, dir, "big.txt", strings.Repeat("x", joMaxContentsBytes+1))

	binding := outGlobBinding("big.txt")
	binding.LoadContents = true

	tool := outTestTool(outTestParam("#tool/big", outTypeFile, binding))

	err := outCollectErr(t, tool, dir)
	assertErrorIs(t, "oversized loadContents", err, ErrContentsTooLarge)
}

func TestCollectOutputsLoadContentsAtExactlyTheLimit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, "exact.txt", strings.Repeat("x", joMaxContentsBytes))

	binding := outGlobBinding("exact.txt")
	binding.LoadContents = true

	tool := outTestTool(outTestParam("#tool/exact", outTypeFile, binding))
	outputs := outCollect(t, tool, dir, 0)

	file, ok := outputs["exact"].(*cwlcore.File)
	if !ok {
		t.Fatalf("exact = %#v, want a File", outputs["exact"])
	}

	// "64 KiB or smaller" — exactly 64 KiB is inside the limit, not over it.
	assertInt(t, "len(contents)", len(file.Contents.Value()), joMaxContentsBytes)
}

func TestCollectOutputsLoadContentsOnBinary(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, "blob.bin", "\xff\xfe not utf-8")

	binding := outGlobBinding("blob.bin")
	binding.LoadContents = true

	tool := outTestTool(outTestParam("#tool/blob", outTypeFile, binding))

	err := outCollectErr(t, tool, dir)
	assertErrorIs(t, "binary loadContents", err, ErrContentsNotText)
}

func TestCollectOutputsOutputEvalSeesAnArrayAndTheExitCode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, "only.txt", outContentHello)

	binding := outGlobBinding("only.txt")
	binding.OutputEval = "$(self.length)/$(self[0].basename)/$(runtime.exitCode)"

	tool := outTestTool(outTestParam("#tool/summary", outTypeString, binding))
	outputs := outCollect(t, tool, dir, 7)

	// Even a single match arrives as a one-element array, and runtime.exitCode is defined here
	// and nowhere else.
	assertDeepEqual(t, "summary", outputs["summary"], "1/only.txt/7")
}

func TestCollectOutputsOutputEvalSeesAZeroLengthArray(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	binding := outGlobBinding("nothing-here-*.txt")
	binding.OutputEval = "$(self.length)"

	tool := outTestTool(outTestParam("#tool/count", outTypeLong, binding))
	outputs := outCollect(t, tool, dir, 0)

	assertDeepEqual(t, "count", outputs["count"], int64(0))
}

func TestCollectOutputsOutputEvalReplacesTheValue(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, "a.txt", "alpha")
	outWriteFile(t, dir, "b.txt", "beta")

	binding := outGlobBinding("*.txt")
	binding.OutputEval = "$(self[1])"

	tool := outTestTool(outTestParam("#tool/second", outTypeFile, binding))
	outputs := outCollect(t, tool, dir, 0)

	// The globbed list is gone; what outputEval returned took its place, re-typed back into a
	// File on the way out.
	file := outWantFile(t, outputs, "second", "b.txt", outSumBeta, int64(len("beta")))
	assertDeepEqual(t, "path", file.Path, filepath.Join(dir, "b.txt"))
}

func TestCollectOutputsOutputEvalCanReturnAnyValue(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	binding := &cwlcore.CommandOutputBinding{OutputEval: outRefN}
	tool := outTestTool(outTestParam("#tool/n", outTypeLong, binding))

	outputs, err := CollectOutputs(tool, dir, 0, map[string]any{"n": int64(42)},
		cwlcore.NewEvaluator(), cwlcore.RuntimeContext{Outdir: dir})
	if err != nil {
		t.Fatalf("CollectOutputs: %v", err)
	}

	assertDeepEqual(t, "n", outputs["n"], int64(42))
}

func TestCollectOutputsOutputEvalFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	binding := outGlobBinding("*.txt")
	binding.OutputEval = outBadRef

	tool := outTestTool(outTestParam("#tool/x", outTypeString, binding))

	err := outCollectErr(t, tool, dir)
	assertErrorIs(t, "bad outputEval", err, cwlcore.ErrExpressionEval)
}

func TestExitCodeIsNotVisibleOutsideOutputEval(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tool := outTestTool(outTestParam("#tool/x", outTypeOptionalFile, outGlobBinding("$(runtime.exitCode).txt")))

	// CommandLineTool.yml offers runtime.exitCode to outputEval and to nothing else, so a glob
	// expression that reaches for it must fail rather than quietly read a plausible zero.
	err := outCollectErr(t, tool, dir)
	assertErrorIs(t, "exitCode in a glob", err, cwlcore.ErrExpressionEval)
}

func TestCollectOutputsUsesJavaScriptWhenEnabled(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, "a.txt", "alpha")
	outWriteFile(t, dir, "b.txt", "beta")

	binding := outGlobBinding("*.txt")
	binding.OutputEval = "${ return self.map(function(f) { return f.basename; }).join(\",\"); }"

	tool := outTestTool(outTestParam("#tool/names", outTypeString, binding))

	outputs, err := CollectOutputs(tool, dir, 0, nil, cwlcore.NewEvaluator(cwlcore.WithJS(nil)),
		cwlcore.RuntimeContext{Outdir: dir})
	if err != nil {
		t.Fatalf("CollectOutputs: %v", err)
	}

	assertDeepEqual(t, "names", outputs["names"], "a.txt,b.txt")
}

// outSymlink links target to name inside dir, skipping the test on a filesystem that cannot.
func outSymlink(t *testing.T, dir, name, target string) {
	t.Helper()

	err := os.Symlink(target, filepath.Join(dir, name))
	if err != nil {
		t.Skipf("this filesystem does not support symlinks: %v", err)
	}
}

func TestGlobbedSymlinkLeadingOutOfTheOutputDirectory(t *testing.T) {
	t.Parallel()

	outside := t.TempDir()
	target := outWriteFile(t, outside, "original.txt", outContentHello)

	dir := t.TempDir()
	outSymlink(t, dir, "link.txt", target)

	tool := outTestTool(outTestParam("#tool/l", outTypeFile, outGlobBinding("link.txt")))

	// CommandLineTool.yml: "It is an error if a symlink in the output directory (or any symlink
	// in a chain of links) refers to any file or directory that is not under an input or output
	// directory." The tool declared no inputs, so the output directory is the whole of what it
	// may publish from.
	assertErrorIs(t, "symlink out of the output directory", outCollectErr(t, tool, dir), ErrGlobSymlink)
}

func TestGlobbedSymlinkToAnInputIsRetrieved(t *testing.T) {
	t.Parallel()

	staged := t.TempDir()
	target := outWriteFile(t, staged, "whale.txt", outContentHello)

	dir := t.TempDir()
	outSymlink(t, dir, "whale.txt", target)

	tool := outTestTool(outTestParam("#tool/l", outTypeFile, outGlobBinding("whale.txt")))

	// The ordinary case, and the reason the rule names input directories at all: an input staged
	// into the output directory is a link straight back out of it.
	outputs, err := CollectOutputs(tool, dir, 0,
		map[string]any{"f": &cwlcore.File{Path: target, Basename: "whale.txt"}},
		cwlcore.NewEvaluator(), cwlcore.RuntimeContext{Outdir: dir})
	if err != nil {
		t.Fatalf("CollectOutputs: %v", err)
	}

	outWantFile(t, outputs, "l", "whale.txt", outSumHello, int64(len(outContentHello)))
}

func TestGlobbedDanglingSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outSymlink(t, dir, "link.txt", filepath.Join(dir, "nowhere"))

	tool := outTestTool(outTestParam("#tool/l", outTypeFile, outGlobBinding("link.txt")))

	// glob(3) matches the link by name, and there is nothing at the end of it to resolve, let
	// alone to publish.
	assertNames(t, outCollectErr(t, tool, dir), "nowhere")
}

func TestAllowedRootsFromAnInputObject(t *testing.T) {
	t.Parallel()

	inputs := outExpressionObject(map[string]any{
		"d":  &cwlcore.Directory{Path: "/in/tree"},
		"fs": []any{&cwlcore.File{Path: "/in/reads/a.bam", Basename: "a.bam"}},

		// A literal has nowhere to be yet, a plain record that happens to carry a `path` field is
		// not a filesystem value, and a number is not an object: none of the three occupies a path.
		"lit": &cwlcore.File{Basename: "x", Contents: cwlcore.NewOptString("hi")},
		"rec": map[string]any{outKeyPath: "/elsewhere"},
		"n":   int64(3),
	})

	// Neither root exists, so neither resolves to anything else and the two forms collapse.
	assertDeepEqual(t, "roots", outAllowedRoots(inputs, nil), []string{"/in/reads/a.bam", "/in/tree"})
}

func TestAllowedRootsFromAnInitialWorkDirListing(t *testing.T) {
	t.Parallel()

	tool := outStagingTool(cwlcore.NewInitialWorkDirListing(
		[]cwlcore.InitialWorkDirEntry{
			cwlcore.NewInitialWorkDirFile(&cwlcore.File{Path: "/src/a.txt"}),
			cwlcore.NewInitialWorkDirDirectory(&cwlcore.Directory{Location: "file:///src/tree"}),
			cwlcore.NewInitialWorkDirObjects([]cwlcore.FileOrDirectory{
				&cwlcore.Directory{Path: "/src/nested"},

				// A literal names no host path, so it contributes no root.
				&cwlcore.File{Basename: "lit", Contents: cwlcore.NewOptString("hi")},
			}),

			// None of these stages from anywhere: a Dirent's content is created in the
			// output directory, and an expression names nothing until it is evaluated.
			cwlcore.NewInitialWorkDirDirent(&cwlcore.Dirent{Entryname: "d", Entry: "text"}),
			cwlcore.NewInitialWorkDirExpression("$(inputs.f)"),
			cwlcore.NewInitialWorkDirNull(),
		}))

	assertDeepEqual(t, "roots", outAllowedRoots(nil, cwlcore.NewScope(tool)),
		[]string{"/src/a.txt", "/src/nested", "/src/tree"})
}

func TestAllowedRootsIgnoreAnExpressionListing(t *testing.T) {
	t.Parallel()

	// A listing that is one expression holds no entries to read a path off, and evaluating it
	// here to find out would run the document's expressions a second time.
	tool := outStagingTool(cwlcore.NewInitialWorkDirListingExpression("$(inputs.f)"))

	assertDeepEqual(t, "roots", outAllowedRoots(nil, cwlcore.NewScope(tool)), make([]string, 0))
}

func TestGlobbedInitialWorkDirEntryIsRetrieved(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	target := outWriteFile(t, source, "whale.txt", outContentHello)

	dir := t.TempDir()
	outSymlink(t, dir, "whale.txt", target)

	// What iwd-fileobjs1 leaves behind: an InitialWorkDirRequirement named a File the input
	// object never mentions, and staging linked it into the output directory. The link leads
	// straight back out, and the entry's own path is what says that is legitimate.
	tool := outTestTool(outTestParam("#tool/l", outTypeFile, outGlobBinding("whale.txt")))
	tool.Requirements = []cwlcore.ProcessRequirement{
		&cwlcore.InitialWorkDirRequirement{Listing: cwlcore.NewInitialWorkDirListing(
			[]cwlcore.InitialWorkDirEntry{cwlcore.NewInitialWorkDirFile(&cwlcore.File{Path: target})},
		)},
	}

	outputs, err := CollectOutputs(tool, dir, 0, nil,
		cwlcore.NewEvaluator(), cwlcore.RuntimeContext{Outdir: dir})
	if err != nil {
		t.Fatalf("CollectOutputs: %v", err)
	}

	outWantFile(t, outputs, "l", "whale.txt", outSumHello, int64(len(outContentHello)))
}

func TestGlobbedStagedInputIsRetrieved(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	target := outWriteFile(t, source, "whale.txt", outContentHello)

	dir := t.TempDir()
	outSymlink(t, dir, "staged.txt", target)

	tool := outTestTool(outTestParam("#tool/l", outTypeFile, outGlobBinding("staged.txt")))

	// What an InitialWorkDirRequirement leaves behind: the input's own path is the link inside the
	// output directory, and the link leads back out to wherever the input really lives. Rejecting
	// that would reject every tool that names a staged input as one of its outputs.
	staged := &cwlcore.File{Path: filepath.Join(dir, "staged.txt"), Basename: "staged.txt"}

	outputs, err := CollectOutputs(tool, dir, 0, map[string]any{"f": staged},
		cwlcore.NewEvaluator(), cwlcore.RuntimeContext{Outdir: dir})
	if err != nil {
		t.Fatalf("CollectOutputs: %v", err)
	}

	outWantFile(t, outputs, "l", "staged.txt", outSumHello, int64(len(outContentHello)))
}

func TestCollectOutputsCompletesADirectoryInsideAnExpressionResult(t *testing.T) {
	t.Parallel()

	dir := outTreeDir(t)

	binding := outGlobBinding(outNameTree)
	binding.OutputEval = "${ return {found: self[0]}; }"

	tool := outTestTool(outTestParam("#tool/report", cwlcore.TypeRef{}, binding))

	outputs, err := CollectOutputs(tool, dir, 0, nil, cwlcore.NewEvaluator(cwlcore.WithJS(nil)),
		cwlcore.RuntimeContext{Outdir: dir})
	if err != nil {
		t.Fatalf("CollectOutputs: %v", err)
	}

	report, ok := outputs["report"].(map[string]any)
	if !ok {
		t.Fatalf("report = %#v, want an object", outputs["report"])
	}

	// The completion pass reaches a Directory wherever an expression put it, because that is
	// where the published output object carries it.
	found, ok := report["found"].(*cwlcore.Directory)
	if !ok {
		t.Fatalf("found = %#v, want a *cwlcore.Directory", report["found"])
	}

	outFillListings(outputs["report"])
	assertDeepEqual(t, "listing", outEntryNames(t, found.Listing), []string{outNameA, outNameSub})
}

func TestFillListingsLeavesAnUnreadableDirectoryUncompleted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	binding := &cwlcore.CommandOutputBinding{
		OutputEval: cwlcore.Expression("${ return {class: 'Directory', basename: 'gone', path: '" +
			filepath.Join(dir, "gone") + "'}; }"),
	}

	tool := outTestTool(outTestParam("#tool/d", outTypeDirectory, binding))

	outputs, err := CollectOutputs(tool, dir, 0, nil, cwlcore.NewEvaluator(cwlcore.WithJS(nil)),
		cwlcore.RuntimeContext{Outdir: dir})
	if err != nil {
		t.Fatalf("CollectOutputs: %v", err)
	}

	value := outWantDirectory(t, outputs)
	outFillListings(value)

	// An expression may name a directory that is not there — one a later stage will create, say.
	// Completing the value is a courtesy, so failing to is not a reason to refuse the output.
	if value.Listing != nil {
		t.Errorf("listing = %v, want nil for a directory that is not on disk", outEntryNames(t, value.Listing))
	}
}

func TestFillListingsLeavesADirectoryItCannotWalkUncompleted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.Mkdir(filepath.Join(dir, outNameTree), 0o755)
	if err != nil {
		t.Fatalf("creating the directory: %v", err)
	}

	outSymlink(t, filepath.Join(dir, outNameTree), "dangling", filepath.Join(dir, "nowhere"))

	tool := outTestTool(outTestParam("#tool/d", outTypeDirectory, outGlobBinding(outNameTree)))
	value := outWantDirectory(t, outCollect(t, tool, dir, 0))

	outFillListings(value)

	// The walk cannot measure an entry that leads nowhere. A loadListing that asked for the
	// listing reports that; the completion pass, which nobody asked for, does not.
	if value.Listing != nil {
		t.Errorf("listing = %v, want nil for a directory the walk could not read",
			outEntryNames(t, value.Listing))
	}
}
