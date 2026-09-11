package salad

import (
	"maps"
	"slices"
	"strings"
)

// JSON-LD keywords that Schema Salad's simplified context logic understands.
const (
	keywordID    = "@id"
	keywordType  = "@type"
	keywordVocab = "@vocab"
)

// TermDef is one jsonldPredicate entry describing how a field maps into the vocabulary.
type TermDef struct {
	// ID is the _id of the predicate: a vocabulary IRI, or a JSON-LD keyword
	// such as "@id" or "@type".
	ID string
	// Type is the _type of the predicate, such as "@id" or "@vocab".
	Type string
	// Subscope is appended to the identifier scope of objects assigned to this field.
	Subscope string
	// MapSubject is the field an identifier map's keys are assigned to.
	MapSubject string
	// MapPredicate is the field a non-object identifier map value is assigned to.
	MapPredicate string
	// RefScope is how many levels to strip before the parent-scope search.
	RefScope int
	// Identity reports whether missing targets for this field are not errors.
	Identity bool
	// Noconvert suppresses vocabulary conversion of the field's value.
	Noconvert bool
	// NoLinkCheck suppresses link validation at this field.
	NoLinkCheck bool
	// TypeDSL reports that the field's value is expanded with the type DSL.
	TypeDSL bool
	// SecondaryFilesDSL enables secondary files DSL expansion on this field.
	SecondaryFilesDSL bool
	// ScopedRef reports whether a refScope was declared for this field.
	ScopedRef bool
	// IsIdentifier reports that the field carries the object's identifier.
	IsIdentifier bool
}

// IsLink reports whether the field's value is a link.
func (t *TermDef) IsLink() bool {
	return t != nil && t.Type == keywordID
}

// IsVocabField reports whether the field uses vocabulary resolution.
func (t *TermDef) IsVocabField() bool {
	return t != nil && t.Type == keywordVocab
}

// isURLField reports whether the field holds any kind of reference.
func (t *TermDef) isURLField() bool {
	return t.IsLink() || t.IsVocabField()
}

// Context maps short names to IRIs and holds jsonldPredicate definitions.
// A nil *Context behaves as an empty one.
type Context struct {
	namespaces  map[string]string
	vocab       map[string]string
	rvocab      map[string]string
	terms       map[string]*TermDef
	identifiers []string
	schemas     []string
}

// newContext builds an empty Context with every table allocated.
func newContext() *Context {
	return &Context{
		namespaces:  make(map[string]string),
		vocab:       make(map[string]string),
		rvocab:      make(map[string]string),
		terms:       make(map[string]*TermDef),
		identifiers: make([]string, 0),
		schemas:     make([]string, 0),
	}
}

// BuildContext derives a Context from a resolved schema document and its $namespaces.
func BuildContext(schemaDoc Node, metadata *MapNode) (*Context, error) {
	c := newContext()
	c.addNamespaces(metadata)
	c.addSchemas(metadata)

	err := c.addTypeTree(schemaDoc)
	if err != nil {
		return nil, err
	}

	c.finish()

	return c, nil
}

// MergeContexts combines two contexts; base vocabulary takes precedence on collision.
func MergeContexts(base, ext *Context) *Context {
	c := newContext()

	maps.Copy(c.namespaces, base.namespaces)
	maps.Copy(c.namespaces, ext.namespaces)

	// Base wins on vocab collision: copy ext first, then overwrite with base.
	maps.Copy(c.vocab, ext.vocab)
	maps.Copy(c.vocab, base.vocab)

	for term, iri := range c.vocab {
		if _, taken := c.rvocab[iri]; !taken {
			c.rvocab[iri] = term
		}
	}

	maps.Copy(c.terms, base.terms)
	maps.Copy(c.terms, ext.terms)

	seen := make(map[string]bool)
	for _, id := range base.identifiers {
		if !seen[id] {
			c.identifiers = append(c.identifiers, id)
			seen[id] = true
		}
	}

	for _, id := range ext.identifiers {
		if !seen[id] {
			c.identifiers = append(c.identifiers, id)
			seen[id] = true
		}
	}

	slices.Sort(c.identifiers)

	c.schemas = append(c.schemas, base.schemas...)
	c.schemas = append(c.schemas, ext.schemas...)

	return c
}

// emptyTerm is the zero-value TermDef returned for undefined fields. Must not be mutated.
var emptyTerm = &TermDef{
	ID:                "",
	Type:              "",
	Subscope:          "",
	MapSubject:        "",
	MapPredicate:      "",
	RefScope:          0,
	Identity:          false,
	Noconvert:         false,
	NoLinkCheck:       false,
	TypeDSL:           false,
	SecondaryFilesDSL: false,
	ScopedRef:         false,
	IsIdentifier:      false,
}

