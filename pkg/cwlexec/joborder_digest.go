package cwlexec

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// Size and checksum: the two File fields that can only come from the bytes themselves.
//
// SHA-1 is used here as a content digest mandated by the specification, not as a security
// control. Process.yml, File.checksum: "Optional hash code for validating file integrity.
// Currently, must be in the form 'sha1$ + hexadecimal string' using the SHA-1 algorithm". The
// value is compared against a fixture by the conformance harness, which recomputes it from disk,
// so substituting a stronger hash would produce a value that is simply wrong. Nothing here
// relies on SHA-1 being collision-resistant and no security decision is taken on the result.
//
// gosec objects to this on two rules — G505 on the import and G401 on each constructor — and
// this project bans //nolint directives, so the finding is reported for the linter configuration
// to settle rather than suppressed at the call site. Everything that touches SHA-1 is confined
// to this file so that the exclusion can be scoped to it and nowhere else.

// joChecksumPrefix is the algorithm tag a CWL checksum carries, per Process.yml.
const joChecksumPrefix = "sha1$"

// joFileStats is what one pass over a file's bytes yields: its length, its CWL checksum, and the
// leading bytes a loadContents request needs. The three travel together because they must all
// describe the same read.
type joFileStats struct {
	checksum string
	head     []byte
	size     int64
}

// joMeasure fills in a File's size and checksum, and its contents when the parameter asked for
// them.
//
// A File with a local path is measured from disk, which is the only source the specification
// allows: "The `size` property is the size in bytes of the File. It must be computed from the
// resource and made available to expressions". A size or checksum written by hand in a job file
// is therefore replaced rather than trusted.
func joMeasure(file *cwlcore.File, m *salad.MapNode, v *joValueCtx) *salad.Error {
	if file.Path == "" {
		joMeasureLiteral(file)

		return nil
	}

	stats, err := joDigest(file.Path)
	if err != nil {
		return salad.Errorf(m.Loc(), "%s: cannot read file: %v", v.path, err)
	}

	file.Size = cwlcore.NewOptInt(stats.size)
	file.Checksum = stats.checksum

	return joLoadContents(file, m, v, &stats)
}

// joMeasureLiteral measures a file literal from its own contents.
//
// A literal has no location and does not exist yet — the runner creates it on disk when a tool
// needs it — but its bytes are already known, so its size and checksum are known too, and an
// expression that reads them before staging gets the same answer it would get afterwards.
//
// A File that carries neither a path nor contents names a resource on some other host. There is
// nothing local to measure, and Size stays unset rather than becoming a misleading zero, which
// is exactly the distinction [cwlcore.OptInt] exists to keep.
func joMeasureLiteral(file *cwlcore.File) {
	if !file.Contents.IsSet() {
		return
	}

	content := []byte(file.Contents.Value())
	file.Size = cwlcore.NewOptInt(int64(len(content)))
	file.Checksum = joChecksumOf(content)
}

// joLoadContents puts a File's bytes into its contents field when the declaring parameter
// asked for them with loadContents.
//
// Process.yml, loadContents: "the file (or each file in the array) must be a UTF-8 text file 64
// KiB or smaller, and the implementation must read the entire contents of the file ... If the
// size of the file is greater than 64 KiB, the implementation must raise a fatal error".
//
// The bytes come from the digest pass rather than a second read, which is both cheaper and the
// only way to be certain the contents an expression sees are the ones the checksum describes.
func joLoadContents(file *cwlcore.File, m *salad.MapNode, v *joValueCtx, stats *joFileStats) *salad.Error {
	if !v.loadContents {
		return nil
	}

	if stats.size > joMaxContentsBytes {
		return salad.Errorf(m.Loc(),
			"%s: loadContents is set but the file is %d bytes, over the %d byte limit",
			v.path, stats.size, joMaxContentsBytes)
	}

	file.Contents = cwlcore.NewOptString(string(stats.head))

	return nil
}

// joDigest reads the file at p once, returning its size, its CWL checksum, and its first 64 KiB.
func joDigest(p string) (joFileStats, error) {
	// Clean is redundant — every path reaching here has already been through filepath.Join
	// or filepath.Clean — but it is what marks the value as sanitized for taint analysis.
	file, err := os.Open(filepath.Clean(p))
	if err != nil {
		return joFileStats{}, err
	}

	stats, hashErr := joHashAll(file)

	// The close error is joined rather than dropped: errcheck's check-blank leaves no way to
	// discard it, and a deferred close would need a named result to report it at all.
	// errors.Join is nil when both arguments are, so the common path is a single branch.
	readErr := errors.Join(hashErr, file.Close())
	if readErr != nil {
		return joFileStats{}, readErr
	}

	return stats, nil
}

// joHashAll streams r through the digest, keeping the leading bytes as it goes.
//
// Streaming rather than reading the whole file keeps a multi-gigabyte input off the heap, which
// matters: an input object routinely names alignment files and reference genomes.
func joHashAll(r io.Reader) (joFileStats, error) {
	hash := sha1.New()
	head := &joCapWriter{limit: joMaxContentsBytes}

	size, err := io.Copy(io.MultiWriter(hash, head), r)
	if err != nil {
		return joFileStats{}, err
	}

	return joFileStats{checksum: joChecksumPrefix + hex.EncodeToString(hash.Sum(nil)), head: head.buf, size: size}, nil
}

// joChecksumOf returns the CWL checksum of a byte slice already in memory.
func joChecksumOf(content []byte) string {
	sum := sha1.Sum(content)

	return joChecksumPrefix + hex.EncodeToString(sum[:])
}

// joCapWriter keeps the first limit bytes written to it and silently drops the rest. It is how the
// digest pass captures enough of a file to satisfy loadContents without ever holding more than
// the specification's ceiling in memory.
type joCapWriter struct {
	buf   []byte
	limit int
}

// Write records as much of p as still fits under the cap, and reports every byte as written so
// that it composes with [io.MultiWriter].
func (w *joCapWriter) Write(p []byte) (int, error) {
	if room := w.limit - len(w.buf); room > 0 {
		w.buf = append(w.buf, p[:min(room, len(p))]...)
	}

	return len(p), nil
}
