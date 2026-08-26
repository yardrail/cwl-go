package conformance

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Re-verifying a File output against the bytes on disk.
//
// SHA-1 is used here as the *content digest the specification mandates*, not as a security
// control: Process.yml, File.checksum -- "must be in the form 'sha1$ + hexadecimal string'
// using the SHA-1 algorithm". The whole point of this file is to compute the same value
// cwltest computes, so a stronger hash would produce an answer that is simply wrong.
//
// gosec objects on two rules, G505 on the import and G401 on the constructor, and this
// project bans //nolint directives; everything here that touches SHA-1 is confined to this
// one file so that the existing exclusion, scoped to pkg/cwlexec/*_digest.go, still covers
// it and covers nothing else.

// checksumPrefix is the algorithm tag a CWL checksum carries.
const checksumPrefix = "sha1$"

// digestBufferSize is how much of a file is hashed per read.
const digestBufferSize = 1024 * 1024

// compareDigest re-verifies a reported File's checksum and size against the file itself.
//
// Neither field is taken on trust, and both are checked twice over: against the value the
// run declared and against the value the expectation names. Checking the run's own claim
// is what makes the harness worth running -- an engine that reports a checksum it did not
// compute is wrong whether or not the suite thought to ask about that file.
func compareDigest(expected, actual map[string]any) error {
	where, ok := digestPath(actual)
	if !ok {
		return fmt.Errorf("%w: the reported File carries neither a path nor a location", errMismatch)
	}

	stats, err := digestOf(localPath(where))
	if err != nil {
		return fmt.Errorf("%w: measuring %s: %w", errMismatch, where, err)
	}

	for _, check := range []struct {
		object map[string]any
		whose  string
		key    string
		found  any
	}{
		{object: actual, whose: "the run", key: keyChecksum, found: stats.checksum},
		{object: expected, whose: "the suite", key: keyChecksum, found: stats.checksum},
		{object: actual, whose: "the run", key: keySize, found: measured(stats.size)},
		{object: expected, whose: "the suite", key: keySize, found: measured(stats.size)},
	} {
		err = checkAgainstDisk(check.object, check.key, check.found, check.whose)
		if err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
	}

	return nil
}

// digestPath is the file the measurements are taken from: the reported path when there is
// one, the reported location otherwise.
func digestPath(actual map[string]any) (string, bool) {
	where, ok := actual[keyPath].(string)
	if ok {
		return where, true
	}

	where, ok = actual[keyLocation].(string)

	return where, ok
}

// checkAgainstDisk compares one declared checksum or size against the value measured from
// disk. A field neither side declares is nothing to check.
func checkAgainstDisk(object map[string]any, key string, found any, whose string) error {
	declared, present := object[key]
	if !present || equalScalar(declared, found) {
		return nil
	}

	return fmt.Errorf("%w: %s says the %s is %s, the file on disk has %s",
		errMismatch, whose, key, render(declared), render(found))
}

// fileStats is what one pass over a file's bytes yields. The two travel together because
// they must describe the same read: a checksum taken from one read and a size from another
// could disagree about a file a tool is still flushing.
type fileStats struct {
	// checksum is the "sha1$<hex>" digest of everything read.
	checksum string
	// size is the number of bytes read, which is the file's size.
	size int64
}

// digestOf reads the file at local once and reports its CWL checksum and its size.
func digestOf(local string) (fileStats, error) {
	// Clean is redundant -- every path reaching here came out of an output object the
	// engine built from filepath operations -- but it is what marks the value as
	// sanitized for taint analysis.
	file, err := os.Open(filepath.Clean(local))
	if err != nil {
		return fileStats{}, err
	}

	digest := sha1.New()

	size, copyErr := io.CopyBuffer(digest, file, make([]byte, digestBufferSize))

	// The close error is joined rather than dropped: errcheck's check-blank leaves no
	// way to discard it, and a deferred close would need a named result to report it.
	readErr := errors.Join(copyErr, file.Close())
	if readErr != nil {
		return fileStats{}, readErr
	}

	return fileStats{checksum: checksumPrefix + hex.EncodeToString(digest.Sum(nil)), size: size}, nil
}
