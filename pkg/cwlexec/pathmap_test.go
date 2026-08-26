package cwlexec

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// The directories and names the path-map tests reason about. Nothing here touches a disk, so they
// need not exist.
const (
	pmWork  = "/work"
	pmStage = "/scratch"
	pmName  = "named.txt"

	// pmHostPath is the host file most rows stage, and pmStagedPath where they stage it.
	pmHostPath   = "/data/x.txt"
	pmStagedPath = "/work/named.txt"

	// pmTestdir is the host directory the renaming rows place under a different basename.
	pmTestdir = "/data/testdir"
)

// pmMap returns an empty path map over the two notional directories.
func pmMap() *PathMap {
	return NewPathMap(pmWork, pmStage)
}

// pmHostFile returns a File that names a host path and nothing else.
func pmHostFile(local string) *cwlcore.File {
	return &cwlcore.File{Path: local, Basename: filepath.Base(local)}
}

func TestPathMapStagesAFileAndItsSecondaryFiles(t *testing.T) {
	t.Parallel()

	primary := pmHostFile("/data/reads.bam")
	primary.SecondaryFiles = []cwlcore.FileOrDirectory{pmHostFile("/elsewhere/reads.bai")}

	mapper := pmMap()

	err := mapper.Stage(primary, "reads.bam", false)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	want := []PathMapping{
		{Resolved: "/data/reads.bam", Target: "/work/reads.bam", Action: StageLink},
		{Resolved: "/elsewhere/reads.bai", Target: "/work/reads.bai", Action: StageLink},
	}

	pmWantPlan(t, mapper, want)

	if target, found := mapper.Target("/data/reads.bam"); !found || target != "/work/reads.bam" {
		t.Errorf("Target = %q, %v; want the staged path", target, found)
	}

	if _, found := mapper.Target("/data/absent"); found {
		t.Error("Target found a path that was never staged")
	}
}

func TestPathMapWritableStagingCopies(t *testing.T) {
	t.Parallel()

	mapper := pmMap()

	err := mapper.Stage(pmHostFile(pmHostPath), pmName, true)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	pmWantPlan(t, mapper, []PathMapping{
		{Resolved: pmHostPath, Target: pmStagedPath, Action: StageCopy},
	})
}

func TestPathMapStagesADirectoryLiteral(t *testing.T) {
	t.Parallel()

	literal := &cwlcore.Directory{
		Basename: stgTreeName,
		Listing: []cwlcore.FileOrDirectory{
			execLiteralFile("inner.txt", execGreeting),
			&cwlcore.Directory{Basename: "deep", Listing: make([]cwlcore.FileOrDirectory, 0)},
		},
	}

	mapper := pmMap()

	err := mapper.Stage(literal, stgTreeName, false)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	pmWantPlan(t, mapper, []PathMapping{
		{Target: stgWorkTree, Action: StageMkdir},
		{Target: "/work/tree/inner.txt", Contents: execGreeting, Action: StageWrite},
		{Target: "/work/tree/deep", Action: StageMkdir},
	})
}

func TestPathMapStagesADirectoryByPath(t *testing.T) {
	t.Parallel()

	mapper := pmMap()

	err := mapper.Stage(&cwlcore.Directory{Path: stgHostTree, Basename: stgTreeName}, stgTreeName, false)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	pmWantPlan(t, mapper, []PathMapping{
		{Resolved: stgHostTree, Target: stgWorkTree, Action: StageLink},
	})
}

