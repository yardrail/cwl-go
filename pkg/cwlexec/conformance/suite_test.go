package conformance

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	conf "github.com/yardrail/cwl-go/pkg/cwlcore/conformance"
)

// The cwltest invocation the comparison runs, mirroring scripts/conformance-run so that
// the two harnesses are given the same corpus, the same parallelism and the same patience.
const (
	harnessName    = "cwltest"
	harnessJobs    = "4"
	harnessSeconds = "120"
	badgeDirName   = "badges"
	badgeFile      = "all.md"
	runnerPackage  = "github.com/yardrail/cwl-go/cmd/cwl-run"
	runnerName     = "cwl-run"
)

// Section headings and entry shape of a cwltest badge listing, which is the only output it
// produces that names the individual tests rather than a rounded percentage.
var (
	sectionPattern = regexp.MustCompile(`(?m)^## List of (passed|failed|unsupported) tests\s*$`)
	entryPattern   = regexp.MustCompile(`(?m)^- \[([^\]]+)\]`)
)

// sectionOutcome maps a badge listing's section heading onto a verdict.
var sectionOutcome = map[string]outcome{
	"passed":      outcomePass,
	"failed":      outcomeFail,
	"unsupported": outcomeSkip,
}

// suiteOnce memoizes the in-process run, so that two tests in one "go test" invocation
// share a single pass over the corpus instead of running the whole thing twice.
var suiteOnce struct {
	sync.Mutex

	root    string
	entries []conf.Entry
	results []result
	err     error
	done    bool
}

// requireEnabled skips unless the conformance suite was asked for.
func requireEnabled(t *testing.T) {
	t.Helper()

	if os.Getenv(envEnable) != "1" {
		t.Skipf("set %s=1 to run the conformance suite", envEnable)
	}
}

// sharedSuite runs the whole suite in process, at most once per test binary.
func sharedSuite(t *testing.T) ([]conf.Entry, []result) {
	t.Helper()

	suiteOnce.Lock()
	defer suiteOnce.Unlock()

	if !suiteOnce.done {
		suiteOnce.entries, suiteOnce.results, suiteOnce.err = loadAndRun(context.Background())
		suiteOnce.done = true
	}

	if suiteOnce.err != nil {
		if errors.Is(suiteOnce.err, errNoCorpus) {
			t.Skipf("%v", suiteOnce.err)
		}

		t.Fatalf("running the suite in process: %v", suiteOnce.err)
	}

	return suiteOnce.entries, suiteOnce.results
}

// loadAndRun locates the corpus, reads the manifest and runs every entry.
func loadAndRun(ctx context.Context) ([]conf.Entry, []result, error) {
	root, err := findCorpus()
	if err != nil {
		return nil, nil, err
	}

	suiteOnce.root = root

	entries, err := conf.LoadEntries(root)
	if err != nil {
		return nil, nil, err
	}

	if len(entries) == 0 {
		return nil, nil, fmt.Errorf("%w: %s holds no test entries", errNoCorpus, root)
	}

	return entries, runSuite(ctx, root, entries), nil
}

// TestInProcessSuite runs the whole conformance suite through the engine, in process, and
// reports the result. It is the development loop: no Python, no virtualenv, no subprocess
// per test.
//
// It reports rather than gates. The authoritative numbers are cwltest's, ratcheted by
// scripts/conformance-run, and a second gate over the same corpus would only ever
// duplicate or contradict that one.
func TestInProcessSuite(t *testing.T) {
	t.Parallel()

	requireEnabled(t)

	entries, results := sharedSuite(t)
	sets := partition(results)

	t.Logf("in-process conformance: %d/%d passed, %d failed, %d skipped, over %d entries",
		len(sets[outcomePass]), len(entries), len(sets[outcomeFail]), len(sets[outcomeSkip]), len(results))

	t.Logf("required: %s", requiredBreakdown(entries, results))

	for _, judged := range results {
		if judged.outcome == outcomeFail {
			t.Logf("FAIL %s: %s", judged.id, firstLine(judged.reason))
		}
	}
}

// requiredBreakdown counts the tests cwltest may never skip.
func requiredBreakdown(entries []conf.Entry, results []result) string {
	passed, total := 0, 0

	for i := range results {
		if !isRequired(entries[i].Tags) {
			continue
		}

		total++

		if results[i].outcome == outcomePass {
			passed++
		}
	}

	return strconv.Itoa(passed) + "/" + strconv.Itoa(total)
}

// firstLine trims a reason to its opening line, which is the part that names the
// difference; the rest is the rendered error tree.
func firstLine(reason string) string {
	line, _, _ := strings.Cut(reason, "\n")

	return line
}

