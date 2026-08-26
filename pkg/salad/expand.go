package salad

import (
	"net/url"
	"strings"
)

// expandMode selects which of the specification's reference-resolution rule sets
// an expansion follows.
type expandMode int

const (
	// modeLink is link resolution: a plain URI reference resolved against the base.
	modeLink expandMode = iota
	// modeIdentifier is identifier resolution, where a name with no fragment
	// becomes a parent-relative fragment of the base URI.
	modeIdentifier
	// modeVocab is vocabulary resolution: link resolution followed by a reverse
	// lookup that replaces a known IRI with its vocabulary term.
	modeVocab
)

// ExpandURL resolves a short name or prefixed name to a full IRI, relative to
// base, following the specification's link resolution rules.
//
// It is the analogue of ref_resolver.Loader.expand_url with vocab_term and
// scoped_id both false. The vocabTerm flag the frozen signature carried was
// split out into ExpandVocabTerm, because the two rule sets return different
// kinds of value: a link resolves to an IRI, a vocabulary reference may resolve
// back to a short term.
func (c *Context) ExpandURL(name, base string) string {
	return c.expand(name, base, modeLink, false)
}

// ExpandIdentifier resolves an identifier relative to base, following the
// specification's identifier resolution rules: a name that carries no fragment
// becomes a parent-relative fragment of the base URI.
//
// It is the analogue of ref_resolver.Loader.expand_url with scoped_id true.
func (c *Context) ExpandIdentifier(name, base string) string {
	return c.expand(name, base, modeIdentifier, false)
}

// ExpandVocabTerm resolves a vocabulary reference relative to base: the link
// resolution rules are applied first, and if the result is an IRI the vocabulary
// maps to a term, the term is returned instead.
//
// It is the analogue of ref_resolver.Loader.expand_url with vocab_term true.
func (c *Context) ExpandVocabTerm(name, base string) string {
	return c.expand(name, base, modeVocab, false)
}

// expand applies one of the three reference-resolution rule sets.
//
// scopedRef suppresses resolution of a fragment-less reference: a field with a
// refScope is resolved later by searching successive parent scopes, which cannot
// be done until every identifier in the document is known.
func (c *Context) expand(name, base string, mode expandMode, scopedRef bool) string {
	if name == "" || isKeyword(name) || isTemplate(name) {
		return name
	}

	if mode == modeVocab && c.hasVocabTerm(name) {
		return name
	}

	iri := placeReference(c.expandPrefix(name), base, mode, scopedRef)

	if mode == modeVocab {
		if term, ok := c.vocabTermFor(iri); ok {
			return term
		}
	}

	return iri
}

// placeReference positions an already prefix-expanded reference relative to base.
func placeReference(iri, base string, mode expandMode, scopedRef bool) string {
	hasFragment := strings.Contains(iri, "#")

	switch {
	case hasScheme(iri):
		// Rule 8 of identifier resolution and rule 4 of link resolution: an
		// absolute URI is left alone. schema-salad narrows this to http, https
		// and file; the specification does not, so neither do we.
		return iri
	case mode == modeIdentifier && !hasFragment:
		return scopeFragment(base, iri)
	case scopedRef && !hasFragment:
		return iri
	default:
		return resolveReference(base, iri)
	}
}

// resolveFieldName applies the specification's field name resolution rules: a
// declared namespace prefix is expanded, and a resolved field whose IRI is in
// the vocabulary is replaced by its vocabulary term. Anything else is left as it
// is, including names that are neither absolute nor part of the vocabulary.
func (c *Context) resolveFieldName(name string) string {
	if name == "" || isKeyword(name) || isDirective(name) || c.hasVocabTerm(name) {
		return name
	}

	iri := c.expandPrefix(name)
	if term, ok := c.vocabTermFor(iri); ok {
		return term
	}

	return iri
}

// scopeFragment implements identifier resolution rules 3, 4 and 6: a parent
// relative fragment identifier extends the base URI's fragment, or becomes it
// when the base URI has none.
func scopeFragment(base, name string) string {
	if base == "" {
		return name
	}

	b, err := url.Parse(base)
	if err != nil {
		return name
	}

	b.RawFragment = ""

	if b.Fragment == "" {
		b.Fragment = name
	} else {
		b.Fragment = b.Fragment + "/" + name
	}

	if b.Path == "" && b.Opaque == "" {
		b.Path = "/"
	}

	return b.String()
}

// scopeSubscope implements identifier resolution rule 5: the subscope declared
// on the parent field is appended to the identifier scope before the child
// object's own identifier is resolved.
//
// schema-salad concatenates the subscope onto the base URI as plain text, which
// appends to the path when the base has no fragment. The specification says the
// subscope is appended to the fragment, so a base URI with no fragment gets one.
func scopeSubscope(base, subscope string) string {
	if subscope == "" {
		return base
	}

	return scopeFragment(base, subscope)
}

// resolveReference resolves a URI reference against a base URI per RFC 3986.
func resolveReference(base, ref string) string {
	if base == "" {
		return ref
	}

	b, err := url.Parse(base)
	if err != nil {
		return ref
	}

	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}

	return b.ResolveReference(r).String()
}

// hasScheme reports whether s begins with a URI scheme followed by a colon.
func hasScheme(s string) bool {
	i := strings.IndexByte(s, ':')
	if i <= 0 {
		return false
	}

	for j := range i {
		if !isSchemeByte(s[j], j) {
			return false
		}
	}

	return true
}

// isSchemeByte reports whether b may appear at position pos of a URI scheme.
func isSchemeByte(b byte, pos int) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z':
		return true
	case pos == 0:
		return false
	default:
		return b >= '0' && b <= '9' || b == '+' || b == '-' || b == '.'
	}
}

// isTemplate reports whether s is a template expression of the host vocabulary
// rather than a URI reference. Salad itself defines no expression syntax, but a
// value that opens with "$(" or "${" is by convention interpolated later and
// must survive preprocessing untouched.
func isTemplate(s string) bool {
	return strings.HasPrefix(s, "$(") || strings.HasPrefix(s, "${")
}

// isDirective reports whether a field name is a processing directive rather than
// a vocabulary field.
func isDirective(name string) bool {
	return strings.HasPrefix(name, "$")
}
