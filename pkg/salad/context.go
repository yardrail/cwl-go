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

// TermDef is one jsonldPredicate entry: how a field's value maps into the
// linked-data vocabulary.
//
// It is reachable from Field.JSONLDPred so that consumers can tell, per field,
// whether a value is an identifier, a link, or a vocabulary term, which is what
// drives identifier resolution, link resolution and vocabulary resolution.
type TermDef struct {
	// ID is the _id of the predicate: a vocabulary IRI, or a JSON-LD keyword
	// such as "@id" or "@type".
	ID string
	// Type is the _type of the predicate, such as "@id" or "@vocab".
	Type string
	// Subscope, when non-empty, is appended to the identifier scope of objects
	// assigned to this field, per identifier resolution rule 5.
	Subscope string
	// MapSubject is the field an identifier map's keys are assigned to.
	MapSubject string
	// MapPredicate is the field a non-object identifier map value is assigned to.
	MapPredicate string
	// RefScope is how many levels to strip from the containing identifier scope
	// before starting the successive-parent-scope search. It is meaningful only
	// when ScopedRef is true.
	RefScope int
	// Identity reports whether the field is an identifier, meaning the absence of
	// an object with that URI in the loaded document is not an error.
	Identity bool
	// Noconvert suppresses vocabulary conversion of the field's value.
	Noconvert bool
	// NoLinkCheck reports that link validation traversal must stop at this field.
	NoLinkCheck bool
	// TypeDSL reports that the field's value is expanded with the type DSL.
	TypeDSL bool
	// SecondaryFilesDSL reports that the field's value is expanded with the
	// secondary files DSL.
	SecondaryFilesDSL bool
	// ScopedRef reports whether a refScope was declared for this field.
	ScopedRef bool
	// IsIdentifier reports that the field carries the identifier of its object,
	// which becomes the base URI for the object's children.
	IsIdentifier bool
}

// IsLink reports whether the field's value is a link, resolved with the link
// resolution rules.
func (t *TermDef) IsLink() bool {
	return t != nil && t.Type == keywordID
}

// IsVocabField reports whether the field's value is resolved with the
// vocabulary resolution rules.
func (t *TermDef) IsVocabField() bool {
	return t != nil && t.Type == keywordVocab
}

// isURLField reports whether the field's value is any kind of reference, which
// is the set of fields link validation inspects.
func (t *TermDef) isURLField() bool {
	return t.IsLink() || t.IsVocabField()
}

// Context maps between short names and fully-qualified IRIs and holds the
// per-term jsonldPredicate definitions derived from a schema.
//
// It is schema-salad's own simplified context logic — a flat term table plus the
// vocabulary bookkeeping the Python Loader carries — and deliberately not a
// general JSON-LD processor. JSON-LD context generation and RDF triple output
// are out of scope for this package.
//
// A nil *Context behaves as an empty one: every lookup misses and expansion
// falls back to plain URI reference resolution.
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

// BuildContext derives a Context from a resolved schema document's type
// definitions and its $namespaces metadata.
//
// The schema document is normally one that has already been through identifier
// resolution, so that type and field names are absolute IRIs, but BuildContext
// also accepts a raw document: names are reduced to their short form either way,
// and namespace prefixes are expanded against metadata's $namespaces.
//
// It is the analogue of jsonld_context.salad_to_jsonld_context.
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

// MergeContexts combines two already-finished contexts into one. The base
// context's vocabulary entries take precedence on collision, matching the
// first-writer-wins semantics of putVocab. Extension namespaces, terms, and
// schemas are added on top.
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

// emptyTerm is what Term reports for a field the schema says nothing about. It
// is shared and must never be mutated; term definitions are only ever written
// while a Context is being built, onto values freshly allocated there.
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

// Term returns the term definition for a resolved field name, and whether the
// schema defines one. The returned pointer is never nil, so a caller that does
// not care whether the field is known can use the result directly: an undefined
// field behaves as one with no jsonldPredicate at all.
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

// Vocab returns the full short-name to IRI vocabulary table. The validator uses
// it to interpret vocabulary-typed values, and consumers use it to interpret
// class-style fields. The result is a fresh map.
//
// It is the analogue of the Python Loader's vocab attribute.
func (c *Context) Vocab() map[string]string {
	out := make(map[string]string)
	if c == nil {
		return out
	}

	maps.Copy(out, c.vocab)

	return out
}

// Namespaces returns the prefix to IRI table declared by $namespaces. The result
// is a fresh map.
func (c *Context) Namespaces() map[string]string {
	out := make(map[string]string)
	if c == nil {
		return out
	}

	maps.Copy(out, c.namespaces)

	return out
}

// Schemas returns the $schemas URIs the context was built with. Salad only
// surfaces them; interpreting the RDF they name is a consumer's concern.
func (c *Context) Schemas() []string {
	out := make([]string, 0)
	if c == nil {
		return out
	}

	return append(out, c.schemas...)
}

// Shortname returns the trailing short name of an identifier IRI: the last
// "/"-separated segment of the fragment if there is one, otherwise the last
// segment of the path. It is used pervasively for field and symbol lookup.
//
// It is the analogue of schema.shortname / validate.avro_shortname.
func (c *Context) Shortname(id string) string {
	return shortName(id)
}

// termOf returns the term definition for a field without reporting whether the
// schema defines one. An undefined field yields emptyTerm, which behaves exactly
// as a field declaring no jsonldPredicate, so callers that only need to ask what
// the predicate says can use the result without a guard.
func (c *Context) termOf(field string) *TermDef {
	if c == nil {
		return emptyTerm
	}

	if t, ok := c.terms[field]; ok && t != nil {
		return t
	}

	return emptyTerm
}

// identifierFields returns the field names whose value is the identifier of the
// containing object, in sorted order so that resolution is deterministic.
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
//
// This is the lenient path: it is used to seed $namespaces prefixes into the
// vocabulary in finish() (where iteration order over a Go map is not
// deterministic, so a hard error here could not be reported deterministically),
// by the hand-authored bootstrap vocabulary in bootstrap.go, and by
// registerField for field predicates — a field's default predicate is
// per-record-scoped, so two unrelated records sharing a plain field name is
// ordinary and must not fail. Type names and enum symbols go through
// putVocabTerm instead, which is where the real collision check lives.
func (c *Context) putVocab(term, iri string) {
	if term == "" || iri == "" || isKeyword(iri) {
		return
	}

	if _, exists := c.vocab[term]; !exists {
		c.vocab[term] = iri
	}
}

// putVocabTerm records a vocabulary entry derived from a schema's own named
// type declarations, rejecting a term whose short name already maps to a
// different IRI.
//
// This is the analogue of schema-salad's
// SchemaException("Predicate collision on %s, %r != %r"): two different
// vocabulary IRIs — typically declared under different $bases in one $graph —
// must not resolve to the same short name for a type name. Only type names go
// through this path; enum symbols and field predicates keep their own
// per-container-scoped default and stay on putVocab (see registerSymbols and
// registerField).
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

// expandPrefix applies rule 7 of identifier resolution: a declared namespace
// prefix followed by a colon is replaced by the namespace IRI.
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
