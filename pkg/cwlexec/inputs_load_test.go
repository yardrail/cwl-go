package cwlexec

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// The enrichment a step input needs and a job order's input gets for free: loadContents on a File,
// loadListing on a Directory, and the materialization of a `default` written as a document mapping.
// A step's input object is assembled by the scheduler and never passes through job-order loading,
// so these tests are what pin that it is enriched all the same.

// loadingWorkflow is one producer step feeding a sink step that runs run, so that the value the
// sink's handler observes is a value that travelled along a workflow edge.
func loadingWorkflow(run cwlcore.Process) *wfSpec {
	return &wfSpec{
		inputs: []string{"x"},
		steps: []stepSpec{
			{name: "p1", in: []inSpec{{name: portIn, sources: []string{"x"}}}, out: []string{portA}},
			{
				name: sinkStep,
				run:  run,
				in:   []inSpec{{name: sinkPort, sources: []string{"p1/" + portA}}},
				out:  []string{portA},
			},
		},
		outputs: []outSpec{{name: gotPort, sources: []string{sinkStep + "/" + portA}}},
	}
}

// loadingRun builds the Operation the sink step runs, declaring loadContents on its one input.
func loadingRun() *cwlcore.Operation {
	run := newOperation(wfID(sinkStep)+"/run", []string{sinkPort}, []string{portA}, nil)
	run.Inputs[0].LoadContents = true

	return run
}

// writeFile writes content into a fresh temporary directory and returns the File value an upstream
// step would have published for it.
func writeFile(t *testing.T, name string, content []byte) *cwlcore.File {
	t.Helper()

	local := filepath.Join(t.TempDir(), name)

	err := os.WriteFile(local, content, 0o600)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	return &cwlcore.File{Location: "file://" + local, Path: local, Basename: name}
}

// runLoading runs spec with an upstream value of upstream and returns what reached the sink.
//
// The value is taken inside the sink step's handler rather than from the run's output object,
// because the two are no longer the same statement: the object a run publishes has had every
// Directory's listing completed on the way out, which would make a test asking whether a listing was
// read at the step input pass whatever the answer was.
func runLoading(t *testing.T, spec *wfSpec, upstream any) any {
	t.Helper()

	return runObserved(t, spec, object("p1", upstream))
}

// runObserved runs spec with the given producer values and returns the value the sink step was
// handed, taken inside the handler.
func runObserved(t *testing.T, spec *wfSpec, values map[string]any) any {
	t.Helper()

	return runObservedWorkflow(t, buildWorkflow(spec), values)
}

// runObservedWorkflow is [runObserved] over an already-built workflow, for the tests that have to
// reach into one and set a field the spec builder does not carry.
func runObservedWorkflow(t *testing.T, workflow *cwlcore.Workflow, values map[string]any) any {
	t.Helper()

	var seen any

	// The sink publishes a placeholder rather than the value it was handed. Handing it back would
	// wire the observed value into the run's own output, where the publication pass completes
	// every Directory's listing — through the very pointer the observation holds, so a test asking
	// whether the *step input* had a listing read would be answered by what happened to it
	// afterwards.
	registry := testRegistry(func(_ context.Context, call *StepCall) (Result, error) {
		if call.StepID == sinkStep {
			seen = call.Inputs[sinkPort]

			return Success(object(portA, "observed"))
		}

		return Success(object(portA, values[call.StepID]))
	})

	runner, err := NewRunner(t.Context(), workflow, registry, nil)
	if err != nil {
		t.Fatalf("NewRunner: unexpected error: %v", err)
	}

	mustRun(t, runner, object("x", 0))

	return seen
}

// contentsOf reports the contents of a File that arrived on a step input.
func contentsOf(t *testing.T, value any) string {
	t.Helper()

	file, isFile := value.(*cwlcore.File)
	if !isFile {
		t.Fatalf("step input = %#v, want a *cwlcore.File", value)
	}

	if !file.Contents.IsSet() {
		t.Fatal("step input File has no contents; loadContents was not honoured")
	}

	return file.Contents.Value()
}

// TestResolveInputsLoadsContentsFromAnUpstreamStep is the shape count-lines1-wf.cwl takes: the
// value comes from an upstream step rather than from a job order, so nothing has parsed a document
// node for it and only the resolution path can read the file.
func TestResolveInputsLoadsContentsFromAnUpstreamStep(t *testing.T) {
	t.Parallel()

	file := writeFile(t, "counted.txt", []byte("16\n"))

	assertDeepEqual(t, "contents", contentsOf(t, runLoading(t, loadingWorkflow(loadingRun()), file)), "16\n")
}

