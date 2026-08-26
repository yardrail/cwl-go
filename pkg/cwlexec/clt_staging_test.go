package cwlexec

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// The names the staging tests share.
const (
	stgEntryName = "entry.txt"
	stgTreeName  = "tree"

	// stgHostTree, stgWorkTree and stgHostFile are the paths the planning tables reason about.
	stgHostTree = "/data/tree"
	stgWorkTree = "/work/tree"
	stgHostFile = "/data/c.txt"

	// stgTreeLocation is stgHostTree named the way a document names it, as a file: IRI.
	stgTreeLocation = "file:///data/tree"
)

// stgPlan plans an InitialWorkDirRequirement listing over notional directories and returns the map.
func stgPlan(t *testing.T, listing cwlcore.InitialWorkDirListing, inputs map[string]any) *PathMap {
	t.Helper()

	mapper := pmMap()

	err := stgStage(mapper, listing, inputs)
	if err != nil {
		t.Fatalf("StageInitialWorkDir: %v", err)
	}

	return mapper
}

// stgStage runs StageInitialWorkDir over a scope carrying just the given listing.
func stgStage(mapper *PathMap, listing cwlcore.InitialWorkDirListing, inputs map[string]any) error {
	scope := execScope(&cwlcore.InitialWorkDirRequirement{Listing: listing})

	return StageInitialWorkDir(mapper, scope, inputs, cltEval(), cwlcore.RuntimeContext{})
}

// stgEntries wraps written-out listing entries.
func stgEntries(entries ...cwlcore.InitialWorkDirEntry) cwlcore.InitialWorkDirListing {
	return cwlcore.NewInitialWorkDirListing(entries)
}

func TestStageInitialWorkDirWithoutTheRequirement(t *testing.T) {
	t.Parallel()

	mapper := pmMap()

	err := StageInitialWorkDir(mapper, nil, nil, nil, cwlcore.RuntimeContext{})
	if err != nil {
		t.Fatalf("a nil scope must plan nothing: %v", err)
	}

	err = StageInitialWorkDir(mapper, execScope(&cwlcore.ShellCommandRequirement{}), nil, nil,
		cwlcore.RuntimeContext{})
	if err != nil {
		t.Fatalf("a scope without the requirement must plan nothing: %v", err)
	}

	if len(mapper.Plan()) != 0 {
		t.Errorf("plan = %+v, want nothing planned", mapper.Plan())
	}
}

func TestStageInitialWorkDirEntryKinds(t *testing.T) {
	t.Parallel()

	listing := stgEntries(
		cwlcore.NewInitialWorkDirNull(),
		cwlcore.InitialWorkDirEntry{},
		cwlcore.NewInitialWorkDirFile(pmHostFile("/data/a.txt")),
		cwlcore.NewInitialWorkDirDirectory(&cwlcore.Directory{Path: stgHostTree, Basename: stgTreeName}),
		cwlcore.NewInitialWorkDirObjects([]cwlcore.FileOrDirectory{pmHostFile("/data/b.txt")}),
		cwlcore.NewInitialWorkDirExpression("$(inputs.f)"),
	)

	inputs := map[string]any{execInPort: pmHostFile(stgHostFile)}

	pmWantPlan(t, stgPlan(t, listing, inputs), []PathMapping{
		{Resolved: "/data/a.txt", Target: "/work/a.txt", Action: StageLink},
		{Resolved: stgHostTree, Target: stgWorkTree, Action: StageLink},
		{Resolved: "/data/b.txt", Target: "/work/b.txt", Action: StageLink},
		{Resolved: stgHostFile, Target: "/work/c.txt", Action: StageLink},
	})
}

func TestStageInitialWorkDirListingExpression(t *testing.T) {
	t.Parallel()

	listing := cwlcore.NewInitialWorkDirListingExpression("${return [inputs.f];}")
	inputs := map[string]any{execInPort: pmHostFile(stgHostFile)}

	pmWantPlan(t, stgPlan(t, listing, inputs), []PathMapping{
		{Resolved: stgHostFile, Target: "/work/c.txt", Action: StageLink},
	})
}

