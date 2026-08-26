package cwlexec

import (
	"errors"
	"slices"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// cltToolID is the resolved document identifier the fixture parameters hang off, so that the tests
// exercise ShortName rather than pretending identifiers arrive already short.
const cltToolID = "file:///tool.cwl#tool/"

// Fixture spellings repeated across the command-line tables, named so that the tables stay readable
// and so that no literal is written out three times over.
const (
	cltRec    = "rec"
	cltAlpha  = "alpha"
	cltBeta   = "beta"
	cltEither = "either"
	cltRaw    = "raw"
	cltSolo   = "solo"
	cltFast   = "fast"
	cltSym    = "sym"
	cltOpt    = "--opt"
	cltHello  = "hello world"
	cltToggle = "toggle"
	cltEcho   = "echo"
	cltText   = "text"
	cltLabel  = "label"
)

// The CWLType symbols the fixtures use, named once so a table row reads as its command line rather
// than as type construction.
var (
	cltString      = cwlcore.NewPrimitiveType(cwlcore.PrimitiveString)
	cltInt         = cwlcore.NewPrimitiveType(cwlcore.PrimitiveInt)
	cltBool        = cwlcore.NewPrimitiveType(cwlcore.PrimitiveBoolean)
	cltFile        = cwlcore.NewPrimitiveType(cwlcore.PrimitiveFile)
	cltDir         = cwlcore.NewPrimitiveType(cwlcore.PrimitiveDirectory)
	cltNull        = cwlcore.NewPrimitiveType(cwlcore.PrimitiveNull)
	cltAny         = cwlcore.NewPrimitiveType(cwlcore.PrimitiveAny)
	cltStringArray = cwlcore.NewArrayType(&cwlcore.ArraySchema{Items: cltString})
)

// cltInput builds one CommandInputParameter with a resolved identifier.
func cltInput(name string, typ cwlcore.TypeRef, binding *cwlcore.CommandLineBinding) cwlcore.CommandInputParameter {
	return cwlcore.CommandInputParameter{
		ParameterBase: cwlcore.ParameterBase{IDField: cltToolID + name, Type: typ},
		InputBinding:  binding,
	}
}

// cltAt returns a binding at position n, the shape most ordering rows need.
func cltAt(n int64) *cwlcore.CommandLineBinding {
	return &cwlcore.CommandLineBinding{Position: cwlcore.NewExprLong(n)}
}

// cltPrefixed returns a binding at position n emitting prefix before its value.
func cltPrefixed(n int64, prefix string) *cwlcore.CommandLineBinding {
	binding := cltAt(n)
	binding.Prefix = prefix

	return binding
}

// cltItemBound returns an array schema of strings whose *items* carry binding, which is the nested
// binding the specification distinguishes from a binding on the array itself.
func cltItemBound(binding *cwlcore.CommandLineBinding) cwlcore.TypeRef {
	return cwlcore.NewArrayType(&cwlcore.ArraySchema{Items: cltString, InputBinding: binding})
}

// cltEval is the evaluator the tables run under: an InlineJavascriptRequirement is in scope, so
// both parameter references and full expressions are legal.
func cltEval() *cwlcore.Evaluator {
	return cwlcore.NewEvaluator(cwlcore.WithJS(nil))
}

// cltSchemaDefScope builds a requirement scope whose SchemaDefRequirement declares the types in
// src, a YAML sequence of type declarations written exactly as a document writes them.
//
// The declarations are carried as salad nodes because that is what SchemaDefRequirement holds: one
// declaration may name another, and only the whole scope can follow that edge.
func cltSchemaDefScope(t *testing.T, src string) *cwlcore.RequirementScope {
	t.Helper()

	node, err := salad.Parse("schemadef.yml", []byte(src))
	if err != nil {
		t.Fatalf("salad.Parse: %v", err)
	}

	declarations, ok := salad.AsSeq(node)
	if !ok {
		t.Fatalf("schemadef fixture parsed as %s, want a sequence", salad.NodeKind(node))
	}

	return cwlcore.NewScope(&cwlcore.CommandLineTool{
		ProcessBase: cwlcore.ProcessBase{
			Requirements: []cwlcore.ProcessRequirement{
				&cwlcore.SchemaDefRequirement{Types: declarations.Items()},
			},
		},
	})
}

// cltCase is one row of the command-line tables: a tool, the input object it runs with, and the
// argv it must produce, written out literally.
type cltCase struct {
	tool   *cwlcore.CommandLineTool
	inputs map[string]any

	// scope supplies the requirement scope the row runs under, for the rows that need one. It
	// is a function rather than a scope because building one takes a *testing.T, and the tables
	// themselves are package-level values. A nil scope is the common case and is valid.
	scope func(t *testing.T) *cwlcore.RequirementScope

	name string
	want []string
}

// run builds the row's command line and compares it against the expected argv.
func (c cltCase) run(t *testing.T) {
	t.Helper()

	var scope *cwlcore.RequirementScope
	if c.scope != nil {
		scope = c.scope(t)
	}

	line, err := BuildCommandLine(c.tool, c.inputs, cltEval(), scope, cwlcore.RuntimeContext{})
	if err != nil {
		t.Fatalf("BuildCommandLine: unexpected error: %v", err)
	}

	if got := line.Argv(); !slices.Equal(got, c.want) {
		t.Errorf("argv = %q, want %q", got, c.want)
	}

	if line.Shell {
		t.Error("Shell = true, want false with no ShellCommandRequirement in scope")
	}
}

// runCltCases runs one whole table as parallel subtests.
func runCltCases(t *testing.T, cases []cltCase) {
	t.Helper()

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			testCase.run(t)
		})
	}
}

