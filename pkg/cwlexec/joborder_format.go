package cwlexec

import (
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// Vocabulary for expanding `format` IRIs in job orders using the process document's $namespaces.

// joDirNamespaces and joDirSchemas are the document directive keys for format resolution.
const (
	joDirNamespaces = "$namespaces"
	joDirSchemas    = "$schemas"
)

// joVocabulary holds prefix-to-IRI mappings and ontology presence for format expansion.
type joVocabulary struct {
	// namespaces maps a declared prefix to the IRI it stands for.
	namespaces map[string]string

	// hasOntology reports whether a $schemas directive named an ontology document.
	hasOntology bool
}

// joReadVocabulary collects $namespaces/$schemas from the process and job documents. Job wins on conflict.
func joReadVocabulary(p cwlcore.Process, job salad.Node) joVocabulary {
	vocab := joFileVocabulary(joProcessFile(p))
	vocab.merge(joNodeVocabulary(job))

	return vocab
}

// joFileVocabulary reads $namespaces/$schemas directives from the document at path.
func joFileVocabulary(path string) joVocabulary {
	if path == "" {
		return joVocabulary{namespaces: nil, hasOntology: false}
	}

	src, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return joVocabulary{namespaces: nil, hasOntology: false}
	}

	root, err := salad.Parse(path, src)
	if err != nil {
		return joVocabulary{namespaces: nil, hasOntology: false}
	}

	return joNodeVocabulary(root)
}

// joNodeVocabulary reads the directives from a document's root mapping.
func joNodeVocabulary(root salad.Node) joVocabulary {
	m, ok := salad.AsMap(root)
	if !ok {
		return joVocabulary{namespaces: nil, hasOntology: false}
	}

	vocab := joVocabulary{namespaces: make(map[string]string), hasOntology: joNamesOntology(m)}

	spaces, ok := salad.AsMap(joField(m, joDirNamespaces))
	if !ok {
		return vocab
	}

	for prefix, value := range spaces.All() {
		if iri, isText := salad.AsString(value); isText {
			vocab.namespaces[prefix] = iri
		}
	}

	return vocab
}

// joNamesOntology reports whether $schemas names at least one ontology.
func joNamesOntology(m *salad.MapNode) bool {
	schemas := joField(m, joDirSchemas)

	if _, ok := salad.AsString(schemas); ok {
		return true
	}

	seq, ok := salad.AsSeq(schemas)

	return ok && seq.Len() > 0
}

// joField reads a key from a mapping, returning nil when it is absent.
func joField(m *salad.MapNode, key string) salad.Node {
	node, ok := m.Get(key)
	if !ok {
		return nil
	}

	return node
}

// merge overlays other's prefixes onto v, other winning, and takes an ontology declared by either.
func (v *joVocabulary) merge(other joVocabulary) {
	v.hasOntology = v.hasOntology || other.hasOntology

	if len(other.namespaces) == 0 {
		return
	}

	if v.namespaces == nil {
		v.namespaces = make(map[string]string, len(other.namespaces))
	}

	maps.Copy(v.namespaces, other.namespaces)
}

// expandFormat resolves a prefixed format name to its full IRI using $namespaces.
func (v *joVocabulary) expandFormat(name string) string {
	prefix, rest, found := strings.Cut(name, ":")
	if !found || prefix == "" {
		return name
	}

	iri, declared := v.namespaces[prefix]
	if !declared {
		return name
	}

	return iri + rest
}

// joAllowedFormats returns the allowed format IRIs. Returns nil if any entry is an expression.
func joAllowedFormats(declared []cwlcore.Expression) []string {
	iris := make([]string, 0, len(declared))

	for _, entry := range declared {
		if cwlcore.NeedsParsing(string(entry)) {
			return nil
		}

		iris = append(iris, string(entry))
	}

	return iris
}

// checkFormat validates a File's format against allowed IRIs. Skipped when an ontology is present.
func (l *joLoader) checkFormat(file *cwlcore.File, v *joValueCtx) *salad.Error {
	if len(v.format) == 0 || l.vocab.hasOntology {
		return nil
	}

	err := cwlcore.CheckFormat(file, v.format, nil)
	if err != nil {
		return salad.Errorf(joNodeLoc(file.Node), "%s: %v", v.path, err)
	}

	return nil
}
