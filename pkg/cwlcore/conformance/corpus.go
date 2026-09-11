package conformance

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The environment variables the sweep honours. See the package doc comment.
const (
	envEnable = "CWL_CONFORMANCE"
	envCorpus = "CWL_CONFORMANCE_CORPUS"
	envCache  = "CWL_CONFORMANCE_CACHE"
)

// Layout of the upstream corpus, relative to its root directory.
const (
	manifestName = "conformance_tests.yaml"
	testsDirName = "tests"
	cwlExt       = ".cwl"
)

// codeloadBase is GitHub's tarball endpoint for a tag.
const codeloadBase = "https://codeload.github.com/common-workflow-language/cwl-v1.2/tar.gz/refs/tags/"

// Limits and permissions for fetching and unpacking.
const (
	fetchTimeout    = 5 * time.Minute
	maxArchiveBytes = 256 << 20
	maxEntryBytes   = 32 << 20
	dirPerm         = 0o750
	filePerm        = 0o600
)

// expectedDocuments pre-sizes the document list.
const expectedDocuments = 512

// Sentinel errors for corpus acquisition.
var (
	errCorpusIncomplete  = errors.New("corpus directory does not contain " + manifestName)
	errUnsafeArchivePath = errors.New("archive entry escapes the destination directory")
	errEntryTooLarge     = errors.New("archive entry exceeds the size limit")
	errFetchStatus       = errors.New("unexpected HTTP status")
	errEmptyResponse     = errors.New("empty HTTP response")
)

// corpus is a located copy of the pinned cwl-v1.2 corpus on the local filesystem.
type corpus struct {
	// root is the directory holding conformance_tests.yaml and tests/.
	root string
	// tag is the upstream release tag the corpus was pinned to, e.g. "v1.2.1".
	tag string
	// fetched reports whether this run had to download the corpus.
	fetched bool
}

// corpusOnce memoizes the located corpus across parallel tests.
var corpusOnce struct {
	sync.Mutex

	found *corpus
	err   error
	done  bool
}

// sharedCorpus is openCorpus, resolved at most once per process.
func sharedCorpus(ctx context.Context, tag, cacheDir string) (*corpus, error) {
	corpusOnce.Lock()
	defer corpusOnce.Unlock()

	if !corpusOnce.done {
		corpusOnce.found, corpusOnce.err = openCorpus(ctx, codeloadBase, tag, cacheDir)
		corpusOnce.done = true
	}

	return corpusOnce.found, corpusOnce.err
}

// openCorpus locates the corpus, downloading from base+tag into cacheDir if needed.
func openCorpus(ctx context.Context, base, tag, cacheDir string) (*corpus, error) {
	explicit := strings.TrimSpace(os.Getenv(envCorpus))
	if explicit != "" {
		if !hasManifest(explicit) {
			return nil, fmt.Errorf("%s=%s: %w", envCorpus, explicit, errCorpusIncomplete)
		}

		return &corpus{root: explicit, tag: tag, fetched: false}, nil
	}

	dest := filepath.Join(cacheDir, "cwl-v1.2-"+tag)
	if hasManifest(dest) {
		return &corpus{root: dest, tag: tag, fetched: false}, nil
	}

	err := downloadCorpus(ctx, base, tag, dest)
	if err != nil {
		return nil, err
	}

	return &corpus{root: dest, tag: tag, fetched: true}, nil
}

// manifestPath is the path to the corpus test manifest.
func (c *corpus) manifestPath() string {
	return filepath.Join(c.root, manifestName)
}

// documents walks tests/ and returns every *.cwl path relative to the corpus root.
func (c *corpus) documents() ([]string, error) {
	testsRoot := filepath.Join(c.root, testsDirName)
	found := make([]string, 0, expectedDocuments)

	walk := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), cwlExt) {
			return nil
		}

		rel, relErr := filepath.Rel(c.root, path)
		if relErr != nil {
			return relErr
		}

		found = append(found, filepath.ToSlash(rel))

		return nil
	}

	err := filepath.WalkDir(testsRoot, walk)
	if err != nil {
		return nil, err
	}

	return found, nil
}

