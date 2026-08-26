package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlexec"
)

// The fixtures the tests share, named once so that a rename is one edit.
const (
	expressionTool = "expression.cwl"
	echoTool       = "echo.cwl"
	requiredInput  = "required_input.cwl"
	jobFile        = "job.yml"
	dockerTool     = "docker.cwl"
	dockerHint     = "docker_hint.cwl"
	oldVersion     = "version_1_0.cwl"
	oldVersionStep = "workflow_old_step.cwl"
	noVersion      = "no_version.cwl"
	unknownType    = "unknown_type.cwl"
	notCWL         = "not_cwl.yml"
	inlineWorkflow = "workflow_inline.cwl"
	externalRunWF  = "workflow.cwl"
	nestedWorkflow = "subworkflow.cwl"
	unplannable    = "unplannable.cwl"
	missingFile    = "no_such_document.cwl"
)

// classFile is the class discriminator a File object carries on the wire.
const classFile = "File"

// errPlain stands in for an error carrying none of this tool's sentinels.
var errPlain = errors.New("something went wrong")

// result is one invocation of run, captured for assertions.
type result struct {
	err    error
	stdout string
	stderr string
}

// status is the exit status main would have produced for this result.
func (r result) status() int {
	if r.err == nil {
		return 0
	}

	return exitStatus(r.err)
}

// outputs parses the run's stdout as the CWL output object, failing the test
// when stdout is not exactly one JSON object. Asserting through a parse rather
// than a substring is deliberate: the contract is that stdout is machine
// readable in full, so a banner or a stray log line has to fail a test.
func (r result) outputs(t *testing.T) map[string]any {
	t.Helper()

	var object map[string]any

	decoder := json.NewDecoder(strings.NewReader(r.stdout))

	err := decoder.Decode(&object)
	if err != nil {
		t.Fatalf("stdout is not a JSON object: %v\nstdout was:\n%s", err, r.stdout)
	}

	rest, err := decoder.Token()
	if err == nil {
		t.Fatalf("stdout carries %v after the output object; it must carry nothing else", rest)
	}

	return object
}

// fixture names a file in testdata.
func fixture(name string) string {
	return filepath.Join("testdata", name)
}

// exercise runs the tool over args with captured output.
func exercise(t *testing.T, args ...string) result {
	t.Helper()

	var stdout, stderr bytes.Buffer

	err := run(args, &stdout, &stderr)

	return result{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

// exerciseIn runs the tool over a fixture with its outputs directed into a
// scratch directory, so that a test never writes into the source tree.
func exerciseIn(t *testing.T, outdir string, args ...string) result {
	t.Helper()

	return exercise(t, append([]string{"--outdir=" + outdir}, args...)...)
}

func TestRunWritesTheOutputObjectAndNothingElseToStdout(t *testing.T) {
	t.Parallel()

	got := exerciseIn(t, t.TempDir(), fixture(expressionTool))
	if got.err != nil {
		t.Fatalf("run: %v\n%s", got.err, got.stderr)
	}

	outputs := got.outputs(t)
	if outputs["greeting"] != "hello" {
		t.Errorf("greeting = %v, want hello", outputs["greeting"])
	}

	if outputs["count"] != float64(len("hello")) {
		t.Errorf("count = %v, want %d", outputs["count"], len("hello"))
	}

	if got.stderr != "" {
		t.Errorf("stderr = %q, want nothing", got.stderr)
	}
}

func TestRunExecutesACommandLineToolAndDescribesItsFile(t *testing.T) {
	t.Parallel()

	outdir := t.TempDir()

	got := exerciseIn(t, outdir, fixture(echoTool))
	if got.err != nil {
		t.Fatalf("run: %v\n%s", got.err, got.stderr)
	}

	file, ok := got.outputs(t)["out"].(map[string]any)
	if !ok {
		t.Fatalf("output out = %v, want a File object", got.outputs(t)["out"])
	}

	for _, key := range []string{"class", "location", "basename", "checksum", "size"} {
		if _, present := file[key]; !present {
			t.Errorf("File object has no %q; it has %v", key, file)
		}
	}

	if file["class"] != classFile {
		t.Errorf("class = %v, want %s", file["class"], classFile)
	}

	path, ok := file["path"].(string)
	if !ok {
		t.Fatalf("path = %v, want a string", file["path"])
	}

	if !strings.HasPrefix(path, outdir) {
		t.Errorf("path = %q, want it under the -outdir %q", path, outdir)
	}

	_, err := os.Stat(path)
	if err != nil {
		t.Errorf("the collected output is not on disk: %v", err)
	}
}

func TestRunReadsInputsFromAJobFile(t *testing.T) {
	t.Parallel()

	got := exerciseIn(t, t.TempDir(), fixture(requiredInput), fixture(jobFile))
	if got.err != nil {
		t.Fatalf("run: %v\n%s", got.err, got.stderr)
	}

	if want := "from the job file"; got.outputs(t)["out"] != want {
		t.Errorf("out = %v, want %q", got.outputs(t)["out"], want)
	}
}

func TestRunFailsWhenARequiredInputHasNoValue(t *testing.T) {
	t.Parallel()

	got := exerciseIn(t, t.TempDir(), fixture(requiredInput))
	if got.err == nil {
		t.Fatalf("run succeeded; want a failure naming the missing input\n%s", got.stdout)
	}

	if got.status() != exitFailure {
		t.Errorf("exit status = %d, want %d", got.status(), exitFailure)
	}

	if got.stdout != "" {
		t.Errorf("stdout = %q, want nothing on a failed run", got.stdout)
	}

	for _, want := range []string{`input "needed"`, "declares no default"} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", got.stderr, want)
		}
	}
}

