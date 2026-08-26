package salad

import (
	"io/fs"
	"net/http"
)

// Compile-time assertions that pin the public surface other packages code
// against. The bodies of these entry points are still unimplemented, so the
// assertions are written as function values rather than calls: they fail the
// build the moment a signature drifts, without invoking anything.
var (
	_ func(...LoaderOption) *Loader                                     = NewLoader
	_ func(*Loader, string) (*Document, error)                          = (*Loader).Load
	_ func(*Loader, Node, string) (*Document, error)                    = (*Loader).LoadNode
	_ func(Fetcher) LoaderOption                                        = WithFetcher
	_ func(string) LoaderOption                                         = WithBaseURL
	_ func(bool) LoaderOption                                           = WithSkipLinkCheck
	_ func(*Context) LoaderOption                                       = WithContext
	_ func(*Loader) *Context                                            = (*Loader).Context
	_ func(*Loader) Fetcher                                             = (*Loader).Fetcher
	_ func(...FetcherOption) *DefaultFetcher                            = NewDefaultFetcher
	_ func(string) FetcherOption                                        = WithCacheDir
	_ func(*http.Client) FetcherOption                                  = WithHTTPClient
	_ func(fs.FS, string) *FSFetcher                                    = NewFSFetcher
	_ func(Node, *MapNode) (*Context, error)                            = BuildContext
	_ func(*Context, string, string) string                             = (*Context).ExpandURL
	_ func(*Context, string, string) string                             = (*Context).ExpandIdentifier
	_ func(*Context, string, string) string                             = (*Context).ExpandVocabTerm
	_ func(*Context, string) (*TermDef, bool)                           = (*Context).Term
	_ func(*Context, string) string                                     = (*Context).Shortname
	_ func(*Context) map[string]string                                  = (*Context).Vocab
	_ func(*Context) map[string]string                                  = (*Context).Namespaces
	_ func(*Context) []string                                           = (*Context).Schemas
	_ func(string, ...LoaderOption) (*LoadedSchema, error)              = LoadSchema
	_ func(*LoadedSchema, string, ...ValidateOption) (*Document, error) = (*LoadedSchema).LoadAndValidate
	_ func(Node, *Context) (*Schema, error)                             = Flatten
	_ func() (*Schema, *Context, error)                                 = Metaschema
	_ func([]Type) *Schema                                              = NewSchema
	_ func(*Schema, string) (Type, bool)                                = (*Schema).Type
	_ func(*Schema) []string                                            = (*Schema).Names
	_ func(*Schema) []*RecordType                                       = (*Schema).DocumentRoots
	_ func(*Schema, Type, Type) bool                                    = (*Schema).IsSubtype
	_ func(*Schema, Node, ...ValidateOption) error                      = (*Schema).Validate
	_ func(*Schema, string, Node, ...ValidateOption) error              = (*Schema).ValidateAgainst
	_ func(bool) ValidateOption                                         = Strict
	_ func(bool) ValidateOption                                         = StrictForeign
	_ func(string, []byte) (Node, error)                                = Parse
	_ func(Node) any                                                    = ToAny
	_ func(any, SourceLine) (Node, error)                               = FromAny
)
