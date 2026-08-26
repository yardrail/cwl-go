package salad

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Suffixes of the two files one cached response occupies, and the permissions
// they are created with.
const (
	cacheBodySuffix = ".body"
	cacheMetaSuffix = ".meta"
	cacheDirPerm    = 0o755
	cacheFilePerm   = 0o600
	// defaultFreshness is how long a response with no explicit Cache-Control
	// lifetime is served from the cache before being revalidated.
	defaultFreshness = time.Hour
)

// cacheEntry is the freshness metadata stored alongside a cached response body.
type cacheEntry struct {
	// URL is the request URL, kept so that a cache file is self-describing.
	URL string `json:"url"`
	// ETag is the entity tag to revalidate with, if the server sent one.
	ETag string `json:"etag,omitempty"`
	// LastModified is the modification date to revalidate with, if any.
	LastModified string `json:"lastModified,omitempty"`
	// Expires is the Unix time the cached body stops being fresh.
	Expires int64 `json:"expires,omitempty"`
}

// fresh reports whether the cached body may be served without revalidating.
func (e cacheEntry) fresh(now time.Time) bool {
	return e.Expires > now.Unix()
}

// httpResult is what one HTTP exchange yields once the response body has been
// read and closed.
type httpResult struct {
	header http.Header
	status string
	body   []byte
	code   int
}

// fetchHTTP retrieves a document over HTTP, serving it from the disk cache while
// it is fresh and revalidating with If-None-Match / If-Modified-Since once it is
// not.
func (f *DefaultFetcher) fetchHTTP(u string) ([]byte, error) {
	entry, body, cached := f.readCache(u)
	if cached && entry.fresh(time.Now()) {
		return body, nil
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}

	if cached {
		setRevalidationHeaders(req, entry)
	}

	res, err := f.do(req)
	if err != nil {
		return nil, err
	}

	if res.code == http.StatusNotModified && cached {
		f.writeCache(u, res.header, body)

		return body, nil
	}

	if res.code != http.StatusOK {
		return nil, Errorf(SourceLine{File: u}, "fetching %s: HTTP %s", u, res.status)
	}

	f.writeCache(u, res.header, res.body)

	return res.body, nil
}

// do performs one HTTP exchange, reading the body to completion and closing it.
func (f *DefaultFetcher) do(req *http.Request) (httpResult, error) {
	resp, err := f.client.Do(req)
	if err != nil {
		return httpResult{}, err
	}

	if resp == nil || resp.Body == nil {
		return httpResult{}, Errorf(SourceLine{File: req.URL.String()}, "fetching %s: empty response", req.URL)
	}

	body, readErr := io.ReadAll(resp.Body)

	closeErr := resp.Body.Close()
	if readErr == nil {
		readErr = closeErr
	}

	return httpResult{header: resp.Header, status: resp.Status, body: body, code: resp.StatusCode}, readErr
}

// setRevalidationHeaders asks the server to answer 304 when nothing changed.
func setRevalidationHeaders(req *http.Request, entry cacheEntry) {
	if entry.ETag != "" {
		req.Header.Set("If-None-Match", entry.ETag)
	}

	if entry.LastModified != "" {
		req.Header.Set("If-Modified-Since", entry.LastModified)
	}
}

// cachePath returns the base path of the cache files for a URL.
func (f *DefaultFetcher) cachePath(u string) string {
	if f.cacheDir == "" {
		return ""
	}

	sum := sha256.Sum256([]byte(u))

	return filepath.Join(f.cacheDir, hex.EncodeToString(sum[:]))
}

// readCache loads a cached response, reporting whether one was available.
func (f *DefaultFetcher) readCache(u string) (cacheEntry, []byte, bool) {
	base := f.cachePath(u)
	if base == "" {
		return cacheEntry{}, nil, false
	}

	meta, err := os.ReadFile(base + cacheMetaSuffix)
	if err != nil {
		return cacheEntry{}, nil, false
	}

	var entry cacheEntry

	jsonErr := json.Unmarshal(meta, &entry)
	if jsonErr != nil || entry.URL != u {
		return cacheEntry{}, nil, false
	}

	body, err := os.ReadFile(base + cacheBodySuffix)
	if err != nil {
		return cacheEntry{}, nil, false
	}

	return entry, body, true
}

// writeCache stores a response body and its freshness metadata. Cache failures
// are not reported: an unwritable cache must not fail a fetch that succeeded.
func (f *DefaultFetcher) writeCache(u string, header http.Header, body []byte) {
	base := f.cachePath(u)
	if base == "" || noStore(header) {
		return
	}

	meta, err := json.Marshal(cacheEntry{
		URL:          u,
		ETag:         header.Get("ETag"),
		LastModified: header.Get("Last-Modified"),
		Expires:      time.Now().Add(maxAge(header)).Unix(),
	})
	if err != nil {
		return
	}

	mkErr := os.MkdirAll(filepath.Dir(base), cacheDirPerm)
	if mkErr != nil {
		return
	}

	bodyErr := os.WriteFile(base+cacheBodySuffix, body, cacheFilePerm)
	if bodyErr != nil {
		return
	}

	metaErr := os.WriteFile(base+cacheMetaSuffix, meta, cacheFilePerm)
	if metaErr != nil {
		return
	}
}

// noStore reports whether Cache-Control forbids caching the response at all.
func noStore(header http.Header) bool {
	directives := cacheControl(header)

	return directives["no-store"] || directives["private"]
}

// maxAge reports how long a response stays fresh: the Cache-Control max-age when
// there is one, nothing when the response must be revalidated, and a short
// default otherwise.
func maxAge(header http.Header) time.Duration {
	directives := cacheControl(header)
	if directives["no-cache"] || directives["must-revalidate"] {
		return 0
	}

	for directive := range directives {
		age, ok := strings.CutPrefix(directive, "max-age=")
		if !ok {
			continue
		}

		seconds, err := strconv.Atoi(age)
		if err != nil || seconds < 0 {
			return 0
		}

		return time.Duration(seconds) * time.Second
	}

	return defaultFreshness
}

// cacheControl parses the Cache-Control response header into a directive set.
func cacheControl(header http.Header) map[string]bool {
	out := make(map[string]bool)

	for _, value := range header.Values("Cache-Control") {
		for directive := range strings.SplitSeq(value, ",") {
			out[strings.ToLower(strings.TrimSpace(directive))] = true
		}
	}

	return out
}
