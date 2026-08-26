package cwlexec

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// The two namespaces a container mapper spans, written out so that a row reads as the pairing it is
// asserting rather than as string arithmetic.
const (
	pmcToolWork  = "/tool/work"
	pmcToolStage = "/tool/stage"
)

// pmcMap returns a container mapper over the two notional host directories and the two the tool
// sees.
func pmcMap() *PathMap {
	return NewContainerPathMap(pmWork, pmStage, pmcToolWork, pmcToolStage)
}

func TestContainerPathMapPlansToolTargetsAndHostPaths(t *testing.T) {
	t.Parallel()

	mapper := pmcMap()

	err := mapper.Stage(pmHostFile(pmHostPath), pmName, false)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	_, err = mapper.StageContents("note.txt", execGreeting)
	if err != nil {
		t.Fatalf("StageContents: %v", err)
	}

	// Resolved stays a host path, Target becomes what the tool sees, and Host is where Apply
	// writes for the tool to see it there.
	pmWantExactPlan(t, mapper, []PathMapping{
		{
			Resolved: pmHostPath,
			Target:   pmcToolWork + "/" + pmName,
			Host:     pmWork + "/" + pmName,
			Action:   StageLink,
		},
		{
			Target:   pmcToolWork + "/note.txt",
			Host:     pmWork + "/note.txt",
			Contents: execGreeting,
			Action:   StageWrite,
		},
	})

	// RewriteInputs is what carries the tool's view into every later stage, and it is the tool's
	// view it carries, not this host's.
	rewritten := mapper.RewriteInputs(map[string]any{execInPort: pmHostFile(pmHostPath)})
	pmWantPath(t, rewritten[execInPort], pmcToolWork+"/"+pmName)
}

func TestContainerPathMapStagesAValueAlreadyInPlace(t *testing.T) {
	t.Parallel()

	// On this host a value lying where the document wants it is left alone, which keeps the
	// common case free of links. Inside a container "where it lies" is a path the tool does not
	// have, so the exemption cannot apply: conformance test cwloutput_nolimit runs a script that
	// arrives as an input default and is never otherwise staged.
	value := pmHostFile(pmHostPath)
	host := pmMap()

	err := host.Materialize(value)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	if plan := host.Plan(); len(plan) != 0 {
		t.Errorf("plan = %+v, want nothing planned for a value already in place", plan)
	}

	contained := pmcMap()

	err = contained.Materialize(value)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	pmWantExactPlan(t, contained, []PathMapping{{
		Resolved: pmHostPath,
		Target:   pmcToolStage + "/x.txt",
		Host:     pmStage + "/x.txt",
		Action:   StageLink,
	}})
}

func TestAbsoluteEntrynameNeedsADockerRequirement(t *testing.T) {
	t.Parallel()

	cases := []struct {
		mapper *PathMap
		want   error
		name   string
	}{{
		name:   "no container at all",
		mapper: pmMap(),
		want:   ErrStagePath,
	}, {
		name:   "a container the entry may not reach outside of",
		mapper: pmcMap(),
		want:   ErrStagePath,
	}, {
		name:   "a container and the requirement that unlocks it",
		mapper: pmcAllowing(),
		want:   nil,
	}}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := testCase.mapper.Stage(pmHostFile(pmHostPath), pmEscape, false)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("Stage: error %v does not wrap %v", err, testCase.want)
			}

			if testCase.want != nil {
				return
			}

			if target := testCase.mapper.Plan()[0].Target; target != pmEscape {
				t.Errorf("target = %q, want the absolute entryname as written", target)
			}
		})
	}
}

// pmcAllowing returns a container mapper that a DockerRequirement in requirements has unlocked
// absolute targets on.
func pmcAllowing() *PathMap {
	mapper := pmcMap()
	mapper.AllowAbsoluteTargets()

	return mapper
}

