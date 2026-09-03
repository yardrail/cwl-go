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
	{
		keyName,
		TermDef{
			ID:                keywordID,
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
			IsIdentifier:      true,
		},
	},
	{
		"_id",
		TermDef{
			ID:                "sld:_id",
			Type:              keywordID,
			Subscope:          "",
			MapSubject:        "",
			MapPredicate:      "",
			RefScope:          0,
			Identity:          true,
			Noconvert:         false,
			NoLinkCheck:       false,
			TypeDSL:           false,
			SecondaryFilesDSL: false,
			ScopedRef:         false,
			IsIdentifier:      false,
		},
	},
	{
		keyType,
		TermDef{
			ID:                "sld:type",
			Type:              keywordVocab,
			Subscope:          "",
			MapSubject:        "",
			MapPredicate:      "",
			RefScope:          refScopeType,
			Identity:          false,
			Noconvert:         false,
			NoLinkCheck:       false,
			TypeDSL:           true,
			SecondaryFilesDSL: false,
			ScopedRef:         true,
			IsIdentifier:      false,
		},
	},
	{
		keyFields,
		TermDef{
			ID:                "sld:fields",
			Type:              "",
			Subscope:          "",
			MapSubject:        keyName,
			MapPredicate:      keyType,
			RefScope:          0,
			Identity:          false,
			Noconvert:         false,
			NoLinkCheck:       false,
			TypeDSL:           false,
			SecondaryFilesDSL: false,
			ScopedRef:         false,
			IsIdentifier:      false,
		},
	},
	{
		keySymbols,
		TermDef{
			ID:                "sld:symbols",
			Type:              keywordID,
			Subscope:          "",
			MapSubject:        "",
			MapPredicate:      "",
			RefScope:          0,
			Identity:          true,
			Noconvert:         false,
			NoLinkCheck:       false,
			TypeDSL:           false,
			SecondaryFilesDSL: false,
			ScopedRef:         false,
			IsIdentifier:      false,
		},
	},
	{
		keyItems,
		TermDef{
			ID:                "sld:items",
			Type:              keywordVocab,
			Subscope:          "",
			MapSubject:        "",
			MapPredicate:      "",
			RefScope:          refScopeType,
			Identity:          false,
			Noconvert:         false,
			NoLinkCheck:       false,
			TypeDSL:           false,
			SecondaryFilesDSL: false,
			ScopedRef:         true,
			IsIdentifier:      false,
		},
	},
	{
		"values",
		TermDef{
			ID:                "sld:values",
			Type:              keywordVocab,
			Subscope:          "",
			MapSubject:        "",
			MapPredicate:      "",
			RefScope:          refScopeType,
			Identity:          false,
			Noconvert:         false,
			NoLinkCheck:       false,
			TypeDSL:           false,
			SecondaryFilesDSL: false,
			ScopedRef:         true,
			IsIdentifier:      false,
		},
	},
	{
		"names",
		TermDef{
			ID:                "sld:names",
			Type:              keywordVocab,
			Subscope:          "",
			MapSubject:        "",
			MapPredicate:      "",
			RefScope:          refScopeType,
			Identity:          false,
			Noconvert:         false,
			NoLinkCheck:       false,
			TypeDSL:           false,
			SecondaryFilesDSL: false,
			ScopedRef:         true,
			IsIdentifier:      false,
		},
	},
	{
		"extends",
		TermDef{
			ID:                "sld:extends",
			Type:              keywordID,
			Subscope:          "",
			MapSubject:        "",
			MapPredicate:      "",
			RefScope:          refScopeName,
			Identity:          false,
			Noconvert:         false,
			NoLinkCheck:       false,
			TypeDSL:           false,
			SecondaryFilesDSL: false,
			ScopedRef:         true,
			IsIdentifier:      false,
		},
	},
	{
		"specialize",
		TermDef{
			ID:                "sld:specialize",
			Type:              "",
			Subscope:          "",
			MapSubject:        "specializeFrom",
			MapPredicate:      "specializeTo",
			RefScope:          0,
			Identity:          false,
			Noconvert:         false,
			NoLinkCheck:       false,
			TypeDSL:           false,
			SecondaryFilesDSL: false,
			ScopedRef:         false,
			IsIdentifier:      false,
		},
	},
	{
		"specializeFrom",
		TermDef{
			ID:                "sld:specializeFrom",
			Type:              keywordID,
			Subscope:          "",
			MapSubject:        "",
			MapPredicate:      "",
			RefScope:          refScopeName,
			Identity:          false,
			Noconvert:         false,
			NoLinkCheck:       false,
			TypeDSL:           false,
			SecondaryFilesDSL: false,
			ScopedRef:         true,
			IsIdentifier:      false,
		},
	},
	{
		"specializeTo",
		TermDef{
			ID:                "sld:specializeTo",
			Type:              keywordID,
			Subscope:          "",
			MapSubject:        "",
			MapPredicate:      "",
			RefScope:          refScopeName,
			Identity:          false,
			Noconvert:         false,
			NoLinkCheck:       false,
			TypeDSL:           false,
			SecondaryFilesDSL: false,
			ScopedRef:         true,
			IsIdentifier:      false,
		},
	},
	{
		"jsonldPredicate",
		TermDef{
			ID:                "sld:jsonldPredicate",
			Type:              "",
			Subscope:          "",
			MapSubject:        "",
			MapPredicate:      "",
			RefScope:          0,
			Identity:          false,
			Noconvert:         false,
			NoLinkCheck:       true,
			TypeDSL:           false,
			SecondaryFilesDSL: false,
			ScopedRef:         false,
			IsIdentifier:      false,
		},
	},
	{
		"default",
		TermDef{
			ID:                "sld:default",
			Type:              "",
			Subscope:          "",
			MapSubject:        "",
			MapPredicate:      "",
			RefScope:          0,
			Identity:          false,
			Noconvert:         false,
			NoLinkCheck:       true,
			TypeDSL:           false,
			SecondaryFilesDSL: false,
			ScopedRef:         false,
			IsIdentifier:      false,
		},
	},
	{
		"doc",
		TermDef{
			ID:                "rdfs:comment",
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
		},
	},
	{
		"docParent",
		TermDef{
			ID:                "sld:docParent",
			Type:              keywordID,
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
		},
	},
	{
		"docChild",
		TermDef{
			ID:                "sld:docChild",
			Type:              keywordID,
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
		},
	},
	{
		"docAfter",
		TermDef{
			ID:                "sld:docAfter",
			Type:              keywordID,
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
		},
	},
	{
		"documentRoot",
		TermDef{
			ID:                "sld:documentRoot",
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
		},
	},
	{
		"abstract",
		TermDef{
			ID:                "sld:abstract",
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
		},
	},
	{
		"inVocab",
		TermDef{
			ID:                "sld:inVocab",
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
		},
	},
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
