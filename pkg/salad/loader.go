package salad

import (
	"fmt"
	"sync"
)

// Processing directives. $base, $namespaces, $schemas and $graph make up the
// explicit context of a document; $import and $include splice other resources
// into it. Every other directive beginning with "$" must be ignored.
const (
	dirImport     = "$import"
	dirInclude    = "$include"
	dirBase       = "$base"
	dirNamespaces = "$namespaces"
	dirSchemas    = "$schemas"
	dirGraph      = "$graph"
)

// unimplemented builds the panic message used by the entry points that are
// declared here to freeze the package's public surface but are filled in by a
// later implementation stream.
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

// WithFetcher makes the loader retrieve documents through f. Without it, the
// loader uses a default fetcher that reads file:// and http(s):// URLs.
func WithFetcher(f Fetcher) LoaderOption {
	return func(c *loaderConfig) { c.fetcher = f }
}

// WithBaseURL sets the base URL that relative references in the root document
// resolve against. Without it, the base URL is the reference passed to Load.
func WithBaseURL(base string) LoaderOption {
	return func(c *loaderConfig) { c.baseURL = base }
}

// WithSkipLinkCheck opts out of link validation, which is otherwise performed
// and is fatal when a link cannot be resolved.
func WithSkipLinkCheck(skip bool) LoaderOption {
	return func(c *loaderConfig) { c.skipLinkCheck = skip }
}

// WithContext gives the loader the term table that drives identifier, link,
// vocabulary and identifier-map resolution. Without it a loader still resolves
// $import and $include and the explicit context directives, but knows no
// vocabulary, so no field is treated as an identifier or a link.
func WithContext(ctx *Context) LoaderOption {
	return func(c *loaderConfig) { c.context = ctx }
}

// Loader resolves $import and $include references and caches parsed documents by
// normalized URL. It is the analogue of schema-salad's ref_resolver.Loader.
//
// A Loader keeps a normalized document cache and an explicit in-progress set, so
// an import cycle produces a clear error rather than being silently deduplicated
// by the cache.
//
// A Loader is safe for concurrent use.
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

// Context returns the term table the loader resolves documents against. It is
// never nil; a loader constructed without WithContext reports an empty context.
func (l *Loader) Context() *Context {
	if l.cfg.context == nil {
		return newContext()
	}

	return l.cfg.context
}

// Fetcher returns the fetcher the loader retrieves documents through, which is
// the shared default fetcher when none was configured.
func (l *Loader) Fetcher() Fetcher {
	if l.cfg.fetcher == nil {
		return defaultFetcher()
	}

	return l.cfg.fetcher
}

// Load fetches, parses and fully resolves the document at ref.
//
// $import references are parsed and spliced in as fully-resolved subtrees;
// $include references are inserted verbatim as string scalars and are never
// re-parsed. Source lines survive splicing, so an error in an imported document
// still points at the imported file.
//
// It is the analogue of ref_resolver.Loader.resolve_ref.
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

// LoadNode resolves references within an already-parsed in-memory document,
// without fetching a root. baseURL is what relative references resolve against.
//
// It is the analogue of ref_resolver.Loader.resolve_all.
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

// parse fetches and parses a document, memoizing the result by normalized URL so
// that a document imported from several places is read and parsed once.
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
