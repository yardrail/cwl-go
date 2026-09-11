package salad

import (
	"maps"
	"slices"
	"strings"
)

// scope is the resolution state flowing down the document tree.
type scope struct {
	ctx      *Context
	base     string
	fileBase string
	field    string
	top      bool
}

// child returns sc for a nested node.
func (sc scope) child() scope {
	return scope{ctx: sc.ctx, base: sc.base, fileBase: sc.fileBase, field: sc.field, top: false}
}

// descend returns sc for a field's value.
func (sc scope) descend(field string) scope {
	return scope{ctx: sc.ctx, base: sc.base, fileBase: sc.fileBase, field: field, top: false}
}

// rebase returns sc with a different base URI for identifier and link resolution.
func (sc scope) rebase(base string) scope {
	return scope{ctx: sc.ctx, base: base, fileBase: sc.fileBase, field: sc.field, top: sc.top}
}

// resolver carries the state of one Load call.
type resolver struct {
	loader   *Loader
	fetcher  Fetcher
	ids      map[string]declaration
	idx      map[string]Node
	docs     map[string]Node
	active   map[string]bool
	meta     *MapNode
	stack    []string
	linkErrs []*Error
	spaces   []string
	asserted map[string]bool
}

// declaration records where an identifier was first declared.
type declaration struct {
	loc   SourceLine
	field string
}

// newResolver starts a resolution run.
func (l *Loader) newResolver() *resolver {
	return &resolver{
		loader:   l,
		fetcher:  l.Fetcher(),
		ids:      make(map[string]declaration),
		idx:      make(map[string]Node),
		docs:     make(map[string]Node),
		active:   make(map[string]bool),
		meta:     nil,
		stack:    make([]string, 0),
		linkErrs: make([]*Error, 0),
		spaces:   namespaceIRIs(l.Context()),
		asserted: make(map[string]bool),
	}
}

// finish runs link validation over a resolved tree and assembles the Document.
func (r *resolver) finish(root Node, baseURI string) (*Document, error) {
	if r.loader.cfg.skipLinkCheck {
		return &Document{Root: root, Metadata: r.meta, BaseURI: baseURI}, nil
	}

	checked, err := r.checkLinks(
		root,
		scope{ctx: r.loader.Context(), base: baseURI, fileBase: baseURI, field: "", top: false},
	)
	if err != nil {
		return nil, err
	}

	return &Document{Root: checked, Metadata: r.meta, BaseURI: baseURI}, nil
}

// resolve applies the preprocessing rules to one node.
func (r *resolver) resolve(n Node, sc scope) (Node, error) {
	switch v := n.(type) {
	case *MapNode:
		return r.resolveMap(v, sc)
	case *SeqNode:
		return r.resolveSeq(v, sc)
	default:
		return n, nil
	}
}

// resolveSeq resolves every item, flattening $import sequences.
func (r *resolver) resolveSeq(s *SeqNode, sc scope) (Node, error) {
	out := make([]Node, 0, s.Len())

	for _, item := range s.Items() {
		res, err := r.resolve(item, sc.child())
		if err != nil {
			return nil, err
		}

		if spliced, ok := AsSeq(res); ok && isImportNode(item) {
			out = append(out, spliced.Items()...)

			continue
		}

		out = append(out, res)
	}

	return NewSeqNode(s.Loc(), out), nil
}

// resolveMap resolves a mapping: directive, explicit context, or object.
func (r *resolver) resolveMap(m *MapNode, sc scope) (Node, error) {
	if m.Has(dirImport) {
		return r.resolveImport(m, sc)
	}

	if m.Has(dirInclude) {
		return r.resolveInclude(m, sc)
	}

	sc = applyExplicitContext(m, sc)
	r.recordNamespaces(m)

	if sc.top {
		r.meta = documentMetadata(m)
	}

	if graph, ok := m.Get(dirGraph); ok {
		return r.resolve(graph, sc.child())
	}

	return r.resolveObject(m, sc)
}

