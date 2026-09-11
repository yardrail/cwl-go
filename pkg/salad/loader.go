package salad

import (
	"fmt"
	"sync"
)

// Processing directives.
const (
	dirImport     = "$import"
	dirInclude    = "$include"
	dirBase       = "$base"
	dirNamespaces = "$namespaces"
	dirSchemas    = "$schemas"
	dirGraph      = "$graph"
)

// unimplemented builds a panic message for unimplemented entry points.
func unimplemented(stream, symbol string, args ...any) string {
	return fmt.Sprintf("%s: %s is not implemented yet (called with %v)", stream, symbol, args)
}

// loaderConfig holds the options a Loader was constructed with.
type loaderConfig struct {
	fetcher       Fetcher
	context       *Context
	baseURL       string
	skipLinkCheck bool
}

// LoaderOption configures a Loader. Pass options to NewLoader.
type LoaderOption func(*loaderConfig)

// WithFetcher sets the document fetcher. Defaults to file:// and http(s)://.
func WithFetcher(f Fetcher) LoaderOption {
	return func(c *loaderConfig) { c.fetcher = f }
}

// WithBaseURL sets the base URL for relative references.
func WithBaseURL(base string) LoaderOption {
	return func(c *loaderConfig) { c.baseURL = base }
}

// WithSkipLinkCheck disables link validation.
func WithSkipLinkCheck(skip bool) LoaderOption {
	return func(c *loaderConfig) { c.skipLinkCheck = skip }
}

// WithContext sets the term table for identifier, link and vocabulary resolution.
func WithContext(ctx *Context) LoaderOption {
	return func(c *loaderConfig) { c.context = ctx }
}

// Loader resolves $import/$include references and caches parsed documents.
// Safe for concurrent use.
type Loader struct {
	cfg    *loaderConfig
	parsed map[string]Node
	mu     sync.Mutex
}

// NewLoader constructs a Loader configured by opts.
func NewLoader(opts ...LoaderOption) *Loader {
	cfg := &loaderConfig{fetcher: nil, context: nil, baseURL: "", skipLinkCheck: false}
	for _, opt := range opts {
		opt(cfg)
	}

	return &Loader{cfg: cfg, parsed: make(map[string]Node), mu: sync.Mutex{}}
}

// Context returns the term table. Never nil.
func (l *Loader) Context() *Context {
	if l.cfg.context == nil {
		return newContext()
	}

	return l.cfg.context
}

// Fetcher returns the configured fetcher, or the shared default.
func (l *Loader) Fetcher() Fetcher {
	if l.cfg.fetcher == nil {
		return defaultFetcher()
	}

	return l.cfg.fetcher
}

// Load fetches, parses and fully resolves the document at ref.
func (l *Loader) Load(ref string) (*Document, error) {
	docURL, err := l.Fetcher().Normalize(l.cfg.baseURL, ref)
	if err != nil {
		return nil, Errorf(
			SourceLine{
				File:  "",
				Start: Position{Line: 0, Column: 0, Offset: 0},
				End:   Position{Line: 0, Column: 0, Offset: 0},
			},
			"cannot resolve document reference %q: %s",
			ref,
			err,
		)
	}

	r := l.newResolver()

	root, err := r.loadReference(
		docURL,
		SourceLine{
			File:  "",
			Start: Position{Line: 0, Column: 0, Offset: 0},
			End:   Position{Line: 0, Column: 0, Offset: 0},
		},
		l.Context(),
		true,
	)
	if err != nil {
		return nil, err
	}

	return r.finish(root, docURL)
}

// LoadNode resolves references in an already-parsed in-memory document.
func (l *Loader) LoadNode(doc Node, baseURL string) (*Document, error) {
	base := baseURL
	if base == "" {
		base = l.cfg.baseURL
	}

	r := l.newResolver()

	root, err := r.resolve(doc, scope{ctx: l.Context(), base: base, fileBase: base, field: "", top: true})
	if err != nil {
		return nil, err
	}

	return r.finish(root, base)
}

// parse fetches and parses a document, memoizing by normalized URL.
func (l *Loader) parse(docURL string) (Node, error) {
	l.mu.Lock()
	cached, ok := l.parsed[docURL]
	l.mu.Unlock()

	if ok {
		return cached, nil
	}

	text, err := l.Fetcher().FetchText(docURL)
	if err != nil {
		return nil, Errorf(
			SourceLine{
				File:  docURL,
				Start: Position{Line: 0, Column: 0, Offset: 0},
				End:   Position{Line: 0, Column: 0, Offset: 0},
			},
			"cannot fetch %s: %s",
			docURL,
			err,
		)
	}

	node, err := Parse(docURL, text)
	if err != nil {
		return nil, err
	}

	l.mu.Lock()
	l.parsed[docURL] = node
	l.mu.Unlock()

	return node, nil
}
