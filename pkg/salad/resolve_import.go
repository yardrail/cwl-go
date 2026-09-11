package salad

import "strings"

// resolveImport replaces an $import directive with the resolved target.
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

// resolveInclude replaces an $include directive with the raw text as a string.
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

// normalize resolves a directive's target to a canonical document URL.
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

// loadReference resolves a URL, following any fragment to the target object.
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

// loadDocument fetches, parses and resolves one document, detecting cycles.
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

	resolved, err := r.resolve(node, scope{ctx: ctx, base: docURL, fileBase: docURL, field: "", top: top})

	delete(r.active, docURL)
	r.stack = r.stack[:len(r.stack)-1]

	if err != nil {
		return nil, err
	}

	r.docs[docURL] = resolved

	return resolved, nil
}

// cycleError reports an import cycle.
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
