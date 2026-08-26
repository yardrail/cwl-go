package cwlcore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// Turning a validated Schema Salad document tree into the typed model.
//
// The split between this package and pkg/salad is deliberate: salad parses,
// resolves and validates, and knows nothing about CWL; decoding here takes the
// tree salad produced and reads the typed model out of it. Nothing below
// re-validates the document, and nothing below consults a registry of extension
// classes — a class this package has no type for becomes a RawProcess carrying
// the node it was decoded from, and recognizing it is the caller's business.

// The vocabulary prefixes a resolved discriminator may carry. pkg/salad maps a
// resolved IRI back to its vocabulary term wherever the schema declares one, so
// a core class normally arrives already spelled short; these are stripped anyway
// so that a tree resolved by some other means decodes identically.
const (
	cwlNamespace   = "https://w3id.org/cwl/cwl#"
	saladNamespace = "https://w3id.org/cwl/salad#"
	cwlPrefix      = "cwl:"
	saladPrefix    = "sld:"
)

// The identifier a $graph document gives the process to execute, in the two
// spellings the specification's generic execution step 3 accepts.
const (
	graphMainName     = "main"
	graphMainFragment = "#" + graphMainName
	blankNodePrefix   = "_:"
)

// The reference the embedded CWL v1.2 schema is loaded through. The mount point
// is a synthetic file URL so that the relative $import references between the
// vendored documents resolve without touching the real filesystem.
const (
	schemaMountURL  = "file:///cwl-go/cwl-v1.2/"
	schemaRootRef   = "schema/CommonWorkflowLanguage.yml"
	schemaSourceURL = schemaMountURL + schemaRootRef
)

// Load parses src, validates it against the embedded CWL v1.2 schema, and
// decodes it into a typed Process.
//
// When the document holds several processes under $graph, Load returns the one
// the specification's generic execution step 3 selects: the process whose id is
// "#main" or "main". A $graph document with no such process is an error, because
// there is nothing to execute; use DecodeAll to reach every process in a graph,
// or LoadFile with a fragment to address one of them by name.
//
// baseURI is what relative references inside the document resolve against, and
// is normally the URL src was read from. A nil error guarantees a non-nil
// Process.
//
// Validation is permissive by default: a field the schema does not declare is an
// advisory rather than a failure, because the loader has already done the strict
// part of the job by resolving every link. Pass salad.Strict(true) to turn those
// advisories into errors.
func Load(ctx context.Context, src []byte, baseURI string, opts ...salad.ValidateOption) (Process, error) {
	doc, err := LoadDocument(ctx, src, baseURI, opts...)
	if err != nil {
		return nil, err
	}

	return decodeAndResolve(ctx, doc, "", opts)
}

// LoadFile is Load reading from a file or URL, resolving $import and $include
// relative to it. A nil error guarantees a non-nil Process.
//
// A fragment identifier selects one object inside the document rather than the
// whole of it, which is how a single member of a $graph is addressed:
//
//	cwlcore.LoadFile(ctx, "pack.cwl#count-lines")
//
// Without a fragment, the entry-point rules described on Load apply.
func LoadFile(ctx context.Context, uri string, opts ...salad.ValidateOption) (Process, error) {
	doc, err := LoadFileDocument(ctx, uri, opts...)
	if err != nil {
		return nil, err
	}

	return decodeAndResolve(ctx, doc, fragmentPart(uri), opts)
}

// decodeAndResolve decodes a loaded document and follows the run references that
// decoding could not, which are the ones naming other documents.
func decodeAndResolve(
	ctx context.Context,
	doc *salad.Document,
	fragment string,
	opts []salad.ValidateOption,
) (Process, error) {
	process, err := decodeTarget(doc, fragment)
	if err != nil {
		return nil, err
	}

	err = resolveExternalRuns(ctx, process, doc.BaseURI, fragment, opts)
	if err != nil {
		return nil, err
	}

	return process, nil
}

