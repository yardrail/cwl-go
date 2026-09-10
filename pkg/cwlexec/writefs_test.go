package cwlexec

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"
)

func TestLocalDirFSCreate(t *testing.T) {
	t.Parallel()

	fsys := NewLocalDirFS(t.TempDir())

	w, err := fsys.Create("hello.txt")
	if err != nil {
		t.Fatal(err)
	}

	_, err = w.Write([]byte("content"))
	if err != nil {
		t.Fatal(err)
	}

	err = w.Close()
	if err != nil {
		t.Fatal(err)
	}

	got, err := fs.ReadFile(fsys, "hello.txt")
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != "content" {
		t.Fatalf("got %q, want %q", got, "content")
	}
}

func TestLocalDirFSCreateNestedPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fsys := NewLocalDirFS(dir)

	err := fsys.MkdirAll("sub/dir", 0o755)
	if err != nil {
		t.Fatal(err)
	}

	w, err := fsys.Create("sub/dir/file.txt")
	if err != nil {
		t.Fatal(err)
	}

	err = w.Close()
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(filepath.Join(dir, "sub", "dir", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
}

func TestLocalDirFSMkdirAll(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fsys := NewLocalDirFS(dir)

	err := fsys.MkdirAll("a/b/c", 0o755)
	if err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(dir, "a", "b", "c"))
	if err != nil {
		t.Fatal(err)
	}

	if !info.IsDir() {
		t.Fatal("expected directory")
	}
}

func TestLocalDirFSSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fsys := NewLocalDirFS(dir)

	target := filepath.Join(dir, "target.txt")

	err := os.WriteFile(target, []byte("linked"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = fsys.Symlink(target, "link.txt")
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "link.txt"))
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != "linked" {
		t.Fatalf("got %q, want %q", got, "linked")
	}
}

func TestLocalDirFSRemove(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fsys := NewLocalDirFS(dir)

	err := os.WriteFile(filepath.Join(dir, "doomed.txt"), []byte("bye"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = fsys.Remove("doomed.txt")
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(filepath.Join(dir, "doomed.txt"))
	if !os.IsNotExist(err) {
		t.Fatalf("expected file to be removed, got err=%v", err)
	}
}

func TestLocalDirFSRemoveDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fsys := NewLocalDirFS(dir)

	err := os.MkdirAll(filepath.Join(dir, "tree", "child"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(dir, "tree", "child", "f.txt"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = fsys.Remove("tree")
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(filepath.Join(dir, "tree"))
	if !os.IsNotExist(err) {
		t.Fatalf("expected directory to be removed, got err=%v", err)
	}
}

func TestLocalDirFSRename(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fsys := NewLocalDirFS(dir)

	err := os.WriteFile(filepath.Join(dir, "old.txt"), []byte("data"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = fsys.Rename("old.txt", "new.txt")
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(filepath.Join(dir, "old.txt"))
	if !os.IsNotExist(err) {
		t.Fatalf("old file should not exist, got err=%v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "new.txt"))
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != "data" {
		t.Fatalf("got %q, want %q", got, "data")
	}
}

func TestLocalDirFSOpen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fsys := NewLocalDirFS(dir)

	err := os.WriteFile(filepath.Join(dir, "read.txt"), []byte(execGreeting), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	f, err := fsys.Open("read.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != execGreeting {
		t.Fatalf("got %q, want %q", got, execGreeting)
	}
}

func TestLocalDirFSStat(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fsys := NewLocalDirFS(dir)

	err := os.WriteFile(filepath.Join(dir, "sized.txt"), []byte("12345"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	info, err := fsys.Stat("sized.txt")
	if err != nil {
		t.Fatal(err)
	}

	if info.Size() != 5 {
		t.Fatalf("got size %d, want 5", info.Size())
	}
}

func TestLocalDirFSReadDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fsys := NewLocalDirFS(dir)

	err := os.WriteFile(filepath.Join(dir, "a.txt"), nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(dir, "b.txt"), nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	entries, err := fsys.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	if !slices.Contains(names, "a.txt") || !slices.Contains(names, "b.txt") {
		t.Fatalf("expected a.txt and b.txt in %v", names)
	}
}

func TestLocalDirFSGlob(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fsys := NewLocalDirFS(dir)

	for _, name := range []string{"one.txt", "two.txt", "three.dat"} {
		err := os.WriteFile(filepath.Join(dir, name), nil, 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	matches, err := fsys.Glob("*.txt")
	if err != nil {
		t.Fatal(err)
	}

	if len(matches) != 2 {
		t.Fatalf("got %d matches, want 2: %v", len(matches), matches)
	}

	if !slices.Contains(matches, "one.txt") || !slices.Contains(matches, "two.txt") {
		t.Fatalf("expected one.txt and two.txt in %v", matches)
	}
}

func TestLocalDirFSEvalSymlinks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fsys := NewLocalDirFS(dir)

	err := os.WriteFile(filepath.Join(dir, "real.txt"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink(filepath.Join(dir, "real.txt"), filepath.Join(dir, "sym.txt"))
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := fsys.EvalSymlinks("sym.txt")
	if err != nil {
		t.Fatal(err)
	}

	if resolved != "real.txt" {
		t.Fatalf("got %q, want %q", resolved, "real.txt")
	}
}

func TestLocalDirFSRejectsAbsolutePaths(t *testing.T) {
	t.Parallel()

	fsys := NewLocalDirFS(t.TempDir())

	for _, tc := range []struct {
		name string
		fn   func() error
	}{
		{"Open", func() error {
			_, err := fsys.Open("/absolute")

			return err
		}},
		{"Create", func() error {
			_, err := fsys.Create("/absolute")

			return err
		}},
		{"MkdirAll", func() error { return fsys.MkdirAll("/absolute", 0o755) }},
		{"Symlink", func() error { return fsys.Symlink("target", "/absolute") }},
		{"Remove", func() error { return fsys.Remove("/absolute") }},
		{"Rename old", func() error { return fsys.Rename("/absolute", "ok") }},
		{"Rename new", func() error { return fsys.Rename("ok", "/absolute") }},
		{"Stat", func() error {
			_, err := fsys.Stat("/absolute")

			return err
		}},
		{"ReadDir", func() error {
			_, err := fsys.ReadDir("/absolute")

			return err
		}},
		{"EvalSymlinks", func() error {
			_, err := fsys.EvalSymlinks("/absolute")

			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.fn()
			if !errors.Is(err, fs.ErrInvalid) {
				t.Fatalf("got %v, want fs.ErrInvalid", err)
			}
		})
	}
}

func TestLocalDirFSRejectsDotDotEscape(t *testing.T) {
	t.Parallel()

	fsys := NewLocalDirFS(t.TempDir())

	_, err := fsys.Open("../escape")
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Open: got %v, want fs.ErrInvalid", err)
	}

	_, err = fsys.Create("../escape")
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Create: got %v, want fs.ErrInvalid", err)
	}

	err = fsys.MkdirAll("../escape", 0o755)
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("MkdirAll: got %v, want fs.ErrInvalid", err)
	}
}

func TestLocalDirFSCompliance(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "file1.txt"), []byte("one"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(dir, "sub", "file2.txt"), []byte("two"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	fsys := NewLocalDirFS(dir)

	err = fstest.TestFS(fsys, "file1.txt", "sub/file2.txt")
	if err != nil {
		t.Fatal(err)
	}
}

func TestLocalDirFSRoot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fsys := NewLocalDirFS(dir)

	if fsys.Root() != dir {
		t.Fatalf("got %q, want %q", fsys.Root(), dir)
	}
}
