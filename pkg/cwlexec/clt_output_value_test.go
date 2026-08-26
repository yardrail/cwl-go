package cwlexec

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// outNoExtName is a basename with no extension at all.
const outNoExtName = "README"

// outObjectKeys lists an expression object's field names in sorted order.
func outObjectKeys(t *testing.T, value any) []string {
	t.Helper()

	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("value = %#v, want an object", value)
	}

	names := make([]string, 0, len(object))
	for key := range object {
		names = append(names, key)
	}

	slices.Sort(names)

	return names
}

func TestExpressionValueRendersEachShape(t *testing.T) {
	t.Parallel()

	file := &cwlcore.File{Basename: outNameA}
	dir := &cwlcore.Directory{Basename: "d"}

	assertDeepEqual(t, "File", outObjectKeys(t, outExpressionValue(file)),
		[]string{outKeyBasename, outKeyClass, outKeyNameext, outKeyNameroot})
	assertDeepEqual(t, "Directory", outObjectKeys(t, outExpressionValue(dir)),
		[]string{outKeyBasename, outKeyClass})

	list, ok := outExpressionValue([]cwlcore.FileOrDirectory{file, dir}).([]any)
	if !ok {
		t.Fatal("a FileOrDirectory list must render as a list")
	}

	assertInt(t, "len(list)", len(list), 2)

	nested, ok := outExpressionValue([]any{map[string]any{"f": file}}).([]any)
	if !ok {
		t.Fatal("a plain list must render as a list")
	}

	wrapper, ok := nested[0].(map[string]any)
	if !ok {
		t.Fatalf("nested[0] = %#v, want an object", nested[0])
	}

	assertDeepEqual(t, "nested File", outObjectKeys(t, wrapper["f"]),
		[]string{outKeyBasename, outKeyClass, outKeyNameext, outKeyNameroot})

	// Anything that is not a filesystem value, a list or an object passes through untouched.
	assertDeepEqual(t, "scalar", outExpressionValue(int64(3)), int64(3))
	assertDeepEqual(t, "nil", outExpressionValue(nil), nil)
}

func TestFileObjectKeepsZeroValuedFieldsAndDropsUnsetOnes(t *testing.T) {
	t.Parallel()

	empty := outFileObject(&cwlcore.File{
		Basename: "empty.txt",
		Size:     cwlcore.NewOptInt(0),
		Contents: cwlcore.NewOptString(""),
	})

	// A size of 0 and contents of "" describe an empty file. Dropping them because they are the
	// Go zero value would tell an expression the file was never measured or never read.
	assertDeepEqual(t, "size", empty[outKeySize], int64(0))
	assertDeepEqual(t, "contents", empty[outKeyContents], "")

	if _, present := empty[outKeySecondaryFiles]; present {
		t.Error("a nil secondaryFiles must be absent, not an empty array")
	}

	resolved := outFileObject(&cwlcore.File{
		Basename:       outNameA,
		SecondaryFiles: make([]cwlcore.FileOrDirectory, 0),
	})

	// An empty-but-present list is the opposite claim: the patterns were applied and matched
	// nothing.
	assertDeepEqual(t, "empty secondaryFiles", resolved[outKeySecondaryFiles], make([]any, 0))

	// nameroot and nameext accompany the basename even when the extension is empty, because
	// Process.yml requires nameroot + nameext == basename.
	plain := outFileObject(&cwlcore.File{Basename: outNoExtName, Nameroot: outNoExtName})
	assertDeepEqual(t, "nameroot", plain[outKeyNameroot], outNoExtName)
	assertDeepEqual(t, "nameext", plain[outKeyNameext], "")

	// A file literal has no name at all until it is staged, and then it has no name fields.
	literal := outFileObject(&cwlcore.File{Contents: cwlcore.NewOptString("x")})
	assertDeepEqual(t, "literal", outObjectKeys(t, literal), []string{outKeyClass, outKeyContents})
}

func TestDirectoryObjectHasOnlyItsFiveSchemaFields(t *testing.T) {
	t.Parallel()

	unread := outDirectoryObject(&cwlcore.Directory{
		Location: "file:///d",
		Path:     "/d",
		Basename: "d",
	})

	// The vendored schema gives Directory class, location, path, basename and listing and
	// nothing else — no checksum, no size, no format, no dirname, no nameroot, no nameext.
	assertDeepEqual(t, "unread", outObjectKeys(t, unread),
		[]string{outKeyBasename, outKeyClass, outKeyLocation, outKeyPath})

	read := outDirectoryObject(&cwlcore.Directory{
		Basename: "d",
		Listing:  make([]cwlcore.FileOrDirectory, 0),
	})

	assertDeepEqual(t, "read but empty", read[outKeyListing], make([]any, 0))
}

