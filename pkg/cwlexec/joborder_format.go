package cwlexec

import (
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// The vocabulary a job order's `format` values are read in.
//
// A `format` is an IRI naming a concept node in a file-format ontology, and a document may write
// one as a prefixed name — `edam:format_1929` — against a prefix its `$namespaces` declares. The
// process document declares the prefixes; the job order uses them. tests/formattest2-job.json in
// the conformance suite is exactly that: a job order whose only namespace prefix is defined in
// tests/formattest2.cwl.
//
// pkg/salad expands such a name while it resolves a document, because Process.yml gives `format`
// a jsonldPredicate of `_type: "@id"`. A job order goes through no such resolution — it is not a
// CWL document and has no schema — so the expansion has to happen here, against the process
// document's table. This is what the reference implementation does too: cwltool's load_job_order
// merges the tool's $namespaces into the context the job order is resolved with.

// joDirNamespaces and joDirSchemas are the two document directives a job order's formats are read
// against.
const (
	joDirNamespaces = "$namespaces"
	joDirSchemas    = "$schemas"
)

// joVocabulary is the linked-data view of the documents a job order is read against: the
// `$namespaces` prefix table a `format` expands with, and whether any `$schemas` ontology was
// named that could describe the resulting IRIs.
//
// The zero value is usable and means "no prefixes, no ontology", which is what a process built in
// memory or loaded from a document nothing can read gets.
type joVocabulary struct {
	// namespaces maps a declared prefix to the IRI it stands for.
	namespaces map[string]string

	// hasOntology reports whether a $schemas directive named an ontology document.
	hasOntology bool
}

// joReadVocabulary collects the directives in force for a job order: those of the process
// document, overlaid with any the job document declares for itself.
//
// The job order wins on a conflicting prefix, because it is the document the value being expanded
// is written in, and a document's own directives govern its own contents.
//
// Nothing here is fatal. A process with no readable document — a blank-node identifier, a remote
// URL, a file since deleted — simply contributes no prefixes, which leaves a prefixed name
// unexpanded exactly as it arrived.
func joReadVocabulary(p cwlcore.Process, job salad.Node) joVocabulary {
	vocab := joFileVocabulary(joProcessFile(p))
	vocab.merge(joNodeVocabulary(job))

	return vocab
}

// joFileVocabulary reads the directives of the document at path.
//
// The document is parsed raw rather than loaded through pkg/cwlcore: `$namespaces` and `$schemas`
// are processing directives, already in their final form before any resolution runs, so reaching
// them needs neither the schema nor a second validation of a tree that has already been decoded
// once.
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

// joNamesOntology reports whether a document's $schemas directive names at least one ontology
// document, in either of the shapes the directive takes: a single IRI or a list of them.
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

// expandFormat resolves a `format` value to its full IRI, applying rule 7 of the specification's
// identifier resolution: "If the value is a string, and matches the pattern of a namespace prefix
// followed by a colon, and the prefix is declared in $namespaces, the prefix is replaced by the
// namespace IRI."
//
// Only that rule is applied. The remaining link-resolution rules would resolve a reference with
// no prefix against the base URI, which for a job order is the job file's own directory: a
// `format: fasta` would become a file:// IRI naming a file that does not exist and that no
// ontology can describe. Leaving such a value alone is both closer to what the author wrote and
// the only form that can still match an exact-match comparison.
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

// joAllowedFormats reduces a parameter's or record field's declared `format` to the list of IRIs a
// File bound there may carry.
//
// An entry that is an expression drops the whole constraint. Process.yml types an input's format
// as `string | string[] | Expression`, and an expression's value is not known until the input
// object is complete — which is after job-order loading, since the expression may read `inputs`.
// Enforcing the entries around it would reject values the parameter in fact accepts.
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

// checkFormat validates a File against the format IRIs the parameter or record field declaring it
// allows.
//
// Process.yml, InputFormat: the declared value "must be one or more IRIs of concept nodes that
// represents file formats which are allowed as input to this parameter, preferably defined within
// an ontology. If no ontology is available, file formats may be tested by exact match."
//
// That last sentence is the whole shape of this check, and why it is skipped when the process
// document names a `$schemas` ontology. Compatibility is not IRI equality: a format may satisfy a
// declaration by being its rdfs:subClassOf or its owl:equivalentClass, and deciding that needs the
// ontology. Applying exact match in its presence would reject a file whose format is a legitimate
// subtype of the declared one — a wrong answer rather than a missing one — so exact match is
// applied only in the case the specification licenses it for.
//
// The ontologies themselves are not loaded here. [cwlcore.LoadOntology] reads RDF/XML, and the
// suite's own ontologies include a Turtle document it cannot read, so a document that names one
// is left unchecked rather than failed on a format the engine simply cannot reason about.
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
