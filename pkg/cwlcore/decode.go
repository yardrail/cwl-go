package cwlcore

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// Decoding a validated Schema Salad document tree into the typed CWL model.

// Vocabulary prefixes stripped from resolved discriminators.
const (
	cwlNamespace   = "https://w3id.org/cwl/cwl#"
	saladNamespace = "https://w3id.org/cwl/salad#"
	cwlPrefix      = "cwl:"
	saladPrefix    = "sld:"
)

// Entry-point identifiers for $graph documents.
const (
	graphMainName     = "main"
	graphMainFragment = "#" + graphMainName
	blankNodePrefix   = "_:"
)

// Load parses, validates, upgrades, and decodes src into a typed Process.
// For $graph documents, returns the "#main" process. A nil error guarantees a non-nil Process.
// baseURI is the resolution base for relative references. Pass Strict(true) for strict validation.
func Load(ctx context.Context, src []byte, baseURI string, opts ...LoadOption) (Process, error) {
	cfg := buildLoadConfig(opts)

	resolved, err := loadDocumentResolved(ctx, src, baseURI, cfg)
	if err != nil {
		return nil, err
	}

	return decodeAndResolve(ctx, resolved, "", cfg)
}

// LoadFile is [Load] reading from a file or URL. A fragment selects a $graph member.
func LoadFile(ctx context.Context, uri string, opts ...LoadOption) (Process, error) {
	cfg := buildLoadConfig(opts)

	resolved, err := loadFileDocumentResolved(ctx, uri, cfg)
	if err != nil {
		return nil, err
	}

	return decodeAndResolve(ctx, resolved, fragmentPart(uri), cfg)
}

// decodeAndResolve decodes a loaded document and follows external run references.
func decodeAndResolve(
	ctx context.Context,
	resolved resolvedDocument,
	fragment string,
	cfg *loadConfig,
) (Process, error) {
	process, err := decodeTargetWithSchema(resolved.doc, fragment, resolved.loaded)
	if err != nil {
		return nil, err
	}

	err = resolveExternalRuns(ctx, process, resolved.doc.BaseURI, fragment, cfg)
	if err != nil {
		return nil, err
	}

	return process, nil
}

// LoadDocument parses, validates, and upgrades src into a resolved salad document without decoding.
func LoadDocument(
	ctx context.Context,
	src []byte,
	baseURI string,
	opts ...LoadOption,
) (*salad.Document, error) {
	return loadDocument(ctx, src, baseURI, buildLoadConfig(opts))
}

func loadDocument(
	ctx context.Context,
	src []byte,
	baseURI string,
	cfg *loadConfig,
) (*salad.Document, error) {
	resolved, err := loadDocumentResolved(ctx, src, baseURI, cfg)
	if err != nil {
		return nil, err
	}

	return resolved.doc, nil
}

func loadDocumentResolved(
	ctx context.Context,
	src []byte,
	baseURI string,
	cfg *loadConfig,
) (resolvedDocument, error) {
	err := ctx.Err()
	if err != nil {
		return resolvedDocument{}, err
	}

	parsed, err := salad.Parse(baseURI, src)
	if err != nil {
		return resolvedDocument{}, err
	}

	return resolveDocument(parsed, baseURI, cfg)
}

// LoadFileDocument is [LoadDocument] reading from a file or URL. Fragments are ignored.
func LoadFileDocument(ctx context.Context, uri string, opts ...LoadOption) (*salad.Document, error) {
	return loadFileDocument(ctx, uri, buildLoadConfig(opts))
}

func loadFileDocument(ctx context.Context, uri string, cfg *loadConfig) (*salad.Document, error) {
	resolved, err := loadFileDocumentResolved(ctx, uri, cfg)
	if err != nil {
		return nil, err
	}

	return resolved.doc, nil
}

