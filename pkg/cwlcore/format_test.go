package cwlcore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// IRIs from testdata/format/simple.rdf and testdata/format/extension.rdf.
const (
	iriText    = "http://example.org/formats#text"
	iriTabular = "http://example.org/formats#tabular"
	iriCSV     = "http://example.org/formats#csv"
	iriTSV     = "http://example.org/formats#tsv"
	iriBinary  = "http://example.org/formats#binary"
	iriCSVGzip = "http://example.org/formats#csv-gzip"
	iriAny     = "http://example.org/formats#any"
	iriUnknown = "http://example.org/formats#unknown"
)

// IRIs from testdata/format/nested.rdf, equivalent.rdf and cyclic.rdf.
const (
	iriAlpha = "http://example.org/nested#alpha"
	iriGamma = "http://example.org/nested#gamma"
	iriDelta = "http://example.org/nested#delta"

	iriEqA            = "http://example.org/eq#a"
	iriEqB            = "http://example.org/eq#b"
	iriEqC            = "http://example.org/eq#c"
	iriEqD            = "http://example.org/eq#d"
	iriEqE            = "http://example.org/eq#e"
	iriIntersection   = "http://example.org/eq#intersection"
	iriCycX           = "http://example.org/cyc#x"
	iriCycY           = "http://example.org/cyc#y"
	iriCycRing1       = "http://example.org/cyc#ring1"
	iriCycRing2       = "http://example.org/cyc#ring2"
	iriCycRing3       = "http://example.org/cyc#ring3"
	iriCycSelf        = "http://example.org/cyc#self"
	iriCycP           = "http://example.org/cyc#p"
	iriCycQ           = "http://example.org/cyc#q"
	iriCycOutside     = "http://example.org/cyc#outside"
	edamFormat        = "http://edamontology.org/format_1915"
	edamTextual       = "http://edamontology.org/format_2330"
	edamFASTALike     = "http://edamontology.org/format_2200"
	edamFASTQLikeText = "http://edamontology.org/format_2182"
	edamFASTA         = "http://edamontology.org/format_1929"
	edamFASTQ         = "http://edamontology.org/format_1930"
	edamData2044      = "http://edamontology.org/data_2044"
)

// Locations identifying the File and Directory values the CheckFormat
// tests build.
const (
	testFileLocation = "in.txt"
	testDirLocation  = "out"
)

// Fixture file names.
const (
	fixtureSimple     = "simple.rdf"
	fixtureNested     = "nested.rdf"
	fixtureEquivalent = "equivalent.rdf"
	fixtureCyclic     = "cyclic.rdf"
	fixtureEDAM       = "edam-excerpt.rdf"
	fixtureExtension  = "extension.rdf"
	fixtureRelative   = "relative.rdf"
)

// testdata/format/relative.rdf declares no xml:base, so its identifiers are
// only absolute when it is loaded through LoadOntologyAt.
const (
	relativeBaseURI = "http://example.org/onto/formats.rdf"

	relChild  = "#child"
	relParent = "#parent"
	relRoot   = "shared.rdf#root"

	absChild  = relativeBaseURI + relChild
	absParent = relativeBaseURI + relParent
	absRoot   = "http://example.org/onto/" + relRoot
)

// readFormatFixture reads an RDF/XML fixture from testdata/format.
func readFormatFixture(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", "format", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}

	return data
}

// loadFormatFixture loads an RDF/XML fixture as a FormatOntology.
func loadFormatFixture(t *testing.T, name string) *FormatOntology {
	t.Helper()

	ontology, err := LoadOntology(readFormatFixture(t, name))
	if err != nil {
		t.Fatalf("LoadOntology(%s): %v", name, err)
	}

	return ontology
}

// loadFormatFixtureAt loads an RDF/XML fixture against an external base URI.
func loadFormatFixtureAt(t *testing.T, name, baseURI string) *FormatOntology {
	t.Helper()

	ontology, err := LoadOntologyAt(readFormatFixture(t, name), baseURI)
	if err != nil {
		t.Fatalf("LoadOntologyAt(%s, %s): %v", name, baseURI, err)
	}

	return ontology
}

