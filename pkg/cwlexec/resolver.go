package cwlexec

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// OutputRewriter is optionally implemented by an [Invocation] that needs to
// transform output paths after collection (e.g. from temp paths to S3 keys).
// cwl-go discovers it via type assertion after output collection.
type OutputRewriter interface {
	RewriteOutputPaths(outputs map[string]any) map[string]any
}

// OutputResolver returns a readable filesystem for a path found in a prior
// step's output object. The returned [fs.FS] is rooted at a directory containing
// the file; rel is the path within that FS.
//
// Set via [Config.OutputResolver]; defaults to local filesystem.
type OutputResolver interface {
	ResolveOutputFS(path string) (fsys fs.FS, rel string, err error)

	// ResolveOutputDirFS returns a readable [fs.FS] rooted at the directory
	// identified by path. The caller walks this FS directly rather than
	// constructing child paths — this avoids filepath.Join mangling URIs.
	ResolveOutputDirFS(path string) (fs.FS, error)
}

// ErrNotDirectory reports a path that is not a directory or whose resolver
// does not support directory resolution.
var ErrNotDirectory = errors.New("path is not a directory")

// CopyOptimizer is optionally implemented by an [OutputResolver] that can copy
// files directly between storage locations without streaming bytes through the
// process (e.g. S3 server-side CopyObject).
type CopyOptimizer interface {
	// CopyDirect copies the file at srcPath into dest at destName. Returns true
	// if the optimization was applied. Returns false if source and destination
	// are on different backends — the caller falls back to read-then-write.
	CopyDirect(srcPath string, dest WriteFS, destName string) (bool, error)
}

// localOutputResolver resolves paths via the local filesystem.
type localOutputResolver struct{}

func (localOutputResolver) ResolveOutputFS(path string) (fs.FS, string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, "", err
	}

	return os.DirFS(filepath.Dir(abs)), filepath.Base(abs), nil
}

func (localOutputResolver) ResolveOutputDirFS(path string) (fs.FS, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %s", ErrNotDirectory, path)
	}

	return os.DirFS(abs), nil
}
