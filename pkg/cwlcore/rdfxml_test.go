package cwlcore

import (
	"reflect"
	"strings"
	"testing"
)

const (
	rdfTestHeader = `<?xml version="1.0"?>` +
		`<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"` +
		` xmlns:rdfs="http://www.w3.org/2000/01/rdf-schema#"` +
		` xmlns:owl="http://www.w3.org/2002/07/owl#"`

	rdfTestBase = "http://example.org/t"
	rdfTestA    = rdfTestBase + "#a"
	rdfTestB    = rdfTestBase + "#b"

	rdfErrPrefix = "rdf-xml:"

	owlClassIRI = owlNS + "Class"
	rdfTypeIRI  = rdfNS + "type"
)

// rdfTestDoc wraps body in an rdf:RDF document element carrying attrs.
func rdfTestDoc(attrs, body string) []byte {
	return []byte(rdfTestHeader + " " + attrs + ">" + body + "</rdf:RDF>")
}

// rdfFormCase is one RDF/XML document body and the triples it must yield.
// base is the external base URI the document is parsed against.
type rdfFormCase struct {
	name  string
	attrs string
	body  string
	base  string
	want  []rdfTriple
}

// runRDFFormCases parses each case's document and compares the triples.
func runRDFFormCases(t *testing.T, tests []rdfFormCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseRDFXML(rdfTestDoc(tt.attrs, tt.body), tt.base)
			if err != nil {
				t.Fatalf("parseRDFXML() error = %v", err)
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseRDFXML() =\n  %v\nwant\n  %v", got, tt.want)
			}
		})
	}
}

func TestParseRDFXMLNodeAndPropertyForms(t *testing.T) {
	t.Parallel()

	runRDFFormCases(t, []rdfFormCase{
		{
			name: "about and rdf:resource attribute form",
			body: `<owl:Class rdf:about="http://example.org/t#a">` +
				`<rdfs:subClassOf rdf:resource="http://example.org/t#b"/>` +
				`</owl:Class>`,
			want: []rdfTriple{
				{rdfTestA, rdfTypeIRI, owlClassIRI},
				{rdfTestA, subClassOfIRI, rdfTestB},
			},
		},
		{
			name: "nested node element form",
			body: `<owl:Class rdf:about="http://example.org/t#a">` +
				`<rdfs:subClassOf><owl:Class rdf:about="http://example.org/t#b"/></rdfs:subClassOf>` +
				`</owl:Class>`,
			want: []rdfTriple{
				{rdfTestA, rdfTypeIRI, owlClassIRI},
				{rdfTestB, rdfTypeIRI, owlClassIRI},
				{rdfTestA, subClassOfIRI, rdfTestB},
			},
		},
		{
			name:  "rdf:ID and relative resource resolve against xml:base",
			attrs: `xml:base="http://example.org/t"`,
			body: `<rdf:Description rdf:ID="a">` +
				`<rdfs:subClassOf rdf:resource="#b"/>` +
				`</rdf:Description>`,
			want: []rdfTriple{
				{rdfTestA, subClassOfIRI, rdfTestB},
			},
		},
		{
			name:  "inner xml:base overrides the document base",
			attrs: `xml:base="http://example.org/outer"`,
			body: `<rdf:Description rdf:about="#a" xml:base="http://example.org/t">` +
				`<rdfs:subClassOf rdf:resource="#b"/>` +
				`</rdf:Description>`,
			want: []rdfTriple{
				{rdfTestA, subClassOfIRI, rdfTestB},
			},
		},
		{
			name: "equivalentClass in both forms",
			body: `<owl:Class rdf:about="http://example.org/t#a">` +
				`<owl:equivalentClass><rdf:Description rdf:about="http://example.org/t#b"/></owl:equivalentClass>` +
				`</owl:Class>`,
			want: []rdfTriple{
				{rdfTestA, rdfTypeIRI, owlClassIRI},
				{rdfTestA, equivalentClassIRI, rdfTestB},
			},
		},
	})
}

func TestParseRDFXMLIgnoredConstructs(t *testing.T) {
	t.Parallel()

	runRDFFormCases(t, []rdfFormCase{
		{
			name: "anonymous restriction yields no edge",
			body: `<owl:Class rdf:about="http://example.org/t#a">` +
				`<rdfs:subClassOf><owl:Restriction>` +
				`<owl:onProperty rdf:resource="http://example.org/t#p"/>` +
				`</owl:Restriction></rdfs:subClassOf>` +
				`</owl:Class>`,
			want: []rdfTriple{{rdfTestA, rdfTypeIRI, owlClassIRI}},
		},
		{
			name: "rdf:nodeID subject yields no edge",
			body: `<owl:Class rdf:nodeID="n1">` +
				`<rdfs:subClassOf rdf:resource="http://example.org/t#b"/>` +
				`</owl:Class>`,
			want: nil,
		},
		{
			name: "parseType collection is skipped",
			body: `<owl:Class rdf:about="http://example.org/t#a">` +
				`<owl:intersectionOf rdf:parseType="Collection">` +
				`<owl:Class rdf:about="http://example.org/t#b"/>` +
				`</owl:intersectionOf>` +
				`</owl:Class>`,
			want: []rdfTriple{{rdfTestA, rdfTypeIRI, owlClassIRI}},
		},
		{
			name: "literal property values are ignored",
			body: `<owl:Class rdf:about="http://example.org/t#a">` +
				`<rdfs:label rdf:datatype="http://www.w3.org/2001/XMLSchema#string">Alpha</rdfs:label>` +
				`</owl:Class>`,
			want: []rdfTriple{{rdfTestA, rdfTypeIRI, owlClassIRI}},
		},
		{
			name: "comments and processing instructions are ignored",
			body: `<!-- a comment --><?pi data?>` +
				`<owl:Class rdf:about="http://example.org/t#a"/>`,
			want: []rdfTriple{{rdfTestA, rdfTypeIRI, owlClassIRI}},
		},
		{
			name: "empty document",
			body: "",
			want: nil,
		},
	})
}

