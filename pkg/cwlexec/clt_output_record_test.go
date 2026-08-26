package cwlexec

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// The names the record fixtures repeat, named once so that a reader can tell at a glance which
// occurrences are meant to be the same string.
const (
	// outRecPort is the output port every record fixture declares.
	outRecPort = "rec"

	// outFieldLeft, outFieldRight, outFieldInner, outFieldText and outFieldNext are the record
	// fields those fixtures collect into.
	outFieldLeft  = "left"
	outFieldRight = "right"
	outFieldInner = "inner"
	outFieldText  = "text"
	outFieldNext  = "next"

	// outMissingName is a file the fixtures deliberately never write.
	outMissingName = "missing.txt"

	// outRecFormat is the format IRI a per-field format declaration produces.
	outRecFormat = "urn:example:record-field"
)

// outField builds one output record field with a resolved identifier, the way decoding leaves one.
func outField(
	name string, declared cwlcore.TypeRef, binding *cwlcore.CommandOutputBinding,
) cwlcore.RecordField {
	return cwlcore.RecordField{
		Name:          outToolID + "/" + outRecPort + "/" + name,
		Type:          declared,
		OutputBinding: binding,
	}
}

// outRecord builds an inline record type over the given fields.
func outRecord(fields ...cwlcore.RecordField) cwlcore.TypeRef {
	return cwlcore.NewRecordType(&cwlcore.RecordSchema{Fields: fields})
}

// outRecordTool builds a tool whose single output is the port outRecPort, typed as declared.
func outRecordTool(declared cwlcore.TypeRef) *cwlcore.CommandLineTool {
	return outTestTool(outTestParam(outToolID+"/"+outRecPort, declared, nil))
}

// outRecordToolBound builds a tool whose single output is the port outRecPort, typed as declared and
// carrying an output binding of its own.
func outRecordToolBound(
	declared cwlcore.TypeRef, binding *cwlcore.CommandOutputBinding,
) *cwlcore.CommandLineTool {
	return outTestTool(outTestParam(outToolID+"/"+outRecPort, declared, binding))
}

// outCollectWith runs CollectOutputs over a tool with an input object and the given evaluator.
func outCollectWith(t *testing.T, tool *cwlcore.CommandLineTool, dir string,
	inputs map[string]any, eval *cwlcore.Evaluator,
) map[string]any {
	t.Helper()

	outputs, err := CollectOutputs(tool, dir, 0, inputs, eval, cwlcore.RuntimeContext{Outdir: dir})
	if err != nil {
		t.Fatalf("CollectOutputs: %v", err)
	}

	return outputs
}

// outSchemaDefTool builds a tool that declares src as its SchemaDefRequirement types and types its
// single output as a bare reference to the declaration called outRecPort.
func outSchemaDefTool(t *testing.T, src string) *cwlcore.CommandLineTool {
	t.Helper()

	node, err := salad.Parse("schemadef.yml", []byte(src))
	if err != nil {
		t.Fatalf("salad.Parse: %v", err)
	}

	declarations, ok := salad.AsSeq(node)
	if !ok {
		t.Fatalf("the schemadef fixture parsed as %s, want a sequence", salad.NodeKind(node))
	}

	tool := outRecordTool(cwlcore.NewNamedType(outRecPort))
	tool.Requirements = []cwlcore.ProcessRequirement{
		&cwlcore.SchemaDefRequirement{Types: declarations.Items()},
	}

	return tool
}

// outWantRecord requires that the collected record port holds a record object.
func outWantRecord(t *testing.T, outputs map[string]any) map[string]any {
	t.Helper()

	object, ok := outputs[outRecPort].(map[string]any)
	if !ok {
		t.Fatalf("output %q = %#v, want a record object", outRecPort, outputs[outRecPort])
	}

	return object
}

// outWantRecordFile requires that one field of a record object holds a File with the given basename.
func outWantRecordFile(t *testing.T, object map[string]any, field, basename string) *cwlcore.File {
	t.Helper()

	file, ok := object[field].(*cwlcore.File)
	if !ok {
		t.Fatalf("field %q = %#v, want a *cwlcore.File", field, object[field])
	}

	if file.Basename != basename {
		t.Errorf("field %q basename = %q, want %q", field, file.Basename, basename)
	}

	return file
}

