package salad

import (
	"errors"
	"fmt"
	"sync"
)

// metaschemaMount is the synthetic base URL the embedded Schema Salad metaschema
// is served from. Nothing is ever fetched from the network for it; the mount
// point exists so that the relative $import and $include references between the
// vendored files resolve against a stable absolute base.
const metaschemaMount = "file:///schema-salad/"

// metaschemaRef is the entry point of the embedded metaschema. Paths inside the
// embedded file system are rooted at "metaschema/", matching the on-disk layout.
const metaschemaRef = metaschemaMount + "metaschema/metaschema.yml"

// LoadedSchema bundles everything needed to load and validate instance documents
// against one schema. It groups what schema-salad's load_schema returns as a
// four-tuple into a single value.
type LoadedSchema struct {
	// Schema is the flattened schema type graph.
	Schema *Schema
	// Context is the context and vocabulary used when resolving instance documents.
	Context *Context
	// Loader is configured to resolve $import and $include in instance documents.
	// It is built with the schema's context and the default fetcher, because a
	// schema is often served from somewhere the documents validated against it are
	// not — an embedded file system, say. Replace it to load instance documents
	// through a fetcher of your own.
	Loader *Loader
	// Metadata holds the schema document's own $namespaces, $schemas and $base directives.
	Metadata *MapNode
}

// LoadSchema loads a Schema Salad schema document, validates it against the
// Salad metaschema, flattens extends and specialize, and returns a
// validator-ready Schema together with the Loader and Context configured for
// loading instance documents against it.
//
// LoadSchema followed by LoadedSchema.LoadAndValidate is the two-call flow every
// consumer of this package uses: load the schema once, then validate each
// document against it.
//
// The options configure how the schema document itself is fetched and where its
// relative references resolve from; they do not carry over to the returned
// Loader, since a schema is commonly served from somewhere its instance documents
// are not. WithContext is overridden: a schema document is read with the built-in
// Schema Salad vocabulary, which is the whole point of the metaschema.
//
// It is the analogue of schema.load_schema.
func LoadSchema(ref string, opts ...LoaderOption) (*LoadedSchema, error) {
	meta, metaCtx, err := Metaschema()
	if err != nil {
		return nil, err
	}

	doc, err := NewLoader(withContext(opts, metaCtx)...).Load(ref)
	if err != nil {
		return nil, err
	}

	invalid := meta.Validate(doc.Root, Strict(true))
	if invalid != nil {
		return nil, Group(nodeLoc(doc.Root), ref+" is not a valid Schema Salad schema, because", asError(invalid))
	}

	ctx, err := BuildContext(doc.Root, doc.Metadata)
	if err != nil {
		return nil, err
	}

	schema, err := Flatten(doc.Root, ctx)
	if err != nil {
		return nil, err
	}

	return &LoadedSchema{
		Schema:   schema,
		Context:  ctx,
		Loader:   NewLoader(WithContext(ctx)),
		Metadata: doc.Metadata,
	}, nil
}

// LoadAndValidate loads an instance document, resolving its references, and
// validates it against the loaded schema.
//
// It is the analogue of schema.load_and_validate.
func (ls *LoadedSchema) LoadAndValidate(ref string, opts ...ValidateOption) (*Document, error) {
	if ls == nil || ls.Loader == nil || ls.Schema == nil {
		return nil, Errorf(SourceLine{File: ref}, "the schema is not loaded, so %s cannot be validated against it", ref)
	}

	doc, err := ls.Loader.Load(ref)
	if err != nil {
		return nil, err
	}

	invalid := ls.Schema.Validate(doc.Root, opts...)
	if invalid != nil {
		return nil, Group(nodeLoc(doc.Root), ref+" is not valid, because", asError(invalid))
	}

	return doc, nil
}

// Flatten applies extends and specialize to already-resolved schema definitions
// and produces the flattened type graph.
//
// Inherited fields are merged base-first then own, preserving declaration order,
// and any re-specified field must narrow rather than widen the inherited type,
// which is checked with Schema.IsSubtype.
//
// It is exposed for tooling and testing; most callers use LoadSchema instead. It
// is the analogue of extend_and_specialize followed by make_avro_schema.
func Flatten(schemaDefs Node, ctx *Context) (*Schema, error) {
	s, err := flattenSchema(schemaDefs, ctx)
	if err != nil {
		return nil, err
	}

	return s, nil
}

// flattenSchema is Flatten with the package's own error type, so that the stages
// can group their diagnostics.
func flattenSchema(schemaDefs Node, ctx *Context) (*Schema, *Error) {
	defs, err := collectDefinitions(schemaDefs)
	if err != nil {
		return nil, err
	}

	f := newFlattener(defs, ctx)

	flat, err := f.definitions()
	if err != nil {
		return nil, err
	}

	b := newTypeBuilder(ctx)

	s, err := b.build(flat)
	if err != nil {
		return nil, err
	}

	narrowing := f.checkNarrowing(s, b)
	if narrowing != nil {
		return nil, narrowing
	}

	return s, nil
}

// Metaschema returns the built-in Schema Salad metaschema and its context, used
// to validate schema documents themselves. It is served from the metaschemaFS
// embedded file system, so it is never fetched at runtime; the result is
// memoized, since every LoadSchema call needs it.
//
// The context returned is the built-in Schema Salad vocabulary, which is what a
// schema document is read with: it is the term table that makes name, type,
// fields, symbols, extends and specialize mean what the specification says they
// mean, before any schema has told the loader anything.
//
// It is the analogue of schema.get_metaschema.
func Metaschema() (*Schema, *Context, error) {
	loaded := metaschema()

	return loaded.schema, loaded.ctx, loaded.err
}

// metaschemaLoad is one memoized load of the embedded metaschema.
type metaschemaLoad struct {
	schema *Schema
	ctx    *Context
	err    error
}

// metaschema loads the embedded metaschema once per process.
var metaschema = sync.OnceValue(loadMetaschema)

// loadMetaschema reads the embedded metaschema and flattens it.
func loadMetaschema() *metaschemaLoad {
	ctx := saladBootstrapContext()

	loader := NewLoader(WithFetcher(NewFSFetcher(metaschemaFS, metaschemaMount)), WithContext(ctx))

	doc, err := loader.Load(metaschemaRef)
	if err != nil {
		return &metaschemaLoad{err: fmt.Errorf("loading the built-in Schema Salad metaschema: %w", err)}
	}

	schema, err := Flatten(doc.Root, ctx)
	if err != nil {
		return &metaschemaLoad{err: fmt.Errorf("flattening the built-in Schema Salad metaschema: %w", err)}
	}

	return &metaschemaLoad{schema: schema, ctx: ctx}
}

// withContext returns opts with ctx appended, so that the context a loader is
// built with is the one this package derived rather than one a caller supplied.
func withContext(opts []LoaderOption, ctx *Context) []LoaderOption {
	out := make([]LoaderOption, 0, len(opts)+1)
	out = append(out, opts...)

	return append(out, WithContext(ctx))
}

// asError recovers the diagnostic tree from an error so that it can be grouped
// under a context line, falling back to a leaf when the error is not one of ours.
func asError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}

	return Errorf(SourceLine{}, "%s", err)
}