func TestRunReportsAnInvalidDocumentAsAReadableTree(t *testing.T) {
	t.Parallel()

	got := exerciseIn(t, t.TempDir(), fixture(unknownType))
	if got.err == nil {
		t.Fatalf("run succeeded on an invalid document\n%s", got.stdout)
	}

	if got.status() != exitFailure {
		t.Errorf("exit status = %d, want %d", got.status(), exitFailure)
	}

	if got.stdout != "" {
		t.Errorf("stdout = %q, want nothing on a failed run", got.stdout)
	}

	// The file, the line and column, the offending value, and the nesting
	// that makes a tree a tree.
	for _, want := range []string{unknownType + ": FAILED", unknownType + ":6:11", `"strng"`, "\n  "} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", got.stderr, want)
		}
	}
}

func TestRunExitsUnsupportedForFeaturesThisEngineLacks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
		want string
	}{
		{name: "an old cwlVersion", file: oldVersion, want: `cwlVersion "v1.0"`},
		{name: "an old cwlVersion under a step's run", file: oldVersionStep, want: `step "legacy"`},
		{name: "a DockerRequirement", file: dockerTool, want: "DockerRequirement"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := exerciseIn(t, t.TempDir(), fixture(tc.file))
			if got.status() != exitUnsupported {
				t.Fatalf("exit status = %d, want %d\n%s", got.status(), exitUnsupported, got.stderr)
			}

			if got.stdout != "" {
				t.Errorf("stdout = %q, want nothing when nothing ran", got.stdout)
			}

			if !strings.Contains(got.stderr, tc.want) {
				t.Errorf("stderr = %q, want it to say why: %q", got.stderr, tc.want)
			}
		})
	}
}

func TestRunRequiresATopLevelCWLVersion(t *testing.T) {
	t.Parallel()

	got := exerciseIn(t, t.TempDir(), fixture(noVersion))
	if !errors.Is(got.err, errNoCWLVersion) {
		t.Fatalf("err = %v, want a missing-cwlVersion failure", got.err)
	}

	// A document that says nothing about its version is malformed, not
	// written against a version we lack, so it must not be reported as a
	// skip.
	if got.status() != exitFailure {
		t.Errorf("exit status = %d, want %d", got.status(), exitFailure)
	}
}

