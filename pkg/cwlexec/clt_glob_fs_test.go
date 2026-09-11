package cwlexec

import (
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

func TestOutDigestFSMatchesHostDigest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := outWriteFile(t, dir, "data.txt", "consistent across paths")

	hostStats, err := outDigest(path)
	if err != nil {
		t.Fatal(err)
	}

	fsStats, err := outDigestFS(NewLocalDirFS(dir), "data.txt")
	if err != nil {
		t.Fatal(err)
	}

	if hostStats.checksum != fsStats.checksum {
		t.Errorf("checksum mismatch: host=%q fs=%q", hostStats.checksum, fsStats.checksum)
	}

	if hostStats.size != fsStats.size {
		t.Errorf("size mismatch: host=%d fs=%d", hostStats.size, fsStats.size)
	}
}

func TestOutCollectFileFS(t *testing.T) {
	t.Parallel()

	content := []byte("file content here")
	mfs := mapWriteFS{fstest.MapFS{
		"result.txt": &fstest.MapFile{Data: content},
	}}

	binding := &cwlcore.CommandOutputBinding{
		OutputEval: "", LoadListing: "", Glob: nil, LoadContents: false,
	}

	file, err := outCollectFile("/fake/out/result.txt", binding, mfs, "/fake/out")
	if err != nil {
		t.Fatal(err)
	}

	if file.Basename != "result.txt" {
		t.Errorf("basename = %q, want result.txt", file.Basename)
	}

	if file.Size.Int() != int64(len(content)) {
		t.Errorf("size = %d, want %d", file.Size.Int(), len(content))
	}

	wantChecksum := outChecksumOf(content)
	if file.Checksum != wantChecksum {
		t.Errorf("checksum = %q, want %q", file.Checksum, wantChecksum)
	}
}

func TestOutListDirectoryFS(t *testing.T) {
	t.Parallel()

	mfs := mapWriteFS{fstest.MapFS{
		"sub/a.txt": &fstest.MapFile{Data: []byte("aaa")},
		"sub/b.txt": &fstest.MapFile{Data: []byte("bb")},
	}}

	dir, err := outListDirectory("/fake/out/sub", outShallowWalk, nil, mfs, "/fake/out")
	if err != nil {
		t.Fatal(err)
	}

	if len(dir.Listing) != 2 {
		t.Fatalf("listing has %d entries, want 2", len(dir.Listing))
	}
}

func TestOutAlreadyWalkedPathBased(t *testing.T) {
	t.Parallel()

	walked := []string{"sub/dir1", "sub/dir2"}

	if !outAlreadyWalked("sub/dir1", walked) {
		t.Error("should detect already-walked path")
	}

	if outAlreadyWalked("sub/dir3", walked) {
		t.Error("should not flag unwatched path")
	}
}

func TestGlobMatchesMapFS(t *testing.T) {
	t.Parallel()

	mfs := mapWriteFS{fstest.MapFS{
		"a.txt":     &fstest.MapFile{Data: []byte("a")},
		"b.txt":     &fstest.MapFile{Data: []byte("b")},
		"sub/c.txt": &fstest.MapFile{Data: []byte("c")},
	}}

	collector := newOutputCollector(outTestTool(), "/fake/out", mfs, nil)

	matches, err := collector.globMatches("*.txt")
	if err != nil {
		t.Fatal(err)
	}

	if len(matches) != 2 {
		t.Fatalf("got %d matches, want 2", len(matches))
	}

	wantA := filepath.Join("/fake/out", "a.txt")
	wantB := filepath.Join("/fake/out", "b.txt")

	if matches[0] != wantA || matches[1] != wantB {
		t.Errorf("matches = %v, want [%s %s]", matches, wantA, wantB)
	}
}

func TestCheckRetrievableNoSymlinkEvaluator(t *testing.T) {
	t.Parallel()

	mfs := mapWriteFS{fstest.MapFS{
		"result.txt": &fstest.MapFile{Data: []byte("ok")},
	}}

	collector := newOutputCollector(outTestTool(), "/fake/out", mfs, nil)

	err := collector.checkRetrievable("/fake/out/result.txt")
	if err != nil {
		t.Errorf("path inside outdir should be retrievable: %v", err)
	}
}

func TestOutFillListingsMapFS(t *testing.T) {
	t.Parallel()

	mfs := mapWriteFS{fstest.MapFS{
		"sub/file.txt": &fstest.MapFile{Data: []byte("data")},
	}}

	dir := &cwlcore.Directory{
		Node:     nil,
		Location: "file:///fake/out/sub",
		Path:     "/fake/out/sub",
		Basename: "sub",
		Listing:  nil,
	}

	outFillListings(dir, mfs, "/fake/out")

	if dir.Listing == nil {
		t.Fatal("listing should have been filled")
	}

	if len(dir.Listing) != 1 {
		t.Fatalf("listing has %d entries, want 1", len(dir.Listing))
	}
}

func TestOutFillListingsLocalUsesHostFS(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	outWriteFile(t, tmpDir, "hello.txt", "hi")

	dir := &cwlcore.Directory{
		Node:     nil,
		Location: outFileURI(tmpDir),
		Path:     tmpDir,
		Basename: filepath.Base(tmpDir),
		Listing:  nil,
	}

	outFillListingsLocal(dir)

	if dir.Listing == nil {
		t.Fatal("listing should have been filled via host FS")
	}

	if len(dir.Listing) != 1 {
		t.Fatalf("listing has %d entries, want 1", len(dir.Listing))
	}
}

func TestCollectOutputsMapFS(t *testing.T) {
	t.Parallel()

	content := []byte("hello from mapfs")

	mfs := mapWriteFS{fstest.MapFS{
		"result.txt": &fstest.MapFile{Data: content},
	}}

	tool := outTestTool(outTestParam("out",
		cwlcore.NewPrimitiveType(cwlcore.PrimitiveFile),
		outGlobBinding("result.txt")))

	outputs, err := CollectOutputs(tool, "/virtual/out", mfs, 0, nil,
		cwlcore.NewEvaluator(), cwlcore.RuntimeContext{
			Cores:      nil,
			RAM:        nil,
			OutdirSize: nil,
			TmpdirSize: nil,
			ExitCode:   nil,
			Outdir:     "/virtual/out",
			Tmpdir:     "/virtual/tmp",
		})
	if err != nil {
		t.Fatal(err)
	}

	wantChecksum := outChecksumOf(content)
	file := outWantFile(t, outputs, "out", "result.txt", wantChecksum, int64(len(content)))

	if file.Path != "/virtual/out/result.txt" {
		t.Errorf("path = %q, want /virtual/out/result.txt", file.Path)
	}
}