func TestStageInitialWorkDirDirentShapes(t *testing.T) {
	t.Parallel()

	dirent := func(name, entry string, writable bool) cwlcore.InitialWorkDirEntry {
		return cwlcore.NewInitialWorkDirDirent(&cwlcore.Dirent{
			Entryname: cwlcore.Expression(name),
			Entry:     cwlcore.Expression(entry),
			Writable:  writable,
		})
	}

	listing := stgEntries(
		dirent(stgEntryName, execGreeting, false),
		dirent("$(inputs.name)", execNullExpr, false),
		dirent("renamed.txt", "$(inputs.f)", true),
		dirent("numbers.json", "${return {a: 1};}", false),
		dirent("list.json", "${return [1, 2];}", false),
	)

	inputs := map[string]any{
		namePort:   "computed.txt",
		execInPort: pmHostFile(stgHostFile),
	}

	pmWantPlan(t, stgPlan(t, listing, inputs), []PathMapping{
		{Target: "/work/entry.txt", Contents: execGreeting, Action: StageWrite},
		{Resolved: stgHostFile, Target: "/work/renamed.txt", Action: StageCopy, Writable: true},
		{Target: "/work/numbers.json", Contents: `{"a": 1}`, Action: StageWrite},
		{Target: "/work/list.json", Contents: `[1, 2]`, Action: StageWrite},
	})
}

func TestStageInitialWorkDirNamesAnEntryByItsLocation(t *testing.T) {
	t.Parallel()

	// The shape iwd-fileobjs1 and iwd-fileobjs2 stage: a File or Directory written straight into
	// the listing, which reaches this package holding the absolute location salad resolved and
	// nothing else — no path, and no basename to name it under.
	listing := stgEntries(
		cwlcore.NewInitialWorkDirFile(&cwlcore.File{Location: "file:///data/a.txt"}),
		cwlcore.NewInitialWorkDirObjects([]cwlcore.FileOrDirectory{
			&cwlcore.File{Location: "file:///data/b.txt"},
			&cwlcore.Directory{Location: stgTreeLocation},
		}),
	)

	pmWantPlan(t, stgPlan(t, listing, nil), []PathMapping{
		{Resolved: "/data/a.txt", Target: "/work/a.txt", Action: StageLink},
		{Resolved: "/data/b.txt", Target: "/work/b.txt", Action: StageLink},
		{Resolved: stgHostTree, Target: stgWorkTree, Action: StageLink},
	})
}

func TestStageInitialWorkDirKeepsTrailingWhitespace(t *testing.T) {
	t.Parallel()

	// initial_workdir_trailingnl: `entry: |` ends the text with a newline and the staged file has
	// to keep it, all 16 bytes of it. The spec's whitespace rule decides whether an interpolated
	// field keeps its type, not what its string contains, so nothing on this path trims.
	listing := stgEntries(cwlcore.NewInitialWorkDirDirent(&cwlcore.Dirent{
		Entryname: "example.conf",
		Entry:     "CONFIGVAR=$(inputs.name)\n",
	}))

	pmWantPlan(t, stgPlan(t, listing, map[string]any{namePort: "hello"}), []PathMapping{
		{Target: "/work/example.conf", Contents: "CONFIGVAR=hello\n", Action: StageWrite},
	})
}

func TestStageInitialWorkDirKeepsAWholeFragmentsTrailingNewline(t *testing.T) {
	t.Parallel()

	// escaping_expression_no_extra_quotes: an `entry: |` holding nothing but ${...} keeps the
	// newline the block scalar put there, and the string the expression returned keeps no quotes
	// of its own — 14 bytes. It is EvalContent, not Eval, that leaves the newline alone.
	listing := stgEntries(cwlcore.NewInitialWorkDirDirent(&cwlcore.Dirent{
		Entryname: "file.txt",
		Entry:     `${return 'quote "' + inputs.name + '"'}` + "\n",
	}))

	pmWantPlan(t, stgPlan(t, listing, map[string]any{namePort: "Hello"}), []PathMapping{
		{Target: "/work/file.txt", Contents: "quote \"Hello\"\n", Action: StageWrite},
	})
}

