package salad

import "strings"

// resolveImport replaces an $import directive with the fully-resolved document,
// or the fully-resolved object, that it names.
//
// It is an error for the mapping to carry any field other than $import: the
// specification defines the directive as "an object consisting of exactly one
// field".
func (r *resolver) resolveImport(m *MapNode, sc scope) (Node, error) {
	ref, err := directiveTarget(m, dirImport)
	if err != nil {
		return nil, err
	}

	target, err := r.normalize(sc, ref, m.Loc())
	if err != nil {
		return nil, err
	}

	return r.loadReference(target, m.Loc(), sc.ctx, false)
}

// resolveInclude replaces an $include directive with the raw text of the
// resource it names. The text is never parsed: it becomes a string scalar.
func (r *resolver) resolveInclude(m *MapNode, sc scope) (Node, error) {
	ref, err := directiveTarget(m, dirInclude)
	if err != nil {
		return nil, err
	}

	target, err := r.normalize(sc, ref, m.Loc())
	if err != nil {
		return nil, err
	}

	text, err := r.fetcher.FetchText(target)
	if err != nil {
		return nil, Errorf(m.Loc(), "cannot $include %s: %s", target, err)
	}

	return NewStringNode(m.Loc(), string(text)), nil
}

// normalize resolves a directive's target with the link resolution rules and
// then through the fetcher, so that the result is the cache key that identifies
// the document.
func (r *resolver) normalize(sc scope, ref string, loc SourceLine) (string, error) {
	target, err := r.fetcher.Normalize(sc.fileBase, sc.ctx.expandPrefix(ref))
	if err != nil {
		return "", Errorf(loc, "cannot resolve %q against %q: %s", ref, sc.fileBase, err)
	}

	return target, nil
}

// directiveTarget reads the single string operand of a processing directive.
func directiveTarget(m *MapNode, directive string) (string, error) {
	if m.Len() != 1 {
		return "", Errorf(m.Loc(), "a %s directive must be the only field of its object, but it has %d fields",
			directive, m.Len())
	}

	val, _ := m.Get(directive)

	ref, ok := AsString(val)
	if !ok {
		return "", Errorf(m.Loc(), "%s must be a string, not a %s", directive, NodeKind(val))
	}

	return ref, nil
}

// loadReference resolves the document a URL names, following a fragment
// identifier to the object it selects.
func (r *resolver) loadReference(target string, loc SourceLine, ctx *Context, top bool) (Node, error) {
	docURL, fragment, _ := strings.Cut(target, "#")

	root, err := r.loadDocument(docURL, loc, ctx, top)
	if err != nil {
		return nil, err
	}

	if fragment == "" {
		return root, nil
	}

	obj, ok := r.idx[target]
	if !ok {
		return nil, Errorf(loc, "%s has no object with the identifier %q", docURL, target)
	}

	return obj, nil
}

// loadDocument fetches, parses and resolves one document, memoizing the result
// and refusing to re-enter a document that is still being resolved.
func (r *resolver) loadDocument(docURL string, loc SourceLine, ctx *Context, top bool) (Node, error) {
	if cached, ok := r.docs[docURL]; ok {
		return cached, nil
	}

	if r.active[docURL] {
		return nil, r.cycleError(docURL, loc)
	}

	node, err := r.loader.parse(docURL)
	if err != nil {
		return nil, err
	}

	r.active[docURL] = true
	r.stack = append(r.stack, docURL)

	resolved, err := r.resolve(node, scope{ctx: ctx, base: docURL, fileBase: docURL, top: top})

	delete(r.active, docURL)
	r.stack = r.stack[:len(r.stack)-1]

	if err != nil {
		return nil, err
	}

	r.docs[docURL] = resolved

	return resolved, nil
}

// cycleError reports an import cycle, naming every document on the path back to
// the one being re-entered.
func (r *resolver) cycleError(docURL string, loc SourceLine) error {
	path := make([]string, 0, len(r.stack)+1)

	for i, entry := range r.stack {
		if entry == docURL || len(path) > 0 {
			path = append(path, r.stack[i])
		}
	}

	path = append(path, docURL)

	return Errorf(loc, "$import cycle: %s", strings.Join(path, " -> "))
}