// outWantNestedRecord requires that one field of a record object holds a record object of its own.
func outWantNestedRecord(t *testing.T, object map[string]any, field string) map[string]any {
	t.Helper()

	nested, ok := object[field].(map[string]any)
	if !ok {
		t.Fatalf("field %q = %#v, want a nested record object", field, object[field])
	}

	return nested
}

func TestCollectOutputsFlatRecord(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outNameA, cltAlpha)
	outWriteFile(t, dir, outNameB, cltBeta)

	// Each field is globbed independently, from the binding the field itself carries. The record
	// parameter has no binding at all, and needs none.
	tool := outRecordTool(outRecord(
		outField(outFieldLeft, outTypeFile, outGlobBinding(outNameA)),
		outField(outFieldRight, outTypeFile, outGlobBinding(outNameB)),
	))

	object := outWantRecord(t, outCollect(t, tool, dir, 0))
	assertInt(t, "len(rec)", len(object), 2)

	assertDeepEqual(t, "left checksum", outWantRecordFile(t, object, outFieldLeft, outNameA).Checksum, outSumAlpha)
	assertDeepEqual(t, "right checksum", outWantRecordFile(t, object, outFieldRight, outNameB).Checksum, outSumBeta)
}

func TestCollectOutputsRecordMixesAFileAndAnEvaluatedString(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outNameA, cltAlpha)

	evaluated := outGlobBinding(outNameA)
	evaluated.OutputEval = "$(self[0].basename)"

	tool := outRecordTool(outRecord(
		outField(outFieldLeft, outTypeFile, outGlobBinding(outNameA)),
		outField(outFieldText, outTypeString, evaluated),
	))

	object := outWantRecord(t, outCollect(t, tool, dir, 0))

	outWantRecordFile(t, object, outFieldLeft, outNameA)
	assertDeepEqual(t, "text", object[outFieldText], outNameA)
}

func TestCollectOutputsNestedRecord(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outNameA, cltAlpha)
	outWriteFile(t, dir, outNameB, cltBeta)

	inner := outRecord(outField(outFieldRight, outTypeFile, outGlobBinding(outNameB)))

	// The nested field carries no binding: the bindings that fill it are the ones on its own
	// fields, one level down.
	tool := outRecordTool(outRecord(
		outField(outFieldLeft, outTypeFile, outGlobBinding(outNameA)),
		outField(outFieldInner, inner, nil),
	))

	object := outWantRecord(t, outCollect(t, tool, dir, 0))

	outWantRecordFile(t, object, outFieldLeft, outNameA)
	outWantRecordFile(t, outWantNestedRecord(t, object, outFieldInner), outFieldRight, outNameB)
}

func TestCollectOutputsOptionalRecordFieldGlobMatchesNothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tool := outRecordTool(outRecord(
		outField(outFieldLeft, outTypeOptionalFile, outGlobBinding(outMissingName)),
	))

	object := outWantRecord(t, outCollect(t, tool, dir, 0))

	if _, present := object[outFieldLeft]; !present {
		t.Fatal("a field that collected nothing must still appear in the record")
	}

	if object[outFieldLeft] != nil {
		t.Errorf("left = %#v, want nil", object[outFieldLeft])
	}
}

func TestCollectOutputsRequiredRecordFieldGlobMatchesNothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tool := outRecordTool(outRecord(
		outField(outFieldLeft, outTypeFile, outGlobBinding(outMissingName)),
	))

	err := outCollectErr(t, tool, dir)

	assertErrorIs(t, "required record field with no match", err, ErrOutputMissing)
	assertNames(t, err, outRecPort, outFieldLeft, outMissingName)
}

func TestCollectOutputsRecordThroughAnArray(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outNameA, cltAlpha)

	items := outRecord(outField(outFieldLeft, outTypeFile, outGlobBinding(outNameA)))
	tool := outRecordTool(cwlcore.NewArrayType(&cwlcore.ArraySchema{Items: items}))

	outputs := outCollect(t, tool, dir, 0)

	// One set of field bindings describes one record's worth of collection, so an array-typed
	// record collects that record as the array's single element rather than as the value itself.
	list, ok := outputs[outRecPort].([]any)
	if !ok {
		t.Fatalf("rec = %#v, want a list", outputs[outRecPort])
	}

	assertInt(t, "len(rec)", len(list), 1)

	object, ok := list[0].(map[string]any)
	if !ok {
		t.Fatalf("rec[0] = %#v, want a record object", list[0])
	}

	outWantRecordFile(t, object, outFieldLeft, outNameA)
}

