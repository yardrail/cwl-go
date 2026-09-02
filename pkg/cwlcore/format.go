package cwlcore

import (
	"errors"
	"fmt"
	"slices"
)

// Format-compatibility failure sentinels, for callers that want to distinguish
// the two ways CheckFormat can reject a File.
var (
	// ErrFormatMissing reports a File value that carries no usable format IRI
	// while the parameter it is bound to requires one.
	ErrFormatMissing = errors.New("file format not specified")

	// ErrFormatIncompatible reports a File whose format IRI is neither equal
	// to, owl:equivalentClass of, nor rdfs:subClassOf any format the parameter
	// allows.
	ErrFormatIncompatible = errors.New("incompatible file format")

	errNotAFileObject = errors.New("value is not a File object")
)

// Predicates that carry format-compatibility edges.
const (
	subClassOfIRI      = rdfsNS + "subClassOf"
	equivalentClassIRI = owlNS + "equivalentClass"
)

// Keys read off a File or Directory decoded as a generic map.
const (
	fileKeyClass    = "class"
	fileKeyFormat   = "format"
	fileKeyLocation = "location"
	fileKeyPath     = "path"
	fileKeyBasename = "basename"
)

// FormatOntology answers format-compatibility questions over the
// rdfs:subClassOf and owl:equivalentClass edges declared by the RDF/XML
// ontologies named in a document's $schemas metadata.
//
// It stores the one-hop edge sets and derives their transitive closure on
// demand in Compatible, rather than materializing the closure up front. The
// two are equivalent, and keeping the edges means Merge stays a cheap union
// instead of invalidating precomputed state.
//
// The zero value is a usable empty ontology, and every method is safe to call
// on a nil *FormatOntology — a nil ontology behaves as the spec's fallback,
// where formats are compared by exact match. A FormatOntology is immutable
// once built except through Merge, and is safe for concurrent use as long as
// no Merge is in flight.
type FormatOntology struct {
	// superClasses maps a class IRI to the IRIs it declares itself a
	// rdfs:subClassOf. Directed: subclass -> superclass.
	superClasses map[string][]string

	// equivalents maps a class IRI to its owl:equivalentClass peers. Stored
	// symmetrically, because owl:equivalentClass is a symmetric property.
	equivalents map[string][]string
}

// LoadOntology parses one RDF/XML $schemas document into a FormatOntology with
// no external base URI, so relative IRIs are resolved against the document's
// own xml:base and otherwise kept verbatim. It is equivalent to
// LoadOntologyAt(rdfxml, "").
//
// Prefer LoadOntologyAt whenever the URL the document was fetched from is
// known: a document that uses relative identifiers without declaring xml:base
// would otherwise keep them relative, and they would then never compare equal
// to the absolute IRI a format field names.
func LoadOntology(rdfxml []byte) (*FormatOntology, error) {
	return LoadOntologyAt(rdfxml, "")
}

// LoadOntologyAt is LoadOntology with an explicit base URI, used to resolve
// relative IRIs in documents that do not carry their own xml:base. baseURI is
// the URL the $schemas entry was fetched from.
//
// It keeps the document's rdfs:subClassOf and owl:equivalentClass statements
// and ignores everything else. It returns an error only for input that is not
// readable as RDF/XML at all; an ontology that declares no format edges loads
// successfully as an empty one.
//
// baseURI is a fallback, not an override: a document's own xml:base wins over
// it, and an inner xml:base still wins over an outer one, per XML Base
// scoping. An empty baseURI leaves relative references verbatim.
func LoadOntologyAt(rdfxml []byte, baseURI string) (*FormatOntology, error) {
	triples, err := parseRDFXML(rdfxml, baseURI)
	if err != nil {
		return nil, err
	}

	ontology := &FormatOntology{superClasses: nil, equivalents: nil}

	for _, triple := range triples {
		switch triple.Predicate {
		case subClassOfIRI:
			ontology.addSuperClass(triple.Subject, triple.Object)
		case equivalentClassIRI:
			ontology.addEquivalent(triple.Subject, triple.Object)
		default:
			// Every other statement — labels, definitions, ontology headers,
			// property declarations — carries no compatibility edge.
		}
	}

	return ontology, nil
}

// Merge folds the edges of other into o, which is how the ontologies from
// several $schemas entries combine into the single graph compatibility is
// reasoned over. Edges may span documents: a class declared in one may be a
// subclass of one declared in another.
//
// Merging nil, or merging into a nil ontology, is a no-op.
func (o *FormatOntology) Merge(other *FormatOntology) {
	if o == nil || other == nil {
		return
	}

	for sub, supers := range other.superClasses {
		for _, super := range supers {
			o.addSuperClass(sub, super)
		}
	}

	for class, peers := range other.equivalents {
		for _, peer := range peers {
			o.addEquivalent(class, peer)
		}
	}
}