func TestContainerPathMapLeavesAnOutsideLinkToTheExecutor(t *testing.T) {
	t.Parallel()

	// The bytes are already on this host at Resolved and the tool will see them at Target
	// through a bind mount the executor adds. There is no third path, so there is nothing here
	// for Apply to do — writing a symbolic link under the staging directory would place it where
	// nothing looks.
	mapper := pmcAllowing()

	err := mapper.Stage(pmHostFile(pmHostPath), pmEscape, false)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	pmWantExactPlan(t, mapper, []PathMapping{
		{Resolved: pmHostPath, Target: pmEscape, Action: StageLink},
	})

	// A literal at the same target does need one: a mount has to have a source.
	written := pmcAllowing()

	_, err = written.StageContents(pmEscape, execGreeting)
	if err != nil {
		t.Fatalf("StageContents: %v", err)
	}

	pmWantExactPlan(t, written, []PathMapping{{
		Target:   pmEscape,
		Host:     filepath.Join(pmStage, outsideName, "etc", "escape"),
		Contents: execGreeting,
		Action:   StageWrite,
	}})
}

func TestContainerPathMapWritesOnTheHost(t *testing.T) {
	t.Parallel()

	// The load-bearing property: the plan reads as container paths, and carrying it out touches
	// only this machine.
	base := t.TempDir()
	work := filepath.Join(base, "out")
	stage := filepath.Join(base, "stg")
	source := outWriteFile(t, base, execSourceName, execGreeting)

	mapper := NewContainerPathMap(work, stage, pmcToolWork, pmcToolStage)

	err := mapper.Stage(pmHostFile(source), pmName, false)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	err = mapper.Materialize(execLiteralFile("lit.txt", execGreeting))
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	err = mapper.Apply()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Nothing was created at the paths the tool sees...
	_, err = os.Lstat(filepath.Join(pmcToolWork, pmName))
	if err == nil {
		t.Errorf("Apply wrote to %s, which is a path only the tool has", pmcToolWork)
	}

	// ...the literal was materialized in full, because nothing else will place it...
	if got := execRead(t, filepath.Join(stage, "lit.txt")); got != execGreeting {
		t.Errorf("the literal holds %q, want %q", got, execGreeting)
	}

	// ...and the staged input is an empty mount point rather than the link a host invocation
	// would have placed. A link here would be followed by the engine rather than mounted over;
	// see [PathMap.placeLink].
	info, err := os.Lstat(filepath.Join(work, pmName))
	if err != nil {
		t.Fatalf("lstat the mount point: %v", err)
	}

	if info.Mode()&os.ModeSymlink != 0 || info.Size() != 0 {
		t.Errorf("%s is %v of %d bytes, want an empty mount point", pmName, info.Mode(), info.Size())
	}
}

// TestContainerPathMapRelinksAfterTheMountIsGone pins the other end of that deferral: once the tool
// has exited the mount no longer stands in for the link, so the link is placed after all and output
// collection reads the source's bytes through it rather than the mount point's none.
func TestContainerPathMapRelinksAfterTheMountIsGone(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	work := filepath.Join(base, "out")
	source := outWriteFile(t, base, execSourceName, execGreeting)

	mapper := NewContainerPathMap(work, filepath.Join(base, "stg"), pmcToolWork, pmcToolStage)

	err := mapper.Stage(pmHostFile(source), pmName, false)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	err = mapper.Apply()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	err = mapper.Relink()
	if err != nil {
		t.Fatalf("Relink: %v", err)
	}

	staged := filepath.Join(work, pmName)
	if got := execRead(t, staged); got != execGreeting {
		t.Errorf("after Relink %s holds %q, want the source's contents", pmName, got)
	}

	dest, err := os.Readlink(staged)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}

	if dest != source {
		t.Errorf("after Relink %s links to %q, want %q", pmName, dest, source)
	}
}