// TestResolveInputsLoadsContentsDeclaredOnTheStepInput covers the other place the request can be
// written: on the step's own `in` entry rather than on the run process's parameter.
func TestResolveInputsLoadsContentsDeclaredOnTheStepInput(t *testing.T) {
	t.Parallel()

	file := writeFile(t, "counted.txt", []byte("16\n"))

	spec := loadingWorkflow(newOperation(wfID(sinkStep)+"/run", []string{sinkPort}, []string{portA}, nil))
	workflow := buildWorkflow(spec)
	workflow.Steps[1].In[0].LoadContents = true

	got := runObservedWorkflow(t, workflow, object("p1", file))
	assertDeepEqual(t, "contents", contentsOf(t, got), "16\n")
}

// TestResolveInputsLoadsContentsElementByElement covers the array case the specification calls out:
// "type: File or an array of items: File".
func TestResolveInputsLoadsContentsElementByElement(t *testing.T) {
	t.Parallel()

	first := writeFile(t, "one.txt", []byte("one"))
	second := writeFile(t, "two.txt", []byte("two"))

	got := runLoading(t, loadingWorkflow(loadingRun()), list(first, second))

	items, isArray := got.([]any)
	if !isArray || len(items) != 2 {
		t.Fatalf("step input = %#v, want a two-element array", got)
	}

	assertDeepEqual(t, "first", contentsOf(t, items[0]), "one")
	assertDeepEqual(t, "second", contentsOf(t, items[1]), "two")
}

// TestResolveInputsLeavesTheUpstreamFileAlone pins the copy in loadFileContents: a step's
// loadContents request must not rewrite the value its upstream step published.
func TestResolveInputsLeavesTheUpstreamFileAlone(t *testing.T) {
	t.Parallel()

	file := writeFile(t, "counted.txt", []byte("16\n"))

	runLoading(t, loadingWorkflow(loadingRun()), file)

	if file.Contents.IsSet() {
		t.Fatalf("upstream File.Contents = %q, want it left unset", file.Contents.Value())
	}
}

// TestResolveInputsPassesThroughValuesLoadContentsDoesNotApplyTo covers the three shapes a request
// finds nothing to do with: a value that is not a File, a file literal that already carries its
// contents, and a File naming a resource with no local path.
func TestResolveInputsPassesThroughValuesLoadContentsDoesNotApplyTo(t *testing.T) {
	t.Parallel()

	literal := &cwlcore.File{Basename: "lit.txt", Contents: cwlcore.NewOptString("already here")}
	remote := &cwlcore.File{Location: "https://example.org/far.txt", Basename: "far.txt"}

	cases := []struct {
		upstream any
		want     any
		name     string
	}{
		{name: "not a File", upstream: jobPlainText, want: jobPlainText},
		{name: "file literal", upstream: literal, want: literal},
		{name: "no local path", upstream: remote, want: remote},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assertDeepEqual(t, "step input", runLoading(t, loadingWorkflow(loadingRun()), tc.upstream), tc.want)
		})
	}
}

// TestResolveInputsRejectsAnUnusableLoadContentsRead covers the three ways the read itself fails.
// None of them truncates: Process.yml requires a fatal error over the 64 KiB ceiling.
func TestResolveInputsRejectsAnUnusableLoadContentsRead(t *testing.T) {
	t.Parallel()

	cases := []struct {
		file    func(t *testing.T) *cwlcore.File
		name    string
		want    error
		inArray bool
	}{
		{
			name: "over the 64 KiB ceiling",
			file: func(t *testing.T) *cwlcore.File {
				t.Helper()

				return writeFile(t, "big.txt", make([]byte, joMaxContentsBytes+1))
			},
			want: ErrContentsTooLarge,
		},
		{
			name: "not UTF-8 text",
			file: func(t *testing.T) *cwlcore.File {
				t.Helper()

				return writeFile(t, "binary.dat", []byte{0xff, 0xfe, 0x00})
			},
			want: ErrContentsNotText,
		},
		{
			name: "no such file",
			file: func(t *testing.T) *cwlcore.File {
				t.Helper()

				gone := writeFile(t, "gone.txt", []byte("x"))

				err := os.Remove(gone.Path)
				if err != nil {
					t.Fatalf("Remove: %v", err)
				}

				return gone
			},
			want: ErrLoadContents,
		},
		{
			name: "one bad element of an array",
			file: func(t *testing.T) *cwlcore.File {
				t.Helper()

				return writeFile(t, "binary.dat", []byte{0xff})
			},
			want:    ErrContentsNotText,
			inArray: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var upstream any = tc.file(t)
			if tc.inArray {
				upstream = list(upstream)
			}

			spec := loadingWorkflow(loadingRun())
			runner := mustRunner(t, spec, producerRegistry(object("p1", upstream)), nil)

			_, err := runner.Run(t.Context(), object("x", 0))
			assertErrorIs(t, "Run", err, tc.want)
		})
	}
}