// Compatible reports whether a File whose format is fileFormat satisfies a
// parameter that requires the format required.
//
// Per the CWL v1.2 spec: "Reasoning about format compatibility must be done by
// checking that an input file format is the same, owl:equivalentClass or
// rdfs:subClassOf the format required by the input parameter.
// owl:equivalentClass is transitive with rdfs:subClassOf, e.g. if
// <B> owl:equivalentClass <C> and <B> owl:subclassOf <A> then infer
// <C> owl:subclassOf <A>."
//
// Note the direction: the file's format is the subclass and the parameter's
// format is the superclass, so a more specific input satisfies a more general
// requirement and never the reverse. Compatible therefore walks upwards from
// fileFormat, following rdfs:subClassOf towards superclasses and
// owl:equivalentClass in both directions, and reports whether required is
// reachable. Cycles in the ontology are traversed at most once.
//
// On a nil ontology Compatible degrades to exact IRI equality, per the spec's
// "If no ontologies are specified in $schemas, the runtime may perform exact
// file format matches".
func (o *FormatOntology) Compatible(fileFormat, required string) bool {
	if fileFormat == required {
		return true
	}

	if o == nil {
		return false
	}

	seen := make(map[string]struct{})
	seen[fileFormat] = struct{}{}

	queue := make([]string, 0, len(o.superClasses))
	queue = append(queue, fileFormat)

	for len(queue) > 0 {
		last := len(queue) - 1
		current := queue[last]
		queue = queue[:last]

		if current == required {
			return true
		}

		queue = append(queue, o.unseenNeighbours(current, seen)...)
	}

	return false
}

// addSuperClass records a directed rdfs:subClassOf edge.
func (o *FormatOntology) addSuperClass(sub, super string) {
	if o.superClasses == nil {
		o.superClasses = make(map[string][]string)
	}

	o.superClasses[sub] = appendUniqueIRI(o.superClasses[sub], super)
}

// addEquivalent records an owl:equivalentClass edge in both directions, since
// the property is symmetric.
func (o *FormatOntology) addEquivalent(class, peer string) {
	if o.equivalents == nil {
		o.equivalents = make(map[string][]string)
	}

	o.equivalents[class] = appendUniqueIRI(o.equivalents[class], peer)
	o.equivalents[peer] = appendUniqueIRI(o.equivalents[peer], class)
}

// neighbours returns the IRIs one closure hop from iri: its declared
// superclasses and its equivalence peers, which the spec's transitivity rule
// makes interchangeable when walking towards a required format.
func (o *FormatOntology) neighbours(iri string) []string {
	supers := o.superClasses[iri]
	peers := o.equivalents[iri]

	out := make([]string, 0, len(supers)+len(peers))
	out = append(out, supers...)
	out = append(out, peers...)

	return out
}

// unseenNeighbours returns the not-yet-visited neighbours of iri, marking them
// visited. Marking on enqueue is what keeps a cyclic ontology from looping.
func (o *FormatOntology) unseenNeighbours(iri string, seen map[string]struct{}) []string {
	candidates := o.neighbours(iri)
	out := make([]string, 0, len(candidates))

	for _, candidate := range candidates {
		if _, visited := seen[candidate]; visited {
			continue
		}

		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}

	return out
}

// appendUniqueIRI appends iri to list unless it is already present, keeping
// edge order stable and deterministic.
func appendUniqueIRI(list []string, iri string) []string {
	if slices.Contains(list, iri) {
		return list
	}

	return append(list, iri)
}

// CheckFormat validates a File value's format against the format IRIs a
// parameter allows.
//
// file is the value bound to the parameter, in either representation: a typed
// *File (or File) as decode.go produces, or a decoded map with a "format" key
// as an unconverted default or a generic caller holds. For an array-of-File
// parameter it may be a slice of either — []any, []FileOrDirectory, []*File,
// []File or []map[string]any. nil, and nil entries within a slice, are skipped.
// allowed is the parameter's format field normalized to a list of IRIs.
//
// An empty allowed list imposes no constraint and always passes: format is an
// optional field on input parameters, and a parameter that declares none
// accepts any file, including one with no format. A File with no format bound
// to a parameter that does require one is a validation failure
// (ErrFormatMissing) — the converse is not symmetric.
//
// A Directory is skipped rather than rejected, in both representations. The
// schema gives Directory only class, location, path, basename and listing: it
// has no format field at all, and the spec discusses format compatibility
// solely in terms of File.format. A Directory therefore cannot carry a format
// to check, and treating one as a violation would report a format error for a
// value the format vocabulary does not describe. This matters concretely for
// File.SecondaryFiles and Directory.Listing, which are []FileOrDirectory and
// so mix the two freely.
//
// Only the value bound to the parameter is checked. Secondary files are not
// examined against the parameter's format: they are a companion of the primary
// file with a format of their own — an index beside an alignment — and which
// ones are required is decided by the parameter's own secondaryFiles patterns,
// a separate mechanism.
//
// A nil ontology falls back to exact IRI match, per the spec's "If no
// ontologies are specified in $schemas, the runtime may perform exact file
// format matches".
func CheckFormat(file any, allowed []string, o *FormatOntology) error {
	if len(allowed) == 0 {
		return nil
	}

	for _, value := range formatFileList(file) {
		err := checkOneFormat(value, allowed, o)
		if err != nil {
			return err
		}
	}

	return nil
}

