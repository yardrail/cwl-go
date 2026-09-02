package conformance

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// testDoc is a stand-in corpus document path.
const testDoc = "tests/a.cwl"

// testFixtureTag is the corpus release tag used by tests that build their own tarball
// rather than fetching the real one.
const testFixtureTag = "v9.9.9"

// plainToolBody is a stand-in document body for tests that only care that the right
// bytes come back out, not that they parse as CWL.
const plainToolBody = "class: CommandLineTool"

// testFileName is a stand-in archive entry name used by the extractEntry/writeEntry
// tests, named so goconst does not see it repeated.
const testFileName = "a.txt"

// mustWriteFile writes body to path, creating any missing parent directories.
func mustWriteFile(t *testing.T, path, body string) {
	t.Helper()

	err := os.MkdirAll(filepath.Dir(path), dirPerm)
	if err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}

	err = os.WriteFile(path, []byte(body), filePerm)
	if err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// buildTarGz builds a GitHub-style source tarball in memory: every member is wrapped in a
// single "<repo>-<tag>/" directory, matching what codeload.github.com serves and what
// stripLeadingComponent expects to find.
func buildTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for name, body := range files {
		header := &tar.Header{
			Name:     "cwl-v1.2-v9.9.9/" + name,
			Mode:     0o600,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}

		err := tw.WriteHeader(header)
		if err != nil {
			t.Fatalf("writing tar header for %s: %v", name, err)
		}

		_, err = tw.Write([]byte(body))
		if err != nil {
			t.Fatalf("writing tar body for %s: %v", name, err)
		}
	}

	err := tw.Close()
	if err != nil {
		t.Fatalf("closing tar writer: %v", err)
	}

	err = gz.Close()
	if err != nil {
		t.Fatalf("closing gzip writer: %v", err)
	}

	return buf.Bytes()
}

// tarballServer starts an [httptest.Server] that answers every request with status and
// body, standing in for codeload.github.com.
func tarballServer(t *testing.T, status int, body []byte) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)

		_, err := w.Write(body)
		if err != nil {
			t.Errorf("writing the response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestSafeJoinRefusesEscapes(t *testing.T) {
	t.Parallel()

	root := filepath.FromSlash("/dest")

	tests := []struct {
		name string
		rel  string
		ok   bool
	}{
		{name: "a normal member", rel: testDoc, ok: true},
		{name: "the wrapper entry itself", rel: "", ok: true},
		{name: "a parent traversal", rel: "../evil", ok: false},
		{name: "a deep traversal", rel: "tests/../../evil", ok: false},
		{name: "an absolute path", rel: "/etc/passwd", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, ok := safeJoin(root, tt.rel)
			if ok != tt.ok {
				t.Errorf("safeJoin(%q, %q) ok = %v, want %v", root, tt.rel, ok, tt.ok)
			}
		})
	}
}