func TestPutTextAndTextField(t *testing.T) {
	t.Parallel()

	object := make(map[string]any, 2)
	outPutText(object, outKeyKept, "value")
	outPutText(object, "dropped", "")

	assertDeepEqual(t, "keys", outObjectKeys(t, object), []string{outKeyKept})
	assertDeepEqual(t, "kept", outTextField(object, outKeyKept), "value")
	assertDeepEqual(t, "absent", outTextField(object, "dropped"), "")

	object["number"] = int64(3)
	assertDeepEqual(t, "non-string", outTextField(object, "number"), "")
}

func TestOutputEvalReturningANestedStructure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outNameA, "alpha")

	binding := outGlobBinding(outNameA)
	binding.OutputEval = "${ return {picked: [self[0]], count: self.length}; }"

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

	assertDeepEqual(t, "count", report["count"], int64(1))

	picked, ok := report["picked"].([]any)
	if !ok {
		t.Fatalf("picked = %#v, want a list", report["picked"])
	}

	// A File nested inside whatever shape the expression invented is still re-typed on the way
	// back out, so the output object holds one representation throughout.
	file, ok := picked[0].(*cwlcore.File)
	if !ok {
		t.Fatalf("picked[0] = %#v, want a *cwlcore.File", picked[0])
	}

	assertDeepEqual(t, "checksum", file.Checksum, outSumAlpha)
}

func TestOutputEvalReturningADirectoryWithAListing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outNameA, "alpha")

	binding := &cwlcore.CommandOutputBinding{
		OutputEval: "${ return {class: 'Directory', basename: 'made', " +
			"listing: [{class: 'File', location: '" + outNameA + "'}]}; }",
	}

	tool := outTestTool(outTestParam("#tool/d", outTypeDirectory, binding))

	outputs, err := CollectOutputs(tool, dir, 0, nil, cwlcore.NewEvaluator(cwlcore.WithJS(nil)),
		cwlcore.RuntimeContext{Outdir: dir})
	if err != nil {
		t.Fatalf("CollectOutputs: %v", err)
	}

	made, ok := outputs["d"].(*cwlcore.Directory)
	if !ok {
		t.Fatalf("d = %#v, want a *cwlcore.Directory", outputs["d"])
	}

	assertDeepEqual(t, "basename", made.Basename, "made")
	assertDeepEqual(t, "listing", outEntryNames(t, made.Listing), []string{outNameA})

	entry, ok := made.Listing[0].(*cwlcore.File)
	if !ok {
		t.Fatalf("listing[0] = %#v", made.Listing[0])
	}

	// A relative location in a hand-built value resolves against the output directory, and the
	// file is measured because the expression gave no checksum.
	assertDeepEqual(t, "path", entry.Path, filepath.Join(dir, outNameA))
	assertDeepEqual(t, "checksum", entry.Checksum, outSumAlpha)
}

func TestOutputEvalReturningABadListingEntry(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	binding := &cwlcore.CommandOutputBinding{
		OutputEval: "${ return {class: 'Directory', basename: 'made', listing: [3]}; }",
	}

	tool := outTestTool(outTestParam("#tool/d", outTypeDirectory, binding))

	_, err := CollectOutputs(tool, dir, 0, nil, cwlcore.NewEvaluator(cwlcore.WithJS(nil)),
		cwlcore.RuntimeContext{Outdir: dir})
	assertErrorIs(t, "numeric listing entry", err, ErrFilesystemEntry)
}

func TestSecondaryFilesWithABadNestedEntry(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outPrimaryName, outContentHello)
	outWriteFile(t, dir, outIndexName, "index-data\n")

	tool := outTestTool(outSecondaryParam(cwlcore.SecondaryFileSchema{
		Pattern: "${ return {class: 'File', location: '" + outIndexName + "', secondaryFiles: [3]}; }",
	}))

	_, err := CollectOutputs(tool, dir, 0, nil, cwlcore.NewEvaluator(cwlcore.WithJS(nil)),
		cwlcore.RuntimeContext{Outdir: dir})
	assertErrorIs(t, "numeric nested entry", err, ErrFilesystemEntry)
}