func TestFormatOntologyCompatible(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		fixture    string
		fileFormat string
		required   string
		want       bool
	}{
		// Exact match, the only rule that holds without an ontology.
		{"exact match", fixtureSimple, iriCSV, iriCSV, true},
		{"exact match of an IRI absent from the ontology", fixtureSimple, iriUnknown, iriUnknown, true},
		{"unknown file format", fixtureSimple, iriUnknown, iriText, false},

		// rdfs:subClassOf, in the spec's direction: a more specific input
		// format satisfies a more general requirement.
		{"direct subclass", fixtureSimple, iriCSV, iriTabular, true},
		{"two-hop subclass", fixtureSimple, iriCSV, iriText, true},
		{"superclass does not satisfy a specific requirement", fixtureSimple, iriTabular, iriCSV, false},
		{"root does not satisfy a leaf requirement", fixtureSimple, iriText, iriCSV, false},
		{"siblings are incompatible", fixtureSimple, iriCSV, iriTSV, false},
		{"unrelated branches are incompatible", fixtureSimple, iriCSV, iriBinary, false},

		// Nested node elements, rdf:ID and xml:base resolution.
		{"nested form two-hop", fixtureNested, iriAlpha, iriGamma, true},
		{"nested form three-hop", fixtureNested, iriAlpha, iriDelta, true},
		{"nested form wrong direction", fixtureNested, iriDelta, iriAlpha, false},

		// owl:equivalentClass, which is symmetric...
		{"equivalent class", fixtureEquivalent, iriEqB, iriEqC, true},
		{"equivalent class is symmetric", fixtureEquivalent, iriEqC, iriEqB, true},
		{"equivalent class in nested form", fixtureEquivalent, iriEqD, iriEqE, true},
		{"equivalent class in nested form, reversed", fixtureEquivalent, iriEqE, iriEqD, true},

		// ...and transitive with rdfs:subClassOf. The spec's worked example:
		// <B> owl:equivalentClass <C> and <B> owl:subclassOf <A> infers
		// <C> owl:subclassOf <A>.
		{"declared subclass", fixtureEquivalent, iriEqB, iriEqA, true},
		{"equivalentClass transitive with subClassOf", fixtureEquivalent, iriEqC, iriEqA, true},
		{"transitivity does not run backwards", fixtureEquivalent, iriEqA, iriEqC, false},
		{"anonymous class expression yields no edge", fixtureEquivalent, iriIntersection, iriEqA, false},

		// Cycles must terminate, both when the answer is yes and when it is no.
		{"mutual subclass pair", fixtureCyclic, iriCycX, iriCycY, true},
		{"mutual subclass pair, reversed", fixtureCyclic, iriCycY, iriCycX, true},
		{"subclass ring forwards", fixtureCyclic, iriCycRing1, iriCycRing3, true},
		{"subclass ring wrapping", fixtureCyclic, iriCycRing3, iriCycRing2, true},
		{"subclass ring cannot reach outside", fixtureCyclic, iriCycRing1, iriCycOutside, false},
		{"mutual pair cannot reach outside", fixtureCyclic, iriCycX, iriCycOutside, false},
		{"self loop", fixtureCyclic, iriCycSelf, iriCycSelf, true},
		{"self loop cannot reach outside", fixtureCyclic, iriCycSelf, iriCycOutside, false},
		{"equivalence ring", fixtureCyclic, iriCycP, iriCycQ, true},
		{"equivalence ring cannot reach outside", fixtureCyclic, iriCycQ, iriCycOutside, false},

		// A real-shaped ontology.
		{"EDAM one hop", fixtureEDAM, edamFASTA, edamFASTALike, true},
		{"EDAM two hops", fixtureEDAM, edamFASTA, edamTextual, true},
		{"EDAM three hops", fixtureEDAM, edamFASTA, edamFormat, true},
		{"EDAM FASTQ up its own branch", fixtureEDAM, edamFASTQ, edamFASTQLikeText, true},
		{"EDAM FASTQ reaches the shared root", fixtureEDAM, edamFASTQ, edamFormat, true},
		{"EDAM cousins are incompatible", fixtureEDAM, edamFASTA, edamFASTQ, false},
		{"EDAM parent does not satisfy child", fixtureEDAM, edamTextual, edamFASTA, false},
		{"EDAM restriction target is not a superclass", fixtureEDAM, edamFASTA, edamData2044, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ontology := loadFormatFixture(t, tt.fixture)
			if got := ontology.Compatible(tt.fileFormat, tt.required); got != tt.want {
				t.Errorf("Compatible(%q, %q) = %v, want %v", tt.fileFormat, tt.required, got, tt.want)
			}
		})
	}
}