func TestRunReportsAMissingDocument(t *testing.T) {
	t.Parallel()

	got := exerciseIn(t, t.TempDir(), fixture(missingFile))
	if got.status() != exitFailure {
		t.Fatalf("exit status = %d, want %d", got.status(), exitFailure)
	}

	if !strings.Contains(got.stderr, missingFile) {
		t.Errorf("stderr = %q, want it to name the document", got.stderr)
	}
}

func TestRunQuietSuppressesAdvisoriesButNotResults(t *testing.T) {
	t.Parallel()

	const advisory = "DockerRequirement is a hint"

	loud := exerciseIn(t, t.TempDir(), fixture(dockerHint))
	if loud.err != nil {
		t.Fatalf("run: %v\n%s", loud.err, loud.stderr)
	}

	if !strings.Contains(loud.stderr, advisory) {
		t.Fatalf("stderr = %q, want the advisory %q; the fixture no longer exercises -quiet", loud.stderr, advisory)
	}

	quiet := exerciseIn(t, t.TempDir(), "--quiet", fixture(dockerHint))
	if quiet.err != nil {
		t.Fatalf("run: %v\n%s", quiet.err, quiet.stderr)
	}

	if quiet.stderr != "" {
		t.Errorf("stderr under -quiet = %q, want nothing", quiet.stderr)
	}

	if quiet.outputs(t)["out"] == nil {
		t.Error("-quiet suppressed the output object; it must only suppress stderr")
	}
}

func TestRunQuietStillReportsAFailure(t *testing.T) {
	t.Parallel()

	got := exerciseIn(t, t.TempDir(), "-q", fixture(requiredInput))
	if got.err == nil {
		t.Fatal("run succeeded; want a failure")
	}

	if got.stderr == "" {
		t.Error("stderr = empty; a failure with no explanation is not a quieter result")
	}
}

func TestRunTrimsALongReportUnlessVerbose(t *testing.T) {
	t.Parallel()

	const trimmed = "re-run with -verbose"

	brief := exerciseIn(t, t.TempDir(), fixture(notCWL))
	if brief.err == nil {
		t.Fatal("run succeeded on a document that is not CWL")
	}

	if !strings.Contains(brief.stderr, trimmed) {
		t.Fatalf("stderr = %q, want it trimmed with %q; the fixture no longer exercises -verbose",
			brief.stderr, trimmed)
	}

	full := exerciseIn(t, t.TempDir(), "-verbose", fixture(notCWL))
	if strings.Contains(full.stderr, trimmed) {
		t.Error("-verbose still trimmed the report")
	}

	if len(full.stderr) <= len(brief.stderr) {
		t.Errorf("-verbose report is %d bytes, not longer than the trimmed %d", len(full.stderr), len(brief.stderr))
	}
}

func TestRunRunsAWorkflowWithAnInlineStep(t *testing.T) {
	t.Parallel()

	got := exerciseIn(t, t.TempDir(), fixture(inlineWorkflow))
	if got.err != nil {
		t.Fatalf("run: %v\n%s", got.err, got.stderr)
	}

	if want := "FROM A WORKFLOW"; got.outputs(t)["result"] != want {
		t.Errorf("result = %v, want %q", got.outputs(t)["result"], want)
	}
}

func TestRunRunsAWorkflowWithAnExternalRunReference(t *testing.T) {
	t.Parallel()

	got := exerciseIn(t, t.TempDir(), fixture(externalRunWF))
	if got.err != nil {
		t.Fatalf("run: %v\n%s", got.err, got.stderr)
	}

	if got.outputs(t)["result"] == nil {
		t.Error("result = nil, want the file the step produced")
	}
}

// The two tests below pin the seam that cwlexec.WithSubworkflows exists for.
//
// A StepHandler is handed one invocation and nothing about the run around it,
// so a Workflow under a step's run: has no way of its own to reach the registry
// and configuration this command line resolved; without the context it falls
// back to a fresh registry and the zero Config. The fixture nests two levels so
// that the tool doing the observable thing — warning about a DockerRequirement
// hint — runs inside the nested scheduler rather than the outer one.