func TestStageInitialWorkDirFailures(t *testing.T) {
	t.Parallel()

	cases := []struct {
		listing cwlcore.InitialWorkDirListing
		want    error
		name    string
	}{
		{
			name:    "a listing expression that is not an array",
			listing: cwlcore.NewInitialWorkDirListingExpression("${return 1;}"),
			want:    ErrStageEntry,
		},
		{
			name:    "a listing expression that does not evaluate",
			listing: cwlcore.NewInitialWorkDirListingExpression("$(inputs.absent.deeper)"),
			want:    cwlcore.ErrExpressionEval,
		},
		{
			name: "an entryname that does not evaluate",
			listing: stgEntries(cwlcore.NewInitialWorkDirDirent(&cwlcore.Dirent{
				Entryname: outBadRef, Entry: execGreeting,
			})),
			want: cwlcore.ErrExpressionEval,
		},
		{
			name: "an entry that does not evaluate",
			listing: stgEntries(cwlcore.NewInitialWorkDirDirent(&cwlcore.Dirent{
				Entryname: stgEntryName, Entry: outBadRef,
			})),
			want: cwlcore.ErrExpressionEval,
		},
		{
			name:    "text with no entryname to create it under",
			listing: stgEntries(cwlcore.NewInitialWorkDirExpression(execGreeting)),
			want:    ErrStageEntry,
		},
		{
			// The bare-expression entry, which is evaluated apart from a Dirent's `entry`
			// because only the latter is file content.
			name:    "a bare expression entry that does not evaluate",
			listing: stgEntries(cwlcore.NewInitialWorkDirExpression(outBadRef)),
			want:    cwlcore.ErrExpressionEval,
		},
		{
			name: "an array of objects given an entryname",
			listing: stgEntries(cwlcore.NewInitialWorkDirDirent(&cwlcore.Dirent{
				Entryname: stgEntryName, Entry: "${return [inputs.f];}",
			})),
			want: ErrStageEntry,
		},
		{
			name:    "a staged value with no name at all",
			listing: stgEntries(cwlcore.NewInitialWorkDirFile(&cwlcore.File{Contents: cwlcore.NewOptString("x")})),
			want:    ErrStageEntry,
		},
		{
			name:    "an unstageable listing entry",
			listing: stgEntries(cwlcore.NewInitialWorkDirFile(&cwlcore.File{Basename: stgEntryName})),
			want:    ErrStageValue,
		},
		{
			name:    "an unstageable entry inside a listing expression",
			listing: cwlcore.NewInitialWorkDirListingExpression(`${return [{class: "File", basename: "x"}];}`),
			want:    ErrStageValue,
		},
		{
			name: "an entry whose secondaryFiles are not filesystem objects",
			listing: stgEntries(cwlcore.NewInitialWorkDirExpression(
				`${return {class: "File", path: "/data/x", secondaryFiles: [1]};}`)),
			want: ErrFilesystemEntry,
		},
		{
			name: "an array of objects one of which cannot be staged",
			listing: stgEntries(cwlcore.NewInitialWorkDirExpression(
				`${return [inputs.f, {class: "File", basename: "x"}];}`)),
			want: ErrStageValue,
		},
	}

	inputs := map[string]any{execInPort: pmHostFile(stgHostFile)}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := stgStage(pmMap(), testCase.listing, inputs)
			if !errors.Is(err, testCase.want) {
				t.Errorf("StageInitialWorkDir: error %v does not wrap %v", err, testCase.want)
			}
		})
	}
}

