package salad

import (
	"net/url"
	"slices"
	"strings"
)

// checkLinks validates every link in a resolved document and rewrites the
// references that a refScope search resolves.
//
// The specification makes link validation optional; this package performs it by
// default and treats a dangling link as fatal. WithSkipLinkCheck opts out. Two
// jsonldPredicate modifiers narrow it: identity means the absence of an object
// with that URI is not an error, and noLinkCheck stops traversal at the field.
func (r *resolver) checkLinks(n Node, sc scope) (Node, error) {
	checked := r.walkLinks(n, sc)
	if len(r.linkErrs) == 0 {
		return checked, nil
	}

	return nil, Group(n.Loc(), "document has unresolved links", r.linkErrs...)
}

// walkLinks descends a resolved tree, checking references as it goes.
func (r *resolver) walkLinks(n Node, sc scope) Node {
	switch v := n.(type) {
	case *MapNode:
		return r.walkLinksMap(v, sc)
	case *SeqNode:
		return r.walkLinksSeq(v, sc)
	default:
		return n
	}
}

// walkLinksSeq checks every item of a sequence.
func (r *resolver) walkLinksSeq(s *SeqNode, sc scope) Node {
	items := make([]Node, 0, s.Len())
	for _, item := range s.Items() {
		items = append(items, r.walkLinks(item, sc))
	}

	return NewSeqNode(s.Loc(), items)
}

// walkLinksMap checks every field of an object. The object's own identifier is
// the scope a refScope search starts from, falling back to the base URI the
// document's explicit context sets.
func (r *resolver) walkLinksMap(m *MapNode, sc scope) Node {
	sc = applyExplicitContext(m, sc)
	child := sc.child()

	for _, field := range sc.ctx.identifierFields() {
		if id, ok := AsString(nodeOrNil(m, field)); ok {
			child = child.rebase(id)
		}
	}

	out := make([]MapEntry, 0, m.Len())

	for key, val := range m.All() {
		term := sc.ctx.termOf(key)
		out = append(out, MapEntry{Key: key, Value: r.checkField(key, term, val, child)})
	}

	return NewMapNode(m.Loc(), out)
}

// checkField checks one field's value, honouring noLinkCheck and the identity
// modifier, and recursing into anything that is not itself a reference.
func (r *resolver) checkField(field string, term *TermDef, val Node, sc scope) Node {
	if term.NoLinkCheck {
		return val
	}

	if !term.isURLField() || term.Identity {
		return r.walkLinks(val, sc)
	}

	if s, ok := AsString(val); ok {
		return NewStringNode(val.Loc(), r.checkLink(field, term, s, val.Loc(), sc))
	}

	seq, ok := AsSeq(val)
	if !ok {
		return r.walkLinks(val, sc)
	}

	items := make([]Node, 0, seq.Len())

	for _, item := range seq.Items() {
		s, isStr := AsString(item)
		if !isStr {
			items = append(items, r.walkLinks(item, sc))

			continue
		}

		items = append(items, NewStringNode(item.Loc(), r.checkLink(field, term, s, item.Loc(), sc)))
	}

	return NewSeqNode(seq.Loc(), items)
}

// checkLink resolves and validates one reference, returning the reference the
// document should carry. A reference that cannot be resolved is recorded as an
// error and returned unchanged.
func (r *resolver) checkLink(field string, term *TermDef, link string, loc SourceLine, sc scope) string {
	if link == "" || isTemplate(link) || r.isKnown(link, term, sc) {
		return link
	}

	if term.ScopedRef && !hasScheme(link) {
		resolved, err := r.searchScopes(field, term, link, loc, sc)
		if err != nil {
			r.linkErrs = append(r.linkErrs, err)

			return link
		}

		return resolved
	}

	if r.fetcher.Exists(link) {
		return link
	}

	r.linkErrs = append(r.linkErrs, Errorf(loc, "field %q contains an undefined reference to %q", field, link))

	return link
}

// isKnown reports whether a reference names something the document, the
// vocabulary, or a declared foreign vocabulary already defines.
func (r *resolver) isKnown(link string, term *TermDef, sc scope) bool {
	if _, ok := r.idx[link]; ok {
		return true
	}

	if _, ok := sc.ctx.vocabTermFor(link); ok {
		return true
	}

	if term.IsVocabField() && sc.ctx.hasVocabTerm(link) {
		return true
	}

	return r.inDeclaredNamespace(link)
}

// searchScopes implements the refScope rule: a relative reference is resolved by
// searching each successive parent scope of the containing identifier, the last
// scope searched being the top level.
func (r *resolver) searchScopes(field string, term *TermDef, link string, loc SourceLine, sc scope) (string, *Error) {
	base, err := url.Parse(sc.base)
	if err != nil {
		return "", Errorf(loc, "field %q: cannot search for %q relative to %q: %s", field, link, sc.base, err)
	}

	segments := fragmentSegments(base.Fragment)
	for n := term.RefScope; n > 0 && len(segments) > 0; n-- {
		segments = segments[:len(segments)-1]
	}

	tried := make([]string, 0, len(segments)+1)

	for {
		candidate := withFragment(base, strings.Join(append(slices.Clone(segments), link), "/"))
		tried = append(tried, candidate)

		if _, ok := r.idx[candidate]; ok {
			return candidate, nil
		}

		if len(segments) == 0 {
			break
		}

		segments = segments[:len(segments)-1]
	}

	return "", Errorf(loc, "field %q references an unknown identifier %q; tried %s",
		field, link, strings.Join(tried, ", "))
}

// fragmentSegments splits a URI fragment into its "/"-separated scope segments.
func fragmentSegments(fragment string) []string {
	if fragment == "" {
		return make([]string, 0)
	}

	return strings.Split(fragment, "/")
}

// withFragment renders base with a different fragment identifier.
func withFragment(base *url.URL, fragment string) string {
	out := *base
	out.RawFragment = ""
	out.Fragment = fragment

	if out.Path == "" && out.Opaque == "" {
		out.Path = "/"
	}

	return out.String()
}
