package salad

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
)

func TestNormalizeURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		base string
		ref  string
		want string
	}{
		{
			name: "an absolute URL passes through",
			base: "",
			ref:  "http://example.com/a.yml",
			want: "http://example.com/a.yml",
		},
		{name: "a relative reference joins the base", base: pathABC, ref: "d.yml", want: "file:///a/b/d.yml"},
		{name: "a fragment is kept", base: pathABC, ref: "d.yml#x", want: "file:///a/b/d.yml#x"},
		{name: "dot segments are collapsed", base: pathABC, ref: "../e/f.yml", want: "file:///a/e/f.yml"},
		{name: "an absolute path becomes a file URL", base: pathABC, ref: "/x/y.yml", want: "file:///x/y.yml"},
		{
			name: "a base with no scheme is a filesystem path",
			base: "/a/b/c.yml",
			ref:  "d.yml",
			want: "file:///a/b/d.yml",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := normalizeURL(tc.base, tc.ref)
			if err != nil {
				t.Fatalf("normalizeURL(%q, %q): %v", tc.base, tc.ref, err)
			}

			if got != tc.want {
				t.Errorf("normalizeURL(%q, %q) = %q, want %q", tc.base, tc.ref, got, tc.want)
			}
		})
	}
}

func TestNormalizeRejectsAnEmptyReference(t *testing.T) {
	t.Parallel()

	_, err := NewDefaultFetcher().Normalize("file:///a/", "")
	if err == nil {
		t.Error("an empty reference must be an error")
	}
}

func TestDefaultFetcherReadsFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "doc.yml")

	writeErr := os.WriteFile(path, []byte(docBody), 0o600)
	if writeErr != nil {
		t.Fatalf("writing the fixture: %v", writeErr)
	}

	fetcher := NewDefaultFetcher(WithCacheDir(""))

	docURL, err := fetcher.Normalize("", path)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}

	body, err := fetcher.FetchText(docURL + "#fragment")
	if err != nil {
		t.Fatalf("FetchText: %v", err)
	}

	if string(body) != docBody {
		t.Errorf("FetchText = %q, want the file's contents", body)
	}

	if !fetcher.Exists(docURL) {
		t.Error("Exists must report an existing file")
	}

	if fetcher.Exists(docURL + ".missing") {
		t.Error("Exists must not report a missing file")
	}

	if fetcher.Exists("gopher://example.com/x") {
		t.Error("Exists must not report an unsupported scheme")
	}

	_, err = fetcher.FetchText("gopher://example.com/x")
	if err == nil {
		t.Error("an unsupported scheme must be a fetch error")
	}
}

func TestFSFetcher(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{"sub/doc.yml": &fstest.MapFile{Data: []byte(docBody)}}
	fetcher := NewFSFetcher(fsys, "file:///embedded")

	docURL, err := fetcher.Normalize("", "sub/doc.yml")
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}

	if docURL != "file:///embedded/sub/doc.yml" {
		t.Fatalf("Normalize = %q, want it rooted at the mount point", docURL)
	}

	body, err := fetcher.FetchText(docURL)
	if err != nil {
		t.Fatalf("FetchText: %v", err)
	}

	if string(body) != docBody {
		t.Errorf("FetchText = %q, want the embedded contents", body)
	}

	if !fetcher.Exists(docURL) || fetcher.Exists("file:///embedded/missing.yml") {
		t.Error("Exists must follow the mounted file system")
	}

	_, err = fetcher.FetchText("file:///elsewhere/doc.yml")
	if err == nil {
		t.Error("a URL outside the mount point must be an error")
	}
}

func TestLoaderReadsThroughAnFSFetcher(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		docMain:  &fstest.MapFile{Data: []byte("id: main\nchild:\n  $import: child.yml\n")},
		docChild: &fstest.MapFile{Data: []byte("id: kid\n")},
	}
	loader := NewLoader(
		WithFetcher(NewFSFetcher(fsys, "file:///embedded/")),
		WithContext(schemaContext(t, identSchema)),
		WithSkipLinkCheck(true),
	)

	doc, err := loader.Load(docMain)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	child := mustMap(t, mustGet(t, mustMap(t, doc.Root), "child"))
	if id, _ := AsString(mustGet(t, child, "id")); id != "file:///embedded/child.yml#kid" {
		t.Errorf("id = %q, want the import resolved inside the mounted file system", id)
	}
}

