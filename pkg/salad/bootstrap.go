package salad

import "sync"

// Namespaces the Schema Salad metaschema declares, needed before any document
// can be read.
const (
	saladNS = "https://w3id.org/cwl/salad#"
	xsdNS   = "http://www.w3.org/2001/XMLSchema#"
	rdfNS   = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	rdfsNS  = "http://www.w3.org/2000/01/rdf-schema#"
	dctNS   = "http://purl.org/dc/terms/"
)

// How many scope levels the metaschema's own reference fields strip before
// searching parent scopes: a type reference sits one level below the field that
// holds it, a bare name reference does not.
const (
	refScopeName = 1
	refScopeType = 2
)

// bootstrapTerm is one entry of the bootstrap term table.
type bootstrapTerm struct {
	field string
	def   TermDef
}

// bootstrapTerms is the term table needed to read a Schema Salad schema
// document.
//
// It breaks the chicken-and-egg at the bottom of the system: resolving a schema
// document requires a context, and building a context requires a resolved schema
// document. schema-salad solves it the same way, with a literal context in
// schema.py's get_metaschema. Every entry here restates a jsonldPredicate the
// metaschema declares on itself; nothing is invented.
var bootstrapTerms = []bootstrapTerm{
	{keyName, TermDef{ID: keywordID, IsIdentifier: true}},
	{"_id", TermDef{ID: "sld:_id", Type: keywordID, Identity: true}},
	{keyType, TermDef{ID: "sld:type", Type: keywordVocab, TypeDSL: true, RefScope: refScopeType, ScopedRef: true}},
	{keyFields, TermDef{ID: "sld:fields", MapSubject: keyName, MapPredicate: keyType}},
	{keySymbols, TermDef{ID: "sld:symbols", Type: keywordID, Identity: true}},
	{keyItems, TermDef{ID: "sld:items", Type: keywordVocab, RefScope: refScopeType, ScopedRef: true}},
	{"values", TermDef{ID: "sld:values", Type: keywordVocab, RefScope: refScopeType, ScopedRef: true}},
	{"names", TermDef{ID: "sld:names", Type: keywordVocab, RefScope: refScopeType, ScopedRef: true}},
	{"extends", TermDef{ID: "sld:extends", Type: keywordID, RefScope: refScopeName, ScopedRef: true}},
	{"specialize", TermDef{ID: "sld:specialize", MapSubject: "specializeFrom", MapPredicate: "specializeTo"}},
	{"specializeFrom", TermDef{ID: "sld:specializeFrom", Type: keywordID, RefScope: refScopeName, ScopedRef: true}},
	{"specializeTo", TermDef{ID: "sld:specializeTo", Type: keywordID, RefScope: refScopeName, ScopedRef: true}},
	{"jsonldPredicate", TermDef{ID: "sld:jsonldPredicate", NoLinkCheck: true}},
	{"default", TermDef{ID: "sld:default", NoLinkCheck: true}},
	{"doc", TermDef{ID: "rdfs:comment"}},
	{"docParent", TermDef{ID: "sld:docParent", Type: keywordID}},
	{"docChild", TermDef{ID: "sld:docChild", Type: keywordID}},
	{"docAfter", TermDef{ID: "sld:docAfter", Type: keywordID}},
	{"documentRoot", TermDef{ID: "sld:documentRoot"}},
	{"abstract", TermDef{ID: "sld:abstract"}},
	{"inVocab", TermDef{ID: "sld:inVocab"}},
}

// bootstrapVocab is the type vocabulary of the metaschema: the primitive type
// names and the type-declaration keywords, which schema documents spell by their
// short names.
var bootstrapVocab = map[string]string{
	nameNull:          "sld:null",
	nameBoolean:       "xsd:boolean",
	nameInt:           "xsd:int",
	nameLong:          "xsd:long",
	nameFloat:         "xsd:float",
	nameDouble:        "xsd:double",
	nameString:        "xsd:string",
	nameAny:           "sld:Any",
	kindRecord:        "sld:record",
	kindEnum:          "sld:enum",
	kindArray:         "sld:array",
	kindMap:           "sld:map",
	kindUnion:         "sld:union",
	kindDocumentation: "sld:documentation",
}

// saladBootstrapContext returns the context a Schema Salad schema document is
// loaded with, before its own vocabulary is known. It is memoized: it is a
// constant table, and every schema load needs it.
var saladBootstrapContext = sync.OnceValue(buildSaladBootstrapContext)

// buildSaladBootstrapContext assembles the bootstrap context from its tables.
func buildSaladBootstrapContext() *Context {
	c := newContext()
	c.namespaces["sld"] = saladNS
	c.namespaces["xsd"] = xsdNS
	c.namespaces["rdf"] = rdfNS
	c.namespaces["rdfs"] = rdfsNS
	c.namespaces["dct"] = dctNS

	for name, iri := range bootstrapVocab {
		c.putVocab(name, c.expandPrefix(iri))
	}

	for _, term := range bootstrapTerms {
		def := term.def
		def.ID = c.expandPrefix(def.ID)
		c.terms[term.field] = &def

		if !def.IsIdentifier {
			c.putVocab(term.field, def.ID)
		}
	}

	c.finish()

	return c
}