func TestCollectOutputsRecordBehindANullUnion(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outNameA, cltAlpha)

	record := outRecord(outField(outFieldLeft, outTypeFile, outGlobBinding(outNameA)))
	tool := outRecordTool(cwlcore.NewUnionType([]cwlcore.TypeRef{outTypeNull, record}))

	object := outWantRecord(t, outCollect(t, tool, dir, 0))
	outWantRecordFile(t, object, outFieldLeft, outNameA)
}

func TestCollectOutputsRecordFieldLoadContents(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outNameA, outContentHello)

	binding := outGlobBinding(outNameA)
	binding.LoadContents = true

	tool := outRecordTool(outRecord(outField(outFieldLeft, outTypeFile, binding)))
	object := outWantRecord(t, outCollect(t, tool, dir, 0))

	file := outWantRecordFile(t, object, outFieldLeft, outNameA)
	if !file.Contents.IsSet() || file.Contents.Value() != outContentHello {
		t.Errorf("contents = %s, want %q", file.Contents, outContentHello)
	}
}

func TestCollectOutputsRecordFieldLoadContentsOverTheLimit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outNameA, strings.Repeat("x", outContentLimit+1))

	binding := outGlobBinding(outNameA)
	binding.LoadContents = true

	tool := outRecordTool(outRecord(outField(outFieldLeft, outTypeFile, binding)))

	// The 64 KiB ceiling is an error rather than a truncation, per field exactly as per parameter.
	err := outCollectErr(t, tool, dir)
	assertErrorIs(t, "oversized loadContents in a record field", err, ErrContentsTooLarge)
}

func TestCollectOutputsRecordFieldLoadListing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outAuxName+"/"+outNameB, cltBeta)

	binding := outGlobBinding(outAuxName)
	binding.LoadListing = cwlcore.LoadListingShallow

	tool := outRecordTool(outRecord(outField(outFieldLeft, outTypeDirectory, binding)))
	object := outWantRecord(t, outCollect(t, tool, dir, 0))

	value, ok := object[outFieldLeft].(*cwlcore.Directory)
	if !ok {
		t.Fatalf("left = %#v, want a *cwlcore.Directory", object[outFieldLeft])
	}

	assertDeepEqual(t, "listing", outEntryNames(t, value.Listing), []string{outNameB})
}

func TestCollectOutputsRecordFieldSecondaryFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outPrimaryName, cltAlpha)
	outWriteFile(t, dir, outBaiName, cltBeta)

	field := outField(outFieldLeft, outTypeFile, outGlobBinding(outPrimaryName))
	field.SecondaryFiles = []cwlcore.SecondaryFileSchema{{Pattern: outCaretBai}}

	tool := outRecordTool(outRecord(field))
	object := outWantRecord(t, outCollect(t, tool, dir, 0))

	// The caret strips reads.bam's extension before the suffix is appended, giving reads.bai.
	file := outWantRecordFile(t, object, outFieldLeft, outPrimaryName)
	assertDeepEqual(t, "secondaryFiles", outEntryNames(t, file.SecondaryFiles), []string{outBaiName})
}

func TestCollectOutputsRecordFieldRequiredSecondaryFileIsMissing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outPrimaryName, cltAlpha)

	field := outField(outFieldLeft, outTypeFile, outGlobBinding(outPrimaryName))
	field.SecondaryFiles = []cwlcore.SecondaryFileSchema{
		{Pattern: outCaretBai, Required: cwlcore.NewExprBool(true)},
	}

	tool := outRecordTool(outRecord(field))

	err := outCollectErr(t, tool, dir)
	assertErrorIs(t, "missing required secondary file in a record field", err, ErrSecondaryMissing)
}

func TestCollectOutputsRecordFieldFormat(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outNameA, cltAlpha)

	field := outField(outFieldLeft, outTypeFile, outGlobBinding(outNameA))
	field.Format = []cwlcore.Expression{outRecFormat}

	tool := outRecordTool(outRecord(field))
	object := outWantRecord(t, outCollect(t, tool, dir, 0))

	assertDeepEqual(t, "format", outWantRecordFile(t, object, outFieldLeft, outNameA).Format, outRecFormat)
}

