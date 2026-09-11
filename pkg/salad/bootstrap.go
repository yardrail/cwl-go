package salad

import "sync"

// Schema Salad metaschema namespaces.
const (
	saladNS = "https://w3id.org/cwl/salad#"
	xsdNS   = "http://www.w3.org/2001/XMLSchema#"
	rdfNS   = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	rdfsNS  = "http://www.w3.org/2000/01/rdf-schema#"
	dctNS   = "http://purl.org/dc/terms/"
)

// Scope levels the metaschema's reference fields strip before parent-scope search.
const (
	refScopeName = 1
	refScopeType = 2
)

// bootstrapTerm is one entry of the bootstrap term table.
type bootstrapTerm struct {
	field string
	def   TermDef
}

// bootstrapTerms is the hard-coded term table for reading schema documents,
// breaking the schema-needs-context-needs-schema cycle.
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

// bootstrapVocab maps primitive type names and type-declaration keywords to their IRIs.
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

// saladBootstrapContext returns the memoized context for loading schema documents.
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
