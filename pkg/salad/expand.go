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

// ExpandURL resolves a name to a full IRI using link resolution rules.
func (c *Context) ExpandURL(name, base string) string {
	return c.expand(name, base, modeLink, false)
}

// ExpandIdentifier resolves an identifier relative to base using identifier resolution rules.
func (c *Context) ExpandIdentifier(name, base string) string {
	return c.expand(name, base, modeIdentifier, false)
}

// ExpandVocabTerm resolves a vocabulary reference, returning the short term if known.
func (c *Context) ExpandVocabTerm(name, base string) string {
	return c.expand(name, base, modeVocab, false)
}

// expand applies one of the three reference-resolution rule sets.
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
		// Absolute URI: leave as-is.
		return iri
	case mode == modeIdentifier && !hasFragment:
		return scopeFragment(base, iri)
	case scopedRef && !hasFragment:
		return iri
	default:
		return resolveReference(base, iri)
	}
}

// resolveFieldName resolves a field name via prefix expansion and vocabulary lookup.
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

// scopeFragment appends name as a fragment segment of the base URI.
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

// scopeSubscope appends a subscope to the base URI's fragment.
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

// isTemplate reports whether s is a $(...) or ${...} expression template.
func isTemplate(s string) bool {
	return strings.HasPrefix(s, "$(") || strings.HasPrefix(s, "${")
}

// isDirective reports whether a field name is a processing directive rather than
// a vocabulary field.
func isDirective(name string) bool {
	return strings.HasPrefix(name, "$")
}