func TestHTTPCacheServesFreshResponsesWithoutRefetching(t *testing.T) {
	t.Parallel()

	var hits atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Cache-Control", "max-age=600")
		w.Header().Set("ETag", `"v1"`)
		writeAll(t, w, docBody)
	}))
	defer server.Close()

	fetcher := NewDefaultFetcher(WithCacheDir(t.TempDir()), WithHTTPClient(server.Client()))

	for range 3 {
		body, err := fetcher.FetchText(server.URL + "/doc.yml")
		if err != nil {
			t.Fatalf("FetchText: %v", err)
		}

		if string(body) != docBody {
			t.Fatalf("FetchText = %q, want the served document", body)
		}
	}

	if hits.Load() != 1 {
		t.Errorf("the server was hit %d times, want the cached response to be reused", hits.Load())
	}
}

func TestHTTPCacheRevalidatesWithTheETag(t *testing.T) {
	t.Parallel()

	var revalidations atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", `"v1"`)

		if r.Header.Get("If-None-Match") == `"v1"` {
			revalidations.Add(1)
			w.WriteHeader(http.StatusNotModified)

			return
		}

		writeAll(t, w, docBody)
	}))
	defer server.Close()

	fetcher := NewDefaultFetcher(WithCacheDir(t.TempDir()), WithHTTPClient(server.Client()))

	for range 2 {
		body, err := fetcher.FetchText(server.URL + "/doc.yml")
		if err != nil {
			t.Fatalf("FetchText: %v", err)
		}

		if string(body) != docBody {
			t.Fatalf("FetchText = %q, want the cached body to survive a 304", body)
		}
	}

	if revalidations.Load() != 1 {
		t.Errorf("the server saw %d conditional requests, want exactly one", revalidations.Load())
	}
}

func TestHTTPErrorsAreReported(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer server.Close()

	fetcher := NewDefaultFetcher(WithCacheDir(""), WithHTTPClient(server.Client()))

	_, err := fetcher.FetchText(server.URL + "/missing.yml")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %v, want the HTTP status reported", err)
	}
}

func TestMaxAgeParsing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		control string
		want    int
	}{
		{name: "an explicit lifetime", control: "public, max-age=120", want: 120},
		{name: "no-cache means revalidate", control: "no-cache", want: 0},
		{name: "a malformed lifetime means revalidate", control: "max-age=soon", want: 0},
		{name: "no directive means the default", control: "", want: int(defaultFreshness.Seconds())},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			header := make(http.Header)
			if tc.control != "" {
				header.Set("Cache-Control", tc.control)
			}

			if got := int(maxAge(header).Seconds()); got != tc.want {
				t.Errorf("maxAge(%q) = %ds, want %ds", tc.control, got, tc.want)
			}
		})
	}
}

// writeAll writes a response body, failing the test if the write does not.
func writeAll(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()

	_, err := w.Write([]byte(body))
	if err != nil {
		t.Errorf("writing the response: %v", err)
	}
}

func TestExistsAcceptsAnythingPresent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	mkErr := os.Mkdir(filepath.Join(dir, testSub), 0o750)
	if mkErr != nil {
		t.Fatalf("creating the subdirectory: %v", mkErr)
	}

	writeErr := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o600)
	if writeErr != nil {
		t.Fatalf("writing the file: %v", writeErr)
	}

	fetcher := NewDefaultFetcher(WithCacheDir(""))

	cases := []struct {
		name string
		path string
		want bool
	}{
		{name: "a file", path: "f.txt", want: true},
		{name: "a directory, which a link may name", path: testSub, want: true},
		{name: "nothing", path: "gone", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			docURL, err := fetcher.Normalize("", filepath.Join(dir, tc.path))
			if err != nil {
				t.Fatalf("Normalize: %v", err)
			}

			if got := fetcher.Exists(docURL); got != tc.want {
				t.Errorf("Exists(%s) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}

	assertDirIsNotADocument(t, fetcher, filepath.Join(dir, testSub))
}

// assertDirIsNotADocument checks the other half of the split: a directory is a
// link target, but it is still not something the loader can read a document out
// of.
func assertDirIsNotADocument(t *testing.T, fetcher *DefaultFetcher, dir string) {
	t.Helper()

	dirURL, err := fetcher.Normalize("", dir)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}

	_, err = fetcher.FetchText(dirURL)
	if err == nil {
		t.Error("a directory exists as a link target but must not fetch as a document")
	}
}
