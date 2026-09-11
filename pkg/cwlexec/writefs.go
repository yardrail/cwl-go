package cwlexec

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteFS extends [fs.FS] with write operations for staging and output collection.
// Names follow [fs.FS] conventions: slash-separated, unrooted, no escaping "..".
type WriteFS interface {
	fs.FS
	Create(name string) (io.WriteCloser, error)
	MkdirAll(name string, perm fs.FileMode) error
	// Symlink creates newname as a symbolic link pointing at oldname.
	// oldname may be absolute (it is a symlink target, not an FS-relative name).
	Symlink(oldname, newname string) error
	Remove(name string) error
	Rename(oldname, newname string) error
}

// GlobFS is an optional [WriteFS] interface for glob pattern matching.
type GlobFS interface {
	Glob(pattern string) ([]string, error)
}

// SymlinkEvaluator is an optional [WriteFS] interface for resolving symlink chains.
type SymlinkEvaluator interface {
	EvalSymlinks(name string) (string, error)
}

// Interface compliance checks.
var (
	_ WriteFS          = (*LocalDirFS)(nil)
	_ fs.StatFS        = (*LocalDirFS)(nil)
	_ fs.ReadDirFS     = (*LocalDirFS)(nil)
	_ GlobFS           = (*LocalDirFS)(nil)
	_ SymlinkEvaluator = (*LocalDirFS)(nil)
)

// LocalDirFS is a [WriteFS] backed by a real directory on the local filesystem.
type LocalDirFS struct {
	root string
}

// NewLocalDirFS returns a [LocalDirFS] rooted at the given absolute directory path.
func NewLocalDirFS(root string) *LocalDirFS {
	return &LocalDirFS{root: root}
}

// Open implements [fs.FS].
func (d *LocalDirFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}

	return os.Open(d.hostPath(name))
}

// Create implements [WriteFS].
func (d *LocalDirFS) Create(name string) (io.WriteCloser, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "create", Path: name, Err: fs.ErrInvalid}
	}

	return os.Create(d.hostPath(name))
}

// MkdirAll implements [WriteFS].
func (d *LocalDirFS) MkdirAll(name string, perm fs.FileMode) error {
	if !fs.ValidPath(name) {
		return &fs.PathError{Op: "mkdir", Path: name, Err: fs.ErrInvalid}
	}

	return os.MkdirAll(d.hostPath(name), perm)
}

// Symlink implements [WriteFS].
func (d *LocalDirFS) Symlink(oldname, newname string) error {
	if !fs.ValidPath(newname) {
		return &fs.PathError{Op: "symlink", Path: newname, Err: fs.ErrInvalid}
	}

	return os.Symlink(oldname, d.hostPath(newname))
}

// Remove implements [WriteFS].
func (d *LocalDirFS) Remove(name string) error {
	if !fs.ValidPath(name) {
		return &fs.PathError{Op: "remove", Path: name, Err: fs.ErrInvalid}
	}

	return os.RemoveAll(d.hostPath(name))
}

// Rename implements [WriteFS].
func (d *LocalDirFS) Rename(oldname, newname string) error {
	if !fs.ValidPath(oldname) {
		return &fs.PathError{Op: "rename", Path: oldname, Err: fs.ErrInvalid}
	}

	if !fs.ValidPath(newname) {
		return &fs.PathError{Op: "rename", Path: newname, Err: fs.ErrInvalid}
	}

	return os.Rename(d.hostPath(oldname), d.hostPath(newname))
}

// Stat implements [fs.StatFS].
func (d *LocalDirFS) Stat(name string) (fs.FileInfo, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrInvalid}
	}

	return os.Stat(d.hostPath(name))
}

// ReadDir implements [fs.ReadDirFS].
func (d *LocalDirFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}

	return os.ReadDir(d.hostPath(name))
}

// Glob implements [GlobFS] by delegating to [filepath.Glob].
func (d *LocalDirFS) Glob(pattern string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(d.root, filepath.FromSlash(pattern)))
	if err != nil {
		return nil, err
	}

	rel := make([]string, 0, len(matches))

	for _, m := range matches {
		r, relErr := filepath.Rel(d.root, m)
		if relErr != nil {
			return nil, relErr
		}

		rel = append(rel, filepath.ToSlash(r))
	}

	return rel, nil
}

// EvalSymlinks implements [SymlinkEvaluator].
func (d *LocalDirFS) EvalSymlinks(name string) (string, error) {
	if !fs.ValidPath(name) {
		return "", &fs.PathError{Op: "evalSymlinks", Path: name, Err: fs.ErrInvalid}
	}

	resolved, err := filepath.EvalSymlinks(d.hostPath(name))
	if err != nil {
		return "", err
	}

	r, err := filepath.Rel(d.root, resolved)
	if err != nil {
		return "", err
	}

	return filepath.ToSlash(r), nil
}

// Root returns the absolute host path this FS wraps.
func (d *LocalDirFS) Root() string { return d.root }

func (d *LocalDirFS) hostPath(name string) string {
	return filepath.Join(d.root, filepath.FromSlash(name))
}