func TestPathMapStagingFailures(t *testing.T) {
	t.Parallel()

	cases := []struct {
		value cwlcore.FileOrDirectory
		want  error
		name  string
		entry string
	}{
		{
			name:  "a name that climbs out of the working directory",
			value: pmHostFile("/data/x"),
			entry: "../escape",
			want:  ErrStagePath,
		},
		{
			name:  "an empty name",
			value: pmHostFile("/data/x"),
			entry: "",
			want:  ErrStagePath,
		},
		{
			name:  "an absolute name",
			value: pmHostFile("/data/x"),
			entry: "/etc/escape",
			want:  ErrUnsupportedFeature,
		},
		{
			name:  "a value that is neither a File nor a Directory",
			value: nil,
			entry: pmName,
			want:  ErrStageValue,
		},
		{
			name:  "a File with neither a path nor contents",
			value: &cwlcore.File{Basename: pmName},
			entry: pmName,
			want:  ErrStageValue,
		},
		{
			name:  "a File on storage this engine cannot read",
			value: &cwlcore.File{Basename: pmName, Location: "s3://bucket/named.txt"},
			entry: pmName,
			want:  ErrUnsupportedFeature,
		},
		{
			name:  "a Directory with neither a path nor a listing",
			value: &cwlcore.Directory{Basename: stgTreeName},
			entry: stgTreeName,
			want:  ErrStageValue,
		},
		{
			name:  "a Directory on storage this engine cannot read",
			value: &cwlcore.Directory{Basename: stgTreeName, Location: "s3://bucket/tree"},
			entry: stgTreeName,
			want:  ErrUnsupportedFeature,
		},
		{
			name:  "a secondary file that cannot be staged",
			value: pmSecondaryless(),
			entry: pmName,
			want:  ErrStageValue,
		},
		{
			name:  "a listing entry that cannot be staged",
			value: &cwlcore.Directory{Listing: []cwlcore.FileOrDirectory{&cwlcore.File{Basename: "x"}}},
			entry: stgTreeName,
			want:  ErrStageValue,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := pmMap().Stage(testCase.value, testCase.entry, false)
			if !errors.Is(err, testCase.want) {
				t.Errorf("Stage: error %v does not wrap %v", err, testCase.want)
			}
		})
	}
}

// pmSecondaryless returns a File whose secondary file names nothing at all.
func pmSecondaryless() *cwlcore.File {
	file := pmHostFile("/data/x")
	file.SecondaryFiles = []cwlcore.FileOrDirectory{&cwlcore.File{Basename: "unreachable"}}

	return file
}

func TestPathMapKeepsTheFirstTargetForAHostPath(t *testing.T) {
	t.Parallel()

	mapper := pmMap()

	for _, name := range []string{"first.txt", "second.txt"} {
		err := mapper.Stage(pmHostFile(pmHostPath), name, false)
		if err != nil {
			t.Fatalf("Stage %s: %v", name, err)
		}
	}

	// "If the same File or Directory appears more than once in the listing, the implementation
	// must choose exactly one value for path."
	if target, _ := mapper.Target(pmHostPath); target != "/work/first.txt" {
		t.Errorf("Target = %q, want the first staged path", target)
	}
}

func TestPathMapMaterializesLiterals(t *testing.T) {
	t.Parallel()

	mapper := pmMap()

	values := []cwlcore.FileOrDirectory{
		execLiteralFile(pmName, "a"),
		execLiteralFile(pmName, "b"),
		execLiteralFile("", "c"),
		pmHostFile("/data/already-there"),
	}

	for index, value := range values {
		err := mapper.Materialize(value)
		if err != nil {
			t.Fatalf("Materialize %d: %v", index, err)
		}
	}

	pmWantPlan(t, mapper, []PathMapping{
		{Target: "/scratch/named.txt", Contents: "a", Action: StageWrite},
		{Target: "/scratch/1/named.txt", Contents: "b", Action: StageWrite},
		{Target: "/scratch/literal", Contents: "c", Action: StageWrite},
	})
}

func TestPathMapStageContents(t *testing.T) {
	t.Parallel()

	mapper := pmMap()

	target, err := mapper.StageContents(pmName, execGreeting)
	if err != nil {
		t.Fatalf("StageContents: %v", err)
	}

	if target != pmStagedPath {
		t.Errorf("target = %q, want the path inside the working directory", target)
	}

	_, err = mapper.StageContents("../escape", execGreeting)
	if !errors.Is(err, ErrStagePath) {
		t.Errorf("StageContents: error %v does not wrap %v", err, ErrStagePath)
	}
}

