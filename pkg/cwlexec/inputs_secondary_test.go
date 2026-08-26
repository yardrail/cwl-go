package cwlexec

import (
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// The step half of the secondaryFiles rule: inside a workflow step a declared pattern is required,
// and nothing goes to disk to satisfy it. The top half — discovery over a job order — is in
// joborder_secondary_test.go, and tests/record-in-secondaryFiles-missing-wf.cwl is the conformance
// case that only passes when the two differ.

// stepPrimaryName is the File the step fixtures pass along, and stepCompanion the companion
// ".bai" derives from it.
const (
	stepPrimaryName = "data.bam"
	stepCompanion   = stepPrimaryName + outExtBai
)

// stepSecondaryRun builds the Operation the sink step runs, declaring schemas on its one input,
// typed as typ.
func stepSecondaryRun(typ cwlcore.TypeRef, schemas ...cwlcore.SecondaryFileSchema) *cwlcore.Operation {
	run := newOperation(wfID(sinkStep)+"/run", []string{sinkPort}, []string{portA}, nil)
	run.Inputs[0].Type = typ
	run.Inputs[0].SecondaryFiles = schemas

	return run
}

// stepSecondaryError runs spec with an upstream value of upstream and requires the run to fail.
func stepSecondaryError(t *testing.T, spec *wfSpec, upstream any) error {
	t.Helper()

	runner := mustRunner(t, spec, producerRegistry(object("p1", upstream)), nil)

	_, err := runner.Run(t.Context(), object("x", 0))
	if err == nil {
		t.Fatal("Run succeeded, want a required secondary file to be reported missing")
	}

	return err
}

// stepPrimary is a File value an upstream step published, carrying the named companions.
func stepPrimary(t *testing.T, companions ...string) *cwlcore.File {
	t.Helper()

	file := writeFile(t, stepPrimaryName, []byte("bam"))
	file.Size = cwlcore.NewOptInt(3)

	if len(companions) == 0 {
		return file
	}

	file.SecondaryFiles = make([]cwlcore.FileOrDirectory, 0, len(companions))
	for _, name := range companions {
		file.SecondaryFiles = append(file.SecondaryFiles, &cwlcore.File{Basename: name})
	}

	return file
}

// TestStepRequiresADeclaredSecondaryFile is the shape of the conformance case: the value reaching
// the step carries no companion, the tool the step runs declares one, and the run must fail. The
// file is sitting right beside the primary on disk, which is exactly what must not save it — a
// discovery here would make the workflow indistinguishable from one whose own input declared the
// pattern.
func TestStepRequiresADeclaredSecondaryFile(t *testing.T) {
	t.Parallel()

	run := stepSecondaryRun(jobTypeFile, jobPatternOf(outExtBai))

	err := stepSecondaryError(t, loadingWorkflow(run), stepPrimary(t))

	assertErrorIs(t, "Run", err, ErrSecondaryMissing)

	if !strings.Contains(err.Error(), stepCompanion) {
		t.Errorf("error = %v, want it to name the companion it looked for", err)
	}
}

// TestStepAcceptsASuppliedSecondaryFile is the other half: the enclosing document did its job, so
// the companion is already on the value and the same declaration is satisfied without a stat.
func TestStepAcceptsASuppliedSecondaryFile(t *testing.T) {
	t.Parallel()

	run := stepSecondaryRun(jobTypeFile, jobPatternOf(outExtBai))
	primary := stepPrimary(t, stepCompanion)

	got, isFile := runLoading(t, loadingWorkflow(run), primary).(*cwlcore.File)
	if !isFile {
		t.Fatalf("step input = %#v, want a *cwlcore.File", primary)
	}

	assertDeepEqual(t, "secondaryFiles", basenameOf(got.SecondaryFiles[0]), stepCompanion)
}

// TestStepSecondaryFilesReachRecordFieldsAndArrayItems is record-in-secondaryFiles.cwl's shape:
// the patterns live on a record's fields rather than on the parameter, so a check that only looked
// at parameters would find nothing to report.
func TestStepSecondaryFilesReachRecordFieldsAndArrayItems(t *testing.T) {
	t.Parallel()

	record := cwlcore.NewRecordType(&cwlcore.RecordSchema{
		Fields: []cwlcore.RecordField{
			{Name: "f1", Type: jobTypeFile},
			{
				Name:           "f2",
				Type:           cwlcore.NewArrayType(&cwlcore.ArraySchema{Items: jobTypeFile}),
				SecondaryFiles: []cwlcore.SecondaryFileSchema{jobPatternOf(".s3")},
			},
		},
	})

	upstream := map[string]any{"f1": stepPrimary(t), "f2": list(stepPrimary(t))}

	err := stepSecondaryError(t, loadingWorkflow(stepSecondaryRun(record)), upstream)

	assertErrorIs(t, "Run", err, ErrSecondaryMissing)

	if !strings.Contains(err.Error(), "f2[0]") {
		t.Errorf("error = %v, want it to name the array element it reached", err)
	}
}

// TestStepSecondaryFilesLetsThroughWhatCannotBeRequired covers everything a required pattern is not
// violated by: an optional declaration, a null an expression returned, an object the expression
// supplied outright, a value that is not a File at all, and a File with no path — which has not
// been staged yet, so blaming the pattern would blame it for the primary's placement.
func TestStepSecondaryFilesLetsThroughWhatCannotBeRequired(t *testing.T) {
	t.Parallel()

	cases := []struct {
		upstream any
		name     string
		schema   cwlcore.SecondaryFileSchema
	}{
		{
			name:   "declared optional",
			schema: cwlcore.SecondaryFileSchema{Pattern: outExtBai, Required: cwlcore.NewExprBool(false)},
		},
		{
			name:   "the optional marker",
			schema: jobPatternOf(outExtBai + "?"),
		},
		{
			name:   "an expression returning null",
			schema: jobPatternOf("$(null)"),
		},
		{
			name:   "an expression supplying the companion itself",
			schema: jobPatternOf("$(inputs." + sinkPort + ")"),
		},
		{
			name:     "not a File",
			schema:   jobPatternOf(outExtBai),
			upstream: "plain text",
		},
		{
			name:     "a File with no path",
			schema:   jobPatternOf(outExtBai),
			upstream: &cwlcore.File{Basename: "literal.bam", Contents: cwlcore.NewOptString("x")},
		},
		{
			// An object bound to a declaration that reaches no record: there are no
			// field declarations to descend to, so there is nothing to require.
			name:     "an object the declared type does not describe",
			schema:   jobPatternOf(outExtBai),
			upstream: map[string]any{"whatever": 1},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			upstream := tc.upstream
			if upstream == nil {
				upstream = stepPrimary(t)
			}

			run := stepSecondaryRun(jobTypeFile, tc.schema)

			assertDeepEqual(t, "step input", runLoading(t, loadingWorkflow(run), upstream), upstream)
		})
	}
}