func TestStripLeadingComponent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "the GitHub wrapper is dropped", in: "cwl-v1.2-1.2.1/" + testDoc, want: testDoc},
		{name: "the wrapper directory itself", in: "cwl-v1.2-1.2.1/", want: ""},
		{name: "a bare name has nothing to strip", in: "README", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := stripLeadingComponent(tt.in)
			if got != tt.want {
				t.Errorf("stripLeadingComponent(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLocalPathAcceptsBothSpellings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ref  string
		want string
	}{
		{name: "a file URL", ref: "file:///a/b/c.yaml", want: filepath.FromSlash("/a/b/c.yaml")},
		{name: "a bare path", ref: "/a/b/c.yaml", want: filepath.FromSlash("/a/b/c.yaml")},
		{name: "nothing", ref: "", want: ""},
		{name: "a scheme we cannot map", ref: "https://example.com/a.yaml", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := localPath(tt.ref)
			if got != tt.want {
				t.Errorf("localPath(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

func TestDefaultCacheDirHonoursTheOverride(t *testing.T) {
	t.Setenv(envCache, filepath.FromSlash("/somewhere/else"))

	got := defaultCacheDir()
	if got != filepath.FromSlash("/somewhere/else") {
		t.Errorf("defaultCacheDir() = %q, want the override", got)
	}
}

// TestDefaultCacheDirFallsBackWhenUserCacheDirFails must not run in parallel with other
// tests that mutate the environment: it blanks XDG_CACHE_HOME and HOME so
// [os.UserCacheDir] errors, which is the only way to reach defaultCacheDir's fallback.
func TestDefaultCacheDirFallsBackWhenUserCacheDirFails(t *testing.T) {
	t.Setenv(envCache, "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "")

	got := defaultCacheDir()

	want := filepath.Join(os.TempDir(), "cwl-go", "conformance")
	if got != want {
		t.Errorf("defaultCacheDir() = %q, want the os.TempDir() fallback %q", got, want)
	}
}

func TestDedupeSortsAndDeduplicates(t *testing.T) {
	t.Parallel()

	got := dedupe([]string{"b", "a", "b", "c", "a"})
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("dedupe = %v, want [a b c]", got)
	}
}

func TestWorkerCountNeverExceedsTheWork(t *testing.T) {
	t.Parallel()

	if got := workerCount(1); got != 1 {
		t.Errorf("workerCount(1) = %d, want 1", got)
	}

	if got := workerCount(0); got != 1 {
		t.Errorf("workerCount(0) = %d, want 1", got)
	}
}

// The four TestOpenCorpus* tests below each mutate CWL_CONFORMANCE_CORPUS via
// t.Setenv, so none of them run in parallel.

func TestOpenCorpusExplicitCorpusWithAManifest(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, manifestName), "[]")
	t.Setenv(envCorpus, dir)

	c, err := openCorpus(context.Background(), "unused://", testFixtureTag, t.TempDir())
	if err != nil {
		t.Fatalf("openCorpus: %v", err)
	}

	if c.root != dir || c.tag != testFixtureTag || c.fetched {
		t.Errorf("openCorpus = %+v, want root=%s tag=%s fetched=false", c, dir, testFixtureTag)
	}
}

func TestOpenCorpusExplicitCorpusWithoutAManifest(t *testing.T) {
	t.Setenv(envCorpus, t.TempDir())

	_, err := openCorpus(context.Background(), "unused://", testFixtureTag, t.TempDir())
	if !errors.Is(err, errCorpusIncomplete) {
		t.Errorf("openCorpus error = %v, want errCorpusIncomplete", err)
	}
}

func TestOpenCorpusCacheHit(t *testing.T) {
	t.Setenv(envCorpus, "")

	cacheDir := t.TempDir()
	dest := filepath.Join(cacheDir, "cwl-v1.2-"+testFixtureTag)
	mustWriteFile(t, filepath.Join(dest, manifestName), "[]")

	c, err := openCorpus(context.Background(), "unused://", testFixtureTag, cacheDir)
	if err != nil {
		t.Fatalf("openCorpus: %v", err)
	}

	if c.root != dest || c.fetched {
		t.Errorf("openCorpus = %+v, want root=%s fetched=false", c, dest)
	}
}

func TestOpenCorpusCacheMissAndDownloadFailure(t *testing.T) {
	t.Setenv(envCorpus, "")

	srv := tarballServer(t, http.StatusInternalServerError, nil)

	_, err := openCorpus(context.Background(), srv.URL+"/", testFixtureTag, t.TempDir())
	if err == nil {
		t.Fatal("openCorpus returned no error for a failed download")
	}
}

func TestDocumentsWalkError(t *testing.T) {
	t.Parallel()

	// No tests/ subdirectory at all, so WalkDir's first callback receives a non-nil err.
	c := &corpus{root: t.TempDir()}

	_, err := c.documents()
	if err == nil {
		t.Fatal("documents() returned no error when tests/ is missing")
	}
}

func TestDocumentsFindsCWLFilesCaseInsensitivelyAndSkipsOthers(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "tests", "a.cwl"), "")
	mustWriteFile(t, filepath.Join(root, "tests", "b.CWL"), "")
	mustWriteFile(t, filepath.Join(root, "tests", "sub", "c.cwl"), "")
	mustWriteFile(t, filepath.Join(root, "tests", "notes.txt"), "")

	c := &corpus{root: root}

	docs, err := c.documents()
	if err != nil {
		t.Fatalf("documents: %v", err)
	}

	want := []string{testDoc, "tests/b.CWL", "tests/sub/c.cwl"}
	if !slices.Equal(docs, want) {
		t.Errorf("documents = %v, want %v", docs, want)
	}
}

func TestHasManifest(t *testing.T) {
	t.Parallel()

	t.Run("directory does not exist", func(t *testing.T) {
		t.Parallel()

		if hasManifest(filepath.Join(t.TempDir(), "missing")) {
			t.Error("hasManifest = true for a directory that does not exist")
		}
	})

	t.Run("manifest file is missing", func(t *testing.T) {
		t.Parallel()

		if hasManifest(t.TempDir()) {
			t.Error("hasManifest = true for a directory with no manifest file")
		}
	})

	t.Run("manifest file is present", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		mustWriteFile(t, filepath.Join(dir, manifestName), "[]")

		if !hasManifest(dir) {
			t.Error("hasManifest = false for a directory with a manifest file")
		}
	})

	t.Run("manifest name is a directory, not a file", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()

		err := os.MkdirAll(filepath.Join(dir, manifestName), dirPerm)
		if err != nil {
			t.Fatalf("creating %s as a directory: %v", manifestName, err)
		}

		if hasManifest(dir) {
			t.Error("hasManifest = true when the manifest name is itself a directory")
		}
	})
}