// resolveObject applies the preprocessing rules to one object.
func (r *resolver) resolveObject(m *MapNode, sc scope) (Node, error) {
	m = normalizeFieldNames(m, sc.ctx)

	m, err := r.expandIdentifierMaps(m, sc)
	if err != nil {
		return nil, err
	}

	m, err = expandDSLFields(m, sc.ctx)
	if err != nil {
		return nil, err
	}

	m, base, err := r.resolveIdentifiers(m, sc)
	if err != nil {
		return nil, err
	}

	out, err := r.resolveFields(m, sc.rebase(base))
	if err != nil {
		return nil, err
	}

	r.reindex(out, sc.ctx)

	return out, nil
}

// reindex updates the identifier index to point at the fully-resolved object.
func (r *resolver) reindex(out *MapNode, ctx *Context) {
	for _, field := range ctx.identifierFields() {
		node := nodeOrNil(out, field)
		if node == nil {
			continue
		}

		id, ok := AsString(node)
		if !ok {
			continue
		}

		if owner, seen := r.ids[id]; seen && owner.loc != node.Loc() {
			continue
		}

		r.idx[id] = out
	}
}

// normalizeFieldNames applies field name resolution to every key of an object.
func normalizeFieldNames(m *MapNode, ctx *Context) *MapNode {
	out := make([]MapEntry, 0, m.Len())
	for key, val := range m.All() {
		out = append(out, MapEntry{Key: ctx.resolveFieldName(key), Value: val})
	}

	return NewMapNode(m.Loc(), out)
}

// resolveIdentifiers expands identifier fields and returns the new base URI.
func (r *resolver) resolveIdentifiers(m *MapNode, sc scope) (*MapNode, string, error) {
	base := sc.base

	for _, field := range sc.ctx.identifierFields() {
		val, ok := m.Get(field)
		if !ok || IsNull(val) {
			continue
		}

		raw, isStr := AsString(val)
		if !isStr {
			return nil, "", Errorf(val.Loc(), "identifier field %q must be a string, not a %s", field, NodeKind(val))
		}

		id := sc.ctx.ExpandIdentifier(raw, base)

		err := r.declare(id, val.Loc(), m, sc.field)
		if err != nil {
			return nil, "", err
		}

		m = m.With(MapEntry{Key: field, Value: NewStringNode(val.Loc(), id)})
		base = id
	}

	return m, base, nil
}

// declare records an identifier, erroring on duplicates within the same field.
func (r *resolver) declare(id string, loc SourceLine, obj Node, field string) error {
	prev, seen := r.ids[id]
	if seen && prev.field == field && prev.loc != loc {
		return Group(loc, "duplicate identifier "+id,
			Errorf(prev.loc, "first declared here"),
			Errorf(loc, "redeclared here"),
		)
	}

	if !seen {
		r.ids[id] = declaration{loc: loc, field: field}
	}

	r.indexObject(id, obj)

	return nil
}

// indexObject records an object as the target of links to id.
func (r *resolver) indexObject(id string, obj Node) {
	if _, taken := r.idx[id]; taken && !r.asserted[id] {
		return
	}

	delete(r.asserted, id)
	r.idx[id] = obj
}

// resolveFields resolves every field value, expanding references.
func (r *resolver) resolveFields(m *MapNode, sc scope) (*MapNode, error) {
	out := make([]MapEntry, 0, m.Len())

	for key, val := range m.All() {
		term := sc.ctx.termOf(key)

		child := sc.descend(key)
		if term.Subscope != "" {
			child = child.rebase(scopeSubscope(sc.base, term.Subscope))
		}

		res, err := r.resolve(r.expandReferences(val, term, sc), child)
		if err != nil {
			return nil, err
		}

		out = append(out, MapEntry{Key: key, Value: res})
	}

	return NewMapNode(m.Loc(), out), nil
}

// expandReferences applies reference-resolution rules to a field's value.
func (r *resolver) expandReferences(val Node, term *TermDef, sc scope) Node {
	if !term.isURLField() {
		return val
	}

	mode := modeLink

	switch {
	case term.IsVocabField():
		mode = modeVocab
	case term.Identity:
		mode = modeIdentifier
	default:
	}

	if s, ok := AsString(val); ok {
		return r.expandReference(s, val.Loc(), mode, term, sc)
	}

	seq, ok := AsSeq(val)
	if !ok {
		return val
	}

	items := make([]Node, 0, seq.Len())

	for _, item := range seq.Items() {
		s, isStr := AsString(item)
		if !isStr {
			items = append(items, item)

			continue
		}

		items = append(items, r.expandReference(s, item.Loc(), mode, term, sc))
	}

	return NewSeqNode(seq.Loc(), items)
}