func TestFormatOntologyCompatibleWithoutOntology(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ontology *FormatOntology
		name     string
	}{
		{name: "nil ontology", ontology: nil},
		{name: "zero value ontology", ontology: &FormatOntology{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if !tt.ontology.Compatible(iriCSV, iriCSV) {
				t.Error("Compatible() = false for identical IRIs, want true")
			}

			if tt.ontology.Compatible(iriCSV, iriText) {
				t.Error("Compatible() = true without an ontology, want exact match only")
			}
		})
	}
}

func TestFormatOntologyMergeSpansDocuments(t *testing.T) {
	t.Parallel()

	base := loadFormatFixture(t, fixtureSimple)
	if base.Compatible(iriCSVGzip, iriAny) {
		t.Fatal("Compatible() = true before Merge, want false")
	}

	base.Merge(loadFormatFixture(t, fixtureExtension))

	for _, required := range []string{iriCSV, iriTabular, iriText, iriAny} {
		if !base.Compatible(iriCSVGzip, required) {
			t.Errorf("after Merge, Compatible(%q, %q) = false, want true", iriCSVGzip, required)
		}
	}

	if base.Compatible(iriAny, iriCSVGzip) {
		t.Error("after Merge, Compatible() = true in the superclass direction, want false")
	}

	if base.Compatible(iriBinary, iriAny) {
		t.Error("after Merge, Compatible() invented an edge for an unrelated class")
	}
}

func TestFormatOntologyMergeIsIdempotent(t *testing.T) {
	t.Parallel()

	merged := loadFormatFixture(t, fixtureSimple)
	other := loadFormatFixture(t, fixtureExtension)
	merged.Merge(other)
	merged.Merge(other)

	if got := len(merged.superClasses[iriText]); got != 1 {
		t.Errorf("len(superClasses[text]) = %d after a repeated Merge, want 1", got)
	}
}

func TestFormatOntologyMergeIntoZeroValue(t *testing.T) {
	t.Parallel()

	into := &FormatOntology{}
	into.Merge(loadFormatFixture(t, fixtureEquivalent))

	if !into.Compatible(iriEqC, iriEqA) {
		t.Error("Compatible() = false after merging into a zero value ontology, want true")
	}
}

func TestFormatOntologyMergeNilOperands(t *testing.T) {
	t.Parallel()

	ontology := loadFormatFixture(t, fixtureSimple)
	ontology.Merge(nil)

	var nilOntology *FormatOntology
	nilOntology.Merge(ontology)

	if !ontology.Compatible(iriCSV, iriText) {
		t.Error("Compatible() = false after Merge(nil), want true")
	}

	if nilOntology.Compatible(iriCSV, iriText) {
		t.Error("a nil ontology gained edges from Merge, want exact match only")
	}
}

func TestLoadOntologyErrors(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"malformed.rdf", "not-rdf.xml"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := LoadOntology(readFormatFixture(t, name))
			if err == nil {
				t.Fatalf("LoadOntology(%s) = %v, want error", name, got)
			}

			if got != nil {
				t.Errorf("LoadOntology(%s) = %v on error, want nil", name, got)
			}
		})
	}
}