func TestFetchTarballSuccess(t *testing.T) {
	t.Parallel()

	body := []byte("archive-bytes")
	srv := tarballServer(t, http.StatusOK, body)

	got, err := fetchTarball(t.Context(), srv.URL)
	if err != nil {
		t.Fatalf("fetchTarball: %v", err)
	}

	if !bytes.Equal(got, body) {
		t.Errorf("fetchTarball = %q, want %q", got, body)
	}
}

func TestFetchTarballNonOKStatus(t *testing.T) {
	t.Parallel()

	srv := tarballServer(t, http.StatusNotFound, []byte("nope"))

	_, err := fetchTarball(t.Context(), srv.URL)
	if !errors.Is(err, errFetchStatus) {
		t.Errorf("fetchTarball error = %v, want errFetchStatus", err)
	}
}

func TestUnpackTarGzSuccess(t *testing.T) {
	t.Parallel()

	archive := buildTarGz(t, map[string]string{
		manifestName: "[]",
		testDoc:      plainToolBody,
	})

	dest := t.TempDir()

	err := unpackTarGz(archive, dest)
	if err != nil {
		t.Fatalf("unpackTarGz: %v", err)
	}

	if !hasManifest(dest) {
		t.Error("unpacked corpus has no manifest")
	}

	got, err := os.ReadFile(filepath.Join(dest, "tests", "a.cwl"))
	if err != nil {
		t.Fatalf("reading unpacked file: %v", err)
	}

	if string(got) != plainToolBody {
		t.Errorf("unpacked content = %q", got)
	}
}

func TestUnpackTarGzRejectsGarbageBytes(t *testing.T) {
	t.Parallel()

	err := unpackTarGz([]byte("not a gzip stream at all"), t.TempDir())
	if err == nil {
		t.Fatal("unpackTarGz accepted non-gzip bytes")
	}
}

func TestUnpackTarGzRejectsATruncatedTarBody(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	gz := gzip.NewWriter(&buf)

	// A valid gzip stream wrapping a body that is not a tar archive at all, so
	// tar.Reader.Next() fails on the very first header with a non-EOF error.
	_, err := gz.Write([]byte("this is nowhere near a valid 512-byte tar header block"))
	if err != nil {
		t.Fatalf("writing gzip payload: %v", err)
	}

	err = gz.Close()
	if err != nil {
		t.Fatalf("closing gzip writer: %v", err)
	}

	err = unpackTarGz(buf.Bytes(), t.TempDir())
	if err == nil {
		t.Fatal("unpackTarGz accepted a corrupt tar body")
	}

	if errors.Is(err, io.EOF) {
		t.Errorf("unpackTarGz reported EOF, want a format error: %v", err)
	}
}

