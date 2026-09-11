package salad

import (
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// URL schemes the default fetcher serves.
const (
	schemeFile  = "file"
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

// httpTimeout bounds one HTTP fetch.
const httpTimeout = 30 * time.Second

// errEmptyReference is returned when a document reference is the empty string.
var errEmptyReference = errors.New("empty document reference")

// Fetcher retrieves raw document text for a URL. Must be safe for concurrent use.
type Fetcher interface {
	// FetchText returns the raw bytes of the document at docURL. The URL is
	// always one that Normalize produced.
	FetchText(docURL string) ([]byte, error)
	// Exists reports whether anything is present at docURL.
	Exists(docURL string) bool
	// Normalize resolves ref against base and returns the canonical document URL.
	Normalize(base, ref string) (string, error)
}

// FetcherOption configures a DefaultFetcher. Pass options to NewDefaultFetcher.
type FetcherOption func(*DefaultFetcher)

// WithCacheDir sets the HTTP response cache directory. Empty disables caching.
func WithCacheDir(dir string) FetcherOption {
	return func(f *DefaultFetcher) { f.cacheDir = dir }
}

// WithHTTPClient sets the client the fetcher makes HTTP requests with.
func WithHTTPClient(c *http.Client) FetcherOption {
	return func(f *DefaultFetcher) { f.client = c }
}

// DefaultFetcher serves file:// and http(s):// URLs with disk-cached HTTP responses.
type DefaultFetcher struct {
	client   *http.Client
	cacheDir string
}

var _ Fetcher = (*DefaultFetcher)(nil)

// NewDefaultFetcher builds a fetcher for file:// and http(s):// URLs.
func NewDefaultFetcher(opts ...FetcherOption) *DefaultFetcher {
	f := &DefaultFetcher{
		client:   &http.Client{Transport: nil, CheckRedirect: nil, Jar: nil, Timeout: httpTimeout},
		cacheDir: userCacheDir(),
	}

	for _, opt := range opts {
		opt(f)
	}

	return f
}

// defaultFetcher returns the process-wide shared fetcher.
var defaultFetcher = sync.OnceValue(func() Fetcher { return NewDefaultFetcher() })

// FetchText reads the document at u.
func (f *DefaultFetcher) FetchText(u string) ([]byte, error) {
	target, err := url.Parse(dropFragment(u))
	if err != nil {
		return nil, err
	}

	switch target.Scheme {
	case schemeFile:
		return os.ReadFile(filepath.FromSlash(target.Path))
	case schemeHTTP, schemeHTTPS:
		return f.fetchHTTP(target.String())
	default:
		return nil, Errorf(
			SourceLine{
				File:  u,
				Start: Position{Line: 0, Column: 0, Offset: 0},
				End:   Position{Line: 0, Column: 0, Offset: 0},
			},
			"unsupported URL scheme %q",
			target.Scheme,
		)
	}
}

// Exists reports whether anything is present at u.
func (f *DefaultFetcher) Exists(u string) bool {
	target, err := url.Parse(dropFragment(u))
	if err != nil {
		return false
	}

	switch target.Scheme {
	case schemeFile:
		_, statErr := os.Stat(filepath.FromSlash(target.Path))

		return statErr == nil
	case schemeHTTP, schemeHTTPS:
		_, fetchErr := f.fetchHTTP(target.String())

		return fetchErr == nil
	default:
		return false
	}
}

// Normalize resolves ref against base into an absolute, normalized URL.
func (f *DefaultFetcher) Normalize(base, ref string) (string, error) {
	return normalizeURL(base, ref)
}

// FSFetcher serves documents from an [fs.FS] mounted at a synthetic base URL.
type FSFetcher struct {
	fsys fs.FS
	base string
}

var _ Fetcher = (*FSFetcher)(nil)

// NewFSFetcher mounts fsys at base (must be an absolute URL ending in "/").
func NewFSFetcher(fsys fs.FS, base string) *FSFetcher {
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}

	return &FSFetcher{fsys: fsys, base: base}
}

// FetchText reads the document at u out of the mounted file system.
func (f *FSFetcher) FetchText(u string) ([]byte, error) {
	name, ok := f.path(u)
	if !ok {
		return nil, Errorf(
			SourceLine{
				File:  u,
				Start: Position{Line: 0, Column: 0, Offset: 0},
				End:   Position{Line: 0, Column: 0, Offset: 0},
			},
			"%s is not under the mount point %s",
			u,
			f.base,
		)
	}

	return fs.ReadFile(f.fsys, name)
}

// Exists reports whether the mounted file system holds anything at u.
func (f *FSFetcher) Exists(u string) bool {
	name, ok := f.path(u)
	if !ok {
		return false
	}

	_, err := fs.Stat(f.fsys, name)

	return err == nil
}

// Normalize resolves ref against base, defaulting to the mount point.
func (f *FSFetcher) Normalize(base, ref string) (string, error) {
	if base == "" {
		base = f.base
	}

	return normalizeURL(base, ref)
}

// path maps a document URL to a path inside the mounted file system.
func (f *FSFetcher) path(u string) (string, bool) {
	rest, ok := strings.CutPrefix(dropFragment(u), f.base)
	if !ok || rest == "" {
		return "", false
	}

	return rest, true
}

// normalizeURL resolves ref against base into an absolute URL.
func normalizeURL(base, ref string) (string, error) {
	return normalizeURLAbs(filepath.Abs, base, ref)
}

// normalizeURLAbs is normalizeURL with an injectable filepath.Abs for testing.
func normalizeURLAbs(abs func(string) (string, error), base, ref string) (string, error) {
	if ref == "" {
		return "", errEmptyReference
	}

	if hasScheme(ref) {
		return cleanURL(ref)
	}

	if base == "" {
		return pathToURLAbs(abs, ref)
	}

	baseURL, err := pathToURLAbs(abs, base)
	if err != nil {
		return "", err
	}

	return cleanURL(resolveReference(baseURL, ref))
}

// pathToURLAbs turns a filesystem path into an absolute file:// URL.
func pathToURLAbs(abs func(string) (string, error), p string) (string, error) {
	if hasScheme(p) {
		return cleanURL(p)
	}

	resolved, err := abs(p)
	if err != nil {
		return "", err
	}

	u := url.URL{
		Scheme:      schemeFile,
		Opaque:      "",
		User:        nil,
		Host:        "",
		Path:        filepath.ToSlash(resolved),
		RawPath:     "",
		RawQuery:    "",
		Fragment:    "",
		RawFragment: "",
		ForceQuery:  false,
		OmitHost:    false,
	}

	return u.String(), nil
}

// cleanURL normalizes a URL for consistent cache keys.
func cleanURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}

	if u.Path != "" {
		cleaned := path.Clean(u.Path)
		if strings.HasSuffix(u.Path, "/") && !strings.HasSuffix(cleaned, "/") {
			cleaned += "/"
		}

		u.Path = cleaned
		u.RawPath = ""
	}

	return u.String(), nil
}

// dropFragment removes a URL's fragment identifier.
func dropFragment(u string) string {
	base, _, _ := strings.Cut(u, "#")

	return base
}

// userCacheDir returns the HTTP cache directory, or "" if unavailable.
func userCacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}

	return filepath.Join(dir, "cwl-go", "salad")
}