func TestRunCarriesQuietIntoASubworkflow(t *testing.T) {
	t.Parallel()

	const advisory = "DockerRequirement is a hint"

	loud := exerciseIn(t, t.TempDir(), fixture(nestedWorkflow))
	if loud.err != nil {
		t.Fatalf("run: %v\n%s", loud.err, loud.stderr)
	}

	// step=inner is the nested run's step, so this warning could only have
	// come from inside the subworkflow.
	if !strings.Contains(loud.stderr, advisory) || !strings.Contains(loud.stderr, "step=inner") {
		t.Fatalf("stderr = %q, want the nested step's advisory; the fixture no longer nests", loud.stderr)
	}

	quiet := exerciseIn(t, t.TempDir(), "--quiet", fixture(nestedWorkflow))
	if quiet.err != nil {
		t.Fatalf("run: %v\n%s", quiet.err, quiet.stderr)
	}

	if quiet.stderr != "" {
		t.Errorf("stderr under -quiet = %q, want nothing; -quiet did not reach the subworkflow", quiet.stderr)
	}

	if quiet.outputs(t)["result"] == nil {
		t.Error("-quiet suppressed the nested run's output")
	}
}

func TestRunCarriesOutdirIntoASubworkflow(t *testing.T) {
	t.Parallel()

	outdir := t.TempDir()

	got := exerciseIn(t, outdir, fixture(nestedWorkflow))
	if got.err != nil {
		t.Fatalf("run: %v\n%s", got.err, got.stderr)
	}

	file, ok := got.outputs(t)["result"].(map[string]any)
	if !ok {
		t.Fatalf("result = %v, want a File object", got.outputs(t)["result"])
	}

	path, ok := file["path"].(string)
	if !ok {
		t.Fatalf("path = %v, want a string", file["path"])
	}

	if !strings.HasPrefix(path, outdir) {
		t.Errorf("path = %q, want it under the -outdir %q", path, outdir)
	}

	// The nesting is visible in the path, which is what keeps two
	// invocations of a scattered subworkflow step from colliding.
	if !strings.Contains(path, filepath.Join("nest", "inner")) {
		t.Errorf("path = %q, want the nesting visible in it", path)
	}
}

func TestRunReportsADocumentItCannotPlan(t *testing.T) {
	t.Parallel()

	// Analysis is eager and fail-closed: a workflow that uses a feature
	// without the requirement it needs is refused before any step runs,
	// rather than half way through a run that has already written files.
	got := exerciseIn(t, t.TempDir(), fixture(unplannable))
	if got.status() != exitFailure {
		t.Fatalf("exit status = %d, want %d\n%s", got.status(), exitFailure, got.stderr)
	}

	if got.stdout != "" {
		t.Errorf("stdout = %q, want nothing when nothing ran", got.stdout)
	}

	if !strings.Contains(got.stderr, "ScatterFeatureRequirement") {
		t.Errorf("stderr = %q, want it to name the missing requirement", got.stderr)
	}
}

func TestRunProducesByteIdenticalOutputAcrossRuns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
	}{
		{name: "several scalar outputs", file: expressionTool},
		{name: "a File output", file: echoTool},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// One output directory for both runs, so that the only
			// thing that could differ is the rendering.
			outdir := t.TempDir()

			first := exerciseIn(t, outdir, fixture(tc.file))
			second := exerciseIn(t, outdir, fixture(tc.file))

			if first.err != nil || second.err != nil {
				t.Fatalf("run: %v / %v", first.err, second.err)
			}

			if first.stdout != second.stdout {
				t.Errorf("two runs differ:\n%s\n---\n%s", first.stdout, second.stdout)
			}
		})
	}
}

func TestRunPrintsVersionOnStdout(t *testing.T) {
	t.Parallel()

	got := exercise(t, "--version")
	if got.err != nil {
		t.Fatalf("run: %v", got.err)
	}

	for _, want := range []string{toolName, "CWL v1.2"} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", got.stdout, want)
		}
	}

	if got.stderr != "" {
		t.Errorf("stderr = %q, want nothing", got.stderr)
	}
}

