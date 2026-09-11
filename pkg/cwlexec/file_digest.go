package cwlexec

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// File size, checksum (sha1, per CWL spec) and leading-bytes capture.

// outChecksumPrefix is the CWL checksum algorithm tag.
const outChecksumPrefix = "sha1$"

// outFileStats holds size, checksum and leading bytes from a single pass over a file.
type outFileStats struct {
	checksum string // "sha1$<hex>"
	head     []byte // first joMaxContentsBytes bytes
	size     int64
}

// outDigest reads the file and returns its size, checksum and leading bytes.
func outDigest(local string) (outFileStats, error) {
	file, err := os.Open(filepath.Clean(local))
	if err != nil {
		return outFileStats{}, err
	}

	stats, hashErr := outHashAll(file)

	readErr := errors.Join(hashErr, file.Close())
	if readErr != nil {
		return outFileStats{}, readErr
	}

	return stats, nil
}

// outDigestFS reads the file from an [fs.FS] and returns its size, checksum and leading bytes.
func outDigestFS(fsys fs.FS, name string) (outFileStats, error) {
	file, err := fsys.Open(name)
	if err != nil {
		return outFileStats{}, err
	}

	stats, hashErr := outHashAll(file)

	readErr := errors.Join(hashErr, file.Close())
	if readErr != nil {
		return outFileStats{}, readErr
	}

	return stats, nil
}

// outHashAll streams r through the SHA-1 digest, capturing leading bytes.
func outHashAll(r io.Reader) (outFileStats, error) {
	digest := sha1.New()
	head := &outHeadWriter{buf: nil, limit: joMaxContentsBytes}

	size, err := io.Copy(io.MultiWriter(digest, head), r)
	if err != nil {
		return outFileStats{}, err
	}

	return outFileStats{
		checksum: outChecksumPrefix + hex.EncodeToString(digest.Sum(nil)),
		head:     head.buf,
		size:     size,
	}, nil
}

// outChecksumOf returns the CWL checksum of an in-memory byte slice.
func outChecksumOf(content []byte) string {
	sum := sha1.Sum(content)

	return outChecksumPrefix + hex.EncodeToString(sum[:])
}

// outMeasureLiteral sets size and checksum on a file literal from its contents.
func outMeasureLiteral(file *cwlcore.File) {
	if !file.Contents.IsSet() {
		return
	}

	content := []byte(file.Contents.Value())
	file.Size = cwlcore.NewOptInt(int64(len(content)))
	file.Checksum = outChecksumOf(content)
}

// outHeadWriter keeps the first limit bytes written to it and drops the rest.
type outHeadWriter struct {
	buf   []byte
	limit int
}

// Write keeps up to limit bytes and reports all of p as written.
func (w *outHeadWriter) Write(p []byte) (int, error) {
	if room := w.limit - len(w.buf); room > 0 {
		w.buf = append(w.buf, p[:min(room, len(p))]...)
	}

	return len(p), nil
}
