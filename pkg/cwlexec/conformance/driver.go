package conformance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	conf "github.com/yardrail/cwl-go/pkg/cwlcore/conformance"
	"github.com/yardrail/cwl-go/pkg/cwlexec"
)

// The cwl-runner contract's exit statuses, as cwltest reads them.
//
// They are named here, in the engine's half of the tree, so that there is one definition
// of the mapping rather than one per harness: cmd/cwl-run exits with these and this driver
// decides verdicts from them. cmd sits above pkg/cwlexec in the layering, so the agreement
// cannot be enforced by the command importing this -- it is asserted from a test on the
// command's side instead.
const (
	// ExitSuccess reports a run that produced an output object.
	ExitSuccess = 0

	// ExitFailure reports a run that did not: an invalid document, a job order that
	// does not fit it, or a step that failed. A should_fail test passes on this.
	ExitFailure = 1

	// ExitUnsupported is the contract's status for a document that "could not be run
	// because a feature is unsupported". cwltest counts it as a skip rather than a
	// failure -- unless the test is tagged required, where a skip is still a failure.
	ExitUnsupported = 33
)

// ExitStatus is the status a cwl-runner exits with having failed for err.
//
// It covers the statuses a *run* can reach. cmd/cwl-run has one more, for a command line
// it could not parse, which no document can provoke and which this driver has no way to
// produce: it is handed documents, not arguments.
func ExitStatus(err error) int {
	switch {
	case err == nil:
		return ExitSuccess
	case errors.Is(err, cwlexec.ErrUnsupportedFeature):
		return ExitUnsupported
	default:
		return ExitFailure
	}
}

// outcome is what cwltest makes of one test. The three spellings are cwltest's badge
// sections, so a result here and a line of its report name the same thing.
type outcome string

const (
	outcomePass outcome = "pass"
	outcomeFail outcome = "fail"
	outcomeSkip outcome = "skip"
)

// requiredTag marks a test that uses no optional feature, and so may never be skipped.
const requiredTag = "required"

// testTimeout bounds one test. It is far below cwltest's own ten-minute default because a
// conformance test that takes two minutes on this engine is hung, not slow.
const testTimeout = 2 * time.Minute

// defaultJobs is how many tests run at once, matching what scripts/conformance-run gives
// cwltest so that the two harnesses put the same amount of load on the machine.
const defaultJobs = 4

// outDirPrefix names the per-test output directories, so that one left behind by a crash
// is recognisable.
const outDirPrefix = "cwl-go-conformance-"

// result is one entry's outcome and, when it is not a pass, why.
type result struct {
	// id is the conformance test id, which is what a report and a badge listing name.
	id string
	// reason describes a failure, and is empty for a pass or a skip.
	reason string
	// outcome is the verdict.
	outcome outcome
}

// produced is one finished run: its output object, or the error that stood in for one.
type produced struct {
	outputs map[string]any
	err     error
}

// runSuite runs every entry and returns one result each, in manifest order.
func runSuite(ctx context.Context, root string, entries []conf.Entry) []result {
	results := make([]result, len(entries))
	work := make(chan int)

	var wg sync.WaitGroup

	for range min(defaultJobs, max(len(entries), 1)) {
		wg.Go(func() {
			for i := range work {
				results[i] = runEntry(ctx, root, &entries[i])
			}
		})
	}

	for i := range entries {
		work <- i
	}

	close(work)
	wg.Wait()

	return results
}

// outcomeKinds is how many verdicts there are, used only to size the partition.
const outcomeKinds = 3

// partition groups results by outcome, each set sorted by test id.
//
// The sets, not the counts, are what one harness's answer is compared against another's:
// two offsetting mistakes -- a test that wrongly passes and one that wrongly fails --
// cancel exactly in a count and are visible only in a set.
func partition(results []result) map[outcome][]string {
	sets := make(map[outcome][]string, outcomeKinds)

	for _, judged := range results {
		sets[judged.outcome] = append(sets[judged.outcome], judged.id)
	}

	for _, ids := range sets {
		slices.Sort(ids)
	}

	return sets
}

// runEntry runs one entry and judges it.
//
// The output directory is removed only once the verdict is in, because the comparison
// reads the files back off it -- re-measuring a checksum from a directory already deleted
// would fail every File output for the wrong reason.
func runEntry(ctx context.Context, root string, entry *conf.Entry) result {
	outDir, err := os.MkdirTemp("", outDirPrefix)
	if err != nil {
		return result{id: entry.ID, outcome: outcomeFail, reason: "allocating an output directory: " + err.Error()}
	}

	run := &invocation{
		process: filepath.Join(root, filepath.FromSlash(entry.Tool)),
		job:     jobPath(root, entry.Job),
		outDir:  outDir,
		baseDir: root,
	}

	bounded, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	judged := judge(entry, runProtected(bounded, run), bounded.Err())

	return withCleanup(judged, outDir)
}