func TestStageInitialWorkDirIgnoresAbsentUnionMembers(t *testing.T) {
	t.Parallel()

	// cwlcore's union accessors return a typed nil pointer for a member that is not there, which
	// is a non-nil interface wrapping nothing. Every one of them must stage nothing rather than
	// panic inside a handler goroutine.
	listing := stgEntries(
		cwlcore.NewInitialWorkDirDirent(nil),
		cwlcore.NewInitialWorkDirFile(nil),
		cwlcore.NewInitialWorkDirDirectory(nil),
	)

	err := stgStage(pmMap(), listing, nil)
	if !errors.Is(err, ErrStageEntry) {
		t.Errorf("StageInitialWorkDir: error %v does not wrap %v", err, ErrStageEntry)
	}
}

func TestExpressionValueRendersAnAbsentFilesystemValueAsNull(t *testing.T) {
	t.Parallel()

	cases := map[string]any{
		"an absent File":      (*cwlcore.File)(nil),
		"an absent Directory": (*cwlcore.Directory)(nil),
	}

	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rendered := outExpressionObject(map[string]any{"v": value})
			if got := rendered["v"]; got != nil {
				t.Errorf("outExpressionObject rendered %#v, want null", got)
			}
		})
	}
}

// TestStagerSerializesThroughTheSharedRenderer pins the two decisions this package makes about an
// entry that is neither text nor a filesystem object: the layout is cwlcore's — Python's json.dumps,
// which iwd-jsondump1 compares byte for byte — and a typed filesystem value is put into its
// expression shape before it is written down.
func TestStagerSerializesThroughTheSharedRenderer(t *testing.T) {
	t.Parallel()

	mapper := pmMap()
	stager := &workDirStager{
		mapper: mapper,
		eval:   cltEval(),
		types:  &outputCollector{outdir: pmWork},
		ctx:    &cwlcore.EvalContext{},
	}

	// The shape that reaches here still holding a typed value: an array cwlcore retyped, one of
	// whose members turned out not to be a filesystem object after all.
	value := []any{
		int64(1),
		&cwlcore.Directory{Location: stgTreeLocation, Basename: stgTreeName},
	}

	err := stager.serialized(stgEntryName, value)
	if err != nil {
		t.Fatalf("serialized: %v", err)
	}

	pmWantPlan(t, mapper, []PathMapping{{
		Target:   "/work/entry.txt",
		Contents: `[1, {"basename": "tree", "class": "Directory", "location": "file:///data/tree"}]`,
		Action:   StageWrite,
	}})
}