func TestOutputEvalKeepsAChecksumTheExpressionSupplied(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outNameA, "alpha")

	binding := &cwlcore.CommandOutputBinding{
		OutputEval: "${ return {class: 'File', location: '" + outNameA + "', " +
			"checksum: 'sha1$0000000000000000000000000000000000000000', size: 99}; }",
	}

	tool := outTestTool(outTestParam("#tool/f", outTypeFile, binding))

	outputs, err := CollectOutputs(tool, dir, 0, nil, cwlcore.NewEvaluator(cwlcore.WithJS(nil)),
		cwlcore.RuntimeContext{Outdir: dir})
	if err != nil {
		t.Fatalf("CollectOutputs: %v", err)
	}

	// The expression already measured it. Re-reading the file would be work nobody asked for,
	// and would quietly contradict a value the document author chose to state.
	outWantFile(t, outputs, "f", outNameA, "sha1$0000000000000000000000000000000000000000", 99)
}

func TestRemeasure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	local := outWriteFile(t, dir, outNameA, "alpha")

	onDisk := &cwlcore.File{Path: local}
	outRemeasure(onDisk)
	assertDeepEqual(t, "on-disk checksum", onDisk.Checksum, outSumAlpha)
	assertDeepEqual(t, "on-disk size", onDisk.Size.Int(), int64(len("alpha")))

	// A file literal is measured from its own bytes: it does not exist yet, but what it will
	// contain is already known.
	literal := &cwlcore.File{Contents: cwlcore.NewOptString("alpha")}
	outRemeasure(literal)
	assertDeepEqual(t, "literal checksum", literal.Checksum, outSumAlpha)

	// A path that is not there is left alone rather than reported: an expression may describe a
	// file some later stage will create.
	absent := &cwlcore.File{Path: filepath.Join(dir, "gone")}
	outRemeasure(absent)
	assertDeepEqual(t, "absent checksum", absent.Checksum, "")

	if absent.Size.IsSet() {
		t.Errorf("size = %s, want unset for a file that is not there", absent.Size)
	}
}

func TestLocalPath(t *testing.T) {
	t.Parallel()

	collector := &outputCollector{outdir: outTestOutdir}

	cases := map[string]string{
		"file://" + outTestAbs: outTestAbs,
		outNameA:               outTestOutdir + "/" + outNameA,
		outTestAbs:             outTestAbs,
		"http://example/a":     "",
		"s3://bucket/a.txt":    "",
		"":                     "",
	}

	for location, want := range cases {
		if got := collector.localPath(location); got != want {
			t.Errorf("localPath(%q) = %q, want %q", location, got, want)
		}
	}
}

func TestDeriveRefPrefersPathAndKeepsARemoteLocation(t *testing.T) {
	t.Parallel()

	collector := &outputCollector{outdir: outTestOutdir}

	fromPath := collector.deriveRef(map[string]any{outKeyPath: "sub/" + outNameA})
	assertDeepEqual(t, "local", fromPath.local, outTestOutdir+"/sub/"+outNameA)
	assertDeepEqual(t, "location", fromPath.location, outFileURI(outTestOutdir+"/sub/"+outNameA))
	assertDeepEqual(t, "basename", fromPath.basename, outNameA)

	// A location on storage this engine cannot read keeps its IRI and gets no path: pretending
	// the IRI were a path would produce a checksum of nothing.
	remote := collector.deriveRef(map[string]any{
		outKeyLocation: "s3://bucket/a.txt",
		outKeyBasename: outNameA,
	})
	assertDeepEqual(t, "remote location", remote.location, "s3://bucket/a.txt")
	assertDeepEqual(t, "remote local", remote.local, "")
	assertDeepEqual(t, "remote basename", remote.basename, outNameA)
}

func TestListingReportsADanglingSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.Mkdir(filepath.Join(dir, outNameTree), 0o755)
	if err != nil {
		t.Fatalf("creating the directory: %v", err)
	}

	err = os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, outNameTree, "dangling"))
	if err != nil {
		t.Skipf("this filesystem does not support symlinks: %v", err)
	}

	tool := outTestTool(outTestParam("#tool/d", outTypeDirectory,
		outListingBinding(outNameTree, cwlcore.LoadListingShallow)))

	err = outCollectErr(t, tool, dir)
	assertNames(t, err, "dangling")
}

