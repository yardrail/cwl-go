package salad

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// failingRoundTripper always fails a request, standing in for a network that is
// down.
type failingRoundTripper struct{}

var errRoundTripFailed = errors.New("simulated network failure")

func (failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errRoundTripFailed
}

func TestFetchHTTPReportsARequestConstructionFailure(t *testing.T) {
	t.Parallel()

	fetcher := NewDefaultFetcher(WithCacheDir(""))

	_, err := fetcher.fetchHTTP("http://[::1]:badport")
	if err == nil {
		t.Error("fetchHTTP must report a malformed request URL")
	}
}

func TestFetchHTTPReportsATransportFailure(t *testing.T) {
	t.Parallel()

	fetcher := NewDefaultFetcher(
		WithCacheDir(""),
		WithHTTPClient(&http.Client{Transport: failingRoundTripper{}}),
	)

	_, err := fetcher.fetchHTTP("http://example.com/doc.yml")
	if err == nil {
		t.Error("fetchHTTP must report a transport failure")
	}
}

func TestSetRevalidationHeadersLastModifiedOnly(t *testing.T) {
	t.Parallel()

	var gotIfModifiedSince, gotIfNoneMatch string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIfModifiedSince = r.Header.Get("If-Modified-Since")
		gotIfNoneMatch = r.Header.Get("If-None-Match")

		if gotIfModifiedSince != "" {
			w.WriteHeader(http.StatusNotModified)

			return
		}

		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		writeAll(t, w, docBody)
	}))
	defer server.Close()

	fetcher := NewDefaultFetcher(WithCacheDir(t.TempDir()), WithHTTPClient(server.Client()))

	for range 2 {
		_, err := fetcher.FetchText(server.URL + "/doc.yml")
		if err != nil {
			t.Fatalf("FetchText: %v", err)
		}
	}

	if gotIfModifiedSince == "" {
		t.Error("the second request must revalidate using If-Modified-Since")
	}

	if gotIfNoneMatch != "" {
		t.Errorf("no ETag was ever served, so If-None-Match must be empty, got %q", gotIfNoneMatch)
	}
}

func TestReadCacheIgnoresCorruptedOrIncompleteEntries(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fetcher := NewDefaultFetcher(WithCacheDir(dir))

	const u = "http://example.com/doc.yml"

	base := fetcher.cachePath(u)

	// A .meta file whose JSON is corrupted.
	writeMetaErr := os.WriteFile(base+cacheMetaSuffix, []byte("not json"), cacheFilePerm)
	if writeMetaErr != nil {
		t.Fatalf("writing a corrupted .meta file: %v", writeMetaErr)
	}

	if _, _, ok := fetcher.readCache(u); ok {
		t.Error("readCache must reject a corrupted .meta file")
	}

	// A .meta file that parses but names a different URL.
	meta := `{"url":"http://example.com/other.yml","expires":9999999999}`

	writeMismatchErr := os.WriteFile(base+cacheMetaSuffix, []byte(meta), cacheFilePerm)
	if writeMismatchErr != nil {
		t.Fatalf("writing a mismatched .meta file: %v", writeMismatchErr)
	}

	if _, _, ok := fetcher.readCache(u); ok {
		t.Error("readCache must reject a .meta file naming a different URL")
	}

	// A well-formed .meta file with no matching .body file.
	meta = `{"url":"` + u + `","expires":9999999999}`

	writeOrphanErr := os.WriteFile(base+cacheMetaSuffix, []byte(meta), cacheFilePerm)
	if writeOrphanErr != nil {
		t.Fatalf("writing the .meta file: %v", writeOrphanErr)
	}

	if _, _, ok := fetcher.readCache(u); ok {
		t.Error("readCache must reject a .meta file with no matching .body file")
	}
}

// noCacheServer starts a server that reports Cache-Control: directive on
// every response and counts how many requests it received.
func noCacheServer(t *testing.T, directive string, hits *int) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*hits++

		w.Header().Set("Cache-Control", directive)
		writeAll(t, w, docBody)
	}))
}