// formatFileList normalizes a File-or-array-of-File value to a slice, widening
// the typed slice shapes a decoded document or a runtime can produce.
func formatFileList(file any) []any {
	switch value := file.(type) {
	case nil:
		return nil
	case []any:
		return value
	case []FileOrDirectory:
		return widenToAny(value)
	case []*File:
		return widenToAny(value)
	case []File:
		return widenToAny(value)
	case []map[string]any:
		return widenToAny(value)
	default:
		return append(make([]any, 0, 1), value)
	}
}

// widenToAny converts a typed slice to []any so one code path handles them all.
func widenToAny[T any](values []T) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}

	return out
}

// formatValue is the format-bearing view of a value bound to a parameter,
// reduced from whichever representation it arrived in.
type formatValue struct {
	// iri is the declared format, empty when the value declares none.
	iri string

	// label identifies the value in an error message.
	label string
}

// checkOneFormat validates a single value from a File-or-array-of-File binding.
func checkOneFormat(file any, allowed []string, o *FormatOntology) error {
	view, bearsFormat, err := asFormatValue(file)
	if err != nil {
		return err
	}

	if !bearsFormat {
		return nil
	}

	if view.iri == "" {
		return fmt.Errorf("%w: %s", ErrFormatMissing, view.label)
	}

	for _, want := range allowed {
		if o.Compatible(view.iri, want) {
			return nil
		}
	}

	return fmt.Errorf(
		"%w: %s has format %q, which is not compatible with any of %v",
		ErrFormatIncompatible,
		view.label,
		view.iri,
		allowed,
	)
}

// asFormatValue reduces a bound value to its format-bearing view. bearsFormat
// is false for a value that carries no format by definition — a nil, or a
// Directory — which the caller skips. The error is reserved for a value that
// is not a filesystem object at all.
func asFormatValue(file any) (formatValue, bool, error) {
	switch value := file.(type) {
	case nil:
		return formatValue{iri: "", label: ""}, false, nil
	case *File:
		if value == nil {
			return formatValue{iri: "", label: ""}, false, nil
		}

		return fileFormatValue(value), true, nil
	case File:
		return fileFormatValue(&value), true, nil
	case *Directory, Directory:
		return formatValue{iri: "", label: ""}, false, nil
	case map[string]any:
		return mapFormatValue(value)
	default:
		return formatValue{}, false, fmt.Errorf("%w: got %T", errNotAFileObject, file)
	}
}

// fileFormatValue is the view of a typed File.
func fileFormatValue(file *File) formatValue {
	return formatValue{
		iri:   file.Format,
		label: filesystemLabel(file.Location, file.Path, file.Basename),
	}
}

// mapFormatValue is the view of a File decoded as a generic map. A map whose
// class is Directory is skipped, matching the typed *Directory case.
func mapFormatValue(object map[string]any) (formatValue, bool, error) {
	label := filesystemLabel(
		mapStringValue(object, fileKeyLocation),
		mapStringValue(object, fileKeyPath),
		mapStringValue(object, fileKeyBasename),
	)

	if mapStringValue(object, fileKeyClass) == ClassDirectory {
		return formatValue{iri: "", label: ""}, false, nil
	}

	raw, declared := object[fileKeyFormat]
	if !declared {
		return formatValue{iri: "", label: label}, true, nil
	}

	iri, ok := raw.(string)
	if !ok {
		return formatValue{}, false,
			fmt.Errorf("%w: %s has a non-IRI format value (%T)", ErrFormatMissing, label, raw)
	}

	return formatValue{iri: iri, label: label}, true, nil
}

// mapStringValue reads a string-valued key, treating absent and non-string
// values alike as empty.
func mapStringValue(object map[string]any, key string) string {
	value, ok := object[key].(string)
	if !ok {
		return ""
	}

	return value
}

// filesystemLabel names a File in an error message, preferring whichever
// identifying field it carries.
func filesystemLabel(location, path, basename string) string {
	for _, value := range []string{location, path, basename} {
		if value != "" {
			return "file " + value
		}
	}

	return "file"
}
