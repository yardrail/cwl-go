package cwlcore

import (
	"errors"
	"fmt"
	"slices"
)

// Format-compatibility failure sentinels.
var (
	// ErrFormatMissing reports a File with no format bound to a parameter requiring one.
	ErrFormatMissing = errors.New("file format not specified")

	// ErrFormatIncompatible reports a File whose format matches no allowed format.
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

// FormatOntology resolves format compatibility via rdfs:subClassOf and
// owl:equivalentClass edges. Zero value and nil are usable (exact match only).
type FormatOntology struct {
	// superClasses maps subclass -> superclass IRIs.
	superClasses map[string][]string

	// equivalents maps a class IRI to its equivalentClass peers (symmetric).
	equivalents map[string][]string
}

// LoadOntology parses an RDF/XML $schemas document with no external base URI.
// Prefer [LoadOntologyAt] when the fetch URL is known.
func LoadOntology(rdfxml []byte) (*FormatOntology, error) {
	return LoadOntologyAt(rdfxml, "")
}

// LoadOntologyAt parses an RDF/XML $schemas document with a base URI for resolving
// relative IRIs. Keeps only rdfs:subClassOf and owl:equivalentClass edges.
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
		}
	}

	return ontology, nil
}

// Merge folds the edges of other into o. Nil-safe.
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

// Compatible reports whether fileFormat satisfies required via subclass or equivalence.
// On a nil ontology, falls back to exact IRI equality.
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

// addEquivalent records an owl:equivalentClass edge (symmetric).
func (o *FormatOntology) addEquivalent(class, peer string) {
	if o.equivalents == nil {
		o.equivalents = make(map[string][]string)
	}

	o.equivalents[class] = appendUniqueIRI(o.equivalents[class], peer)
	o.equivalents[peer] = appendUniqueIRI(o.equivalents[peer], class)
}

// neighbours returns superclasses and equivalence peers of iri.
func (o *FormatOntology) neighbours(iri string) []string {
	supers := o.superClasses[iri]
	peers := o.equivalents[iri]

	out := make([]string, 0, len(supers)+len(peers))
	out = append(out, supers...)
	out = append(out, peers...)

	return out
}

// unseenNeighbours returns unvisited neighbours, marking them visited.
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

// appendUniqueIRI appends iri if not already present.
func appendUniqueIRI(list []string, iri string) []string {
	if slices.Contains(list, iri) {
		return list
	}

	return append(list, iri)
}

// CheckFormat validates a File's format against the allowed IRIs.
// Accepts typed *File, decoded maps, or slices of either. Directories are skipped.
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

// formatFileList normalizes a File-or-array-of-File value to []any.
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

// widenToAny converts a typed slice to []any.
func widenToAny[T any](values []T) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}

	return out
}

// formatValue is the format IRI and label of a value bound to a parameter.
type formatValue struct {
	// iri is the declared format, empty when the value declares none.
	iri string

	// label identifies the value in an error message.
	label string
}

// checkOneFormat validates a single File's format.
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

// asFormatValue extracts the format view. bearsFormat is false for nil or Directory.
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

// mapFormatValue reads format from a decoded map. Directories are skipped.
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

// mapStringValue reads a string key, returning "" if absent or non-string.
func mapStringValue(object map[string]any, key string) string {
	value, ok := object[key].(string)
	if !ok {
		return ""
	}

	return value
}

// filesystemLabel returns a human-readable label for a File.
func filesystemLabel(location, path, basename string) string {
	for _, value := range []string{location, path, basename} {
		if value != "" {
			return "file " + value
		}
	}

	return "file"
}