// assertDirectivePreventsCaching fetches the same URL twice from a server
// that always sends Cache-Control: directive, and checks that the server was
// hit both times.
func assertDirectivePreventsCaching(t *testing.T, directive string) {
	t.Helper()

	var hits int

	server := noCacheServer(t, directive, &hits)
	defer server.Close()

	fetcher := NewDefaultFetcher(WithCacheDir(t.TempDir()), WithHTTPClient(server.Client()))

	for range 2 {
		_, err := fetcher.FetchText(server.URL + "/doc.yml")
		if err != nil {
			t.Fatalf("FetchText: %v", err)
		}
	}

	if hits != 2 {
		t.Errorf("Cache-Control: %s must prevent caching, server was hit %d times, want 2", directive, hits)
	}
}

func TestWriteCacheHonoursNoStoreAndPrivate(t *testing.T) {
	t.Parallel()

	for _, directive := range []string{"no-store", "private"} {
		t.Run(directive, func(t *testing.T) {
			t.Parallel()
			assertDirectivePreventsCaching(t, directive)
		})
	}
}

func TestWriteCacheToleratesAnUnwritableCacheDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Point the cache directory at a path that is actually a regular file, so
	// os.MkdirAll fails.
	blocked := filepath.Join(dir, "blocked")

	writeBlockErr := os.WriteFile(blocked, []byte("x"), 0o600)
	if writeBlockErr != nil {
		t.Fatalf("writing the blocking file: %v", writeBlockErr)
	}

	cacheDir := filepath.Join(blocked, "cache")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "max-age=600")
		writeAll(t, w, docBody)
	}))
	defer server.Close()

	fetcher := NewDefaultFetcher(WithCacheDir(cacheDir), WithHTTPClient(server.Client()))

	// writeCache must not fail the fetch even though the cache directory can
	// never be created.
	_, err := fetcher.FetchText(server.URL + "/doc.yml")
	if err != nil {
		t.Fatalf("FetchText must succeed even when the cache cannot be written: %v", err)
	}
}

func TestWriteCacheToleratesAnUnwritableMetaFile(t *testing.T) {
	t.Parallel()

	cacheDir := t.TempDir()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "max-age=600")
		writeAll(t, w, docBody)
	}))
	defer server.Close()

	fetcher := NewDefaultFetcher(WithCacheDir(cacheDir), WithHTTPClient(server.Client()))
	docURL := server.URL + "/doc.yml"
	base := fetcher.cachePath(docURL)

	// The body file can be written normally, but the .meta path is itself a
	// directory, so only the metaErr write fails.
	mkdirErr := os.MkdirAll(base+cacheMetaSuffix, cacheDirPerm)
	if mkdirErr != nil {
		t.Fatalf("pre-creating the .meta path as a directory: %v", mkdirErr)
	}

	_, err := fetcher.FetchText(docURL)
	if err != nil {
		t.Fatalf("FetchText must succeed even when the .meta file cannot be written: %v", err)
	}

	_, statErr := os.Stat(base + cacheBodySuffix)
	if statErr != nil {
		t.Errorf("the .body file should have been written before the .meta write failed: %v", statErr)
	}
}

func TestWriteCacheToleratesAnUnwritableCacheFile(t *testing.T) {
	t.Parallel()

	cacheDir := t.TempDir()

	chmodErr := os.Chmod(cacheDir, 0o500)
	if chmodErr != nil {
		t.Fatalf("making the cache directory read-only: %v", chmodErr)
	}

	defer func() {
		restoreErr := os.Chmod(cacheDir, 0o700)
		if restoreErr != nil {
			t.Errorf("restoring the cache directory's permissions: %v", restoreErr)
		}
	}()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "max-age=600")
		writeAll(t, w, "unwritable-file body\n")
	}))
	defer server.Close()

	fetcher := NewDefaultFetcher(WithCacheDir(cacheDir), WithHTTPClient(server.Client()))

	_, err := fetcher.FetchText(server.URL + "/doc.yml")
	if err != nil {
		t.Fatalf("FetchText must succeed even when the cache file cannot be written: %v", err)
	}
}