func TestParseRDFXMLBaseResolution(t *testing.T) {
	t.Parallel()

	const (
		elsewhere   = "http://elsewhere.example/doc"
		relativeRef = `<owl:Class rdf:about="#a"><rdfs:subClassOf rdf:resource="#b"/></owl:Class>`
	)

	absolute := []rdfTriple{
		{rdfTestA, rdfTypeIRI, owlClassIRI},
		{rdfTestA, subClassOfIRI, rdfTestB},
	}

	runRDFFormCases(t, []rdfFormCase{
		{
			name: "without any base, references stay relative",
			body: relativeRef,
			want: []rdfTriple{
				{"#a", rdfTypeIRI, owlClassIRI},
				{"#a", subClassOfIRI, "#b"},
			},
		},
		{
			name: "external base resolves relative references",
			body: relativeRef,
			base: rdfTestBase,
			want: absolute,
		},
		{
			name:  "document xml:base overrides the external base",
			attrs: `xml:base="` + rdfTestBase + `"`,
			body:  relativeRef,
			base:  elsewhere,
			want:  absolute,
		},
		{
			name: "inner xml:base overrides the external base",
			body: `<owl:Class rdf:about="#a" xml:base="` + rdfTestBase + `">` +
				`<rdfs:subClassOf rdf:resource="#b"/>` +
				`</owl:Class>`,
			base: elsewhere,
			want: absolute,
		},
	})
}

func TestParseRDFXMLErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantMsg string
	}{
		{
			name:    "unterminated element",
			input:   rdfTestHeader + `><owl:Class rdf:about="http://example.org/t#a"></rdf:RDF>`,
			wantMsg: rdfErrPrefix,
		},
		{
			name:    "not xml at all",
			input:   "this is not xml",
			wantMsg: rdfErrPrefix + " document contains no elements",
		},
		{
			name:    "empty input",
			input:   "",
			wantMsg: rdfErrPrefix + " document contains no elements",
		},
		{
			name:    "wrong root element",
			input:   `<html><body/></html>`,
			wantMsg: "expected an <rdf:RDF> root element",
		},
		{
			name:    "syntax error before the root element",
			input:   `<?xml version="1.0"?><rdf:RDF `,
			wantMsg: rdfErrPrefix,
		},
		{
			name: "malformed subtree under a resource-valued property",
			input: rdfTestHeader + `><owl:Class rdf:about="http://example.org/t#a">` +
				`<rdfs:subClassOf rdf:resource="http://example.org/t#b"><oops></rdfs:subClassOf>` +
				`</owl:Class></rdf:RDF>`,
			wantMsg: rdfErrPrefix,
		},
		{
			name: "malformed subtree under a nested property",
			input: rdfTestHeader + `><owl:Class rdf:about="http://example.org/t#a">` +
				`<rdfs:subClassOf><owl:Class rdf:about="http://example.org/t#b"></rdfs:subClassOf>` +
				`</owl:Class></rdf:RDF>`,
			wantMsg: rdfErrPrefix,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseRDFXML([]byte(tt.input), "")
			if err == nil {
				t.Fatalf("parseRDFXML() = %v, want error", got)
			}

			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("parseRDFXML() error = %q, want it to contain %q", err, tt.wantMsg)
			}
		})
	}
}

func TestRDFResolveIRI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		base string
		ref  string
		want string
	}{
		{name: "no base keeps the reference", base: "", ref: "#a", want: "#a"},
		{name: "fragment against base", base: rdfTestBase, ref: "#a", want: rdfTestA},
		{name: "absolute reference wins", base: rdfTestBase, ref: rdfTestB, want: rdfTestB},
		{
			name: "relative path",
			base: "http://example.org/dir/doc",
			ref:  "other#a",
			want: "http://example.org/dir/other#a",
		},
		{
			name: "unparseable base keeps the reference",
			base: "://not a uri",
			ref:  rdfTestA,
			want: rdfTestA,
		},
		{
			name: "unparseable reference is kept verbatim",
			base: rdfTestBase,
			ref:  "%zz",
			want: "%zz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := rdfResolveIRI(tt.base, tt.ref); got != tt.want {
				t.Errorf("rdfResolveIRI(%q, %q) = %q, want %q", tt.base, tt.ref, got, tt.want)
			}
		})
	}
}