func TestOutDigestOnSomethingItCannotRead(t *testing.T) {
	t.Parallel()

	// A directory opens but does not read, which is the one easy way to reach the read-error
	// path without depending on file permissions.
	_, err := outDigest(t.TempDir())
	if err == nil {
		t.Fatal("outDigest on a directory succeeded, want a read error")
	}
}

func TestOutputEvalFailingInsideAListAndAnObject(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"inside a list":    "${ return [{class: 'Directory', listing: [3]}]; }",
		"inside an object": "${ return {d: {class: 'Directory', listing: [3]}}; }",
	}

	for name, expr := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			binding := &cwlcore.CommandOutputBinding{OutputEval: cwlcore.Expression(expr)}
			tool := outTestTool(outTestParam("#tool/x", cwlcore.TypeRef{}, binding))

			_, err := CollectOutputs(tool, dir, 0, nil, cwlcore.NewEvaluator(cwlcore.WithJS(nil)),
				cwlcore.RuntimeContext{Outdir: dir})
			assertErrorIs(t, name, err, ErrFilesystemEntry)
		})
	}
}

func TestOutputEvalReturningAFileLiteral(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	binding := &cwlcore.CommandOutputBinding{
		OutputEval: "${ return {class: 'File', basename: 'made.txt', contents: 'alpha'}; }",
	}
	tool := outTestTool(outTestParam("#tool/f", outTypeFile, binding))

	outputs, err := CollectOutputs(tool, dir, 0, nil, cwlcore.NewEvaluator(cwlcore.WithJS(nil)),
		cwlcore.RuntimeContext{Outdir: dir})
	if err != nil {
		t.Fatalf("CollectOutputs: %v", err)
	}

	// A literal names nothing on disk yet, so it has no path and no dirname, but its bytes are
	// already known and so are its size and checksum.
	file := outWantFile(t, outputs, "f", "made.txt", outSumAlpha, int64(len("alpha")))
	assertDeepEqual(t, "path", file.Path, "")
	assertDeepEqual(t, "dirname", file.Dirname, "")
	assertDeepEqual(t, "contents", file.Contents.Value(), "alpha")
}

func TestOutputEvalReturningARemoteFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	binding := &cwlcore.CommandOutputBinding{
		OutputEval: "${ return {class: 'File', location: 's3://bucket/a.txt', basename: 'a.txt'}; }",
	}
	tool := outTestTool(outTestParam("#tool/f", outTypeFile, binding))

	outputs, err := CollectOutputs(tool, dir, 0, nil, cwlcore.NewEvaluator(cwlcore.WithJS(nil)),
		cwlcore.RuntimeContext{Outdir: dir})
	if err != nil {
		t.Fatalf("CollectOutputs: %v", err)
	}

	file, ok := outputs["f"].(*cwlcore.File)
	if !ok {
		t.Fatalf("f = %#v", outputs["f"])
	}

	// Nothing local to measure and no contents to measure from, so Size stays unset rather than
	// becoming a misleading zero.
	assertDeepEqual(t, "location", file.Location, "s3://bucket/a.txt")

	if file.Size.IsSet() {
		t.Errorf("size = %s, want unset for a file this engine cannot read", file.Size)
	}
}

func TestOutputEvalReturningANestedDirectoryInAListing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	binding := &cwlcore.CommandOutputBinding{
		OutputEval: "${ return {class: 'Directory', basename: 'outer', " +
			"listing: [{class: 'Directory', basename: 'inner'}]}; }",
	}
	tool := outTestTool(outTestParam("#tool/d", outTypeDirectory, binding))

	outputs, err := CollectOutputs(tool, dir, 0, nil, cwlcore.NewEvaluator(cwlcore.WithJS(nil)),
		cwlcore.RuntimeContext{Outdir: dir})
	if err != nil {
		t.Fatalf("CollectOutputs: %v", err)
	}

	outer := outWantDirectory(t, outputs)
	assertDeepEqual(t, "listing", outEntryNames(t, outer.Listing), []string{"inner"})
}

func TestOutNumber(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		value any
		want  int64
		ok    bool
	}{
		"an int64":    {value: int64(7), want: 7, ok: true},
		"a plain int": {value: 7, want: 7, ok: true},
		"a float64":   {value: 7.9, want: 7, ok: true},
		"text":        {value: "7", want: 0, ok: false},
		"nothing":     {value: nil, want: 0, ok: false},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := outNumber(testCase.value)
			if got != testCase.want || ok != testCase.ok {
				t.Errorf("outNumber(%#v) = (%d, %t), want (%d, %t)",
					testCase.value, got, ok, testCase.want, testCase.ok)
			}
		})
	}
}