// TestMountPointMatchesTheKindOfTheSource pins the constraint a mount point exists under: a
// directory can only be mounted onto a directory and a file only onto a file, so what is created to
// be mounted over has to be the same kind as the bytes that will land there.
func TestMountPointMatchesTheKindOfTheSource(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	tree := filepath.Join(base, "tree")

	err := os.Mkdir(tree, stageDirPerm)
	if err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	// want is the type bits the mount point must carry: none for a regular file, ModeDir for a
	// directory. A bind mount insists on the two matching.
	cases := []struct {
		name   string
		source string
		want   os.FileMode
	}{
		{name: "a file", source: outWriteFile(t, base, execSourceName, execGreeting), want: 0},
		{name: "a directory", source: tree, want: os.ModeDir},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			pmWantMountPoint(t, testCase.source, testCase.want)
		})
	}
}

// pmWantMountPoint creates a mount point for source and asserts it is of the kind that can be
// mounted over: a directory for a directory, and an empty regular file for a file.
func pmWantMountPoint(t *testing.T, source string, want os.FileMode) {
	t.Helper()

	onto := filepath.Join(t.TempDir(), "onto")

	err := mountPoint(source, onto)
	if err != nil {
		t.Fatalf("mountPoint: %v", err)
	}

	info, err := os.Lstat(onto)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}

	if info.Mode()&os.ModeType != want {
		t.Fatalf("mount point is %v, want type bits %v", info.Mode(), want)
	}

	// A directory's size is its own bookkeeping, so emptiness is only a claim about a file.
	if want == 0 && info.Size() != 0 {
		t.Errorf("mount point is %d bytes, want an empty one", info.Size())
	}
}

// TestMountPointRefusesWhatItCannotCreate pins the two ways there is no mount point to make.
func TestMountPointRefusesWhatItCannotCreate(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	source := outWriteFile(t, base, execSourceName, execGreeting)
	onto := filepath.Join(base, "onto")

	err := mountPoint(source, onto)
	if err != nil {
		t.Fatalf("mountPoint: %v", err)
	}

	// A source that is not there has no kind to match, so there is nothing to create.
	err = mountPoint(filepath.Join(base, "absent"), filepath.Join(base, "onto-absent"))
	if err == nil {
		t.Error("mountPoint from a missing source succeeded, want an error")
	}

	// And a mount point is never created over something already in the way: Apply clears the
	// path first, so anything still there is a collision rather than a leftover.
	err = mountPoint(source, onto)
	if err == nil {
		t.Error("mountPoint over an existing path succeeded, want an error")
	}
}

// TestPathMapRelinkReportsWhereItFailed pins the diagnostic. Relink runs after the tool has already
// succeeded, so the path it could not restore is the only clue to what output collection is about
// to miss.
func TestPathMapRelinkReportsWhereItFailed(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	work := filepath.Join(base, "out")
	source := outWriteFile(t, base, execSourceName, execGreeting)

	mapper := NewContainerPathMap(work, filepath.Join(base, "stg"), pmcToolWork, pmcToolStage)

	err := mapper.Stage(pmHostFile(source), pmName, false)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	err = mapper.Apply()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	err = os.RemoveAll(work)
	if err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	err = mapper.Relink()
	if err == nil || !strings.Contains(err.Error(), pmName) {
		t.Errorf("Relink with its directory gone = %v, want an error naming %q", err, pmName)
	}
}

// TestPathMapRelinkIsAHostNoop pins the other half: without a container [PathMap.Apply] placed the
// link itself, so there is nothing deferred and nothing to restore.
func TestPathMapRelinkIsAHostNoop(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	work := filepath.Join(base, "out")
	source := outWriteFile(t, base, execSourceName, execGreeting)

	mapper := NewPathMap(work, filepath.Join(base, "stg"))

	err := mapper.Stage(pmHostFile(source), pmName, false)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	err = mapper.Apply()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	err = mapper.Relink()
	if err != nil {
		t.Fatalf("Relink: %v", err)
	}

	if got := execRead(t, filepath.Join(work, pmName)); got != execGreeting {
		t.Errorf("%s holds %q, want the source's contents", pmName, got)
	}
}