// hasManifest reports whether dir looks like an unpacked corpus root.
func hasManifest(dir string) bool {
	cleaned := filepath.Clean(dir)

	info, err := os.Stat(filepath.Join(cleaned, manifestName))
	if err != nil {
		return false
	}

	return info.Mode().IsRegular()
}

// downloadCorpus fetches and unpacks the tag's source tarball into dest atomically.
func downloadCorpus(ctx context.Context, base, tag, dest string) error {
	archive, err := fetchTarball(ctx, base+tag)
	if err != nil {
		return err
	}

	err = os.MkdirAll(filepath.Dir(dest), dirPerm)
	if err != nil {
		return err
	}

	staging, err := os.MkdirTemp(filepath.Dir(dest), ".unpack-")
	if err != nil {
		return err
	}

	err = unpackTarGz(archive, staging)
	if err != nil {
		return errors.Join(err, os.RemoveAll(staging))
	}

	err = os.Rename(staging, dest)
	if err != nil {
		removeErr := os.RemoveAll(staging)

		// Lost the rename race; the other copy is fine.
		if hasManifest(dest) {
			return removeErr
		}

		return errors.Join(err, removeErr)
	}

	return nil
}

// fetchTarball downloads url into memory.
func fetchTarball(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Transport: nil, CheckRedirect: nil, Jar: nil, Timeout: fetchTimeout}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("fetching %s: %w", url, errEmptyResponse)
	}

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxArchiveBytes))

	closeErr := resp.Body.Close()
	if readErr == nil {
		readErr = closeErr
	}

	if readErr != nil {
		return nil, readErr
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s: %w %s", url, errFetchStatus, resp.Status)
	}

	return body, nil
}

// unpackTarGz extracts a GitHub source tarball into dest, stripping the top-level directory.
func unpackTarGz(archive []byte, dest string) error {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return err
	}

	reader := tar.NewReader(gz)

	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			return gz.Close()
		}

		if nextErr != nil {
			return nextErr
		}

		err = extractEntry(reader, header, dest)
		if err != nil {
			return err
		}
	}
}

// extractEntry writes one archive member below dest. Non-regular entries are skipped.
func extractEntry(reader io.Reader, header *tar.Header, dest string) error {
	target, ok := safeJoin(dest, stripLeadingComponent(header.Name))
	if !ok {
		return fmt.Errorf("%q: %w", header.Name, errUnsafeArchivePath)
	}

	if target == "" {
		return nil
	}

	switch header.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, dirPerm)
	case tar.TypeReg:
		return writeEntry(reader, header, target)
	default:
		return nil
	}
}

// writeEntry creates target from the archive reader, refusing an oversized member.
func writeEntry(reader io.Reader, header *tar.Header, target string) error {
	if header.Size > maxEntryBytes {
		return fmt.Errorf("%q (%d bytes): %w", header.Name, header.Size, errEntryTooLarge)
	}

	err := os.MkdirAll(filepath.Dir(target), dirPerm)
	if err != nil {
		return err
	}

	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, filePerm)
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(file, io.LimitReader(reader, maxEntryBytes))

	return errors.Join(copyErr, file.Close())
}

// stripLeadingComponent drops the first path element (GitHub's wrapper directory).
func stripLeadingComponent(name string) string {
	_, rest, found := strings.Cut(filepath.ToSlash(name), "/")
	if !found {
		return ""
	}

	return rest
}

// safeJoin resolves rel below root, returning false if the result escapes root.
func safeJoin(root, rel string) (string, bool) {
	if rel == "" {
		return "", true
	}

	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return "", false
	}

	target := filepath.Join(root, filepath.FromSlash(rel))

	prefix := root + string(os.PathSeparator)
	if !strings.HasPrefix(target, prefix) {
		return "", false
	}

	return target, true
}

// defaultCacheDir returns the corpus cache directory, falling back to os.TempDir.
func defaultCacheDir() string {
	override := strings.TrimSpace(os.Getenv(envCache))
	if override != "" {
		return override
	}

	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}

	return filepath.Join(base, "cwl-go", "conformance")
}