func loadFileDocumentResolved(ctx context.Context, uri string, cfg *loadConfig) (resolvedDocument, error) {
	err := ctx.Err()
	if err != nil {
		return resolvedDocument{}, err
	}

	url, src, err := fetchDocument(documentPart(uri))
	if err != nil {
		return resolvedDocument{}, err
	}

	parsed, err := salad.Parse(url, src)
	if err != nil {
		return resolvedDocument{}, err
	}

	return resolveDocument(parsed, url, cfg)
}

// resolvedDocument holds a document validated against its declared version's schema, then upgraded to v1.2.
type resolvedDocument struct {
	doc    *salad.Document
	loaded *salad.LoadedSchema
}

func resolveDocument(parsed salad.Node, baseURI string, cfg *loadConfig) (resolvedDocument, error) {
	version := DeclaredVersion(parsed)

	loaded, err := schemaFor(version)
	if err != nil {
		return resolvedDocument{}, err
	}

	for _, ext := range cfg.extensions {
		loaded, err = salad.MergeSchemas(loaded, ext)
		if err != nil {
			return resolvedDocument{}, fmt.Errorf("merging extension schema: %w", err)
		}
	}

	doc, err := loaded.Loader.LoadNode(parsed, baseURI)
	if err != nil {
		return resolvedDocument{}, err
	}

	invalid := loaded.Schema.Validate(doc.Root, cfg.validateOpts...)
	if invalid != nil {
		return resolvedDocument{}, invalidDocument(baseURI, version, invalid)
	}

	return resolvedDocument{doc: Upgrade(doc, version), loaded: loaded}, nil
}

// invalidDocument wraps a validation error with the document URI and CWL version.
func invalidDocument(baseURI, version string, invalid error) error {
	where := salad.SourceLine{
		File:  baseURI,
		Start: salad.Position{Line: 0, Column: 0, Offset: 0},
		End:   salad.Position{Line: 0, Column: 0, Offset: 0},
	}
	heading := fmt.Sprintf("%s is not valid CWL %s, because", baseURI, cmp.Or(version, CWLVersionV12))

	if nested, ok := errors.AsType[*salad.Error](invalid); ok {
		return salad.Group(where, heading, nested)
	}

	return salad.Errorf(where, "%s %s", heading, invalid)
}

// documentFetcher is the process-wide fetcher for instance documents.
var documentFetcher = sync.OnceValue(func() salad.Fetcher { return salad.NewDefaultFetcher() })

// fetchDocument resolves a document reference and returns the absolute URL and raw bytes.
func fetchDocument(ref string) (_ string, _ []byte, _ error) {
	fetcher := documentFetcher()

	url, err := fetcher.Normalize("", ref)
	if err != nil {
		return "", nil, salad.Errorf(
			salad.SourceLine{
				File:  "",
				Start: salad.Position{Line: 0, Column: 0, Offset: 0},
				End:   salad.Position{Line: 0, Column: 0, Offset: 0},
			},
			"cannot resolve document reference %q: %s",
			ref,
			err,
		)
	}

	src, err := fetcher.FetchText(url)
	if err != nil {
		return url, nil, salad.Errorf(
			salad.SourceLine{
				File:  url,
				Start: salad.Position{Line: 0, Column: 0, Offset: 0},
				End:   salad.Position{Line: 0, Column: 0, Offset: 0},
			},
			"cannot fetch %s: %s",
			url,
			err,
		)
	}

	return url, src, nil
}

// LoadedSchema returns the embedded CWL v1.2 schema with its loader and context.
func LoadedSchema() (*salad.LoadedSchema, error) {
	return cwlSchemaV12()
}

// documentPart strips the fragment identifier from a URI.
func documentPart(uri string) string {
	base, _, _ := strings.Cut(uri, "#")

	return base
}

// fragmentPart returns the fragment identifier from a URI, or "".
func fragmentPart(uri string) string {
	_, fragment, _ := strings.Cut(uri, "#")

	return fragment
}