func TestRequiredOutputWithNoGlobAtAll(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tool := outTestTool(outTestParam("#tool/needed", outTypeFile, &cwlcore.CommandOutputBinding{}))

	err := outCollectErr(t, tool, dir)
	assertErrorIs(t, "binding with no glob", err, ErrOutputMissing)
	assertNames(t, err, "none")
}

// relTestCollector builds a collector rooted at dir, which is all [outputCollector.relocate] reads.
func relTestCollector(dir string) *outputCollector {
	return newOutputCollector(outTestTool(), dir, nil)
}

// relFile builds a File value naming local under the given basename, the way an expression that
// asked for a rename leaves one.
func relFile(local, basename string) *cwlcore.File {
	file := outNewFile(local)
	file.Basename = basename

	return file
}

// relWantPath requires that value is a File living at local, with every field the path implies
// derived from it.
func relWantPath(t *testing.T, value any, local string) *cwlcore.File {
	t.Helper()

	file, ok := value.(*cwlcore.File)
	if !ok {
		t.Fatalf("value = %#v, want a File", value)
	}

	assertDeepEqual(t, "path", file.Path, local)
	assertDeepEqual(t, "location", file.Location, outFileURI(local))
	assertDeepEqual(t, "dirname", file.Dirname, filepath.Dir(local))
	assertDeepEqual(t, "basename", file.Basename, filepath.Base(local))

	parts := outSplitName(filepath.Base(local))
	assertDeepEqual(t, "nameroot", file.Nameroot, parts.root)
	assertDeepEqual(t, "nameext", file.Nameext, parts.ext)

	_, err := os.Stat(local)
	if err != nil {
		t.Errorf("stat %s: %v", local, err)
	}

	return file
}

func TestRelocateRenamesAFileToItsDeclaredBasename(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	local := outWriteFile(t, dir, outNameA, cltAlpha)

	file := relFile(local, outNameB)

	err := relTestCollector(dir).relocate(file)
	if err != nil {
		t.Fatalf("relocate: %v", err)
	}

	relWantPath(t, file, filepath.Join(dir, outNameB))

	// The bytes did not change, so nothing was re-measured.
	assertDeepEqual(t, "checksum", file.Checksum, "")

	_, err = os.Stat(local)
	if err == nil {
		t.Error("the file is still at its old name as well as its new one")
	}
}

func TestRelocateLeavesAMatchingValueAlone(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	local := outWriteFile(t, dir, outNameA, cltAlpha)

	// Nothing here disagrees with anything, and a value nobody asked to move must not be moved:
	// this pass exists to rename what a document renamed, not to copy an output directory.
	cases := map[string]*cwlcore.File{
		"basename already matches": relFile(local, outNameA),
		"no basename declared":     {Path: local},
		"no path at all":           {Basename: outNameB, Contents: cwlcore.NewOptString(cltAlpha)},
	}

	for name, file := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			before := *file

			err := relTestCollector(dir).relocate(file)
			if err != nil {
				t.Fatalf("relocate: %v", err)
			}

			assertDeepEqual(t, "value", *file, before)
		})
	}
}

func TestRelocateRenamesASecondaryFileInsideAStructure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	primary := outWriteFile(t, dir, outNameA, cltAlpha)
	secondary := outWriteFile(t, dir, outNameB, cltBeta)

	// rename-outputs.cwl in miniature: the parameter's own value is named correctly and the value
	// that declares a different name is one of its secondaries, reached through a record and a list.
	file := relFile(primary, outNameA)
	file.SecondaryFiles = []cwlcore.FileOrDirectory{relFile(secondary, outNameC)}

	value := map[string]any{outFieldLeft: []any{file}}

	err := relTestCollector(dir).relocate(value)
	if err != nil {
		t.Fatalf("relocate: %v", err)
	}

	relWantPath(t, file, primary)
	relWantPath(t, file.SecondaryFiles[0], filepath.Join(dir, outNameC))
}