// listingRun builds the Operation the sink step runs, declaring mode as its one input's
// loadListing. An empty mode declares none, leaving whatever the requirement in scope says.
func listingRun(mode cwlcore.LoadListingEnum) *cwlcore.Operation {
	run := newOperation(wfID(sinkStep)+"/run", []string{sinkPort}, []string{portA}, nil)
	run.Inputs[0].LoadListing = mode

	return run
}

// makeDir creates a directory holding one file and returns the Directory value an upstream step
// would have published for it.
func makeDir(t *testing.T, entries ...string) *cwlcore.Directory {
	t.Helper()

	local := filepath.Join(t.TempDir(), "d")

	err := os.Mkdir(local, 0o700)
	if err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	for _, name := range entries {
		err = os.WriteFile(filepath.Join(local, name), []byte(name), 0o600)
		if err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	return &cwlcore.Directory{Location: "file://" + local, Path: local, Basename: "d"}
}

// reopen restores a directory the test made unreadable, so the temporary tree can be removed.
func reopen(t *testing.T, local string) {
	t.Helper()

	err := os.Chmod(local, 0o700)
	if err != nil {
		t.Fatalf("Chmod: %v", err)
	}
}

// listingOf reports the basenames of the listing a Directory arrived on a step input with, and nil
// when it arrived with none.
func listingOf(t *testing.T, value any) []string {
	t.Helper()

	dir, isDir := value.(*cwlcore.Directory)
	if !isDir {
		t.Fatalf("step input = %#v, want a *cwlcore.Directory", value)
	}

	if dir.Listing == nil {
		return nil
	}

	names := make([]string, 0, len(dir.Listing))
	for _, entry := range dir.Listing {
		file, isFile := entry.(*cwlcore.File)
		if !isFile {
			t.Fatalf("listing entry = %#v, want a *cwlcore.File", entry)
		}

		names = append(names, file.Basename)
	}

	return names
}

// TestResolveInputsReadsADirectoryListing is the shape inpdir_update_wf.cwl takes: an inner
// ExpressionTool declares loadListing on a Directory that arrives from an upstream step, so nothing
// has passed it through job-order loading.
func TestResolveInputsReadsADirectoryListing(t *testing.T) {
	t.Parallel()

	got := runLoading(t, loadingWorkflow(listingRun(cwlcore.LoadListingShallow)), makeDir(t, dirEntry))

	assertDeepEqual(t, "listing", listingOf(t, got), []string{dirEntry})
}

// TestResolveInputsReadsADirectoryListingDeclaredOnTheStepInput covers the step's own `in` entry,
// which is the more specific of the two declarations and so wins.
func TestResolveInputsReadsADirectoryListingDeclaredOnTheStepInput(t *testing.T) {
	t.Parallel()

	workflow := buildWorkflow(loadingWorkflow(listingRun("")))
	workflow.Steps[1].In[0].LoadListing = cwlcore.LoadListingShallow

	got := runObservedWorkflow(t, workflow, object("p1", makeDir(t, dirEntry)))
	assertDeepEqual(t, "listing", listingOf(t, got), []string{dirEntry})
}

// TestResolveInputsFallsBackToLoadListingRequirement covers the second step of the precedence: a
// declaration that sets nothing inherits the LoadListingRequirement in scope.
func TestResolveInputsFallsBackToLoadListingRequirement(t *testing.T) {
	t.Parallel()

	spec := loadingWorkflow(listingRun(""))
	spec.steps[1].reqs = []cwlcore.ProcessRequirement{
		&cwlcore.LoadListingRequirement{LoadListing: cwlcore.LoadListingShallow},
	}

	got := runLoading(t, spec, makeDir(t, dirEntry))
	assertDeepEqual(t, "listing", listingOf(t, got), []string{dirEntry})
}

// TestResolveInputsLeavesAListingUnreadWhenNothingAsks pins the nil-means-not-read contract: under
// no_listing, and under no declaration at all, a nil Listing stays nil rather than becoming the
// empty slice that would assert an empty directory.
func TestResolveInputsLeavesAListingUnreadWhenNothingAsks(t *testing.T) {
	t.Parallel()

	modes := []cwlcore.LoadListingEnum{"", cwlcore.LoadListingNone}
	for _, mode := range modes {
		t.Run("mode "+string(cmp.Or(mode, "unset")), func(t *testing.T) {
			t.Parallel()

			got := runLoading(t, loadingWorkflow(listingRun(mode)), makeDir(t, dirEntry))
			if names := listingOf(t, got); names != nil {
				t.Fatalf("listing = %v, want it left unread", names)
			}
		})
	}
}

// TestResolveInputsKeepsASuppliedListing covers the two Directories a read finds nothing to do with:
// one that already carries a listing, and one naming a resource with no local path.
func TestResolveInputsKeepsASuppliedListing(t *testing.T) {
	t.Parallel()

	supplied := makeDir(t, dirEntry)
	supplied.Listing = []cwlcore.FileOrDirectory{&cwlcore.File{Basename: "written by hand"}}

	remote := &cwlcore.Directory{Location: "https://example.org/d", Basename: "d"}

	cases := []struct {
		dir  *cwlcore.Directory
		name string
		want []string
	}{
		{name: "listing already supplied", dir: supplied, want: []string{"written by hand"}},
		{name: "no local path", dir: remote, want: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := runLoading(t, loadingWorkflow(listingRun(cwlcore.LoadListingShallow)), tc.dir)
			assertDeepEqual(t, "listing", listingOf(t, got), tc.want)
		})
	}
}