// LoadDocument parses, resolves and validates src against the embedded CWL v1.2
// schema, returning the resolved salad document without decoding it.
//
// It is the seam for a caller that wants the resolved tree itself — to dump it,
// to walk it, or to hand it to DecodeAll rather than Decode. Load is this
// followed by Decode.
func LoadDocument(
	ctx context.Context,
	src []byte,
	baseURI string,
	opts ...salad.ValidateOption,
) (*salad.Document, error) {
	err := ctx.Err()
	if err != nil {
		return nil, err
	}

	loaded, err := cwlSchema()
	if err != nil {
		return nil, err
	}

	parsed, err := salad.Parse(baseURI, src)
	if err != nil {
		return nil, err
	}

	doc, err := loaded.Loader.LoadNode(parsed, baseURI)
	if err != nil {
		return nil, err
	}

	err = loaded.Schema.Validate(doc.Root, opts...)
	if err != nil {
		return nil, err
	}

	return doc, nil
}

// LoadFileDocument is LoadDocument reading from a file or URL. A fragment
// identifier on uri is ignored, because a fragment selects one object inside a
// document and this returns the whole document.
func LoadFileDocument(ctx context.Context, uri string, opts ...salad.ValidateOption) (*salad.Document, error) {
	err := ctx.Err()
	if err != nil {
		return nil, err
	}

	loaded, err := cwlSchema()
	if err != nil {
		return nil, err
	}

	return loaded.LoadAndValidate(documentPart(uri), opts...)
}

// LoadedSchema returns the embedded CWL v1.2 schema together with the loader and
// context configured to resolve documents against it.
//
// It is the escape hatch for a caller that drives pkg/salad itself: the loader it
// carries knows the CWL vocabulary, which is what turns short names into
// identifiers, links and vocabulary terms as a document is resolved. The schema
// is loaded and flattened once and shared, so calling this is cheap after the
// first time. Schema returns the same schema without the loader.
func LoadedSchema() (*salad.LoadedSchema, error) {
	return cwlSchema()
}

// documentPart strips the fragment identifier from a document reference, leaving
// the part that names the document itself.
func documentPart(uri string) string {
	base, _, _ := strings.Cut(uri, "#")

	return base
}

// fragmentPart returns a document reference's fragment identifier, or "" when it
// names no object inside the document.
func fragmentPart(uri string) string {
	_, fragment, _ := strings.Cut(uri, "#")

	return fragment
}

