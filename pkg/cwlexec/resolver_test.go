package cwlexec

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

const resolverTestFile = "data.txt"

func TestLocalOutputResolverResolvesAbsolutePath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := outWriteFile(t, dir, resolverTestFile, "hello")

	var r localOutputResolver

	fsys, rel, err := r.ResolveOutputFS(path)
	if err != nil {
		t.Fatal(err)
	}

	if rel != resolverTestFile {
		t.Errorf("rel = %q, want %q", rel, resolverTestFile)
	}

	f, err := fsys.Open(rel)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	content, readErr := io.ReadAll(f)
	if readErr != nil {
		t.Fatal(readErr)
	}

	if string(content) != "hello" {
		t.Errorf("content = %q, want %q", content, "hello")
	}
}

// mockRewriterInvocation is a minimal Invocation that also implements OutputRewriter.
type mockRewriterInvocation struct {
	stageFS WriteFS
	outFS   WriteFS
	tmpFS   WriteFS
	prefix  string
}

func (m *mockRewriterInvocation) StageFS() WriteFS { return m.stageFS }

func (m *mockRewriterInvocation) OutFS() WriteFS { return m.outFS }

func (m *mockRewriterInvocation) TmpFS() WriteFS                                   { return m.tmpFS }
func (*mockRewriterInvocation) Run(_ context.Context, _ *ProcessSpec) (int, error) { return 0, nil }
func (*mockRewriterInvocation) Close() error                                       { return nil }

func (m *mockRewriterInvocation) RewriteOutputPaths(outputs map[string]any) map[string]any {
	rewritten := make(map[string]any, len(outputs))

	for k, v := range outputs {
		file, ok := v.(*cwlcore.File)
		if !ok {
			rewritten[k] = v

			continue
		}

		clone := *file
		clone.Path = m.prefix + "/" + filepath.Base(file.Path)
		rewritten[k] = &clone
	}

	return rewritten
}

func TestOutputRewriterTypeAssertion(t *testing.T) {
	t.Parallel()

	var inv Invocation = &mockRewriterInvocation{prefix: "s3://bucket/runs/1/out"}

	rw, ok := inv.(OutputRewriter)
	if !ok {
		t.Fatal("type assertion to OutputRewriter failed")
	}

	outputs := map[string]any{
		"result": &cwlcore.File{Path: "/tmp/cwl-out-123/result.mp4"},
	}

	rewritten := rw.RewriteOutputPaths(outputs)

	file, ok := rewritten["result"].(*cwlcore.File)
	if !ok {
		t.Fatal("rewritten result is not a *cwlcore.File")
	}

	if file.Path != "s3://bucket/runs/1/out/result.mp4" {
		t.Errorf("rewritten path = %q, want s3 path", file.Path)
	}
}

// mockCopyResolver implements OutputResolver and optionally CopyOptimizer.
type mockCopyResolver struct {
	fsys      fs.FS
	canCopy   bool
	copyCount int
}

func (m *mockCopyResolver) ResolveOutputFS(path string) (fs.FS, string, error) {
	return m.fsys, filepath.Base(path), nil
}

func (m *mockCopyResolver) CopyDirect(srcPath string, dest WriteFS, destName string) (bool, error) {
	if !m.canCopy {
		return false, nil
	}

	m.copyCount++

	fsys, name, resolveErr := m.ResolveOutputFS(srcPath)
	if resolveErr != nil {
		return false, resolveErr
	}

	src, openErr := fsys.Open(name)
	if openErr != nil {
		return false, openErr
	}
	defer src.Close()

	w, createErr := dest.Create(destName)
	if createErr != nil {
		return false, createErr
	}

	_, copyErr := io.Copy(w, src)

	return true, errors.Join(copyErr, w.Close())
}

func TestCopyOptimizerFastPath(t *testing.T) {
	t.Parallel()

	srcFS := fstest.MapFS{
		resolverTestFile: &fstest.MapFile{Data: []byte("big file contents")},
	}
	resolver := &mockCopyResolver{fsys: srcFS, canCopy: true}

	dst := NewLocalDirFS(t.TempDir())
	mapper := NewPathMap(t.TempDir(), t.TempDir())
	mapper.resolver = resolver

	err := mapper.copyFileFS("/fake/"+resolverTestFile, dst, "copied.txt")
	if err != nil {
		t.Fatal(err)
	}

	if resolver.copyCount != 1 {
		t.Errorf("CopyDirect called %d times, want 1", resolver.copyCount)
	}

	got := execRead(t, filepath.Join(dst.Root(), "copied.txt"))
	if got != "big file contents" {
		t.Errorf("content = %q, want %q", got, "big file contents")
	}
}