// TestRunOutputsCompleteADirectoryListing pins where the completion pass moved to. A Directory
// reaching the run's own output has no further consumer inside the engine and may not survive as a
// directory at all, so the listing it is published with is read here — even though nothing along the
// way asked for one.
func TestRunOutputsCompleteADirectoryListing(t *testing.T) {
	t.Parallel()

	published := makeDir(t, dirEntry)

	spec := loadingWorkflow(listingRun(""))
	runner := mustRunner(t, spec, producerRegistry(object("p1", published)), nil)

	got := mustRun(t, runner, object("x", 0)).Outputs[gotPort]
	assertDeepEqual(t, "listing", listingOf(t, got), []string{dirEntry})
}

// TestStepReadsADirectoryAsItStandsRatherThanAsItWasPublished is tests/inpdir_update_wf.cwl in
// miniature, and the reason the completion pass is not allowed to run when a tool collects an
// output. The producing step's own value is collected while its directory is empty; the entry
// appears afterwards, as InplaceUpdateRequirement lets a later step make it appear; and the
// consumer that declares shallow_listing must see the directory as it now stands.
func TestStepReadsADirectoryAsItStandsRatherThanAsItWasPublished(t *testing.T) {
	t.Parallel()

	local := t.TempDir()

	err := os.Mkdir(filepath.Join(local, outNameTree), 0o700)
	if err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	tool := outTestTool(outTestParam("#tool/d", outTypeDirectory, outGlobBinding(outNameTree)))

	var seen any

	registry := testRegistry(func(_ context.Context, call *StepCall) (Result, error) {
		if call.StepID == sinkStep {
			seen = call.Inputs[sinkPort]

			return Success(object(portA, "observed"))
		}

		published := outWantDirectory(t, outCollect(t, tool, local, 0))

		writeErr := os.WriteFile(filepath.Join(published.Path, dirEntry), nil, 0o600)
		if writeErr != nil {
			return Result{}, writeErr
		}

		return Success(object(portA, published))
	})

	spec := loadingWorkflow(listingRun(cwlcore.LoadListingShallow))
	mustRun(t, mustRunner(t, spec, registry, nil), object("x", 0))

	assertDeepEqual(t, "listing", listingOf(t, seen), []string{dirEntry})
}