// Decode turns an already validated salad document into a typed Process.
//
// It is the seam a caller uses when it drives pkg/salad itself. Graph selection
// works exactly as it does for Load, and so does the linking of run references:
// a step naming a process the same document declares comes back with StepRun.Process
// filled in. A step naming another document does not, because following that
// needs I/O — Load and LoadFile do it.
func Decode(doc *salad.Document) (Process, error) {
	if doc == nil {
		return nil, salad.Errorf(salad.SourceLine{}, "there is no document to decode")
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

// decodeLinked decodes every process a document declares, links the run
// references naming one of them, and returns the process decoded from entry.
//
// Every process is decoded, not only the one asked for, because that is what a
// sibling reference resolves against: a $graph's entry point is rarely usable
// without the tools declared alongside it.
func decodeLinked(nodes []salad.Node, entry salad.Node) (Process, error) {
	d := newDecoder()

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

// decodedDocument is every process one document declares, with the one that was
// asked for picked out of them.
type decodedDocument struct {
	selected Process
	procs    []Process
}

// decodeProcesses decodes every node, keeping the processes in document order
// and noting the one decoded from entry.
func (d *decoder) decodeProcesses(nodes []salad.Node, entry salad.Node) decodedDocument {
	decoded := decodedDocument{procs: make([]Process, 0, len(nodes))}

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

// DecodeNode decodes a single process node: a whole document's root, one entry
// of a $graph, or a workflow step's inline run target.
//
// It is exported for downstream packages that hold a RawProcess and want to run
// core decoding again over a sub-node of it.
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

// DecodeAll decodes every top-level process of a document, keyed by identifier.
//
// A document that declares no $graph yields the single map entry for its root
// process. Processes that declare no id are keyed by the blank node identifier
// decoding assigned them.
func DecodeAll(doc *salad.Document) (map[string]Process, error) {
	if doc == nil {
		return nil, salad.Errorf(salad.SourceLine{}, "there is no document to decode")
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

// Schema returns the embedded, flattened CWL v1.2 salad schema and the upstream
// release tag it was vendored from, so that a caller driving pkg/salad itself
// validates against exactly what decoding expects.
//
// The schema is loaded and flattened once, on first use, and the result is
// shared by every later call. The schema is nil if it could not be loaded, which
// can only happen if the embedded snapshot is corrupt; the version string is
// returned either way.
//
// The results are spelled with blank names rather than left bare. That is not
// decoration: gocritic's unnamedResult rejects an exported function returning an
// unnamed primitive, and nonamedreturns rejects a named result, so the pair can
// only be satisfied at once by naming the results without introducing
// identifiers. The function type is unchanged either way, and the assertion
// below pins it.
func Schema() (_ *salad.Schema, _ string) {
	loaded, err := cwlSchema()
	if err != nil {
		return nil, SchemaVersion()
	}

	return loaded.Schema, SchemaVersion()
}

// Compile-time proof that Schema keeps its frozen signature, so that the blank
// result names it carries cannot quietly become something else.
var _ func() (*salad.Schema, string) = Schema

// cwlSchema loads the embedded schema once. Flattening the CWL schema is not
// cheap, and every Load, LoadFile and Schema call needs the same result, so it
// is memoized for the life of the process.
var cwlSchema = sync.OnceValues(loadEmbeddedSchema)

// loadEmbeddedSchema loads and flattens the vendored CWL v1.2 schema out of the
// embedded file system, without touching the filesystem or the network. Call
// cwlSchema instead; this is separate only so that the cost can be benchmarked
// without the memoization in the way.
func loadEmbeddedSchema() (*salad.LoadedSchema, error) {
	return salad.LoadSchema(
		schemaSourceURL,
		salad.WithFetcher(salad.NewFSFetcher(schemaFS, schemaMountURL)),
		salad.WithBaseURL(schemaMountURL),
	)
}

// graphNodes returns the process nodes of a document root and whether the root
// was a graph.
//
// pkg/salad replaces a top-level $graph with the sequence it holds, so a
// resolved graph document arrives as a sequence; a tree that was not run through
// the resolver still carries the directive, and both are accepted.
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

// selectMain picks the process a graph document names as its entry point.
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

	return nil, salad.Errorf(salad.SourceLine{File: baseURI},
		"the document declares no process with an id of %q or %q, so there is nothing to run; it declares %s",
		graphMainFragment, graphMainName, joinOrNone(found))
}

// decodeFragment decodes the object a document reference's fragment names.
//
// Every process the document declares is decoded, not only the one the fragment
// names, because that is what the named one's run references resolve against.
// Addressing a $graph member by fragment is the normal way to reach a packed
// workflow — "pack.cwl#main" — and such a workflow almost always runs the tools
// packed alongside it, so decoding it alone would leave every one of those
// references pointing at nothing.
func decodeFragment(doc *salad.Document, fragment string) (Process, error) {
	nodes, _ := graphNodes(doc.Root)

	selected, err := selectFragment(nodes, fragment, doc.BaseURI)
	if err != nil {
		return nil, err
	}

	return decodeLinked(nodes, selected)
}

// selectFragment picks the process a fragment identifier names, out of a
// document's graph or out of the document's single root process.
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

	return nil, salad.Errorf(salad.SourceLine{File: baseURI},
		"the document declares no object with the identifier %q; it declares %s",
		"#"+fragment, joinOrNone(found))
}

// isMainID reports whether id names the graph's entry point, in any of the
// spellings identifier resolution may have produced.
func isMainID(id string) bool {
	return idFragment(id) == graphMainName
}

// idFragment returns the fragment of a resolved identifier: everything after the
// first "#", or the whole identifier when it carries none. It is what makes
// "main", "#main" and "file:///pack.cwl#main" the same name.
func idFragment(id string) string {
	before, after, ok := strings.Cut(id, "#")
	if !ok {
		return before
	}

	return after
}

// joinOrNone renders the identifiers a graph did declare, for the error message
// raised when none of them is the entry point.
func joinOrNone(ids []string) string {
	if len(ids) == 0 {
		return "none"
	}

	return strings.Join(ids, ", ")
}

// process decodes one process node, dispatching on its class.
//
// The four core classes get their own typed decoding; every other class becomes
// a RawProcess. That is the whole extension mechanism: this package never
// consults a registry, so a downstream package recognizes its own classes by
// switching on RawProcess.Class and decoding RawProcess.Node itself.
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
		return d.rawProcess(m, class)
	}
}

// shortName strips the CWL and Schema Salad vocabularies from a resolved
// discriminator, leaving the short spelling the model's constants use. A name in
// any other namespace — an extension class, a reference to a type declared by a
// SchemaDefRequirement — is returned unchanged.
func shortName(name string) string {
	for _, prefix := range []string{cwlNamespace, saladNamespace, cwlPrefix, saladPrefix} {
		if rest, ok := strings.CutPrefix(name, prefix); ok {
			return rest
		}
	}

	return name
}

// UUID field widths and the bit positions RFC 9562 reserves for the version and
// variant, used to render a blank node identifier.
const (
	uuidBytes        = 16
	uuidVersionIndex = 6
	uuidVariantIndex = 8
	uuidVersionMask  = 0x0f
	uuidVersionBits  = 0x50
	uuidVariantMask  = 0x3f
	uuidVariantBits  = 0x80
	uuidGroupA       = 8
	uuidGroupB       = 12
	uuidGroupC       = 16
	uuidGroupD       = 20
)

// blankNodeID returns the identifier assigned to a process that declares none.
//
// The schema makes a process's id optional, but a process still has to be
// referable — as a step's run target, as a key in DecodeAll's result — so
// decoding assigns one, in the "_:<uuid>" blank node form the Schema Salad
// identifier rules provide for.
//
// It is deterministic: the identifier is a version-5 UUID over the process
// node's source location followed by a canonical rendering of the node itself.
// Decoding the same document twice therefore produces the same identifiers, so
// test diffs and error messages stay stable. Including the source location is
// what keeps two structurally identical inline processes in one document apart;
// when locations are unknown, as they are for a node built in memory, identical
// processes do collapse onto one identifier.
func blankNodeID(node salad.Node) string {
	seed := append([]byte(nodeLoc(node).String()), 0)
	sum := sha256.Sum256(appendCanonical(seed, node))

	return blankNodePrefix + formatUUID(sum[:uuidBytes])
}

// formatUUID renders 16 bytes as a version-5 UUID string.
func formatUUID(raw []byte) string {
	stamped := make([]byte, uuidBytes)
	copy(stamped, raw)
	stamped[uuidVersionIndex] = stamped[uuidVersionIndex]&uuidVersionMask | uuidVersionBits
	stamped[uuidVariantIndex] = stamped[uuidVariantIndex]&uuidVariantMask | uuidVariantBits

	text := hex.EncodeToString(stamped)

	return strings.Join([]string{
		text[:uuidGroupA],
		text[uuidGroupA:uuidGroupB],
		text[uuidGroupB:uuidGroupC],
		text[uuidGroupC:uuidGroupD],
		text[uuidGroupD:],
	}, "-")
}

// appendCanonical appends a deterministic rendering of n to dst. Key order is
// preserved, and every scalar is tagged with its kind, so that two nodes render
// alike exactly when they hold the same values in the same order.
func appendCanonical(dst []byte, n salad.Node) []byte {
	switch value := n.(type) {
	case *salad.MapNode:
		dst = append(dst, '{')
		for key, item := range value.All() {
			dst = strconv.AppendQuote(dst, key)
			dst = appendCanonical(append(dst, ':'), item)
			dst = append(dst, ',')
		}

		return append(dst, '}')
	case *salad.SeqNode:
		dst = append(dst, '[')
		for _, item := range value.Items() {
			dst = append(appendCanonical(dst, item), ',')
		}

		return append(dst, ']')
	case *salad.ScalarNode:
		dst = append(dst, value.Kind().String()...)

		return append(strconv.AppendQuote(append(dst, '('), value.String()), ')')
	default:
		return append(dst, '~')
	}
}
