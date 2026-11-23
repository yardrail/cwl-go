package cwlexec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

var _ Invocation = (*localInvocation)(nil)

// localInvocation is the [Invocation] for tools running directly on this host.
type localInvocation struct {
	outdir  string
	tmpdir  string
	scratch string
	outfs   *LocalDirFS
	stgfs   *LocalDirFS
	tmpfs   *LocalDirFS
}

func newLocalInvocation(outDir, tmpDir string) (*localInvocation, error) {
	outdir, err := ensureDir(outDir, "cwl-out-")
	if err != nil {
		return nil, err
	}

	tmpdir, err := ensureDir(tmpDir, "cwl-tmp-")
	if err != nil {
		return nil, err
	}

	var scratch string
	if tmpDir == "" {
		scratch = tmpdir
	}

	return &localInvocation{
		outdir:  outdir,
		tmpdir:  tmpdir,
		scratch: scratch,
		outfs:   NewLocalDirFS(outdir),
		stgfs:   NewLocalDirFS(tmpdir),
		tmpfs:   NewLocalDirFS(tmpdir),
	}, nil
}

// ensureDir creates the given directory (or a temp directory if path is empty).
func ensureDir(path, prefix string) (string, error) {
	if path == "" {
		return os.MkdirTemp("", prefix)
	}

	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: %q", ErrInvocationDir, path)
	}

	return path, os.MkdirAll(path, stageDirPerm)
}

func (l *localInvocation) StageFS() WriteFS { return l.stgfs }
func (l *localInvocation) OutFS() WriteFS   { return l.outfs }
func (l *localInvocation) TmpFS() WriteFS   { return l.tmpfs }

func (l *localInvocation) Run(ctx context.Context, spec *ProcessSpec) (*ProcessResult, error) {
	code, err := RunProcess(ctx, spec)

	// Streams go to files per spec; no in-memory capture (CaptureLimit 0).
	return &ProcessResult{ExitCode: code}, err //nolint:exhaustruct_v5 // no capture fields for local exec
}

func (l *localInvocation) Close() error {
	if l.scratch == "" {
		return nil
	}

	return os.RemoveAll(l.scratch)
}

// OutDir returns the absolute host path for the output directory.
func (l *localInvocation) OutDir() string { return l.outdir }

// TmpDir returns the absolute host path for the temp directory.
func (l *localInvocation) TmpDir() string { return l.tmpdir }
