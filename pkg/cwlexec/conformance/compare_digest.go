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

// Re-verifying File outputs against disk. SHA-1 is the CWL-mandated content digest.

// checksumPrefix is the algorithm tag a CWL checksum carries.
const checksumPrefix = "sha1$"

// digestBufferSize is how much of a file is hashed per read.
const digestBufferSize = 1024 * 1024

// compareDigest re-verifies a File's checksum and size against disk.
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

// digestPath returns the reported path, falling back to location.
func digestPath(actual map[string]any) (string, bool) {
	where, ok := actual[keyPath].(string)
	if ok {
		return where, true
	}

	where, ok = actual[keyLocation].(string)

	return where, ok
}

// checkAgainstDisk compares a declared checksum or size against the measured value.
func checkAgainstDisk(object map[string]any, key string, found any, whose string) error {
	declared, present := object[key]
	if !present || equalScalar(declared, found) {
		return nil
	}

	return fmt.Errorf("%w: %s says the %s is %s, the file on disk has %s",
		errMismatch, whose, key, render(declared), render(found))
}

// fileStats holds a file's checksum and size from a single read.
type fileStats struct {
	checksum string // "sha1$<hex>"
	size     int64
}

// digestOf reads the file at local once and reports its CWL checksum and its size.
func digestOf(local string) (fileStats, error) {
	file, err := os.Open(filepath.Clean(local))
	if err != nil {
		return fileStats{}, err
	}

	digest := sha1.New()

	size, copyErr := io.CopyBuffer(digest, file, make([]byte, digestBufferSize))

	readErr := errors.Join(copyErr, file.Close())
	if readErr != nil {
		return fileStats{}, readErr
	}

	return fileStats{checksum: checksumPrefix + hex.EncodeToString(digest.Sum(nil)), size: size}, nil
}
