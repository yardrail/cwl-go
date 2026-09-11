package cwlexec

import (
	"errors"
	"io"
	"io/fs"
	"testing/fstest"
)

// mapWriteFS wraps fstest.MapFS to satisfy WriteFS for read-only test usage.
type mapWriteFS struct {
	fstest.MapFS
}

var errReadOnly = errors.New("read-only test FS")

func (m mapWriteFS) Create(string) (io.WriteCloser, error) { return nil, errReadOnly }
func (m mapWriteFS) MkdirAll(string, fs.FileMode) error    { return errReadOnly }
func (m mapWriteFS) Symlink(string, string) error           { return errReadOnly }
func (m mapWriteFS) Remove(string) error                    { return errReadOnly }
func (m mapWriteFS) Rename(string, string) error            { return errReadOnly }