func TestContainerPathMapResolvesAToolPathBackToTheHost(t *testing.T) {
	t.Parallel()

	// What a redirection needs: the document writes `stdin: $(inputs.x.path)`, which is a path
	// the tool has, and this process is the one that has to open the file.
	mapper := pmcMap()

	cases := map[string]string{
		pmcToolWork:                pmWork,
		pmcToolWork + "/" + pmName: pmWork + "/" + pmName,
		pmcToolStage + "/lit.txt":  pmStage + "/lit.txt",
		outEscapePath:              filepath.Join(pmStage, outsideName, "etc", "passwd"),
	}

	for target, want := range cases {
		if got := mapper.hostPath(target); got != want {
			t.Errorf("hostPath(%q) = %q, want %q", target, got, want)
		}
	}

	// Without a container the tool sees this host's filesystem, so there is nothing to map.
	if got := pmMap().hostPath("/etc/passwd"); got != "/etc/passwd" {
		t.Errorf("hostPath = %q, want the path unchanged on a host mapper", got)
	}
}

func TestContainerPathMapStagesADirectoryListing(t *testing.T) {
	t.Parallel()

	// A directory literal's entries are planned underneath it, and each of those targets is a
	// container path whose host counterpart follows from the one directory both are under.
	literal := &cwlcore.Directory{
		Basename: stgTreeName,
		Listing:  []cwlcore.FileOrDirectory{execLiteralFile("inner.txt", execGreeting)},
	}

	mapper := pmcMap()

	err := mapper.Stage(literal, stgTreeName, true)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	pmWantExactPlan(t, mapper, []PathMapping{
		{
			Target:   pmcToolWork + "/tree",
			Host:     pmWork + "/tree",
			Action:   StageMkdir,
			Writable: true,
		},
		{
			Target:   pmcToolWork + "/tree/inner.txt",
			Host:     pmWork + "/tree/inner.txt",
			Contents: execGreeting,
			Action:   StageWrite,
			Writable: true,
		},
	})
}

func TestContainerPathMapHostViewUndoesTheRewrite(t *testing.T) {
	t.Parallel()

	// Output collection is the one stage that goes back to a real filesystem, so it is the one
	// that has to be handed this host's paths rather than the tool's. Conformance test
	// initial_workdir_output is what needs it: it globs a staged input, and the containment test
	// that admits a symbolic link leading out of the output directory compares against the input
	// paths with their own links resolved — which a container path cannot be.
	mapper := pmcAllowing()

	staged := pmHostFile(pmHostPath)
	outside := pmHostFile("/data/y.txt")

	err := mapper.Stage(staged, pmName, false)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	err = mapper.Stage(outside, pmEscape, false)
	if err != nil {
		t.Fatalf("Stage the outside entry: %v", err)
	}

	// A literal has no bytes anywhere but the placement, which is the other half of the answer.
	literal, err := mapper.StageContents("lit.txt", execGreeting)
	if err != nil {
		t.Fatalf("StageContents: %v", err)
	}

	inputs := mapper.RewriteInputs(map[string]any{
		"staged": staged, "outside": outside, literalName: pmHostFile(literal),
	})

	// What the tool sees.
	pmWantPath(t, inputs["staged"], pmcToolWork+"/"+pmName)
	pmWantPath(t, inputs["outside"], pmEscape)
	pmWantPath(t, inputs[literalName], pmcToolWork+"/lit.txt")

	back := mapper.hostView().RewriteInputs(inputs)

	// Both come back as the bytes rather than as the placement routing the tool to them, and for
	// the same reason: a placement lives in this invocation's scratch directory and does not
	// outlive the step, while an output may name an input outright. The second entry never had a
	// placement at all — nothing outside every mount does.
	pmWantPath(t, back["staged"], pmHostPath)
	pmWantPath(t, back["outside"], "/data/y.txt")
	pmWantPath(t, back[literalName], pmWork+"/lit.txt")
}