func TestLoadOntologyKeepsRelativeIRIsWithoutBase(t *testing.T) {
	t.Parallel()

	ontology := loadFormatFixture(t, fixtureRelative)

	if !ontology.Compatible(relChild, relParent) {
		t.Error("Compatible(#child, #parent) = false, want true: relative IRIs must still match each other")
	}

	if !ontology.Compatible(relChild, relRoot) {
		t.Error("Compatible(#child, shared.rdf#root) = false, want true")
	}

	if ontology.Compatible(absChild, absParent) {
		t.Error("Compatible() resolved relative IRIs without a base URI, want them kept verbatim")
	}
}

func TestLoadOntologyAtResolvesRelativeIRIs(t *testing.T) {
	t.Parallel()

	ontology := loadFormatFixtureAt(t, fixtureRelative, relativeBaseURI)

	if !ontology.Compatible(absChild, absParent) {
		t.Errorf("Compatible(%q, %q) = false, want true", absChild, absParent)
	}

	if !ontology.Compatible(absChild, absRoot) {
		t.Errorf("Compatible(%q, %q) = false, want true: a relative path resolves against the base", absChild, absRoot)
	}

	if ontology.Compatible(absParent, absChild) {
		t.Error("Compatible() = true in the superclass direction after base resolution, want false")
	}

	if ontology.Compatible(relChild, relParent) {
		t.Error("Compatible() still matched the unresolved relative IRIs, want them absolute")
	}
}

func TestLoadOntologyAtDocumentBaseWins(t *testing.T) {
	t.Parallel()

	const conflicting = "http://elsewhere.example/other.rdf"

	// nested.rdf declares xml:base="http://example.org/nested"; the base passed
	// in is only a fallback, so the document's own must win.
	ontology := loadFormatFixtureAt(t, fixtureNested, conflicting)

	if !ontology.Compatible(iriAlpha, iriDelta) {
		t.Error("Compatible() = false, want true: the document's own xml:base must win over the passed base URI")
	}

	for sub, supers := range ontology.superClasses {
		if strings.HasPrefix(sub, conflicting) {
			t.Errorf("subject %q was resolved against the passed base URI, want the document's xml:base", sub)
		}

		for _, super := range supers {
			if strings.HasPrefix(super, conflicting) {
				t.Errorf("object %q was resolved against the passed base URI, want the document's xml:base", super)
			}
		}
	}
}

// checkFormatCase is one CheckFormat invocation and the error it must produce.
type checkFormatCase struct {
	file         any
	wantErr      error
	name         string
	allowed      []string
	withOntology bool
}