func TestPathMapRewriteInputs(t *testing.T) {
	t.Parallel()

	staged := pmHostFile(pmHostPath)
	literal := execLiteralFile("lit.txt", execGreeting)
	untouched := pmHostFile("/data/left-alone.txt")
	tree := &cwlcore.Directory{Path: stgHostTree, Basename: stgTreeName}

	mapper := pmMap()
	pmPlanAll(t, mapper, staged, literal, tree)

	inputs := map[string]any{
		"staged":    staged,
		"literal":   literal,
		"untouched": untouched,
		"tree":      tree,
		"count":     int64(3),
	}

	rewritten := mapper.RewriteInputs(inputs)

	pmWantPath(t, rewritten["staged"], pmStagedPath)
	pmWantPath(t, rewritten["literal"], "/scratch/lit.txt")
	pmWantPath(t, rewritten["untouched"], "/data/left-alone.txt")
	pmWantPath(t, rewritten["tree"], stgWorkTree)

	if rewritten["count"] != int64(3) {
		t.Errorf("count = %#v, want it carried through untouched", rewritten["count"])
	}

	// The original object must be untouched: it is shared with the scheduler and with a
	// scattered step's sibling sub-jobs.
	if staged.Path != pmHostPath {
		t.Errorf("the input value was modified in place: path = %q", staged.Path)
	}
}

func TestPathMapRewriteReachesIntoCollections(t *testing.T) {
	t.Parallel()

	staged := pmHostFile(pmHostPath)

	mapper := pmMap()
	pmPlanAll(t, mapper, staged)

	rewritten := mapper.RewriteInputs(map[string]any{
		"list":   []any{staged, cltText},
		"typed":  []cwlcore.FileOrDirectory{staged},
		"record": map[string]any{"nested": staged},
	})

	list, ok := rewritten["list"].([]any)
	if !ok || len(list) != 2 || list[1] != cltText {
		t.Fatalf("list = %#v, want a two-element list", rewritten["list"])
	}

	pmWantPath(t, list[0], pmStagedPath)

	typed, ok := rewritten["typed"].([]any)
	if !ok || len(typed) != 1 {
		t.Fatalf("typed = %#v, want a one-element list", rewritten["typed"])
	}

	pmWantPath(t, typed[0], pmStagedPath)

	record, ok := rewritten["record"].(map[string]any)
	if !ok {
		t.Fatalf("record = %#v, want an object", rewritten["record"])
	}

	pmWantPath(t, record["nested"], pmStagedPath)
}

// pmPlanAll stages each value under its own name, materializing the literals among them.
func pmPlanAll(t *testing.T, mapper *PathMap, values ...cwlcore.FileOrDirectory) {
	t.Helper()

	for _, value := range values {
		err := pmPlanOne(mapper, value)
		if err != nil {
			t.Fatalf("planning %v: %v", value, err)
		}
	}
}

// pmPlanOne stages one value under the fixture name, or materializes it when it is a literal.
func pmPlanOne(mapper *PathMap, value cwlcore.FileOrDirectory) error {
	if pathOf(value) == "" {
		return mapper.Materialize(value)
	}

	name := pmName
	if _, isDir := value.(*cwlcore.Directory); isDir {
		name = stgTreeName
	}

	return mapper.Stage(value, name, false)
}

func TestPathMapRelocationMovesTheWholeValue(t *testing.T) {
	t.Parallel()

	primary := pmHostFile("/data/reads.bam")
	primary.Checksum = outSumAlpha
	primary.Size = cwlcore.NewOptInt(5)
	primary.SecondaryFiles = []cwlcore.FileOrDirectory{pmHostFile("/data/reads.bai")}

	moved, ok := relocate(primary, "/work/renamed.bam").(*cwlcore.File)
	if !ok {
		t.Fatal("relocate did not return a File")
	}

	if moved.Basename != "renamed.bam" || moved.Nameroot != "renamed" || moved.Nameext != ".bam" {
		t.Errorf("names = %q/%q/%q, want them re-derived from the target",
			moved.Basename, moved.Nameroot, moved.Nameext)
	}

	if moved.Dirname != pmWork || moved.Location != outFileURI("/work/renamed.bam") {
		t.Errorf("dirname = %q, location = %q; want both derived from the target",
			moved.Dirname, moved.Location)
	}

	if moved.Checksum != outSumAlpha || moved.Size.Int() != 5 {
		t.Error("the content-describing fields must survive a move: the bytes did not change")
	}

	pmWantPath(t, moved.SecondaryFiles[0], "/work/reads.bai")
}