func TestApplyCarriesOutThePlan(t *testing.T) {
	t.Parallel()

	host := t.TempDir()
	work := filepath.Join(t.TempDir(), "work")

	source := outWriteFile(t, host, execSourceName, execGreeting)
	outWriteFile(t, host, filepath.Join(stgTreeName, "inner.txt"), execGreeting)

	mapper := NewPathMap(work, work)

	plan := []error{
		mapper.Stage(pmHostFile(source), execSourceName, false),
		mapper.Stage(pmHostFile(source), "writable.txt", true),
		mapper.Stage(&cwlcore.Directory{Path: filepath.Join(host, stgTreeName)}, stgTreeName, true),
		mapper.Stage(execLiteralFile(stgEntryName, execGreeting), stgEntryName, false),
		mapper.Stage(&cwlcore.Directory{
			Listing: []cwlcore.FileOrDirectory{execLiteralFile("inner.txt", execGreeting)},
		}, "made", false),
	}

	for _, err := range plan {
		if err != nil {
			t.Fatalf("planning: %v", err)
		}
	}

	// Applied twice, because a resumed invocation re-runs over a directory a first attempt
	// half-filled.
	for attempt := range 2 {
		err := mapper.Apply()
		if err != nil {
			t.Fatalf("Apply attempt %d: %v", attempt, err)
		}
	}

	staged := []string{
		execSourceName, "writable.txt", stgEntryName,
		filepath.Join(stgTreeName, "inner.txt"), filepath.Join("made", "inner.txt"),
	}

	for _, name := range staged {
		if got := execRead(t, filepath.Join(work, name)); got != execGreeting {
			t.Errorf("%s holds %q, want %q", name, got, execGreeting)
		}
	}

	// A read-only entry may be a link; a writable one must be a copy the tool can change without
	// the original changing with it.
	info, err := os.Lstat(filepath.Join(work, "writable.txt"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("a writable entry was linked rather than copied")
	}
}

func TestApplyFailures(t *testing.T) {
	t.Parallel()

	work := t.TempDir()
	blocker := outWriteFile(t, work, "blocker", execGreeting)

	cases := []struct {
		name    string
		mapping PathMapping
	}{
		{
			name:    "an unknown action",
			mapping: PathMapping{Target: filepath.Join(work, "x"), Action: "no-such-action"},
		},
		{
			name:    "a parent directory that is a file",
			mapping: PathMapping{Target: filepath.Join(blocker, "child"), Action: StageMkdir},
		},
		{
			name: "a copy whose source is missing",
			mapping: PathMapping{
				Resolved: filepath.Join(work, "absent"),
				Target:   filepath.Join(work, "copied"),
				Action:   StageCopy,
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			mapper := NewPathMap(work, work)
			mapper.add(&testCase.mapping, nil)

			err := mapper.Apply()
			if err == nil {
				t.Error("Apply succeeded, want an error")
			}
		})
	}
}

func TestApplySkipsAValueAlreadyInPlace(t *testing.T) {
	t.Parallel()

	work := t.TempDir()
	local := outWriteFile(t, work, execSourceName, execGreeting)

	mapper := NewPathMap(work, work)
	mapper.add(&PathMapping{Resolved: local, Target: local, Action: StageLink}, nil)

	err := mapper.Apply()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if got := execRead(t, local); got != execGreeting {
		t.Errorf("%s holds %q; applying a no-op placement must not disturb it", local, got)
	}
}

func TestReplaceWithReportsAnUnclearableTarget(t *testing.T) {
	t.Parallel()

	blocker := outWriteFile(t, t.TempDir(), "blocker", execGreeting)

	err := replaceWith(filepath.Join(blocker, "child"), func() error { return nil })
	if err == nil {
		t.Error("replaceWith succeeded over a target it could not clear")
	}
}

func TestCopyToReportsAnUnwritableTarget(t *testing.T) {
	t.Parallel()
	stgSkipIfRoot(t)

	dir := t.TempDir()
	source := outWriteFile(t, dir, execSourceName, execGreeting)
	locked := stgLockedDir(t, dir, "locked")

	err := copyTo(source, filepath.Join(locked, "copied"))
	if err == nil {
		t.Error("copyTo succeeded into a read-only directory")
	}

	tree := filepath.Join(dir, stgTreeName)
	outWriteFile(t, tree, "inner.txt", execGreeting)

	err = copyTo(tree, filepath.Join(locked, stgTreeName))
	if err == nil {
		t.Error("copyTo succeeded copying a tree into a read-only directory")
	}
}

func TestCopyToReportsAnUnreadableSource(t *testing.T) {
	t.Parallel()
	stgSkipIfRoot(t)

	dir := t.TempDir()
	tree := filepath.Join(dir, stgTreeName)

	secret := outWriteFile(t, tree, "secret.txt", execGreeting)

	err := os.Chmod(secret, 0o000)
	if err != nil {
		t.Fatalf("making a file unreadable: %v", err)
	}

	t.Cleanup(func() { stgRestore(t, secret, 0o600) })

	err = copyTo(tree, filepath.Join(dir, "copied"))
	if err == nil {
		t.Error("copyTo succeeded over an unreadable file")
	}

	// A directory that cannot even be listed fails before anything is copied out of it.
	locked := stgLockedDir(t, tree, "closed")

	err = os.Chmod(locked, 0o000)
	if err != nil {
		t.Fatalf("making a directory unreadable: %v", err)
	}

	err = copyTo(tree, filepath.Join(dir, "copied-again"))
	if err == nil {
		t.Error("copyTo succeeded over an unreadable directory")
	}
}

func TestCopyToReportsADanglingLink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tree := filepath.Join(dir, stgTreeName)

	err := os.MkdirAll(tree, 0o700)
	if err != nil {
		t.Fatalf("creating a tree: %v", err)
	}

	err = os.Symlink(filepath.Join(dir, "nothing-here"), filepath.Join(tree, "dangling"))
	if err != nil {
		t.Skipf("this filesystem does not support symbolic links: %v", err)
	}

	// A link to nothing must not become a broken link in the staged copy.
	err = copyTo(tree, filepath.Join(dir, "copied"))
	if err == nil {
		t.Error("copyTo succeeded over a link to nothing")
	}
}