// TestResolveInputsLeavesAFileAloneUnderLoadListing covers the crossed case: loadListing says
// nothing about a File, so a File on such an input is passed through with its contents unread.
func TestResolveInputsLeavesAFileAloneUnderLoadListing(t *testing.T) {
	t.Parallel()

	file := writeFile(t, "counted.txt", []byte("16\n"))

	got := runLoading(t, loadingWorkflow(listingRun(cwlcore.LoadListingDeep)), file)
	assertDeepEqual(t, "step input", got, file)

	if file.Contents.IsSet() {
		t.Fatalf("File.Contents = %q, want it left unread", file.Contents.Value())
	}
}

// TestResolveInputsRejectsAnUnreadableDirectory covers both ways the walk can fail: the directory is
// gone before it is stat-ed, and it cannot be read once it has been.
func TestResolveInputsRejectsAnUnreadableDirectory(t *testing.T) {
	t.Parallel()

	cases := []struct {
		dir  func(t *testing.T) *cwlcore.Directory
		name string
	}{
		{
			name: "no such directory",
			dir: func(t *testing.T) *cwlcore.Directory {
				t.Helper()

				gone := makeDir(t)

				err := os.Remove(gone.Path)
				if err != nil {
					t.Fatalf("Remove: %v", err)
				}

				return gone
			},
		},
		{
			name: "directory cannot be read",
			dir: func(t *testing.T) *cwlcore.Directory {
				t.Helper()

				shut := makeDir(t, dirEntry)

				err := os.Chmod(shut.Path, 0o000)
				if err != nil {
					t.Fatalf("Chmod: %v", err)
				}

				t.Cleanup(func() { reopen(t, shut.Path) })

				return shut
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			spec := loadingWorkflow(listingRun(cwlcore.LoadListingDeep))
			runner := mustRunner(t, spec, producerRegistry(object("p1", tc.dir(t))), nil)

			_, err := runner.Run(t.Context(), object("x", 0))
			assertErrorIs(t, "Run", err, ErrLoadListing)
		})
	}
}

// defaultsWorkflow is a single step running run, wired to nothing, so that only the defaults on its
// `in` entry and on the run process decide what it is called with.
func defaultsWorkflow(t *testing.T, run cwlcore.Process, stepDefault any) *cwlcore.Workflow {
	t.Helper()

	spec := wfSpec{
		steps: []stepSpec{{
			name: sinkStep,
			run:  run,
			in:   []inSpec{{name: sinkPort, def: stepDefault}},
			out:  []string{portA},
		}},
		outputs: []outSpec{{name: gotPort, sources: []string{sinkStep + "/" + portA}}},
	}

	workflow := buildWorkflow(&spec)
	workflow.ID = "file://" + filepath.Join(t.TempDir(), "wf.cwl") + "#wf"

	return workflow
}

// dirEntry is the one file the listing fixtures put in a directory.
const dirEntry = "blurb"

// whaleFile and whaleText are the file a File default names, spelled as count-lines11-wf.cwl does.
const (
	whaleFile = "whale.txt"
	whaleText = "whale"
)

// fileDefault renders a File default the way a document writes one: a class and a location relative
// to the document it is written in.
func fileDefault(name string) map[string]any {
	return map[string]any{outKeyClass: cwlcore.ClassFile, "location": name}
}

// materializeDefault plans workflow, runs it, and returns the value the step's handler was called
// with on the sink port.
func materializeDefault(t *testing.T, workflow *cwlcore.Workflow) (any, error) {
	t.Helper()

	runner, err := NewRunner(t.Context(), workflow, producerRegistry(nil), nil)
	if err != nil {
		t.Fatalf("NewRunner: unexpected error: %v", err)
	}

	result, err := runner.Run(t.Context(), nil)

	return result.Outputs[gotPort], err
}

// TestResolveInputsNormalizesAFileStepDefault is the shape count-lines11-wf.cwl takes. A `default`
// written as a mapping is not a File until its location has been resolved against the document it
// appears in and its path read from disk, and until then `$(inputs.file1.path)` has nothing to say.
func TestResolveInputsNormalizesAFileStepDefault(t *testing.T) {
	t.Parallel()

	run := newOperation(wfID(sinkStep)+"/run", []string{sinkPort}, []string{portA}, nil)
	run.Inputs[0].Type = cwlcore.NewPrimitiveType(cwlcore.PrimitiveFile)

	workflow := defaultsWorkflow(t, run, fileDefault(whaleFile))

	docDir := filepath.Dir(strings.TrimPrefix(strings.TrimSuffix(workflow.ID, "#wf"), "file://"))

	err := os.WriteFile(filepath.Join(docDir, whaleFile), []byte(whaleText), 0o600)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := materializeDefault(t, workflow)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}

	file, isFile := got.(*cwlcore.File)
	if !isFile {
		t.Fatalf("step input = %#v, want a *cwlcore.File", got)
	}

	assertDeepEqual(t, "path", file.Path, filepath.Join(docDir, whaleFile))
	assertDeepEqual(t, "checksum", file.Checksum, outChecksumOf([]byte(whaleText)))
}