// cltErrCase is one row of the failure tables: a tool and inputs that must not build, and the
// sentinel the failure has to wrap.
type cltErrCase struct {
	tool    *cwlcore.CommandLineTool
	inputs  map[string]any
	wantErr error
	name    string
}

// run builds the row's command line and asserts that it failed with the expected sentinel.
func (c cltErrCase) run(t *testing.T) {
	t.Helper()

	line, err := BuildCommandLine(c.tool, c.inputs, cltEval(), nil, cwlcore.RuntimeContext{})
	if err == nil {
		t.Fatalf("BuildCommandLine: want an error wrapping %v, got argv %q", c.wantErr, line.Argv())
	}

	if !errors.Is(err, c.wantErr) {
		t.Errorf("BuildCommandLine: error %v does not wrap %v", err, c.wantErr)
	}
}

// runCltErrCases runs one whole failure table as parallel subtests.
func runCltErrCases(t *testing.T, cases []cltErrCase) {
	t.Helper()

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			testCase.run(t)
		})
	}
}

// cltRecordField builds one record field with a resolved identifier, the shape the union-member
// discrimination rows are written from.
func cltRecordField(name string, typ cwlcore.TypeRef, binding *cwlcore.CommandLineBinding) cwlcore.RecordField {
	return cwlcore.RecordField{Name: cltToolID + cltEither + "/" + name, Type: typ, InputBinding: binding}
}

// cltUnionRecord builds a one-field record type, so that a union of two of them can only be told
// apart by which field name the value carries.
func cltUnionRecord(field, prefix string) cwlcore.TypeRef {
	return cwlcore.NewRecordType(&cwlcore.RecordSchema{
		Fields: []cwlcore.RecordField{cltRecordField(field, cltString, cltPrefixed(0, prefix))},
	})
}

// cltTaggedRecord builds a record whose single field is an enum admitting exactly one symbol, so
// that a union of two of them can only be told apart by the symbol the value holds.
func cltTaggedRecord(symbol, prefix string) cwlcore.TypeRef {
	enum := cwlcore.NewEnumType(&cwlcore.EnumSchema{Symbols: []string{symbol}})

	return cwlcore.NewRecordType(&cwlcore.RecordSchema{
		Fields: []cwlcore.RecordField{cltRecordField(cltSym, enum, cltPrefixed(0, prefix))},
	})
}

// cltOptionalTaggedRecord is cltTaggedRecord with the enum field declared optional, so that the
// discriminating type is a union rather than the enum itself.
func cltOptionalTaggedRecord(symbol, prefix string) cwlcore.TypeRef {
	enum := cwlcore.NewEnumType(&cwlcore.EnumSchema{Symbols: []string{symbol}})
	optional := cwlcore.NewUnionType([]cwlcore.TypeRef{cltNull, enum})

	return cwlcore.NewRecordType(&cwlcore.RecordSchema{
		Fields: []cwlcore.RecordField{cltRecordField(cltSym, optional, cltPrefixed(0, prefix))},
	})
}