// runCheckFormatCases runs each case, optionally against testdata/simple.rdf.
func runCheckFormatCases(t *testing.T, tests []checkFormatCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var ontology *FormatOntology
			if tt.withOntology {
				ontology = loadFormatFixture(t, fixtureSimple)
			}

			err := CheckFormat(tt.file, tt.allowed, ontology)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CheckFormat() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// mapFile builds a File in the generic map representation.
func mapFile(format any) map[string]any {
	return map[string]any{
		fileKeyClass:    ClassFile,
		fileKeyLocation: testFileLocation,
		fileKeyFormat:   format,
	}
}

// typedFile builds a File in the typed representation.
func typedFile(format string) *File {
	return &File{Location: testFileLocation, Format: format}
}

func TestCheckFormat(t *testing.T) {
	t.Parallel()

	runCheckFormatCases(t, []checkFormatCase{
		{name: "exact match without an ontology", file: mapFile(iriCSV), allowed: []string{iriCSV}},
		{
			name:    "no ontology means no subclass reasoning",
			file:    mapFile(iriCSV),
			allowed: []string{iriText},
			wantErr: ErrFormatIncompatible,
		},
		{name: "subclass of an allowed format", file: mapFile(iriCSV), allowed: []string{iriText}, withOntology: true},
		{
			name:         "superclass of an allowed format is rejected",
			file:         mapFile(iriText),
			allowed:      []string{iriCSV},
			withOntology: true,
			wantErr:      ErrFormatIncompatible,
		},
		{
			name:         "any one of several allowed formats suffices",
			file:         mapFile(iriTSV),
			allowed:      []string{iriBinary, iriTabular},
			withOntology: true,
		},
		{
			name:         "none of the allowed formats matches",
			file:         mapFile(iriBinary),
			allowed:      []string{iriCSV, iriTSV},
			withOntology: true,
			wantErr:      ErrFormatIncompatible,
		},
		{
			name:    "file without a format fails a parameter that requires one",
			file:    map[string]any{fileKeyClass: ClassFile, fileKeyLocation: testFileLocation},
			allowed: []string{iriCSV},
			wantErr: ErrFormatMissing,
		},
		{name: "non-string format", file: mapFile(42), allowed: []string{iriCSV}, wantErr: ErrFormatMissing},
		{name: "empty format", file: mapFile(""), allowed: []string{iriCSV}, wantErr: ErrFormatMissing},
		{
			name:    "parameter without a format accepts a file without one",
			file:    map[string]any{fileKeyClass: ClassFile, fileKeyLocation: testFileLocation},
			allowed: nil,
		},
		{name: "parameter without a format accepts any format", file: mapFile(iriBinary), allowed: nil},
		{name: "nil file", file: nil, allowed: []string{iriCSV}},
		{
			name:         "array of files, all compatible",
			file:         []any{mapFile(iriCSV), mapFile(iriTSV), nil},
			allowed:      []string{iriText},
			withOntology: true,
		},
		{
			name:         "array of files, one incompatible",
			file:         []any{mapFile(iriCSV), mapFile(iriBinary)},
			allowed:      []string{iriText},
			withOntology: true,
			wantErr:      ErrFormatIncompatible,
		},
		{
			name:    "array of files, one missing its format",
			file:    []any{mapFile(iriCSV), map[string]any{fileKeyClass: ClassFile}},
			allowed: []string{iriCSV},
			wantErr: ErrFormatMissing,
		},
		{
			name:    "value that is not a File object",
			file:    testFileLocation,
			allowed: []string{iriCSV},
			wantErr: errNotAFileObject,
		},
		{
			name:    "a Directory map carries no format and is skipped",
			file:    map[string]any{fileKeyClass: ClassDirectory, fileKeyLocation: testDirLocation},
			allowed: []string{iriCSV},
		},
	})
}

func TestCheckFormatTypedValues(t *testing.T) {
	t.Parallel()

	runCheckFormatCases(t, []checkFormatCase{
		{name: "exact match without an ontology", file: typedFile(iriCSV), allowed: []string{iriCSV}},
		{
			name:    "no ontology means no subclass reasoning",
			file:    typedFile(iriCSV),
			allowed: []string{iriText},
			wantErr: ErrFormatIncompatible,
		},
		{
			name:         "subclass of an allowed format",
			file:         typedFile(iriCSV),
			allowed:      []string{iriText},
			withOntology: true,
		},
		{
			name:         "superclass of an allowed format is rejected",
			file:         typedFile(iriText),
			allowed:      []string{iriCSV},
			withOntology: true,
			wantErr:      ErrFormatIncompatible,
		},
		{
			name:         "any one of several allowed formats suffices",
			file:         typedFile(iriTSV),
			allowed:      []string{iriBinary, iriTabular},
			withOntology: true,
		},
		{
			name:         "none of the allowed formats matches",
			file:         typedFile(iriBinary),
			allowed:      []string{iriCSV, iriTSV},
			withOntology: true,
			wantErr:      ErrFormatIncompatible,
		},
		{
			name:    "file without a format fails a parameter that requires one",
			file:    &File{Location: testFileLocation},
			allowed: []string{iriCSV},
			wantErr: ErrFormatMissing,
		},
		{
			name:    "parameter without a format accepts a file without one",
			file:    &File{Location: testFileLocation},
			allowed: nil,
		},
		{name: "File passed by value", file: *typedFile(iriCSV), allowed: []string{iriCSV}},
		{
			name:         "File passed by value, rejected",
			file:         *typedFile(iriBinary),
			allowed:      []string{iriText},
			withOntology: true,
			wantErr:      ErrFormatIncompatible,
		},
		{name: "typed nil File", file: (*File)(nil), allowed: []string{iriCSV}},
		{
			name:    "a Directory carries no format and is skipped",
			file:    &Directory{Location: testDirLocation},
			allowed: []string{iriCSV},
		},
		{
			name:    "a Directory passed by value is skipped",
			file:    Directory{Location: testDirLocation},
			allowed: []string{iriCSV},
		},
	})
}

func TestCheckFormatTypedSlices(t *testing.T) {
	t.Parallel()

	runCheckFormatCases(t, []checkFormatCase{
		{
			name: "FileOrDirectory slice, all compatible",
			file: []FileOrDirectory{
				typedFile(iriCSV),
				&Directory{Location: testDirLocation},
				typedFile(iriTSV),
			},
			allowed:      []string{iriText},
			withOntology: true,
		},
		{
			name:         "FileOrDirectory slice, one incompatible",
			file:         []FileOrDirectory{typedFile(iriCSV), typedFile(iriBinary)},
			allowed:      []string{iriText},
			withOntology: true,
			wantErr:      ErrFormatIncompatible,
		},
		{
			name:         "pointer slice",
			file:         []*File{typedFile(iriCSV), typedFile(iriTSV)},
			allowed:      []string{iriText},
			withOntology: true,
		},
		{
			name:    "pointer slice, one missing its format",
			file:    []*File{typedFile(iriCSV), {Location: "other.txt"}},
			allowed: []string{iriCSV},
			wantErr: ErrFormatMissing,
		},
		{
			name:         "value slice",
			file:         []File{*typedFile(iriCSV), *typedFile(iriTSV)},
			allowed:      []string{iriTabular},
			withOntology: true,
		},
		{
			name:         "map slice",
			file:         []map[string]any{mapFile(iriCSV), mapFile(iriTSV)},
			allowed:      []string{iriText},
			withOntology: true,
		},
		{
			name:    "map slice, one incompatible",
			file:    []map[string]any{mapFile(iriCSV), mapFile(iriBinary)},
			allowed: []string{iriCSV},
			wantErr: ErrFormatIncompatible,
		},
		{
			name:         "mixed typed and raw values",
			file:         []any{typedFile(iriCSV), mapFile(iriTSV), &Directory{Location: testDirLocation}, nil},
			allowed:      []string{iriText},
			withOntology: true,
		},
	})
}

// Secondary files are companions of the primary file with formats of their
// own, so they are not checked against the parameter's format.
func TestCheckFormatIgnoresSecondaryFiles(t *testing.T) {
	t.Parallel()

	ontology := loadFormatFixture(t, fixtureSimple)
	nested := typedFile(iriBinary)
	nested.SecondaryFiles = []FileOrDirectory{typedFile(iriBinary), &Directory{Location: "nested-dir"}}

	primary := typedFile(iriCSV)
	primary.SecondaryFiles = []FileOrDirectory{
		nested,
		&File{Location: "no-format.idx"},
		&Directory{Location: "aux"},
	}

	err := CheckFormat(primary, []string{iriText}, ontology)
	if err != nil {
		t.Errorf("CheckFormat() error = %v, want nil: only the bound value's own format is checked", err)
	}

	// The secondary file really would fail on its own, so the pass above is a
	// deliberate scope decision rather than an accident of the fixture.
	err = CheckFormat(nested, []string{iriText}, ontology)
	if !errors.Is(err, ErrFormatIncompatible) {
		t.Errorf("CheckFormat() error = %v, want ErrFormatIncompatible when checked directly", err)
	}
}

func TestCheckFormatWithEDAM(t *testing.T) {
	t.Parallel()

	ontology := loadFormatFixture(t, fixtureEDAM)
	fastaFile := map[string]any{
		fileKeyClass:    ClassFile,
		fileKeyLocation: "reads.fasta",
		fileKeyFormat:   edamFASTA,
	}

	err := CheckFormat(fastaFile, []string{edamTextual}, ontology)
	if err != nil {
		t.Errorf("CheckFormat() error = %v, want nil for a FASTA file where textual format is required", err)
	}

	err = CheckFormat(fastaFile, []string{edamFASTQ}, ontology)
	if !errors.Is(err, ErrFormatIncompatible) {
		t.Errorf("CheckFormat() error = %v, want ErrFormatIncompatible", err)
	}
}