// Decode turns a validated salad document into a typed Process.
// Local run references are linked; external ones require [Load] or [LoadFile].
func Decode(doc *salad.Document) (Process, error) {
	if doc == nil {
		return nil, salad.Errorf(
			salad.SourceLine{
				File:  "",
				Start: salad.Position{Line: 0, Column: 0, Offset: 0},
				End:   salad.Position{Line: 0, Column: 0, Offset: 0},
			},
			"there is no document to decode",
		)
	}

	nodes, isGraph := graphNodes(doc.Root)

	entry := doc.Root

	if isGraph {
		main, err := selectMain(nodes, doc.BaseURI)
		if err != nil {
			return nil, err
		}

		entry = main
	}

	return decodeLinked(nodes, entry)
}

// decodeLinked decodes all processes, links local run references, and returns the entry process.
func decodeLinked(nodes []salad.Node, entry salad.Node, opts ...decoderOption) (Process, error) {
	d := newDecoder(opts...)

	decoded := d.decodeProcesses(nodes, entry)
	if decoded.selected == nil {
		return nil, d.errOr(nodeLoc(entry), "the node could not be decoded as a process")
	}

	err := d.err()
	if err != nil {
		return nil, err
	}

	linkLocalRuns(decoded.procs)

	err = checkRunCycles(decoded.selected)
	if err != nil {
		return nil, err
	}

	return decoded.selected, nil
}

// decodedDocument holds all decoded processes and the selected entry process.
type decodedDocument struct {
	selected Process
	procs    []Process
}

// decodeProcesses decodes all nodes and identifies the entry process.
func (d *decoder) decodeProcesses(nodes []salad.Node, entry salad.Node) decodedDocument {
	decoded := decodedDocument{selected: nil, procs: make([]Process, 0, len(nodes))}

	for _, node := range nodes {
		process := d.process(node)
		if process == nil {
			continue
		}

		decoded.procs = append(decoded.procs, process)

		if node == entry {
			decoded.selected = process
		}
	}

	return decoded
}

// DecodeNode decodes a single process node (document root, $graph entry, or inline run target).
func DecodeNode(node salad.Node) (Process, error) {
	d := newDecoder()

	process := d.process(node)
	if process == nil {
		return nil, d.errOr(nodeLoc(node), "the node could not be decoded as a process")
	}

	err := d.err()
	if err != nil {
		return nil, err
	}

	return process, nil
}

// DecodeAll decodes every top-level process, keyed by identifier.
func DecodeAll(doc *salad.Document) (map[string]Process, error) {
	if doc == nil {
		return nil, salad.Errorf(
			salad.SourceLine{
				File:  "",
				Start: salad.Position{Line: 0, Column: 0, Offset: 0},
				End:   salad.Position{Line: 0, Column: 0, Offset: 0},
			},
			"there is no document to decode",
		)
	}

	nodes, _ := graphNodes(doc.Root)
	d := newDecoder()
	decoded := d.decodeProcesses(nodes, nil)

	err := d.err()
	if err != nil {
		return nil, err
	}

	linkLocalRuns(decoded.procs)

	out := make(map[string]Process, len(decoded.procs))

	for _, process := range decoded.procs {
		err = checkRunCycles(process)
		if err != nil {
			return nil, err
		}

		out[process.Base().ID] = process
	}

	return out, nil
}

// Schema returns the embedded CWL v1.2 salad schema and its vendored version tag.
// Nil schema means the embedded snapshot is corrupt.
func Schema() (_ *salad.Schema, _ string) {
	loaded, err := cwlSchemaV12()

	return schemaOrNil(loaded, err), SchemaVersion()
}

// schemaOrNil returns loaded's schema, or nil on error.
func schemaOrNil(loaded *salad.LoadedSchema, err error) *salad.Schema {
	if err != nil {
		return nil
	}

	return loaded.Schema
}

// Compile-time assertion that Schema keeps its signature.
var _ func() (*salad.Schema, string) = Schema

