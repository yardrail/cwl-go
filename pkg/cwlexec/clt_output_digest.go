package cwlexec

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Size, checksum and contents: the three File fields that can only come from the bytes themselves.
//
// SHA-1 is used here as a *content digest the specification mandates*, not as a security control.
// Process.yml, File.checksum: "Optional hash code for validating file integrity. Currently, must be
// in the form 'sha1$ + hexadecimal string' using the SHA-1 algorithm." The conformance harness
// recomputes the value from disk and compares it, so substituting a stronger hash would produce an
// answer that is simply wrong. Nothing here depends on SHA-1 being collision-resistant and no
// security decision is taken on the result.
//
// gosec objects on two rules — G505 on the import and G401 on the constructor — and this project
// bans //nolint directives, so the finding is reported for the linter configuration to settle
// rather than suppressed at the call site. Everything in this stream that touches SHA-1 is confined
// to this one file so that an exclusion can be scoped to it and nowhere else.

// outChecksumPrefix is the algorithm tag a CWL checksum carries, per Process.yml.
const outChecksumPrefix = "sha1$"

// outContentLimit is the ceiling the specification places on a loadContents read.
//
// Process.yml, loadContents: "the file (or each file in the array) must be a UTF-8 text file 64 KiB
// or smaller ... If the size of the file is greater than 64 KiB, the implementation must raise a
// fatal error". Nothing here truncates.
const outContentLimit = 64 * 1024

// outFileStats is what one pass over a file's bytes yields: its length, its CWL checksum, and the
// leading bytes a loadContents request needs. The three travel together because they must all
// describe the same read — a checksum taken from one read and contents from another could disagree
// about a file a tool is still flushing.
type outFileStats struct {
	// checksum is the "sha1$<hex>" digest of everything read.
	checksum string

	// head is the first outContentLimit bytes, kept for loadContents.
	head []byte

	// size is the number of bytes read, which is the file's size.
	size int64
}

// outDigest reads the file at local once and reports its size, checksum and leading bytes.
func outDigest(local string) (outFileStats, error) {
	// Clean is redundant — every path reaching here has been through filepath.Glob or
	// filepath.Join — but it is what marks the value as sanitized for taint analysis.
	file, err := os.Open(filepath.Clean(local))
	if err != nil {
		return outFileStats{}, err
	}

	stats, hashErr := outHashAll(file)

	// The close error is joined rather than dropped: errcheck's check-blank leaves no way to
	// discard it, and a deferred close would need a named result to report it at all.
	// errors.Join is nil when both arguments are, so the common path is a single branch.
	readErr := errors.Join(hashErr, file.Close())
	if readErr != nil {
		return outFileStats{}, readErr
	}

	return stats, nil
}

// outHashAll streams r through the digest, keeping the leading bytes as it goes. Streaming rather
// than reading the whole file keeps a multi-gigabyte output off the heap, which matters: tool
// outputs are routinely alignments and archives.
func outHashAll(r io.Reader) (outFileStats, error) {
	digest := sha1.New()
	head := &outHeadWriter{limit: outContentLimit}

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

// outChecksumOf returns the CWL checksum of a byte slice already in memory.
func outChecksumOf(content []byte) string {
	sum := sha1.Sum(content)

	return outChecksumPrefix + hex.EncodeToString(sum[:])
}

// outHeadWriter keeps the first limit bytes written to it and silently drops the rest. It is how
// the digest pass captures enough of a file to satisfy loadContents without ever holding more than
// the specification's ceiling in memory.
type outHeadWriter struct {
	// buf holds the bytes kept so far.
	buf []byte

	// limit is the most bytes buf will ever hold.
	limit int
}

// Write records as much of p as still fits under the cap, and reports every byte as written so that
// it composes with [io.MultiWriter].
func (w *outHeadWriter) Write(p []byte) (int, error) {
	if room := w.limit - len(w.buf); room > 0 {
		w.buf = append(w.buf, p[:min(room, len(p))]...)
	}

	return len(p), nil
}