// Term returns the term definition for a field. The pointer is never nil.
func (c *Context) Term(field string) (*TermDef, bool) {
	if c == nil {
		return emptyTerm, false
	}

	t, ok := c.terms[field]
	if !ok || t == nil {
		return emptyTerm, false
	}

	return t, true
}

// Vocab returns a copy of the short-name to IRI vocabulary table.
func (c *Context) Vocab() map[string]string {
	out := make(map[string]string)
	if c == nil {
		return out
	}

	maps.Copy(out, c.vocab)

	return out
}

// Namespaces returns a copy of the prefix-to-IRI $namespaces table.
func (c *Context) Namespaces() map[string]string {
	out := make(map[string]string)
	if c == nil {
		return out
	}

	maps.Copy(out, c.namespaces)

	return out
}

// Schemas returns the $schemas URIs the context was built with.
func (c *Context) Schemas() []string {
	out := make([]string, 0)
	if c == nil {
		return out
	}

	return append(out, c.schemas...)
}

// Shortname returns the trailing short name of an identifier IRI.
func (c *Context) Shortname(id string) string {
	return shortName(id)
}

// termOf returns the term definition for a field; never nil.
func (c *Context) termOf(field string) *TermDef {
	if c == nil {
		return emptyTerm
	}

	if t, ok := c.terms[field]; ok && t != nil {
		return t
	}

	return emptyTerm
}

// identifierFields returns the identifier field names in sorted order.
func (c *Context) identifierFields() []string {
	if c == nil {
		return nil
	}

	return c.identifiers
}

// hasVocabTerm reports whether name is already a vocabulary term.
func (c *Context) hasVocabTerm(name string) bool {
	if c == nil {
		return false
	}

	_, ok := c.vocab[name]

	return ok
}

// vocabTermFor returns the vocabulary term whose IRI is iri, if there is one.
func (c *Context) vocabTermFor(iri string) (string, bool) {
	if c == nil {
		return "", false
	}

	term, ok := c.rvocab[iri]

	return term, ok
}

// addNamespaces records the $namespaces prefix table from a document's metadata.
func (c *Context) addNamespaces(metadata *MapNode) {
	ns, ok := AsMap(nodeOrNil(metadata, dirNamespaces))
	if !ok {
		return
	}

	for prefix, val := range ns.All() {
		if iri, isStr := AsString(val); isStr {
			c.namespaces[prefix] = iri
		}
	}
}

// addSchemas records the $schemas URIs from a document's metadata.
func (c *Context) addSchemas(metadata *MapNode) {
	val := nodeOrNil(metadata, dirSchemas)
	if uri, ok := AsString(val); ok {
		c.schemas = append(c.schemas, uri)

		return
	}

	seq, ok := AsSeq(val)
	if !ok {
		return
	}

	for _, item := range seq.Items() {
		if uri, isStr := AsString(item); isStr {
			c.schemas = append(c.schemas, uri)
		}
	}
}

// finish derives the reverse vocabulary and the sorted identifier field list
// once every term has been registered.
func (c *Context) finish() {
	for prefix, iri := range c.namespaces {
		c.putVocab(prefix, iri)
	}

	for term, iri := range c.vocab {
		if _, taken := c.rvocab[iri]; !taken {
			c.rvocab[iri] = term
		}
	}

	for name, def := range c.terms {
		if def.IsIdentifier {
			c.identifiers = append(c.identifiers, name)
		}
	}

	slices.Sort(c.identifiers)
}

// putVocab records a vocabulary entry, keeping the first definition of a term.
func (c *Context) putVocab(term, iri string) {
	if term == "" || iri == "" || isKeyword(iri) {
		return
	}

	if _, exists := c.vocab[term]; !exists {
		c.vocab[term] = iri
	}
}

// putVocabTerm records a type-name vocabulary entry, rejecting collisions.
func (c *Context) putVocabTerm(term, iri string, loc SourceLine) *Error {
	if term == "" || iri == "" || isKeyword(iri) {
		return nil
	}

	if existing, exists := c.vocab[term]; exists {
		if existing != iri {
			return Errorf(loc, "Predicate collision on %s, %q != %q", term, existing, iri)
		}

		return nil
	}

	c.vocab[term] = iri

	return nil
}

// expandPrefix expands a namespace prefix (e.g. "sld:type") to its full IRI.
func (c *Context) expandPrefix(name string) string {
	if c == nil {
		return name
	}

	i := strings.IndexByte(name, ':')
	if i <= 0 {
		return name
	}

	iri, ok := c.namespaces[name[:i]]
	if !ok {
		return name
	}

	return iri + name[i+1:]
}

// isKeyword reports whether name is a JSON-LD keyword rather than an IRI.
func isKeyword(name string) bool {
	return strings.HasPrefix(name, "@")
}

// nodeOrNil reads a key from a possibly-nil map, returning nil when absent.
func nodeOrNil(m *MapNode, key string) Node {
	n, ok := m.Get(key)
	if !ok {
		return nil
	}

	return n
}
