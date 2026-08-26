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

// Fetcher retrieves raw document text for a URL.
//
// It is the seam between the loader and the outside world: callers inject a
// Fetcher to serve documents from memory (an embedded schema, a test fixture),
// from disk, or over the network, without the loader knowing which. It is the
// analogue of schema-salad's fetcher.Fetcher.
//
// Implementations must be safe for concurrent use.
type Fetcher interface {
	// FetchText returns the raw bytes of the document at docURL. The URL is
	// always one that Normalize produced.
	FetchText(docURL string) ([]byte, error)
	// Exists reports whether anything is present at docURL. Its one caller is
	// link checking, where a missing target is a validation error rather than a
	// fetch failure, so the question is whether the reference points at
	// something -- not whether that something is a document this loader could
	// parse. A directory is a legitimate link target even though it can never be
	// fetched as a document.
	Exists(docURL string) bool
	// Normalize resolves ref against base and returns the absolute, normalized
	// URL that identifies the document. It is the cache key the loader uses, so
	// two references that name the same document must normalize identically.
	Normalize(base, ref string) (string, error)
}

// FetcherOption configures a DefaultFetcher. Pass options to NewDefaultFetcher.
type FetcherOption func(*DefaultFetcher)

// WithCacheDir sets the directory the fetcher caches HTTP responses in. An empty
// directory disables caching.
func WithCacheDir(dir string) FetcherOption {
	return func(f *DefaultFetcher) { f.cacheDir = dir }
}

// WithHTTPClient sets the client the fetcher makes HTTP requests with.
func WithHTTPClient(c *http.Client) FetcherOption {
	return func(f *DefaultFetcher) { f.client = c }
}

// DefaultFetcher serves file:// and http(s):// URLs, caching HTTP responses on
// disk so that a schema fetched over the network is read once.
type DefaultFetcher struct {
	client   *http.Client
	cacheDir string
}

var _ Fetcher = (*DefaultFetcher)(nil)

// NewDefaultFetcher builds a fetcher for file:// and http(s):// URLs. Unless
// WithCacheDir says otherwise, HTTP responses are cached under the user's cache
// directory.
func NewDefaultFetcher(opts ...FetcherOption) *DefaultFetcher {
	f := &DefaultFetcher{
		client:   &http.Client{Timeout: httpTimeout},
		cacheDir: userCacheDir(),
	}

	for _, opt := range opts {
		opt(f)
	}

	return f
}

// defaultFetcher returns the process-wide fetcher a Loader uses when none was
// injected.
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
		return nil, Errorf(SourceLine{File: u}, "unsupported URL scheme %q", target.Scheme)
	}
}

// Exists reports whether anything is present at u.
//
// A directory counts: a link may legitimately name one, and only the caller
// knows whether it then intends to read it as a document. This mirrors
// schema-salad's check_exists, which is os.path.exists.
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

// FSFetcher serves documents from an [fs.FS] mounted at a synthetic base URL. It
// is how an embedded schema is loaded without touching the filesystem or the
// network.
type FSFetcher struct {
	fsys fs.FS
	base string
}

var _ Fetcher = (*FSFetcher)(nil)

// NewFSFetcher mounts fsys at base, which must be an absolute URL ending in a
// slash. A document URL under base maps to the path below it inside fsys.
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
		return nil, Errorf(SourceLine{File: u}, "%s is not under the mount point %s", u, f.base)
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

// Normalize resolves ref against base, defaulting to the mount point when there
// is no base.
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

// normalizeURL resolves ref against base and returns the absolute URL that
// identifies the document. A reference with no scheme is a filesystem path when
// there is no base to resolve it against.
func normalizeURL(base, ref string) (string, error) {
	return normalizeURLAbs(filepath.Abs, base, ref)
}

// normalizeURLAbs is normalizeURL with the [filepath.Abs] call taken as a
// parameter, so a test can make pathToURLAbs's otherwise-unreachable failure
// path (os.Getwd erroring) deterministic by passing a stub — without
// mutating the process's actual working directory or any state shared with
// other callers.
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

// pathToURLAbs turns a filesystem path into an absolute file:// URL, passing
// through anything that is already a URL. The [filepath.Abs] call is taken
// as a parameter; see [normalizeURLAbs].
func pathToURLAbs(abs func(string) (string, error), p string) (string, error) {
	if hasScheme(p) {
		return cleanURL(p)
	}

	resolved, err := abs(p)
	if err != nil {
		return "", err
	}

	u := url.URL{Scheme: schemeFile, Path: filepath.ToSlash(resolved)}

	return u.String(), nil
}

// cleanURL normalizes a URL so that two spellings of the same document produce
// the same cache key.
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

// dropFragment removes a URL's fragment identifier, which selects an object
// inside a document rather than a different document.
func dropFragment(u string) string {
	base, _, _ := strings.Cut(u, "#")

	return base
}

// userCacheDir returns the directory HTTP responses are cached in, or "" when
// the platform does not offer one.
func userCacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}

	return filepath.Join(dir, "cwl-go", "salad")
}
