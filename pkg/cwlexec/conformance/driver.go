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

// cwl-runner exit statuses.
const (
	ExitSuccess     = 0
	ExitFailure     = 1
	ExitUnsupported = 33
)

// ExitStatus maps an error to a cwl-runner exit status.
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

// outcome is a test verdict: pass, fail, or skip.
type outcome string

const (
	outcomePass outcome = "pass"
	outcomeFail outcome = "fail"
	outcomeSkip outcome = "skip"
)

// requiredTag marks a test that uses no optional feature, and so may never be skipped.
const requiredTag = "required"

// testTimeout bounds one test.
const testTimeout = 2 * time.Minute

// defaultJobs is the default test parallelism.
const defaultJobs = 4

// outDirPrefix names per-test output directories.
const outDirPrefix = "cwl-go-conformance-"

// result is one entry's outcome and, when it is not a pass, why.
type result struct {
	id      string
	reason  string
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

// partition groups results by outcome, sorted by test id.
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

// withCleanup removes the output directory.
func withCleanup(judged result, outDir string) result {
	err := os.RemoveAll(outDir)
	if err != nil && judged.reason == "" {
		judged.reason = "removing the output directory: " + err.Error()
	}

	return judged
}

// judge turns a finished run into a verdict.
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

// verdict applies cwltest's verdict rules to a status and output comparison.
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

// isRequired reports whether a test may never be skipped.
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

// compareOutputs compares outputs against the expected values after JSON normalization.
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

// runProtected runs one entry, recovering panics as failures.
func runProtected(ctx context.Context, run *invocation) produced {
	return runProtectedWith(ctx, run, produce)
}

// runProtectedWith is [runProtected] with a pluggable run function for testing.
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