func TestPathMapRelocationMovesADirectoryListing(t *testing.T) {
	t.Parallel()

	dir := &cwlcore.Directory{
		Path:    stgHostTree,
		Listing: []cwlcore.FileOrDirectory{pmHostFile("/data/tree/inner.txt")},
	}

	relocated, ok := relocate(dir, stgWorkTree).(*cwlcore.Directory)
	if !ok {
		t.Fatal("relocate did not return a Directory")
	}

	pmWantPath(t, relocated.Listing[0], "/work/tree/inner.txt")

	// A value that is neither a File nor a Directory is returned as it stands rather than
	// losing its identity.
	if got := relocate(nil, "/work/x"); got != nil {
		t.Errorf("relocate(nil) = %#v, want nil", got)
	}
}

func TestPathMapValueHelpers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		value    cwlcore.FileOrDirectory
		name     string
		basename string
		local    string
	}{
		{name: "a File naming its own basename", value: pmHostFile(pmHostPath), basename: "x.txt", local: pmHostPath},
		{
			name:     "a File with only a path",
			value:    &cwlcore.File{Path: "/data/derived.txt"},
			basename: "derived.txt",
			local:    "/data/derived.txt",
		},
		{
			name:     "a Directory with only a path",
			value:    &cwlcore.Directory{Path: stgHostTree},
			basename: stgTreeName,
			local:    stgHostTree,
		},
		{
			// The shape a listing entry written straight into a document arrives in.
			name:     "a File with only a file: location",
			value:    &cwlcore.File{Location: "file:///data/x.txt"},
			basename: "x.txt",
			local:    pmHostPath,
		},
		{
			name:     "a Directory with only a file: location",
			value:    &cwlcore.Directory{Location: stgTreeLocation},
			basename: stgTreeName,
			local:    stgHostTree,
		},
		{
			// A percent-escaped location decodes before it is split, so the name is the one
			// the document meant rather than its escaped spelling.
			name:     "a location carrying an escape",
			value:    &cwlcore.File{Location: "file:///data/a%20b.txt"},
			basename: "a b.txt",
			local:    "/data/a b.txt",
		},
		{
			// Named, so an entryname is not needed, but not readable: staging reports it as
			// the feature this engine lacks rather than as a value with nothing in it.
			name:     "a File on storage this engine cannot read",
			value:    &cwlcore.File{Location: "s3://bucket/remote.txt"},
			basename: "remote.txt",
			local:    "",
		},
		{
			// Locations are absolutized when a document is loaded, so one that is still
			// relative here has no base left to resolve against.
			name:     "a location that is still relative",
			value:    &cwlcore.File{Location: "sub/rel.txt"},
			basename: "rel.txt",
			local:    "",
		},
		{
			name:     "a location that is not an IRI at all",
			value:    &cwlcore.File{Location: ":"},
			basename: "",
			local:    "",
		},
		{
			name:     "a location naming no path",
			value:    &cwlcore.File{Location: "https://example.com"},
			basename: "",
			local:    "",
		},
		{name: "a literal with neither", value: &cwlcore.File{}, basename: "", local: ""},
		{name: "nothing at all", value: nil, basename: "", local: ""},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := basenameOf(testCase.value); got != testCase.basename {
				t.Errorf("basenameOf = %q, want %q", got, testCase.basename)
			}

			if got := pathOf(testCase.value); got != testCase.local {
				t.Errorf("pathOf = %q, want %q", got, testCase.local)
			}
		})
	}
}

// pmWantPlan requires a map's plan to be exactly the given placements, in order.
func pmWantPlan(t *testing.T, mapper *PathMap, want []PathMapping) {
	t.Helper()

	if plan := mapper.Plan(); !slices.Equal(plan, want) {
		t.Errorf("plan = %+v, want %+v", plan, want)
	}
}