func TestCollectOutputsRecordFieldOutputEvalReadsTheExitCode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// A binding with no glob still runs its outputEval, with self bound to the zero-length array
	// the glob produced and runtime.exitCode defined — the one place the specification offers it.
	tool := outRecordTool(outRecord(outField(outFieldText, outTypeString,
		&cwlcore.CommandOutputBinding{OutputEval: "code $(runtime.exitCode) from $(self.length)"})))

	object := outWantRecord(t, outCollect(t, tool, dir, 3))
	assertDeepEqual(t, "text", object[outFieldText], "code 3 from 0")
}

func TestCollectOutputsRecordFieldWithoutABindingIsNullWhenOptional(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tool := outRecordTool(outRecord(outField(outFieldLeft, outTypeOptionalFile, nil)))

	object := outWantRecord(t, outCollect(t, tool, dir, 0))

	if _, present := object[outFieldLeft]; !present {
		t.Fatal("an unbound field must still appear in the record")
	}

	if object[outFieldLeft] != nil {
		t.Errorf("left = %#v, want nil", object[outFieldLeft])
	}
}

func TestCollectOutputsRecordFieldWithoutABindingIsFatalWhenRequired(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tool := outRecordTool(outRecord(outField(outFieldLeft, outTypeFile, nil)))

	// Nothing else can ever fill a record field, so a required one nobody collects is a document
	// that declared a value it never described.
	err := outCollectErr(t, tool, dir)
	assertErrorIs(t, "required record field with no binding", err, ErrOutputUnbound)
	assertNames(t, err, outRecPort, outFieldLeft)
}

func TestCollectOutputsRecordFromASchemaDefRequirement(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outNameA, cltAlpha)

	tool := outSchemaDefTool(t, `
- name: rec
  type: record
  fields:
    - name: left
      type: File
      outputBinding:
        glob: a.txt
`)

	object := outWantRecord(t, outCollect(t, tool, dir, 0))
	outWantRecordFile(t, object, outFieldLeft, outNameA)
}

func TestCollectOutputsSelfReferentialRecordTerminates(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outNameA, cltAlpha)

	// ResolveTypeRef refuses to expand a declaration that reaches itself and leaves the
	// cycle-closing edge as the bare name it was written as. A name is not a record, so the walk
	// stops there rather than descending for ever.
	tool := outSchemaDefTool(t, `
- name: rec
  type: record
  fields:
    - name: left
      type: File
      outputBinding:
        glob: a.txt
    - name: next
      type: ["null", rec]
`)

	object := outWantRecord(t, outCollect(t, tool, dir, 0))

	outWantRecordFile(t, object, outFieldLeft, outNameA)

	if object[outFieldNext] != nil {
		t.Errorf("next = %#v, want nil at the cycle-closing edge", object[outFieldNext])
	}
}

func TestCollectOutputsRecordOutputEvalProducesTheRecord(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outNameA, cltAlpha)

	// Both fields glob a file nothing ever writes, and one of them is a required File. Under the
	// field-by-field rule that is fatal; under the outputEval rule the fields' bindings are never
	// consulted, which is what makes the two readings distinguishable here.
	tool := outRecordToolBound(outRecord(
		outField(outFieldLeft, outTypeFile, outGlobBinding(outMissingName)),
		outField(outFieldText, outTypeString, outGlobBinding(outMissingName)),
	), &cwlcore.CommandOutputBinding{OutputEval: outRefSrc})

	inputs := map[string]any{outNameSrc: map[string]any{
		outFieldLeft: &cwlcore.File{Path: filepath.Join(dir, outNameA), Basename: outNameA},
		outFieldText: cltAlpha,
	}}

	object := outWantRecord(t, outCollectWith(t, tool, dir, inputs, cwlcore.NewEvaluator()))

	// The record the expression produced, re-typed and measured exactly as a globbed value is.
	assertDeepEqual(t, "left checksum", outWantRecordFile(t, object, outFieldLeft, outNameA).Checksum, outSumAlpha)
	assertDeepEqual(t, "text", object[outFieldText], cltAlpha)
}

func TestCollectOutputsRecordOutputEvalBindsSelfToTheGlobbedMatches(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outNameA, cltAlpha)
	outWriteFile(t, dir, outNameB, cltBeta)

	// self is the array of matches, on the same terms as for any other declaration: the glob on a
	// record's own binding feeds the expression rather than becoming the value.
	binding := outGlobBinding(outNameA, outNameB)
	binding.OutputEval = "${ return {left: self[0], text: self[1].basename}; }"

	tool := outRecordToolBound(outRecord(
		outField(outFieldLeft, outTypeFile, nil),
		outField(outFieldText, outTypeString, nil),
	), binding)

	object := outWantRecord(t, outCollectWith(t, tool, dir, nil, cwlcore.NewEvaluator(cwlcore.WithJS(nil))))

	assertDeepEqual(t, "left checksum", outWantRecordFile(t, object, outFieldLeft, outNameA).Checksum, outSumAlpha)
	assertDeepEqual(t, "text", object[outFieldText], outNameB)
}

