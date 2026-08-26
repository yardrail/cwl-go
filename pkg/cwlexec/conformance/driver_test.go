package conformance

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	conf "github.com/yardrail/cwl-go/pkg/cwlcore/conformance"
	"github.com/yardrail/cwl-go/pkg/cwlexec"
)

// errPlain stands in for a run that failed for an ordinary reason.
var errPlain = errors.New("the document is invalid")

// errUnsupported stands in for a run that reported a feature the engine lacks.
var errUnsupported = errors.New("wrapped: " + cwlexec.ErrUnsupportedFeature.Error())

// unsupported wraps the engine's sentinel the way a handler does.
func unsupported() error {
	return errors.Join(cwlexec.ErrUnsupportedFeature, errUnsupported)
}

func TestExitStatusFollowsTheRunnerContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "a run that succeeded", err: nil, want: ExitSuccess},
		{name: "an ordinary failure", err: errPlain, want: ExitFailure},
		{name: "an unsupported feature", err: unsupported(), want: ExitUnsupported},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ExitStatus(tt.err)
			if got != tt.want {
				t.Errorf("ExitStatus = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCompareInvertsShouldFail(t *testing.T) {
	t.Parallel()

	const optionalTag = "docker"

	optional := []string{optionalTag}
	required := []string{requiredTag}

	tests := []struct {
		name       string
		tags       []string
		status     int
		mismatch   error
		shouldFail bool
		want       outcome
	}{
		{
			name: "a matching run", tags: required, status: ExitSuccess,
			want: outcomePass,
		},
		{
			name: "a matching run a should_fail test expected to fail",
			tags: required, status: ExitSuccess, shouldFail: true,
			want: outcomeFail,
		},
		{
			name: "a mismatching run", tags: required, status: ExitSuccess, mismatch: errMismatch,
			want: outcomeFail,
		},
		{
			name: "a failed run", tags: required, status: ExitFailure,
			want: outcomeFail,
		},
		{
			name: "a failed run a should_fail test expected to fail",
			tags: required, status: ExitFailure, shouldFail: true,
			want: outcomePass,
		},
		{
			name: "an unsupported feature on an optional test",
			tags: optional, status: ExitUnsupported,
			want: outcomeSkip,
		},
		{
			// A skip is still a failure when the test uses no optional feature.
			name: "an unsupported feature on a required test",
			tags: required, status: ExitUnsupported,
			want: outcomeFail,
		},
		{
			// The unsupported check declines for a required test and control
			// falls through to should_fail, which passes it. The order of the
			// two branches is the whole of the difference.
			name: "an unsupported feature on a required should_fail test",
			tags: required, status: ExitUnsupported, shouldFail: true,
			want: outcomePass,
		},
		{
			// An entry with no tags at all counts as required.
			name: "an unsupported feature on an untagged test",
			tags: nil, status: ExitUnsupported,
			want: outcomeFail,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			entry := &conf.Entry{Tags: tt.tags, ShouldFail: tt.shouldFail}

			got := verdict(entry, tt.status, tt.mismatch)
			if got != tt.want {
				t.Errorf("verdict = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsRequiredDefaultsToRequired(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tags []string
		want bool
	}{
		{name: "no tags at all", tags: nil, want: true},
		{name: "an empty tag list", tags: make([]string, 0), want: true},
		{name: "the required tag among others", tags: []string{"docker", requiredTag}, want: true},
		{name: "only optional tags", tags: []string{"docker", "workflow"}, want: false},
		{name: "the upstream misspelling", tags: []string{"require"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := isRequired(tt.tags)
			if got != tt.want {
				t.Errorf("isRequired(%v) = %v, want %v", tt.tags, got, tt.want)
			}
		})
	}
}

// TestDriverRunsAMiniatureCorpus drives the whole path -- manifest reader, in-process run,
// output-object rendering and comparison -- over the fixture corpus in testdata.
//
// It needs neither the pinned cwl-v1.2 checkout nor the network, so it runs on every
// "go test ./...". Its three entries are one of each verdict the driver can reach without
// depending on a feature another workstream is in the middle of implementing: a run that
// matches, a run that does not, and a document that must be rejected.
func TestDriverRunsAMiniatureCorpus(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(filepath.Join("testdata", "corpus"))
	if err != nil {
		t.Fatalf("resolving the fixture corpus: %v", err)
	}

	entries, err := conf.LoadEntries(root)
	if err != nil {
		t.Fatalf("reading the fixture manifest: %v", err)
	}

	results := runSuite(t.Context(), root, entries)

	want := map[string]outcome{
		"echo_writes_a_file":             outcomePass,
		"echo_with_the_wrong_size":       outcomeFail,
		"a_document_that_does_not_load":  outcomePass,
		"a_tool_that_needs_no_job_order": outcomePass,
	}

	if len(results) != len(want) {
		t.Fatalf("ran %d entries, want %d", len(results), len(want))
	}

	for _, got := range results {
		if got.outcome != want[got.id] {
			t.Errorf("%s = %q, want %q (%s)", got.id, got.outcome, want[got.id], got.reason)
		}
	}
}

// TestDriverSurvivesAnEntryThatCannotStart asserts the harness reports a broken entry
// rather than taking the rest of the run down with it.
func TestDriverSurvivesAnEntryThatCannotStart(t *testing.T) {
	t.Parallel()

	entries := []conf.Entry{{
		ID:   "missing_document",
		Tool: "tests/there-is-no-such-file.cwl",
		Tags: []string{requiredTag},
	}}

	results := runSuite(t.Context(), t.TempDir(), entries)

	if len(results) != 1 || results[0].outcome != outcomeFail {
		t.Fatalf("results = %+v, want one failure", results)
	}

	if results[0].reason == "" {
		t.Error("a failing result carries no reason")
	}
}

// TestSuiteCountsPartitionTheManifest guards the bookkeeping the comparison test comes to
// rely on: every entry lands in exactly one set.
func TestSuiteCountsPartitionTheManifest(t *testing.T) {
	t.Parallel()

	results := []result{
		{id: "a", outcome: outcomePass},
		{id: "b", outcome: outcomeFail},
		{id: "c", outcome: outcomeSkip},
		{id: "d", outcome: outcomePass},
	}

	sets := partition(results)

	if !slices.Equal(sets[outcomePass], []string{"a", "d"}) {
		t.Errorf("passes = %v, want [a d]", sets[outcomePass])
	}

	if !slices.Equal(sets[outcomeFail], []string{"b"}) {
		t.Errorf("failures = %v, want [b]", sets[outcomeFail])
	}

	if !slices.Equal(sets[outcomeSkip], []string{"c"}) {
		t.Errorf("skips = %v, want [c]", sets[outcomeSkip])
	}
}

// TestRunEntryReportsAnUnallocatableOutputDirectory points TMPDIR at a directory that does
// not exist, so [os.MkdirTemp] fails and runEntry reports the allocation failure rather than
// running anything.
//
// This test does not run in parallel: t.Setenv forbids it, and TMPDIR is read by every
// [os.MkdirTemp]("", ...) call in the process for as long as it is set.
func TestRunEntryReportsAnUnallocatableOutputDirectory(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "does-not-exist"))

	entry := &conf.Entry{ID: "unallocatable", Tool: "tests/echo.cwl"}

	got := runEntry(t.Context(), "root-is-never-consulted", entry)
	if got.outcome != outcomeFail {
		t.Fatalf("outcome = %q, want %q", got.outcome, outcomeFail)
	}

	if !strings.Contains(got.reason, "allocating an output directory") {
		t.Errorf("reason = %q, want it to name the allocation failure", got.reason)
	}
}

// TestWithCleanupReportsARemovalFailure makes the output directory's parent unwritable, so
// [os.RemoveAll] cannot unlink it, and asserts the removal failure is folded into the result
// rather than lost.
func TestWithCleanupReportsARemovalFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")

	err := os.Mkdir(sub, 0o700)
	if err != nil {
		t.Fatalf("creating the fixture directory: %v", err)
	}

	err = os.Chmod(dir, 0o500)
	if err != nil {
		t.Fatalf("removing write permission: %v", err)
	}

	// Restored before t.TempDir()'s own cleanup runs, so it can still remove dir:
	// t.Cleanup functions run in LIFO order, and this one is registered after
	// TempDir's.
	t.Cleanup(func() {
		err := os.Chmod(dir, 0o700)
		if err != nil {
			t.Errorf("restoring write permission: %v", err)
		}
	})

	got := withCleanup(result{id: "x", outcome: outcomePass}, sub)
	if got.reason == "" {
		t.Error("a removal failure was not recorded")
	}
}

// TestJudgeReportsATimeout asserts a run that ran out of time is a plain failure, checked
// ahead of should_fail: a run that never finished has not demonstrated the failure a
// should_fail entry expects.
func TestJudgeReportsATimeout(t *testing.T) {
	t.Parallel()

	entry := &conf.Entry{ID: "slow", ShouldFail: true, Tags: []string{requiredTag}}

	got := judge(entry, produced{}, context.DeadlineExceeded)
	if got.outcome != outcomeFail {
		t.Errorf("outcome = %q, want %q", got.outcome, outcomeFail)
	}

	if got.reason != "the run timed out" {
		t.Errorf("reason = %q, want %q", got.reason, "the run timed out")
	}
}

// TestCompareOutputsReportsUnrenderableSides exercises normalize's failure on each side of
// compareOutputs in turn: cwlcore.EncodeJSON writes a NaN as a bare, unparseable token,
// which normalize's [json.Decoder] then rejects.
func TestCompareOutputsReportsUnrenderableSides(t *testing.T) {
	t.Parallel()

	t.Run("the expected side", func(t *testing.T) {
		t.Parallel()

		err := compareOutputs(map[string]any{outputName: math.NaN()}, nil)
		if err == nil {
			t.Error("an unrenderable expected object was accepted")
		}
	})

	t.Run("the actual side", func(t *testing.T) {
		t.Parallel()

		err := compareOutputs(nil, map[string]any{outputName: math.NaN()})
		if err == nil {
			t.Error("an unrenderable produced object was accepted")
		}
	})
}

// TestRunProtectedRecoversFromAPanic supplies a run step that panics, standing in for a bug
// elsewhere in the engine that a subprocess boundary would otherwise have turned into a
// non-zero exit status.
func TestRunProtectedRecoversFromAPanic(t *testing.T) {
	t.Parallel()

	panics := func(context.Context, *invocation) (map[string]any, error) {
		panic("boom")
	}

	got := runProtectedWith(t.Context(), &invocation{}, panics)
	if !errors.Is(got.err, errRun) {
		t.Errorf("runProtectedWith.err = %v, want it to wrap errRun", got.err)
	}
}
