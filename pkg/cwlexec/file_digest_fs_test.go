package cwlexec

import (
	"bytes"
	"testing"
	"testing/fstest"
)

func TestOutDigestFS(t *testing.T) {
	t.Parallel()

	content := []byte("hello world")
	mfs := fstest.MapFS{
		"output.txt": &fstest.MapFile{Data: content},
	}

	stats, err := outDigestFS(mfs, "output.txt")
	if err != nil {
		t.Fatal(err)
	}

	if stats.size != int64(len(content)) {
		t.Errorf("size = %d, want %d", stats.size, len(content))
	}

	wantChecksum := outChecksumOf(content)
	if stats.checksum != wantChecksum {
		t.Errorf("checksum = %q, want %q", stats.checksum, wantChecksum)
	}

	if !bytes.Equal(stats.head, content) {
		t.Errorf("head = %q, want %q", stats.head, content)
	}
}

func TestOutDigestFSNotFound(t *testing.T) {
	t.Parallel()

	mfs := fstest.MapFS{}

	_, err := outDigestFS(mfs, "missing.txt")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