func TestExtractEntryUnsafePath(t *testing.T) {
	t.Parallel()

	// extractEntry strips the leading "<repo>-<tag>/" wrapper component before checking
	// safety, so the escape must survive that strip.
	header := &tar.Header{Name: "wrapper/../../evil", Typeflag: tar.TypeReg}

	err := extractEntry(strings.NewReader(""), header, t.TempDir())
	if !errors.Is(err, errUnsafeArchivePath) {
		t.Errorf("extractEntry error = %v, want errUnsafeArchivePath", err)
	}
}

func TestExtractEntryWrapperEntryIsANoop(t *testing.T) {
	t.Parallel()

	header := &tar.Header{Name: "cwl-v1.2-v9.9.9/", Typeflag: tar.TypeDir}

	err := extractEntry(strings.NewReader(""), header, t.TempDir())
	if err != nil {
		t.Errorf("extractEntry(wrapper entry) = %v, want nil", err)
	}
}

func TestExtractEntryDirectory(t *testing.T) {
	t.Parallel()

	dest := t.TempDir()
	header := &tar.Header{Name: "repo/tests", Typeflag: tar.TypeDir}

	err := extractEntry(strings.NewReader(""), header, dest)
	if err != nil {
		t.Fatalf("extractEntry: %v", err)
	}

	info, err := os.Stat(filepath.Join(dest, "tests"))
	if err != nil || !info.IsDir() {
		t.Errorf("extractEntry did not create the directory: %v", err)
	}
}