// pmWantPath requires a value to be a filesystem value at the given path.
func pmWantPath(t *testing.T, value any, want string) {
	t.Helper()

	object, ok := value.(cwlcore.FileOrDirectory)
	if !ok {
		t.Fatalf("value = %#v, want a File or Directory", value)
	}

	if got := pathOf(object); got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

// pmRenamed returns a File whose declared basename is not the final component of its host path,
// which is the shape Process.yml permits an ExpressionTool to produce and a job order to write.
func pmRenamed(local, basename string) *cwlcore.File {
	return &cwlcore.File{Path: local, Basename: basename}
}

func TestPathMapMaterializeStagesAValueNotAlreadyUnderItsBasename(t *testing.T) {
	t.Parallel()

	mapper := pmMap()

	// A Directory in the same shape: the value is placed whole, under the name it declares.
	values := []cwlcore.FileOrDirectory{
		pmRenamed("/data/whale.txt", "badger.txt"),
		&cwlcore.Directory{Path: pmTestdir, Basename: "xtestdir"},
	}

	for index, value := range values {
		err := mapper.Materialize(value)
		if err != nil {
			t.Fatalf("Materialize %d: %v", index, err)
		}
	}

	pmWantPlan(t, mapper, []PathMapping{
		{Resolved: "/data/whale.txt", Target: "/scratch/badger.txt", Action: StageLink},
		{Resolved: pmTestdir, Target: "/scratch/xtestdir", Action: StageLink},
	})
}

func TestPathMapMaterializeLeavesADirectoryAlreadyInPlace(t *testing.T) {
	t.Parallel()

	mapper := pmMap()

	err := mapper.Materialize(&cwlcore.Directory{Path: pmTestdir, Basename: "testdir"})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	pmWantPlan(t, mapper, make([]PathMapping, 0))
}

func TestPathMapMaterializeGathersScatteredSecondaryFiles(t *testing.T) {
	t.Parallel()

	// Process.yml: secondary files "must be staged in the same directory as the primary file".
	// One beside the primary already, one in a subdirectory, one renamed.
	primary := pmHostFile("/data/hello.tar")
	primary.SecondaryFiles = []cwlcore.FileOrDirectory{
		pmHostFile("/data/index.py"),
		pmHostFile("/data/sub/hello.py"),
		&cwlcore.Directory{Path: pmTestdir, Basename: "xtestdir"},
	}

	mapper := pmMap()

	err := mapper.Materialize(primary)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	pmWantPlan(t, mapper, []PathMapping{
		{Resolved: "/data/hello.tar", Target: "/scratch/hello.tar", Action: StageLink},
		{Resolved: "/data/index.py", Target: "/scratch/index.py", Action: StageLink},
		{Resolved: "/data/sub/hello.py", Target: "/scratch/hello.py", Action: StageLink},
		{Resolved: pmTestdir, Target: "/scratch/xtestdir", Action: StageLink},
	})
}

func TestPathMapMaterializeLeavesSecondaryFilesAlreadyBesideThePrimary(t *testing.T) {
	t.Parallel()

	primary := pmHostFile("/data/reads.bam")
	primary.SecondaryFiles = []cwlcore.FileOrDirectory{pmHostFile("/data/reads.bai")}

	mapper := pmMap()

	err := mapper.Materialize(primary)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	pmWantPlan(t, mapper, make([]PathMapping, 0))
}

func TestPathMapInplaceUpdateLinksAWritableEntry(t *testing.T) {
	t.Parallel()

	// CommandLineTool.yml, InplaceUpdateRequirement: with inplaceUpdate true "files may be
	// destructively modified in place as opposed to copied and updated", which is exactly the
	// isolation Dirent.writable otherwise demands.
	mapper := pmMap()
	mapper.AllowInplaceUpdate()

	err := mapper.Stage(pmHostFile(pmHostPath), pmName, true)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	err = mapper.Stage(&cwlcore.Directory{Path: "/data/d", Basename: "d"}, "d", true)
	if err != nil {
		t.Fatalf("Stage directory: %v", err)
	}

	pmWantPlan(t, mapper, []PathMapping{
		{Resolved: pmHostPath, Target: pmStagedPath, Action: StageLink},
		{Resolved: "/data/d", Target: "/work/d", Action: StageLink},
	})
}