// graphNodes returns the process nodes of a document root and whether it is a $graph.
func graphNodes(root salad.Node) ([]salad.Node, bool) {
	if seq, ok := salad.AsSeq(root); ok {
		return seq.Items(), true
	}

	if m, ok := salad.AsMap(root); ok {
		if seq, isSeq := salad.AsSeq(fieldNode(m, keyGraph)); isSeq {
			return seq.Items(), true
		}
	}

	return []salad.Node{root}, false
}

// selectMain picks the "#main" process from a $graph.
func selectMain(nodes []salad.Node, baseURI string) (salad.Node, error) {
	found := make([]string, 0, len(nodes))

	for _, node := range nodes {
		m, ok := salad.AsMap(node)
		if !ok {
			continue
		}

		id := lenientText(m, keyID)
		if isMainID(id) {
			return node, nil
		}

		found = append(found, strconv.Quote(id))
	}

	return nil, salad.Errorf(
		salad.SourceLine{
			File:  baseURI,
			Start: salad.Position{Line: 0, Column: 0, Offset: 0},
			End:   salad.Position{Line: 0, Column: 0, Offset: 0},
		},
		"the document declares no process with an id of %q or %q, so there is nothing to run; it declares %s",
		graphMainFragment,
		graphMainName,
		joinOrNone(found),
	)
}

// decodeFragment decodes the process named by a URI fragment.
func decodeFragment(doc *salad.Document, fragment string, opts ...decoderOption) (Process, error) {
	nodes, _ := graphNodes(doc.Root)

	selected, err := selectFragment(nodes, fragment, doc.BaseURI)
	if err != nil {
		return nil, err
	}

	return decodeLinked(nodes, selected, opts...)
}

// selectFragment picks the node matching a fragment identifier.
func selectFragment(nodes []salad.Node, fragment, baseURI string) (salad.Node, error) {
	found := make([]string, 0, len(nodes))

	for _, node := range nodes {
		m, ok := salad.AsMap(node)
		if !ok {
			continue
		}

		id := lenientText(m, keyID)
		if idFragment(id) == fragment {
			return node, nil
		}

		found = append(found, strconv.Quote(id))
	}

	return nil, salad.Errorf(
		salad.SourceLine{
			File:  baseURI,
			Start: salad.Position{Line: 0, Column: 0, Offset: 0},
			End:   salad.Position{Line: 0, Column: 0, Offset: 0},
		},
		"the document declares no object with the identifier %q; it declares %s",
		"#"+fragment,
		joinOrNone(found),
	)
}

// isMainID reports whether id names the graph's entry point.
func isMainID(id string) bool {
	return idFragment(id) == graphMainName
}

// idFragment returns the part after "#", or the whole string if no "#" is present.
func idFragment(id string) string {
	before, after, ok := strings.Cut(id, "#")
	if !ok {
		return before
	}

	return after
}

// joinOrNone joins identifiers for error messages, returning "none" if empty.
func joinOrNone(ids []string) string {
	if len(ids) == 0 {
		return "none"
	}

	return strings.Join(ids, ", ")
}

// process decodes one process node, dispatching on its class.
func (d *decoder) process(node salad.Node) Process {
	m := d.mapping(node, "a process")
	if m == nil {
		return nil
	}

	class := d.text(m, keyClass)
	if class == "" {
		d.failf(m.Loc(), "a process must declare a class")

		return nil
	}

	switch shortName(class) {
	case ClassCommandLineTool:
		return d.commandLineTool(m)
	case ClassWorkflow:
		return d.workflow(m)
	case ClassExpressionTool:
		return d.expressionTool(m)
	case ClassOperation:
		return d.operation(m)
	default:
		if d.extendsWorkflow(class) {
			return d.extensionWorkflow(m, class)
		}

		return d.rawProcess(m, class)
	}
}

// shortName strips CWL/salad namespace prefixes from a resolved discriminator.
func shortName(name string) string {
	for _, prefix := range []string{cwlNamespace, saladNamespace, cwlPrefix, saladPrefix} {
		if rest, ok := strings.CutPrefix(name, prefix); ok {
			return rest
		}
	}

	return name
}
