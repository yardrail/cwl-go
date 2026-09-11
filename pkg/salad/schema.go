package salad

import (
	"errors"
	"fmt"
	"io/fs"
	"sync"
)

// metaschemaMount is the synthetic base URL for the embedded metaschema.
const metaschemaMount = "file:///schema-salad/"

// metaschemaRef is the entry point of the embedded metaschema.
const metaschemaRef = metaschemaMount + "metaschema/metaschema.yml"

// LoadedSchema bundles everything needed to load and validate instance documents.
type LoadedSchema struct {
	// Schema is the flattened schema type graph.
	Schema *Schema
	// Context is the context and vocabulary used when resolving instance documents.
	Context *Context
	// Loader resolves $import/$include in instance documents.
	Loader *Loader
	// Metadata holds the schema document's own $namespaces, $schemas and $base directives.
	Metadata *MapNode
	// SchemaDoc is the resolved schema document root, retained for [MergeSchemas].
	SchemaDoc Node
}

// LoadSchema loads, validates, and flattens a Schema Salad schema document.
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

// LoadAndValidate loads and validates an instance document against the schema.
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

// LoadExtensionSchema loads a schema without flattening, for use with [MergeSchemas].
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

// MergeSchemas combines two schemas by concatenating definitions and re-flattening.
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

// Flatten applies extends/specialize to schema definitions and produces the type graph.
func Flatten(schemaDefs Node, ctx *Context) (*Schema, error) {
	s, err := flattenSchema(schemaDefs, ctx)
	if err != nil {
		return nil, err
	}

	return s, nil
}

// flattenSchema is Flatten with the internal error type.
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

// Metaschema returns the memoized built-in Schema Salad metaschema and its context.
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

// loadMetaschemaFrom loads and flattens the metaschema from fsys. Factored out for testing.
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

// withContext appends ctx to opts, overriding any caller-supplied context.
func withContext(opts []LoaderOption, ctx *Context) []LoaderOption {
	out := make([]LoaderOption, 0, len(opts)+1)
	out = append(out, opts...)

	return append(out, WithContext(ctx))
}

// asError recovers the *Error tree, falling back to a leaf.
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