// TestInProcessMatchesCwltest runs both harnesses over the same corpus and asserts their
// pass, fail and skip *sets* are equal.
//
// Sets rather than counts, because two offsetting mistakes -- one test wrongly passing and
// another wrongly failing -- cancel exactly in a count and leave the totals agreeing while
// the harness is wrong about both.
//
// cwltest is authoritative. It is the harness the project self-certifies against and the
// one CI gates on, so a disagreement is a bug in this driver, not a finding about cwltest.
func TestInProcessMatchesCwltest(t *testing.T) {
	t.Parallel()

	requireEnabled(t)

	harness := requireHarness(t)
	entries, results := sharedSuite(t)

	external := runCWLTest(t, harness, suiteOnce.root)
	internal := outcomeByID(results)

	if len(external) != len(entries) {
		t.Errorf("cwltest reported %d tests, the manifest reader found %d", len(external), len(entries))
	}

	differences := diffOutcomes(internal, external)
	for _, line := range differences {
		t.Error(line)
	}

	if len(differences) > 0 {
		t.Logf("cwltest is authoritative: %d test(s) above are bugs in the in-process driver, "+
			"not in cwltest", len(differences))
	}
}

// requireHarness locates cwltest, skipping when it is not installed.
func requireHarness(t *testing.T) string {
	t.Helper()

	explicit := strings.TrimSpace(os.Getenv(envHarness))
	if explicit != "" {
		return explicit
	}

	harness, err := exec.LookPath(harnessName)
	if err != nil {
		t.Skipf("%s is not on PATH (pip install cwltest), so there is nothing to compare against", harnessName)
	}

	return harness
}

// runCWLTest builds cwl-run, drives cwltest over the corpus with it, and reads the verdict
// it recorded for every test.
func runCWLTest(t *testing.T, harness, root string) map[string]outcome {
	t.Helper()

	badges := filepath.Join(t.TempDir(), badgeDirName)

	// cwltest creates the badge directory itself and refuses to start if it is
	// already there, so it is named but not made.
	args := []string{
		"--test", manifestName,
		"--tool", buildRunner(t),
		"--timeout", harnessSeconds,
		"-j", harnessJobs,
		"--badgedir", badges,
	}

	cmd := exec.CommandContext(t.Context(), harness, args...)
	cmd.Dir = root
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr

	err := cmd.Run()

	// A non-zero status means tests failed, which is the subject rather than an
	// error; only a harness that could not start at all is a problem here.
	exit := new(exec.ExitError)
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("running %s: %v", harness, err)
	}

	return readBadges(t, filepath.Join(badges, badgeFile))
}

// buildRunner builds the cwl-runner binary cwltest drives, and returns its path.
func buildRunner(t *testing.T) string {
	t.Helper()

	binary := filepath.Join(t.TempDir(), runnerName)

	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, runnerPackage)

	out, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("building %s: %v\n%s", runnerPackage, err, out)
	}

	return binary
}

// readBadges reads cwltest's "all" listing, which is the only artefact it writes that
// names each test rather than summarising them.
func readBadges(t *testing.T, listing string) map[string]outcome {
	t.Helper()

	raw, err := os.ReadFile(filepath.Clean(listing))
	if err != nil {
		t.Fatalf("reading %s: %v", listing, err)
	}

	body := string(raw)
	sections := sectionPattern.FindAllStringSubmatchIndex(body, -1)
	found := make(map[string]outcome, len(body)/64)

	for i, section := range sections {
		end := len(body)
		if i+1 < len(sections) {
			end = sections[i+1][0]
		}

		verdict := sectionOutcome[body[section[2]:section[3]]]
		for _, entry := range entryPattern.FindAllStringSubmatch(body[section[1]:end], -1) {
			found[entry[1]] = verdict
		}
	}

	if len(found) == 0 {
		t.Fatalf("%s names no tests; cwltest never got as far as running one", listing)
	}

	return found
}

// outcomeByID indexes the in-process results by test id.
func outcomeByID(results []result) map[string]outcome {
	found := make(map[string]outcome, len(results))
	for _, judged := range results {
		found[judged.id] = judged.outcome
	}

	return found
}

// diffOutcomes renders one line per test the two harnesses judged differently, in id
// order.
func diffOutcomes(internal, external map[string]outcome) []string {
	ids := slices.Sorted(maps.Keys(external))
	for id := range internal {
		_, known := external[id]
		if !known {
			ids = append(ids, id)
		}
	}

	slices.Sort(ids)

	differences := make([]string, 0, len(ids))

	for _, id := range ids {
		mine, ran := internal[id]
		theirs := external[id]

		if !ran {
			differences = append(differences,
				fmt.Sprintf("%s: cwltest says %q, the in-process driver never ran it", id, theirs))

			continue
		}

		if theirs == "" {
			differences = append(differences,
				fmt.Sprintf("%s: the in-process driver says %q, cwltest never ran it", id, mine))

			continue
		}

		if mine != theirs {
			differences = append(differences,
				fmt.Sprintf("%s: cwltest says %q, the in-process driver says %q", id, theirs, mine))
		}
	}

	return differences
}