// stgRestore puts a mode back so that the temporary directory holding it can still be removed.
func stgRestore(t *testing.T, path string, mode os.FileMode) {
	t.Helper()

	err := os.Chmod(path, mode)
	if err != nil {
		t.Logf("restoring the mode of %s: %v", path, err)
	}
}

// stgSkipIfRoot skips a test whose whole point is a permission the superuser does not respect.
func stgSkipIfRoot(t *testing.T) {
	t.Helper()

	if os.Geteuid() == 0 {
		t.Skip("running as root, which no file mode stops")
	}
}

// stgLockedDir creates a directory under parent that cannot be written to, restoring it afterwards
// so that the temporary directory can still be cleaned up.
func stgLockedDir(t *testing.T, parent, name string) string {
	t.Helper()

	locked := filepath.Join(parent, name)

	err := os.MkdirAll(locked, 0o500)
	if err != nil {
		t.Fatalf("creating a read-only directory: %v", err)
	}

	t.Cleanup(func() { stgRestore(t, locked, 0o700) })

	return locked
}

// stgInplaceCases is the table TestStageInitialWorkDirInplaceUpdate runs: which requirements are in
// scope, and the action a `writable: true` entry is then planned with.
var stgInplaceCases = []struct {
	name string
	reqs []cwlcore.ProcessRequirement
	want StageAction
}{
	{
		name: "no requirement isolates a writable entry as a copy",
		want: StageCopy,
	},
	{
		name: "inplaceUpdate false isolates it too",
		reqs: []cwlcore.ProcessRequirement{&cwlcore.InplaceUpdateRequirement{}},
		want: StageCopy,
	},
	{
		name: "inplaceUpdate true links it to the original instead",
		reqs: []cwlcore.ProcessRequirement{&cwlcore.InplaceUpdateRequirement{InplaceUpdate: true}},
		want: StageLink,
	},
}

func TestStageInitialWorkDirInplaceUpdate(t *testing.T) {
	t.Parallel()

	entry := cwlcore.NewInitialWorkDirDirent(&cwlcore.Dirent{
		Entryname: cwlcore.Expression(pmName),
		Entry:     cwlcore.Expression("$(inputs.f)"),
		Writable:  true,
	})

	for _, testCase := range stgInplaceCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			mapper := pmMap()

			reqs := append([]cwlcore.ProcessRequirement{
				&cwlcore.InitialWorkDirRequirement{Listing: stgEntries(entry)},
			}, testCase.reqs...)

			inputs := map[string]any{"f": pmHostFile(stgHostFile)}

			err := StageInitialWorkDir(mapper, execScope(reqs...), inputs, cltEval(), cwlcore.RuntimeContext{})
			if err != nil {
				t.Fatalf("StageInitialWorkDir: %v", err)
			}

			pmWantPlan(t, mapper, []PathMapping{
				{Resolved: stgHostFile, Target: "/work/" + pmName, Action: testCase.want, Writable: true},
			})
		})
	}
}

func TestStageInitialWorkDirInplaceUpdateWithoutAListing(t *testing.T) {
	t.Parallel()

	// The requirement is resolved before the listing is, so a scope carrying only an
	// InplaceUpdateRequirement still reaches the map — there is simply nothing to plan.
	mapper := pmMap()

	err := StageInitialWorkDir(mapper, execScope(&cwlcore.InplaceUpdateRequirement{InplaceUpdate: true}),
		nil, cltEval(), cwlcore.RuntimeContext{})
	if err != nil {
		t.Fatalf("StageInitialWorkDir: %v", err)
	}

	if !mapper.inplace {
		t.Error("an InplaceUpdateRequirement in scope did not reach the path map")
	}
}