// TestStepSecondaryFilesReportsAnUnusablePattern covers the two ways evaluating a declaration fails
// rather than answering: a `required` that will not evaluate, and a pattern that will not.
func TestStepSecondaryFilesReportsAnUnusablePattern(t *testing.T) {
	t.Parallel()

	// The diagnostics arrive as a rendered salad.Error tree rather than a wrapped sentinel, which
	// is what the discovery pass's own reporting produces and what these reuse.
	cases := map[string]struct {
		schema cwlcore.SecondaryFileSchema
		want   string
	}{
		"required does not evaluate": {
			schema: cwlcore.SecondaryFileSchema{
				Pattern:  outExtBai,
				Required: cwlcore.NewExprBoolExpression("$(nonesuch)"),
			},
			want: "nonesuch",
		},
		"pattern does not evaluate": {
			schema: jobPatternOf("$(nonesuch)"),
			want:   "nonesuch",
		},
		"pattern produces a number": {
			schema: jobPatternOf("$(self.size)"),
			want:   ErrSecondaryValue.Error(),
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			run := stepSecondaryRun(jobTypeFile, tc.schema)

			err := stepSecondaryError(t, loadingWorkflow(run), stepPrimary(t))
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestStepSecondaryFilesAcceptASatisfiedRecord is the record walk's other outcome: every field's
// declaration is met, so the value passes through untouched.
func TestStepSecondaryFilesAcceptASatisfiedRecord(t *testing.T) {
	t.Parallel()

	record := cwlcore.NewRecordType(&cwlcore.RecordSchema{
		Fields: []cwlcore.RecordField{{
			Name:           "f1",
			Type:           jobTypeFile,
			SecondaryFiles: []cwlcore.SecondaryFileSchema{jobPatternOf(outExtBai)},
		}},
	})

	upstream := map[string]any{"f1": stepPrimary(t, stepCompanion)}

	assertDeepEqual(t, "step input",
		runLoading(t, loadingWorkflow(stepSecondaryRun(record)), upstream), upstream)
}

// TestStepSecondaryFilesDoNotApplyToABareProcess pins the half of the rule that is about where the
// check runs: a process invoked directly is the top level, where a pattern is discovered from disk
// rather than required, so the same declaration that fails inside a step succeeds here.
func TestStepSecondaryFilesDoNotApplyToABareProcess(t *testing.T) {
	t.Parallel()

	bare := stepSecondaryRun(jobTypeFile, jobPatternOf(outExtBai))

	runner, err := NewRunner(t.Context(), bare, testRegistry(constOutputs(object(portA, nil))), nil)
	if err != nil {
		t.Fatalf("NewRunner: unexpected error: %v", err)
	}

	_, err = runner.Run(t.Context(), object(sinkPort, stepPrimary(t)))
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
}