func TestRelocateRenamesADirectoryAndRebasesItsListing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outNameTree+"/"+outNameSub+"/"+outNameA, cltAlpha)

	tree, err := outListDirectory(filepath.Join(dir, outNameTree), outDeepWalk, nil)
	if err != nil {
		t.Fatalf("outListDirectory: %v", err)
	}

	// A listing entry that names something this engine cannot move stays put; one inside the
	// directory moves with it.
	remote := &cwlcore.File{Location: "https://example.invalid/x", Basename: outNameX}

	// A typed nil is neither of the two things a listing can hold, and rebasing must step over it
	// rather than dereference it.
	tree.Listing = append(tree.Listing, remote, (*cwlcore.Directory)(nil))
	tree.Basename = outNameDeeper

	err = relTestCollector(dir).relocate(tree)
	if err != nil {
		t.Fatalf("relocate: %v", err)
	}

	moved := filepath.Join(dir, outNameDeeper)
	assertDeepEqual(t, "path", tree.Path, moved)
	assertDeepEqual(t, "location", tree.Location, outFileURI(moved))

	sub, ok := tree.Listing[0].(*cwlcore.Directory)
	if !ok {
		t.Fatalf("listing[0] = %#v, want a Directory", tree.Listing[0])
	}

	assertDeepEqual(t, "sub path", sub.Path, filepath.Join(moved, outNameSub))
	relWantPath(t, sub.Listing[0], filepath.Join(moved, outNameSub, outNameA))
	assertDeepEqual(t, "remote location", remote.Location, "https://example.invalid/x")
}

func TestRelocateCopiesAValueFromOutsideTheOutputDirectory(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	local := outWriteFile(t, source, outNameA, cltAlpha)

	dir := t.TempDir()

	// The run does not own a file outside its output directory, so it is copied in under the name
	// it declares rather than renamed out from under whoever else is using it.
	file := relFile(local, outNameB)

	err := relTestCollector(dir).relocate(file)
	if err != nil {
		t.Fatalf("relocate: %v", err)
	}

	relWantPath(t, file, filepath.Join(dir, outNameB))

	_, err = os.Stat(local)
	if err != nil {
		t.Errorf("the original was moved rather than copied: %v", err)
	}
}

func TestRelocateFailures(t *testing.T) {
	t.Parallel()

	missing := func(dir string) *cwlcore.File {
		return relFile(filepath.Join(dir, outMissingName), outNameB)
	}

	outside := func(_ string) *cwlcore.File {
		return relFile(filepath.Join(t.TempDir(), outMissingName), outNameB)
	}

	cases := map[string]func(dir string) any{
		"a file that is not there":         func(dir string) any { return missing(dir) },
		"a file outside that is not there": func(dir string) any { return outside(dir) },
		"a directory that is not there": func(dir string) any {
			return &cwlcore.Directory{Path: filepath.Join(dir, outMissingName), Basename: outNameB}
		},
		"inside a list":   func(dir string) any { return []any{missing(dir)} },
		"inside a record": func(dir string) any { return map[string]any{outFieldLeft: missing(dir)} },
		"inside a listing": func(dir string) any {
			return &cwlcore.Directory{Path: dir, Listing: []cwlcore.FileOrDirectory{missing(dir)}}
		},
	}

	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			assertErrorIs(t, "relocate", relTestCollector(dir).relocate(build(dir)), ErrOutputRename)
		})
	}
}

func TestRelocateIgnoresWhatCannotHoldAValue(t *testing.T) {
	t.Parallel()

	// A typed nil is neither a usable value nor an untyped nil, and a plain scalar carries nothing
	// to rename. None of them is an error.
	for _, value := range []any{(*cwlcore.File)(nil), (*cwlcore.Directory)(nil), cltAlpha, nil} {
		err := relTestCollector(t.TempDir()).relocate(value)
		if err != nil {
			t.Errorf("relocate(%#v) = %v, want nil", value, err)
		}
	}
}

func TestCollectOutputsReportsARenameItCannotPerform(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// The expression names a file that is not there under a name it does not have. Nothing can give
	// it that name, and publishing a value whose path and basename disagree is not an option.
	binding := &cwlcore.CommandOutputBinding{OutputEval: outRefSrc}
	tool := outTestTool(outTestParam("#tool/result", outTypeFile, binding))

	inputs := map[string]any{outNameSrc: &cwlcore.File{
		Path: filepath.Join(dir, outMissingName), Basename: outNameB,
	}}

	_, err := CollectOutputs(tool, dir, 0, inputs, cwlcore.NewEvaluator(),
		cwlcore.RuntimeContext{Outdir: dir})
	assertErrorIs(t, "CollectOutputs", err, ErrOutputRename)
}