func TestCollectOutputsNestedRecordFieldOutputEvalProducesTheRecord(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outNameA, cltAlpha)

	// The rule is the field's as much as the parameter's: a field that is itself a record and
	// names an outputEval produces that record whole.
	inner := outRecord(outField(outFieldRight, outTypeString, outGlobBinding(outMissingName)))

	tool := outRecordTool(outRecord(
		outField(outFieldLeft, outTypeFile, outGlobBinding(outNameA)),
		outField(outFieldInner, inner, &cwlcore.CommandOutputBinding{OutputEval: outRefSrc}),
	))

	inputs := map[string]any{outNameSrc: map[string]any{outFieldRight: cltBeta}}
	object := outWantRecord(t, outCollectWith(t, tool, dir, inputs, cwlcore.NewEvaluator()))

	outWantRecordFile(t, object, outFieldLeft, outNameA)
	assertDeepEqual(t, "right", outWantNestedRecord(t, object, outFieldInner)[outFieldRight], cltBeta)
}

func TestCollectOutputsRecordBindingWithoutOutputEvalCollectsTheFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outWriteFile(t, dir, outNameA, cltAlpha)

	// A glob describes how files are found, which for a record is still the fields' business. Only
	// outputEval says what the value *is*, so a parameter binding without one leaves the fields in
	// charge and its own glob inert.
	tool := outRecordToolBound(
		outRecord(outField(outFieldLeft, outTypeFile, outGlobBinding(outNameA))),
		outGlobBinding(outMissingName),
	)

	object := outWantRecord(t, outCollect(t, tool, dir, 0))

	assertDeepEqual(t, "left checksum", outWantRecordFile(t, object, outFieldLeft, outNameA).Checksum, outSumAlpha)
}

func TestCollectOutputsRecordOutputEvalIsTypeChecked(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Collecting the record through its own binding does not exempt it from the check that makes a
	// glob answer the declaration it was written under.
	tool := outRecordToolBound(
		outRecord(outField(outFieldLeft, outTypeFile, nil)),
		&cwlcore.CommandOutputBinding{OutputEval: outRefSrc},
	)

	_, err := CollectOutputs(tool, dir, 0, map[string]any{outNameSrc: cltAlpha},
		cwlcore.NewEvaluator(), cwlcore.RuntimeContext{Outdir: dir})
	if !errors.Is(err, ErrOutputType) {
		t.Fatalf("error = %v, want ErrOutputType", err)
	}
}

func TestRecordType(t *testing.T) {
	t.Parallel()

	record := outRecord(outField(outFieldLeft, outTypeFile, nil))
	arrayOf := func(items cwlcore.TypeRef) cwlcore.TypeRef {
		return cwlcore.NewArrayType(&cwlcore.ArraySchema{Items: items})
	}

	optional := cwlcore.NewUnionType([]cwlcore.TypeRef{outTypeNull, record})

	cases := map[string]struct {
		declared cwlcore.TypeRef
		found    bool
		depth    int
	}{
		"a record":                {declared: record, found: true},
		"an array of records":     {declared: arrayOf(record), found: true, depth: 1},
		"an array of arrays":      {declared: arrayOf(arrayOf(record)), found: true, depth: 2},
		"an optional record":      {declared: optional, found: true},
		"an array of files":       {declared: outTypeFileArray},
		"an optional file":        {declared: outTypeOptionalFile},
		"a primitive":             {declared: outTypeString},
		"an unresolved name":      {declared: cwlcore.NewNamedType(outRecPort)},
		"a record with no schema": {declared: cwlcore.NewRecordType(nil)},
		"an array with no schema": {declared: cwlcore.NewArrayType(nil)},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			shape := outRecordType(testCase.declared)

			if (shape.schema != nil) != testCase.found {
				t.Fatalf("outRecordType(%s) found = %t, want %t",
					testCase.declared, shape.schema != nil, testCase.found)
			}

			assertInt(t, "depth", shape.depth, testCase.depth)
		})
	}
}