// TestResolveInputsNormalizesAFileProcessDefault covers the same materialization for a `default`
// written on the run process's own parameter, which resolves against that process's document rather
// than the workflow's.
func TestResolveInputsNormalizesAFileProcessDefault(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, whaleFile), []byte(whaleText), 0o600)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	run := &cwlcore.Operation{}
	run.ID = "file://" + filepath.Join(dir, "tool.cwl") + "#tool"

	param := cwlcore.OperationInputParameter{Default: mustNode(fileDefault(whaleFile))}
	param.IDField = run.ID + "/" + sinkPort
	param.Type = cwlcore.NewPrimitiveType(cwlcore.PrimitiveFile)
	param.LoadContents = true
	run.Inputs = []cwlcore.OperationInputParameter{param}

	out := cwlcore.OperationOutputParameter{}
	out.IDField = run.ID + "/" + portA
	run.Outputs = []cwlcore.OperationOutputParameter{out}

	spec := wfSpec{
		steps:   []stepSpec{{name: sinkStep, run: run, out: []string{portA}}},
		outputs: []outSpec{{name: gotPort, sources: []string{sinkStep + "/" + portA}}},
	}

	got, err := materializeDefault(t, buildWorkflow(&spec))
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}

	assertDeepEqual(t, "contents", contentsOf(t, got), whaleText)
}

// TestResolveInputsReportsAnUnusableDefault covers both materialization failures: a step-level
// default and a process-level one that name a file which is not there.
func TestResolveInputsReportsAnUnusableDefault(t *testing.T) {
	t.Parallel()

	fileType := cwlcore.NewPrimitiveType(cwlcore.PrimitiveFile)

	t.Run("step default", func(t *testing.T) {
		t.Parallel()

		run := newOperation(wfID(sinkStep)+"/run", []string{sinkPort}, []string{portA}, nil)
		run.Inputs[0].Type = fileType

		_, err := materializeDefault(t, defaultsWorkflow(t, run, fileDefault("nowhere.txt")))
		if err == nil {
			t.Fatal("Run: want an error for a default naming a file that is not there")
		}
	})

	t.Run("process default", func(t *testing.T) {
		t.Parallel()

		run := newOperation(wfID(sinkStep)+"/run", []string{sinkPort}, []string{portA}, nil)
		run.Inputs[0].Type = fileType
		run.Inputs[0].Default = mustNode(fileDefault("nowhere.txt"))

		spec := wfSpec{
			steps:   []stepSpec{{name: sinkStep, run: run, out: []string{portA}}},
			outputs: []outSpec{{name: gotPort, sources: []string{sinkStep + "/" + portA}}},
		}

		_, err := materializeDefault(t, buildWorkflow(&spec))
		if err == nil {
			t.Fatal("Run: want an error for a default naming a file that is not there")
		}
	})
}

// TestProcessDefaultAppliesToAnExplicitNull pins the rule Process.yml states for
// InputParameter.default: it applies "if the parameter is missing from the input object, *or if the
// value of the parameter in the input object is null*".
//
// The null case is the one that only shows itself a layer in, and it is what
// dynresreq-workflow-tooldefault.cwl is built out of: a workflow input declared `File?` and left
// unsupplied reaches the step as an explicit null under a key that is very much present, and a rule
// testing presence would skip the tool's own default and hand the tool a null it declared a default
// precisely to avoid.
func TestProcessDefaultAppliesToAnExplicitNull(t *testing.T) {
	t.Parallel()

	cases := []struct {
		upstream any
		want     any
		name     string
	}{
		{name: "a wired value beats the default", upstream: "from the edge", want: "from the edge"},
		{name: "a wired null takes the default", upstream: nil, want: "from the tool"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			run := newOperation(wfID(sinkStep)+"/run", []string{sinkPort}, []string{portA}, nil)
			run.Inputs[0].Default = mustNode("from the tool")

			assertDeepEqual(t, "step input", runLoading(t, loadingWorkflow(run), tc.upstream), tc.want)
		})
	}
}