func TestCopyOptimizerFallback(t *testing.T) {
	t.Parallel()

	srcFS := fstest.MapFS{
		resolverTestFile: &fstest.MapFile{Data: []byte("streamed content")},
	}
	resolver := &mockCopyResolver{fsys: srcFS, canCopy: false}

	dst := NewLocalDirFS(t.TempDir())
	mapper := NewPathMap(t.TempDir(), t.TempDir())
	mapper.resolver = resolver

	err := mapper.copyFileFS("/fake/"+resolverTestFile, dst, "copied.txt")
	if err != nil {
		t.Fatal(err)
	}

	if resolver.copyCount != 0 {
		t.Errorf("CopyDirect called %d times, want 0 (should have fallen back)", resolver.copyCount)
	}

	got := execRead(t, filepath.Join(dst.Root(), "copied.txt"))
	if got != "streamed content" {
		t.Errorf("content = %q, want %q", got, "streamed content")
	}
}

func TestNilResolverFallsBackToOS(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := outWriteFile(t, dir, "source.txt", "local content")

	dst := NewLocalDirFS(t.TempDir())
	mapper := NewPathMap(dir, dir)

	err := mapper.copyFileFS(source, dst, "copied.txt")
	if err != nil {
		t.Fatal(err)
	}

	got := execRead(t, filepath.Join(dst.Root(), "copied.txt"))
	if got != "local content" {
		t.Errorf("content = %q, want %q", got, "local content")
	}
}

func TestCopyToFSWithResolver(t *testing.T) {
	t.Parallel()

	srcFS := fstest.MapFS{
		"virtual.txt": &fstest.MapFile{Data: []byte("from resolver")},
	}

	resolver := &mockCopyResolver{fsys: srcFS, canCopy: false}
	dst := NewLocalDirFS(t.TempDir())
	mapper := NewPathMap(t.TempDir(), t.TempDir())
	mapper.resolver = resolver

	err := mapper.copyToFS("/nonexistent/virtual.txt", dst, "staged.txt")
	if err != nil {
		t.Fatal(err)
	}

	got := execRead(t, filepath.Join(dst.Root(), "staged.txt"))
	if got != "from resolver" {
		t.Errorf("content = %q, want %q", got, "from resolver")
	}
}

func TestCopyTreeFSWithResolver(t *testing.T) {
	t.Parallel()

	srcFS := fstest.MapFS{
		outNameA:    &fstest.MapFile{Data: []byte("aaa")},
		"sub":       &fstest.MapFile{Mode: fs.ModeDir},
		"sub/b.txt": &fstest.MapFile{Data: []byte("bbb")},
	}

	resolver := &prefixResolver{prefix: "/virtual/root", fsys: srcFS}
	dst := NewLocalDirFS(t.TempDir())
	mapper := NewPathMap(t.TempDir(), t.TempDir())
	mapper.resolver = resolver

	err := mapper.copyTreeFS("/virtual/root", dst, "copied")
	if err != nil {
		t.Fatal(err)
	}

	if got := execRead(t, filepath.Join(dst.Root(), "copied", outNameA)); got != "aaa" {
		t.Errorf("a.txt = %q, want %q", got, "aaa")
	}

	if got := execRead(t, filepath.Join(dst.Root(), "copied", "sub", "b.txt")); got != "bbb" {
		t.Errorf("sub/b.txt = %q, want %q", got, "bbb")
	}
}

// prefixResolver strips a known prefix from paths and resolves the remainder
// against an [fstest.MapFS]. Mimics an S3-backed resolver with a key prefix.
type prefixResolver struct {
	prefix string
	fsys   fstest.MapFS
}

func (r *prefixResolver) ResolveOutputFS(path string) (fs.FS, string, error) {
	rel := filepath.ToSlash(path)
	prefix := filepath.ToSlash(r.prefix)

	if rel == prefix {
		return r.fsys, ".", nil
	}

	if len(rel) > len(prefix) && rel[:len(prefix)] == prefix && rel[len(prefix)] == '/' {
		rel = rel[len(prefix)+1:]
	}

	return r.fsys, rel, nil
}

func TestMountPointFSWithResolver(t *testing.T) {
	t.Parallel()

	srcFS := fstest.MapFS{
		"file.txt": &fstest.MapFile{Data: []byte("content")},
		jobDirName: &fstest.MapFile{Mode: fs.ModeDir},
	}

	resolver := &mockCopyResolver{fsys: srcFS, canCopy: false}
	mapper := NewPathMap(t.TempDir(), t.TempDir())
	mapper.resolver = resolver

	dir := t.TempDir()
	dst := NewLocalDirFS(dir)

	err := mapper.mountPointFS("/fake/file.txt", dst, "mount-file")
	if err != nil {
		t.Fatal(err)
	}

	err = mapper.mountPointFS("/fake/"+jobDirName, dst, "mount-dir")
	if err != nil {
		t.Fatal(err)
	}

	info, err := fs.Stat(dst, "mount-file")
	if err != nil {
		t.Fatal(err)
	}

	if info.IsDir() {
		t.Error("mount point for file should not be a directory")
	}

	if info.Size() != 0 {
		t.Errorf("mount point for file should be empty, got %d bytes", info.Size())
	}

	info, err = fs.Stat(dst, "mount-dir")
	if err != nil {
		t.Fatal(err)
	}

	if !info.IsDir() {
		t.Error("mount point for directory should be a directory")
	}
}