func TestRunRejectsCommandLinesItCannotUnderstand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "no process", args: nil},
		{name: "an unknown flag", args: []string{"-nope", fixture(expressionTool)}},
		{name: "a third positional argument", args: []string{fixture(requiredInput), fixture(jobFile), "extra"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := exercise(t, tc.args...)
			if got.status() != exitUsage {
				t.Fatalf("exit status = %d, want %d (err %v)", got.status(), exitUsage, got.err)
			}

			if !strings.Contains(got.stderr, "Usage:") {
				t.Errorf("stderr = %q, want the usage message", got.stderr)
			}

			if got.stdout != "" {
				t.Errorf("stdout = %q, want nothing", got.stdout)
			}
		})
	}
}

func TestRunTreatsHelpAsSuccess(t *testing.T) {
	t.Parallel()

	got := exercise(t, "-h")
	if got.err != nil {
		t.Fatalf("run: %v, want -h to succeed", got.err)
	}

	if !strings.Contains(got.stderr, "Usage:") {
		t.Errorf("stderr = %q, want the usage message", got.stderr)
	}
}

func TestExitStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err  error
		name string
		want int
	}{
		{name: "an ordinary failure", err: errPlain, want: exitFailure},
		{name: "a bad command line", err: fmt.Errorf("%w: nope", errUsage), want: exitUsage},
		{
			name: "an unsupported feature",
			err:  fmt.Errorf("wrapped: %w", cwlexec.ErrUnsupportedFeature),
			want: exitUnsupported,
		},
		{
			// Unsupported outranks usage, so that a document
			// reached at all is reported by what it needed.
			name: "both, unsupported first",
			err:  fmt.Errorf("%w: %w", errUsage, cwlexec.ErrUnsupportedFeature),
			want: exitUnsupported,
		},
		{name: "a suspended run", err: errSuspended, want: exitFailure},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := exitStatus(tc.err); got != tc.want {
				t.Errorf("exitStatus(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

func TestSuspendedErrorNamesWhatIsWaiting(t *testing.T) {
	t.Parallel()

	none := suspendedError(nil)
	if !errors.Is(none, errSuspended) || none.Error() != errSuspended.Error() {
		t.Errorf("suspendedError(nil) = %v, want the bare sentinel", none)
	}

	some := suspendedError([]cwlexec.Suspension{{StepID: "approve"}, {StepID: "sign"}})
	if !errors.Is(some, errSuspended) {
		t.Fatalf("suspendedError = %v, want it to wrap the sentinel", some)
	}

	for _, want := range []string{"approve", "sign"} {
		if !strings.Contains(some.Error(), want) {
			t.Errorf("suspendedError = %q, want it to name %q", some.Error(), want)
		}
	}
}

func TestOutputDirDefaultsToTheWorkingDirectory(t *testing.T) {
	t.Parallel()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	got, err := (&config{}).outputDir()
	if err != nil {
		t.Fatalf("outputDir: %v", err)
	}

	if got != cwd {
		t.Errorf("outputDir = %q, want the working directory %q", got, cwd)
	}
}

func TestOutputDirIsAbsoluteAndCreated(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	got, err := (&config{outdir: filepath.Join(root, "nested", "outputs")}).outputDir()
	if err != nil {
		t.Fatalf("outputDir: %v", err)
	}

	if !filepath.IsAbs(got) {
		t.Errorf("outputDir = %q, want an absolute path", got)
	}

	info, err := os.Stat(got)
	if err != nil || !info.IsDir() {
		t.Errorf("outputDir %q was not created: %v", got, err)
	}
}

func TestOutputDirRejectsAPathThatCannotBeCreated(t *testing.T) {
	t.Parallel()

	blocker := filepath.Join(t.TempDir(), "file")

	err := os.WriteFile(blocker, []byte("not a directory"), 0o600)
	if err != nil {
		t.Fatalf("writing the blocking file: %v", err)
	}

	_, err = (&config{outdir: filepath.Join(blocker, "under")}).outputDir()
	if err == nil {
		t.Error("outputDir succeeded under a regular file; want a failure")
	}
}
