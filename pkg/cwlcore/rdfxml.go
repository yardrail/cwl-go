package cwlcore

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
)

// Namespace IRIs used when interpreting RDF/XML format ontologies.
const (
	rdfNS  = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	rdfsNS = "http://www.w3.org/2000/01/rdf-schema#"
	owlNS  = "http://www.w3.org/2002/07/owl#"
	xmlNS  = "http://www.w3.org/XML/1998/namespace"
)

var (
	errRDFEmpty   = errors.New("rdf-xml: document contains no elements")
	errRDFNotRoot = errors.New("rdf-xml: expected an <rdf:RDF> root element")
)

// rdfTriple is a single RDF statement with an IRI-valued object.
type rdfTriple struct {
	Subject   string
	Predicate string
	Object    string
}

// rdfParser is a minimal RDF/XML reader for format ontologies.
// Supports node elements, property elements, and xml:base scoping.
type rdfParser struct {
	dec     *xml.Decoder
	triples []rdfTriple
}

// parseRDFXML extracts IRI-valued triples from an RDF/XML document.
func parseRDFXML(data []byte, baseURI string) ([]rdfTriple, error) {
	p := &rdfParser{dec: xml.NewDecoder(bytes.NewReader(data)), triples: nil}

	root, err := p.root()
	if err != nil {
		return nil, err
	}

	base := rdfElementBase(root, baseURI)

	err = p.eachChild(func(child xml.StartElement) error {
		_, nerr := p.node(child, base)

		return nerr
	})
	if err != nil {
		return nil, err
	}

	return p.triples, nil
}

// root advances to the document element and verifies that it is <rdf:RDF>.
func (p *rdfParser) root() (xml.StartElement, error) {
	for {
		tok, err := p.dec.Token()
		if errors.Is(err, io.EOF) {
			return xml.StartElement{}, errRDFEmpty
		}

		if err != nil {
			return xml.StartElement{}, fmt.Errorf("rdf-xml: %w", err)
		}

		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}

		if start.Name.Space != rdfNS || start.Name.Local != "RDF" {
			return xml.StartElement{}, fmt.Errorf("%w, found <%s>", errRDFNotRoot, rdfElementIRI(start.Name))
		}

		return start, nil
	}
}

// node parses a node element and returns its subject IRI (empty for blank nodes).
func (p *rdfParser) node(start xml.StartElement, base string) (string, error) {
	base = rdfElementBase(start, base)
	subject := rdfNodeSubject(start, base)
	p.addTypeOf(start, subject)

	err := p.eachChild(func(child xml.StartElement) error {
		return p.property(child, subject, base)
	})

	return subject, err
}

// property parses one property element, emitting a triple for IRI-valued objects.
func (p *rdfParser) property(start xml.StartElement, subject, base string) error {
	if _, ok := rdfAttr(start, rdfNS, "parseType"); ok {
		return p.skip()
	}

	base = rdfElementBase(start, base)
	predicate := rdfElementIRI(start.Name)

	if res, ok := rdfAttr(start, rdfNS, "resource"); ok {
		p.add(subject, predicate, rdfResolveIRI(base, res))

		return p.skip()
	}

	return p.eachChild(func(child xml.StartElement) error {
		object, err := p.node(child, base)
		if err != nil {
			return err
		}

		p.add(subject, predicate, object)

		return nil
	})
}

// eachChild invokes fn for every child element until the end tag.
func (p *rdfParser) eachChild(fn func(xml.StartElement) error) error {
	for {
		tok, err := p.dec.Token()
		if err != nil {
			return fmt.Errorf("rdf-xml: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			err := fn(t)
			if err != nil {
				return err
			}
		case xml.EndElement:
			return nil
		default:
			}
	}
}

// skip discards the subtree of the element currently open on the decoder.
func (p *rdfParser) skip() error {
	err := p.dec.Skip()
	if err != nil {
		return fmt.Errorf("rdf-xml: %w", err)
	}

	return nil
}

// addTypeOf records the rdf:type triple implied by a typed node element.
func (p *rdfParser) addTypeOf(start xml.StartElement, subject string) {
	if start.Name.Space == rdfNS && start.Name.Local == "Description" {
		return
	}

	p.add(subject, rdfNS+"type", rdfElementIRI(start.Name))
}

// add records a triple, dropping statements that involve a blank node.
func (p *rdfParser) add(subject, predicate, object string) {
	if subject == "" || object == "" {
		return
	}

	p.triples = append(p.triples, rdfTriple{Subject: subject, Predicate: predicate, Object: object})
}

// rdfElementIRI returns namespace + local name.
func rdfElementIRI(name xml.Name) string {
	return name.Space + name.Local
}

// rdfAttr returns the value of the named attribute, and whether it was present.
func rdfAttr(start xml.StartElement, space, local string) (string, bool) {
	for _, attr := range start.Attr {
		if attr.Name.Space == space && attr.Name.Local == local {
			return attr.Value, true
		}
	}

	return "", false
}

// rdfElementBase resolves the element's xml:base against the current base.
func rdfElementBase(start xml.StartElement, base string) string {
	value, ok := rdfAttr(start, xmlNS, "base")
	if !ok {
		value, ok = rdfAttr(start, "xml", "base")
	}

	if !ok {
		return base
	}

	return rdfResolveIRI(base, value)
}

// rdfNodeSubject derives a node's subject IRI from rdf:about or rdf:ID.
func rdfNodeSubject(start xml.StartElement, base string) string {
	if about, ok := rdfAttr(start, rdfNS, "about"); ok {
		return rdfResolveIRI(base, about)
	}

	if id, ok := rdfAttr(start, rdfNS, "ID"); ok {
		return rdfResolveIRI(base, "#"+id)
	}

	return ""
}

// rdfResolveIRI resolves a possibly relative reference against base.
func rdfResolveIRI(base, ref string) string {
	if base == "" {
		return ref
	}

	baseURL, err := url.Parse(base)
	if err != nil {
		return ref
	}

	refURL, err := url.Parse(ref)
	if err != nil {
		return ref
	}

	return baseURL.ResolveReference(refURL).String()
}