// jobPath resolves an entry's job order, answering "" for an entry that names none.
func jobPath(root, job string) string {
	if job == "" {
		return ""
	}

	return filepath.Join(root, filepath.FromSlash(job))
}

// withCleanup removes the run's output directory, folding a removal failure into the
// result rather than losing it.
func withCleanup(judged result, outDir string) result {
	err := os.RemoveAll(outDir)
	if err != nil && judged.reason == "" {
		judged.reason = "removing the output directory: " + err.Error()
	}

	return judged
}

// judge turns a finished run into a verdict, comparing the output object when the run
// produced one.
//
// timeout is the bounding context's error, and it is consulted separately because cwltest
// treats a test that ran out of time as a plain failure even when the test is should_fail:
// a run that never finished has not demonstrated the failure the entry expects.
func judge(entry *conf.Entry, run produced, timeout error) result {
	if errors.Is(timeout, context.DeadlineExceeded) {
		return result{id: entry.ID, outcome: outcomeFail, reason: "the run timed out"}
	}

	status := ExitStatus(run.err)

	var mismatch error
	if status == ExitSuccess {
		mismatch = compareOutputs(entry.Output, run.outputs)
	}

	return result{id: entry.ID, outcome: verdict(entry, status, mismatch), reason: reasonFor(run.err, mismatch)}
}

// verdict applies cwltest's rules to a finished run.
//
// The order of the branches is cwltest's and is easy to get subtly wrong. An unsupported
// feature is a skip only for a test that is not tagged required; on a required one the
// check declines and control *falls through to should_fail*, so a required should_fail
// test that reports an unsupported feature passes. Every other non-zero status passes a
// should_fail test and fails everything else, and a successful run fails a should_fail
// test and is otherwise judged on its output object.
func verdict(entry *conf.Entry, status int, mismatch error) outcome {
	if status == ExitUnsupported && !isRequired(entry.Tags) {
		return outcomeSkip
	}

	if status != ExitSuccess {
		if entry.ShouldFail {
			return outcomePass
		}

		return outcomeFail
	}

	if entry.ShouldFail || mismatch != nil {
		return outcomeFail
	}

	return outcomePass
}

// isRequired reports whether a test may never be skipped. An entry carrying no tags at all
// counts as required: cwltest reads the field with a default of ["required"].
func isRequired(tags []string) bool {
	if len(tags) == 0 {
		return true
	}

	return slices.Contains(tags, requiredTag)
}

// reasonFor renders the sentence a failing result carries.
func reasonFor(runErr, mismatch error) string {
	switch {
	case mismatch != nil:
		return mismatch.Error()
	case runErr != nil:
		return runErr.Error()
	default:
		return ""
	}
}

// compareOutputs compares a finished run's outputs against what the entry expects, with
// both sides put through the JSON round trip cwltest's own comparison sees.
func compareOutputs(expected any, outputs map[string]any) error {
	want, err := normalize(expected)
	if err != nil {
		return fmt.Errorf("%w: the expected output object is not renderable as JSON: %w", errMismatch, err)
	}

	got, err := normalize(outputObject(outputs))
	if err != nil {
		return fmt.Errorf("%w: the produced output object is not renderable as JSON: %w", errMismatch, err)
	}

	return compare(want, got)
}

// runProtected runs one entry, converting a panic into the ordinary failure it would have
// been on the other side of a process boundary.
//
// cwltest runs the engine as a subprocess, where a crash is just a non-zero exit status.
// In process a panic would take the harness down with it and lose every other result, so
// it is caught here and reported as a failed run.
func runProtected(ctx context.Context, run *invocation) produced {
	return runProtectedWith(ctx, run, produce)
}

// runProtectedWith is [runProtected], taking the run step as a parameter so a test can
// supply one that panics -- no real document reaches the recover below, since every failure
// [produce] itself can hit is already reported as an ordinary error.
func runProtectedWith(
	ctx context.Context,
	run *invocation,
	produce func(context.Context, *invocation) (map[string]any, error),
) produced {
	var out produced

	func() {
		defer func() {
			recovered := recover()
			if recovered != nil {
				out = produced{outputs: nil, err: fmt.Errorf("%w: the engine panicked: %v", errRun, recovered)}
			}
		}()

		out.outputs, out.err = produce(ctx, run)
	}()

	return out
}