func TestExtractEntryRegularFileDelegatesToWriteEntry(t *testing.T) {
	t.Parallel()

	dest := t.TempDir()
	body := "hello"
	header := &tar.Header{Name: "repo/a.txt", Typeflag: tar.TypeReg, Size: int64(len(body))}

	err := extractEntry(strings.NewReader(body), header, dest)
	if err != nil {
		t.Fatalf("extractEntry: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dest, testFileName))
	if err != nil || string(got) != body {
		t.Errorf("extractEntry wrote %q, %v, want %q", got, err, body)
	}
}

func TestExtractEntrySkipsUnhandledTypes(t *testing.T) {
	t.Parallel()

	dest := t.TempDir()
	header := &tar.Header{Name: "repo/link", Typeflag: tar.TypeSymlink, Linkname: testFileName}

	err := extractEntry(strings.NewReader(""), header, dest)
	if err != nil {
		t.Errorf("extractEntry(symlink) = %v, want nil", err)
	}

	_, err = os.Lstat(filepath.Join(dest, "link"))
	if !os.IsNotExist(err) {
		t.Error("extractEntry created something on disk for a skipped entry type")
	}
}

func TestWriteEntryRefusesAnOversizedMember(t *testing.T) {
	t.Parallel()

	header := &tar.Header{Name: "big.bin", Size: maxEntryBytes + 1}

	err := writeEntry(strings.NewReader(""), header, filepath.Join(t.TempDir(), "big.bin"))
	if !errors.Is(err, errEntryTooLarge) {
		t.Errorf("writeEntry error = %v, want errEntryTooLarge", err)
	}
}

func TestWriteEntryMkdirAllFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	blocker := filepath.Join(root, "blocker")
	mustWriteFile(t, blocker, "") // a file occupies the path a directory needs to go

	target := filepath.Join(blocker, "nested", testFileName)
	header := &tar.Header{Name: testFileName, Size: 0}

	err := writeEntry(strings.NewReader(""), header, target)
	if err == nil {
		t.Fatal("writeEntry succeeded despite a file blocking its parent directory")
	}
}

func TestWriteEntryOpenFileFailure(t *testing.T) {
	t.Parallel()

	dest := t.TempDir()
	target := filepath.Join(dest, testFileName)

	// target itself already exists as a directory, so OpenFile must fail.
	err := os.MkdirAll(target, dirPerm)
	if err != nil {
		t.Fatalf("creating blocking directory: %v", err)
	}

	header := &tar.Header{Name: testFileName, Size: 0}

	err = writeEntry(strings.NewReader(""), header, target)
	if err == nil {
		t.Fatal("writeEntry succeeded although the target already exists as a directory")
	}
}

func TestWriteEntrySuccess(t *testing.T) {
	t.Parallel()

	dest := t.TempDir()
	target := filepath.Join(dest, "sub", testFileName)
	body := "payload"
	header := &tar.Header{Name: testFileName, Size: int64(len(body))}

	err := writeEntry(strings.NewReader(body), header, target)
	if err != nil {
		t.Fatalf("writeEntry: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil || string(got) != body {
		t.Errorf("writeEntry wrote %q, %v, want %q", got, err, body)
	}
}

func TestDownloadCorpusFullSuccess(t *testing.T) {
	t.Parallel()

	archive := buildTarGz(t, map[string]string{
		manifestName: "[]",
		testDoc:      plainToolBody,
	})
	srv := tarballServer(t, http.StatusOK, archive)

	dest := filepath.Join(t.TempDir(), "cwl-v1.2-v9.9.9")

	err := downloadCorpus(t.Context(), srv.URL+"/", testFixtureTag, dest)
	if err != nil {
		t.Fatalf("downloadCorpus: %v", err)
	}

	if !hasManifest(dest) {
		t.Error("downloadCorpus did not leave a usable corpus at dest")
	}
}

func TestDownloadCorpusFetchFailure(t *testing.T) {
	t.Parallel()

	srv := tarballServer(t, http.StatusInternalServerError, nil)
	dest := filepath.Join(t.TempDir(), "cwl-v1.2-v9.9.9")

	err := downloadCorpus(t.Context(), srv.URL+"/", testFixtureTag, dest)
	if !errors.Is(err, errFetchStatus) {
		t.Errorf("downloadCorpus error = %v, want errFetchStatus", err)
	}
}

func TestDownloadCorpusUnpackFailure(t *testing.T) {
	t.Parallel()

	srv := tarballServer(t, http.StatusOK, []byte("not a gzip stream"))
	dest := filepath.Join(t.TempDir(), "cwl-v1.2-v9.9.9")

	err := downloadCorpus(t.Context(), srv.URL+"/", testFixtureTag, dest)
	if err == nil {
		t.Fatal("downloadCorpus succeeded despite a corrupt archive")
	}

	_, statErr := os.Stat(dest)
	if !os.IsNotExist(statErr) {
		t.Error("downloadCorpus left a directory at dest after an unpack failure")
	}
}

func TestDownloadCorpusMkdirAllFailure(t *testing.T) {
	t.Parallel()

	archive := buildTarGz(t, map[string]string{manifestName: "[]"})
	srv := tarballServer(t, http.StatusOK, archive)

	root := t.TempDir()
	blocker := filepath.Join(root, "blocker")
	mustWriteFile(t, blocker, "") // occupies the path dest's parent directory needs

	dest := filepath.Join(blocker, "cwl-v1.2-v9.9.9")

	err := downloadCorpus(t.Context(), srv.URL+"/", testFixtureTag, dest)
	if err == nil {
		t.Fatal("downloadCorpus succeeded despite a file blocking dest's parent directory")
	}
}

func TestDownloadCorpusLosesTheRaceButThatIsFine(t *testing.T) {
	t.Parallel()

	archive := buildTarGz(t, map[string]string{manifestName: "[]"})
	srv := tarballServer(t, http.StatusOK, archive)

	dest := filepath.Join(t.TempDir(), "cwl-v1.2-v9.9.9")

	// Simulate another process having already unpacked the same tag while this one was
	// downloading: dest exists, is non-empty, and already looks like a good corpus, so the
	// rename that would move the staging directory into place fails with ENOTEMPTY -- and
	// that must not be reported as an error.
	mustWriteFile(t, filepath.Join(dest, manifestName), "[]")

	err := downloadCorpus(t.Context(), srv.URL+"/", testFixtureTag, dest)
	if err != nil {
		t.Errorf("downloadCorpus = %v, want nil (losing the race is not an error)", err)
	}
}
