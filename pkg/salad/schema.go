package salad

import (
	"errors"
	"fmt"
	"io/fs"
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
	// SchemaDoc is the resolved schema document root, retained so that
	// MergeSchemas can re-collect the raw type definitions for a combined
	// flatten pass.
	SchemaDoc Node
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
		// Practically unreachable: Metaschema() is a process-wide memoized
		// singleton over the embedded metaschema, which always loads
		// successfully, so once any test in the process has resolved it this
		// branch can never be driven to fail again.
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
		// Dead: BuildContext's current implementation always returns a nil error.
		return nil, err
	}

	schema, err := Flatten(doc.Root, ctx)
	if err != nil {
		return nil, err
	}

	return &LoadedSchema{
		Schema:    schema,
		Context:   ctx,
		Loader:    NewLoader(WithContext(ctx)),
		Metadata:  doc.Metadata,
		SchemaDoc: doc.Root,
	}, nil
}

// LoadAndValidate loads an instance document, resolving its references, and
// validates it against the loaded schema.
//
// It is the analogue of schema.load_and_validate.
func (ls *LoadedSchema) LoadAndValidate(ref string, opts ...ValidateOption) (*Document, error) {
	if ls == nil || ls.Loader == nil || ls.Schema == nil {
		return nil, Errorf(
			SourceLine{
				File:  ref,
				Start: Position{Line: 0, Column: 0, Offset: 0},
				End:   Position{Line: 0, Column: 0, Offset: 0},
			},
			"the schema is not loaded, so %s cannot be validated against it",
			ref,
		)
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

// LoadExtensionSchema loads a Schema Salad schema document and builds its
// context, but does not flatten it. The extension's type definitions are meant
// to be flattened together with a base schema by MergeSchemas, because an
// extension typically extends types the base schema defines.
//
// The returned LoadedSchema has a nil Schema field and a nil Loader: both are
// built by MergeSchemas from the combined definitions.
func LoadExtensionSchema(ref string, opts ...LoaderOption) (*LoadedSchema, error) {
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

	return &LoadedSchema{
		Schema:    nil,
		Context:   ctx,
		Loader:    nil,
		Metadata:  doc.Metadata,
		SchemaDoc: doc.Root,
	}, nil
}

// ErrMissingSchemaDoc is returned by MergeSchemas when either schema's
// SchemaDoc field is nil.
var ErrMissingSchemaDoc = errors.New("both schemas must retain their SchemaDoc for merging")

// MergeSchemas combines two loaded schemas into one by concatenating their raw
// type definitions and re-flattening the combined set. The base schema's
// definitions come first, so the extension schema's types can extend them:
// an extension record declaring extends: [base#Process] resolves against the
// base's Process definition naturally, because both are in the same flattener
// name table.
//
// Both schemas must retain their SchemaDoc (the resolved document root), which
// LoadSchema populates automatically.
func MergeSchemas(base, ext *LoadedSchema) (*LoadedSchema, error) {
	if base.SchemaDoc == nil || ext.SchemaDoc == nil {
		return nil, ErrMissingSchemaDoc
	}

	baseDefs, berr := collectDefinitions(base.SchemaDoc)
	if berr != nil {
		return nil, berr
	}

	extDefs, eerr := collectDefinitions(ext.SchemaDoc)
	if eerr != nil {
		return nil, eerr
	}

	allDefs := make([]*MapNode, 0, len(baseDefs)+len(extDefs))
	allDefs = append(allDefs, baseDefs...)
	allDefs = append(allDefs, extDefs...)

	mergedCtx := MergeContexts(base.Context, ext.Context)

	items := make([]Node, len(allDefs))
	for i, d := range allDefs {
		items[i] = d
	}

	merged := NewSeqNode(
		SourceLine{
			File:  "",
			Start: Position{Line: 0, Column: 0, Offset: 0},
			End:   Position{Line: 0, Column: 0, Offset: 0},
		},
		items,
	)

	schema, ferr := Flatten(merged, mergedCtx)
	if ferr != nil {
		return nil, ferr
	}

	return &LoadedSchema{
		Schema:    schema,
		Context:   mergedCtx,
		Loader:    NewLoader(WithContext(mergedCtx)),
		Metadata:  base.Metadata,
		SchemaDoc: merged,
	}, nil
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
	return loadMetaschemaFrom(metaschemaFS)
}

// loadMetaschemaFrom loads and flattens the Schema Salad metaschema out of
// fsys, mounted at metaschemaMount.
//
// It is loadMetaschema's pure logic, factored out so that its two error
// branches can be exercised directly against a broken file system: metaschema
// = [sync.OnceValue](loadMetaschema) is a package-level, process-wide memoized
// singleton, so once any caller has resolved it against the real embedded
// metaschema, loadMetaschema itself can never be made to fail again within the
// same process.
func loadMetaschemaFrom(fsys fs.FS) *metaschemaLoad {
	ctx := saladBootstrapContext()

	loader := NewLoader(WithFetcher(NewFSFetcher(fsys, metaschemaMount)), WithContext(ctx))

	doc, err := loader.Load(metaschemaRef)
	if err != nil {
		return &metaschemaLoad{
			schema: nil,
			ctx:    nil,
			err:    fmt.Errorf("loading the built-in Schema Salad metaschema: %w", err),
		}
	}

	schema, err := Flatten(doc.Root, ctx)
	if err != nil {
		return &metaschemaLoad{
			schema: nil,
			ctx:    nil,
			err:    fmt.Errorf("flattening the built-in Schema Salad metaschema: %w", err),
		}
	}

	return &metaschemaLoad{schema: schema, ctx: ctx, err: nil}
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
	if e, ok := errors.AsType[*Error](err); ok {
		return e
	}

	return Errorf(
		SourceLine{
			File:  "",
			Start: Position{Line: 0, Column: 0, Offset: 0},
			End:   Position{Line: 0, Column: 0, Offset: 0},
		},
		"%s",
		err,
	)
}