// expandReference expands one reference and indexes identity fields as link targets.
func (r *resolver) expandReference(raw string, loc SourceLine, mode expandMode, term *TermDef, sc scope) Node {
	expanded := sc.ctx.expand(raw, sc.base, mode, term.ScopedRef)

	if mode == modeIdentifier {
		r.declareLinkTarget(expanded, loc)
	}

	return NewStringNode(loc, expanded)
}

// declareLinkTarget records a non-object identifier as a link target.
func (r *resolver) declareLinkTarget(id string, loc SourceLine) {
	if id == "" || isKeyword(id) {
		return
	}

	if _, taken := r.idx[id]; !taken {
		r.idx[id] = NewStringNode(loc, id)
		r.asserted[id] = true
	}
}

// recordNamespaces collects namespace IRIs for link checking.
func (r *resolver) recordNamespaces(m *MapNode) {
	ns, ok := AsMap(nodeOrNil(m, dirNamespaces))
	if !ok {
		return
	}

	for _, val := range ns.All() {
		if iri, isStr := AsString(val); isStr {
			r.spaces = appendUniqueString(r.spaces, iri)
		}
	}
}

// inDeclaredNamespace reports whether an IRI belongs to a declared namespace.
func (r *resolver) inDeclaredNamespace(iri string) bool {
	for _, ns := range r.spaces {
		if ns != "" && len(iri) > len(ns) && strings.HasPrefix(iri, ns) {
			return true
		}
	}

	return false
}

// namespaceIRIs collects the namespace IRIs a context was built with.
func namespaceIRIs(ctx *Context) []string {
	out := make([]string, 0, len(ctx.Namespaces()))
	for _, iri := range ctx.Namespaces() {
		out = appendUniqueString(out, iri)
	}

	slices.Sort(out)

	return out
}

// appendUniqueString appends v to out unless it is already there.
func appendUniqueString(out []string, v string) []string {
	if slices.Contains(out, v) {
		return out
	}

	return append(out, v)
}

// applyExplicitContext applies $base and $namespaces directives to the scope.
func applyExplicitContext(m *MapNode, sc scope) scope {
	if base, ok := AsString(nodeOrNil(m, dirBase)); ok {
		sc = sc.rebase(base)
	}

	ns, ok := AsMap(nodeOrNil(m, dirNamespaces))
	if !ok {
		return sc
	}

	return scope{ctx: sc.ctx.withNamespaces(ns), base: sc.base, fileBase: sc.fileBase, field: sc.field, top: sc.top}
}

// documentMetadata collects the root object's directives (excluding $graph).
func documentMetadata(m *MapNode) *MapNode {
	out := make([]MapEntry, 0, m.Len())

	for key, val := range m.All() {
		if isDirective(key) && key != dirGraph {
			out = append(out, MapEntry{Key: key, Value: val})
		}
	}

	if len(out) == 0 {
		return nil
	}

	return NewMapNode(m.Loc(), out)
}

// isImportNode reports whether a node is an $import directive.
func isImportNode(n Node) bool {
	m, ok := AsMap(n)

	return ok && m.Has(dirImport)
}

// withNamespaces returns a copy with additional namespace prefixes.
func (c *Context) withNamespaces(ns *MapNode) *Context {
	out := newContext()
	if c != nil {
		maps.Copy(out.namespaces, c.namespaces)
		maps.Copy(out.vocab, c.vocab)
		maps.Copy(out.rvocab, c.rvocab)
		maps.Copy(out.terms, c.terms)
		out.identifiers = append(out.identifiers, c.identifiers...)
		out.schemas = append(out.schemas, c.schemas...)
	}

	for prefix, val := range ns.All() {
		iri, ok := AsString(val)
		if !ok {
			continue
		}

		out.namespaces[prefix] = iri
		out.vocab[prefix] = iri

		if _, taken := out.rvocab[iri]; !taken {
			out.rvocab[iri] = prefix
		}
	}

	return out
}
