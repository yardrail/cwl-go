package cwlexec

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strconv"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Where a tool sees its files.
//
// Between an input object and a command line sits one question the argv builder deliberately does
// not answer: at which path will the tool actually find each File and Directory? On this host the
// answer is usually "where it already is", but not always — an InitialWorkDirRequirement entry has
// to appear in the designated output directory under the name the document chose, and a file
// literal has no path at all until something writes it.
//
// [PathMap] answers that question for a whole invocation, as a *plan*: a list of placements, in the
// order they must be carried out, plus a lookup from where a value's bytes are now to where the
// tool will see them. Nothing here touches a filesystem; [PathMap.Apply], in clt_staging.go, is
// what carries the plan out.
//
// The split is what makes a container implementation possible without rewriting the executor. A
// container runs the tool in a different filesystem namespace, so every path in its argv is a
// container path while every path in the plan's Resolved field stays a host path — which is exactly
// the pair a bind mount is built from. A container path mapper differs from this one in the two
// directories it is constructed with and in nothing else, because [PathMap.RewriteInputs] is what
// puts the mapped paths into the input object and every later stage — argv, redirections,
// expressions, output collection — reads that one rewritten object.

// StageAction says how one mapped value is placed at its target path.
type StageAction string

const (
	// StageLink places a read-only value, which may be a symbolic link to the original bytes
	// rather than a copy. CommandLineTool.yml, Dirent.writable: "If `writable` is false, the
	// file may be made available using a bind mount or file system link to avoid unnecessary
	// copying of the input file".
	StageLink StageAction = "link"

	// StageCopy places a value the tool may modify, which must therefore be a copy: "Changes to
	// the file or directory must be isolated and not visible by any other CommandLineTool
	// process. This may be implemented by making a copy of the original file or directory".
	StageCopy StageAction = "copy"

	// StageWrite creates a file from literal bytes — a Dirent whose entry is text, or a File
	// value carrying `contents` and no path.
	StageWrite StageAction = "write"

	// StageMkdir creates an empty directory, which is what a directory literal's own entry is:
	// its listing is planned as separate placements underneath it.
	StageMkdir StageAction = "mkdir"
)

// Errors reported while planning where a tool's files go. They are wrapped with the offending name,
// so callers should test them with [errors.Is].
var (
	// ErrStagePath reports a staged entry name that is not a relative path inside the working
	// directory. CommandLineTool.yml, Dirent.entryname: "A relative path starting with `../` or
	// that resolves to location above the designated output directory is an error".
	ErrStagePath = errors.New("staged entry name is not a relative path inside the working directory")

	// ErrStageValue reports a File or Directory that cannot be placed anywhere: one carrying
	// neither a path nor the literal content that would let the engine create it.
	ErrStageValue = errors.New("value cannot be staged")
)

// The modes staged entries are created with. Everything an invocation stages is private to that
// invocation, and the tool runs as this same user, so nothing needs a wider mode than the owner's.
const (
	stageDirPerm  = 0o700
	stageFilePerm = 0o600
)

// literalName is what an unnamed file literal is materialized as when it carries no basename of its
// own to be named after.
const literalName = "literal"

// stageActions maps "this entry must be isolated from the original" onto the placement it calls
// for. An isolated value must be a copy the tool can modify without anybody else seeing it; every
// other value may be a link.
var stageActions = map[bool]StageAction{true: StageCopy, false: StageLink}

// PathMapping is one planned placement: what to put where, and how.
type PathMapping struct {
	// Resolved is the absolute host path the bytes live at now, or "" when there are none yet
	// — a literal to write, or a directory to create.
	Resolved string

	// Target is the absolute path the tool will see the value at.
	Target string

	// Contents is the literal text to write. It is meaningful only for [StageWrite], where ""
	// is an ordinary value: an empty file literal.
	Contents string

	// Action is how Target is produced from Resolved.
	Action StageAction
}

// PathMap is the plan for one invocation: every placement it needs, in the order they must be
// carried out, and the lookup that relocates its input object onto the result.
//
// A PathMap is built by [PathMap.Stage] and [PathMap.Materialize], carried out by [PathMap.Apply],
// and consulted by [PathMap.RewriteInputs]. It is not safe for concurrent use; one invocation builds
// and owns exactly one.
type PathMap struct {
	// byPath maps a host path to the target it was staged at, so that an input parameter naming
	// the same file as a listing entry is relocated onto the staged copy.
	byPath map[string]string

	// byValue maps a staged value to its target by identity, which is the only handle a literal
	// has: a File with no path is distinguishable from another only by being itself.
	byValue map[cwlcore.FileOrDirectory]string

	// used is the set of targets already claimed, so that [PathMap.Materialize] can pick a name
	// that collides with nothing.
	used map[string]bool

	// plan is the placements in the order they must be applied: a directory before whatever
	// goes inside it, a primary file before its secondary files.
	plan []PathMapping

	// workdir is the designated output directory, which is where the specification says an
	// InitialWorkDirRequirement entry must appear.
	workdir string

	// staging is where a value that merely has to be *reachable* is materialized — a file
	// literal handed to an input parameter, say. It is deliberately not the output directory: a
	// value the document never asked to stage must not turn up in an output glob.
	staging string

	// inplace records that an InplaceUpdateRequirement permits a writable entry to be placed as
	// a link to the original rather than as a copy. See [PathMap.AllowInplaceUpdate].
	inplace bool
}

// NewPathMap returns an empty path map.
//
// workdir is the invocation's designated output directory, where an InitialWorkDirRequirement's
// entries are staged and where a relative `entryname` resolves. staging is where values that only
// need to be reachable are materialized; it is normally the invocation's scratch directory, and a
// container implementation would make it the host side of a mount instead.
func NewPathMap(workdir, staging string) *PathMap {
	return &PathMap{
		byPath:  make(map[string]string),
		byValue: make(map[cwlcore.FileOrDirectory]string),
		used:    make(map[string]bool),
		plan:    make([]PathMapping, 0),
		workdir: workdir,
		staging: staging,
	}
}

// Workdir returns the designated output directory this map stages into.
func (m *PathMap) Workdir() string {
	return m.workdir
}

// AllowInplaceUpdate records that an InplaceUpdateRequirement with `inplaceUpdate: true` is in
// scope, which changes how a `writable: true` listing entry is placed: as a link to the original
// rather than as a copy of it.
//
// That inverts the isolation Dirent.writable otherwise demands, and deliberately.
// CommandLineTool.yml, InplaceUpdateRequirement: "If `inplaceUpdate` is true, then an
// implementation supporting this feature may permit tools to directly update files with `writable:
// true` in InitialWorkDirRequirement. That is, as an optimization, files may be destructively
// modified in place as opposed to copied and updated." Dirent.writable says the same thing from the
// other side: "Disruptive changes to the referenced file or directory must not be allowed unless
// `InplaceUpdateRequirement.inplaceUpdate` is true."
//
// A link is what makes the update reach the original: the tool opens the staged name, the
// filesystem follows it, and the bytes it writes are the ones the input value named. A directory is
// linked whole for the same reason, so that a file the tool creates inside it appears inside the
// original too.
//
// The safety conditions are the document's to meet, not this map's to enforce: "An implementation
// must ensure that only one workflow step may access a writable file at a time", "Workflow steps
// which modify a file must produce the modified file as output", and "enabling this feature implies
// that WorkReuse should not be enabled". Nothing here can see a sibling step, so nothing here can
// check them.
//
// It is a method rather than a [NewPathMap] parameter because the requirement is resolved from the
// scope by [StageInitialWorkDir], which is handed a map that is already built.
func (m *PathMap) AllowInplaceUpdate() {
	m.inplace = true
}

// Plan returns the placements in the order they must be carried out. The slice aliases the map's own
// storage and must not be modified.
func (m *PathMap) Plan() []PathMapping {
	return m.plan
}

// Target returns the path a host path was staged at, and whether it was staged at all.
func (m *PathMap) Target(resolved string) (string, bool) {
	target, found := m.byPath[resolved]

	return target, found
}

// Stage plans the placement of one File or Directory in the working directory under name, which must
// be a relative path that stays inside it.
//
// writable selects between a link and a copy, and applies recursively: "A directory marked as
// `writable: true` implies that all files and subdirectories are recursively writable as well".
//
// A File's secondary files are placed beside it and a Directory's listing inside it, both under their
// own basenames, because that is the only arrangement the values themselves describe.
func (m *PathMap) Stage(value cwlcore.FileOrDirectory, name string, writable bool) error {
	target, err := m.targetIn(m.workdir, name)
	if err != nil {
		return err
	}

	return m.stageAt(value, target, writable)
}

// Materialize plans the placement of one File or Directory in the staging directory, under a name
// derived from its own basename and made unique against everything already planned.
//
// It is what a file literal reaching an input parameter needs: the specification says such a value
// "must specify `contents`... This is a 'file literal'", and a tool cannot open one until something
// has written it down.
//
// It is also what a *renamed* value needs, which is the second case here and the reason the check
// is not simply "does it have a path". Process.yml, File.basename: "If `basename` is provided, it
// is not required to match the value from `location`... When this file is made available to a
// CommandLineTool, it must be named with `basename`, i.e. the final component of the `path` field
// must match `basename`" — and File.path repeats it, "the final path component must match the value
// of `basename`". An ExpressionTool is explicitly allowed to produce such a value: "It is legal to
// return a file object with an existing `location` but a different `basename`." A value whose bytes
// are on this filesystem under one name and whose declared name is another therefore cannot be
// handed to a tool where it lies; it is staged under the name it declares, which is the only way
// both halves of that sentence can hold at once.
//
// And it is what a File whose *secondary files are somewhere else* needs, which is the third case.
// Process.yml, File.secondaryFiles: "a list of File or Directory objects that must be staged in the
// same directory as the primary file". A job order is free to name them anywhere — in a
// subdirectory, or under a basename that is not the one their location ends with — and a tool that
// reaches one by building a path out of `$(inputs.x.dirname)` finds nothing there. Staging the
// primary brings its secondary files with it, because [PathMap.stageFile] places them beside it
// under their own basenames, which is the arrangement the sentence describes.
//
// A value already lying where all three sentences want it needs nothing and is left alone. That
// exemption is what keeps the common case — a job order naming files that are already arranged the
// way the document expects — free of copies, links and rewritten paths.
func (m *PathMap) Materialize(value cwlcore.FileOrDirectory) error {
	if stagedInPlace(value) {
		return nil
	}

	return m.stageAt(value, m.unique(basenameOf(value)), false)
}

// stagedInPlace reports whether a value's bytes already lie where a tool must find them: on this
// filesystem, under the name the value declares, with every secondary file of a File beside it
// under the name it declares. It is the exact negation of the three cases [PathMap.Materialize]
// stages.
//
// Only one level of secondary files is examined. A secondary's own secondary files are placed by
// the same recursion that places it, so a nested arrangement follows from the outer decision rather
// than needing one of its own.
func stagedInPlace(value cwlcore.FileOrDirectory) bool {
	local := pathOf(value)
	if local == "" || !namedAs(value, local) {
		return false
	}

	file, isFile := value.(*cwlcore.File)
	if !isFile || file == nil {
		return true
	}

	beside := filepath.Dir(local)

	for _, secondary := range file.SecondaryFiles {
		if pathOf(secondary) != filepath.Join(beside, basenameOf(secondary)) {
			return false
		}
	}

	return true
}

// namedAs reports whether local's final component is the name value declares. A value that declares
// no basename is named by its location, so it is always already named correctly.
func namedAs(value cwlcore.FileOrDirectory, local string) bool {
	basename := fieldsOf(value).basename

	return basename == "" || basename == filepath.Base(local)
}

// StageContents plans a file of literal text in the working directory under name, and returns the
// path it will be written to. It is the Dirent case where `entry` evaluates to text rather than to a
// filesystem value.
func (m *PathMap) StageContents(name, contents string) (string, error) {
	target, err := m.targetIn(m.workdir, name)
	if err != nil {
		return "", err
	}

	m.add(PathMapping{Target: target, Contents: contents, Action: StageWrite}, nil)

	return target, nil
}

// RewriteInputs returns a copy of an input object in which every File and Directory this map
// relocated points at the path the tool will see it at.
//
// This is the step CommandLineTool.yml requires of an implementation: "Files or Directories which are
// listed in the input parameters and appear in the `InitialWorkDirRequirement` listing must have
// their `path` set to their staged location". Doing it once, here, is what keeps the argv builder,
// the redirection filenames, every expression and output collection agreeing about where a file is —
// they all read the object this returns.
//
// The original object is not modified. That is not tidiness: [StepCall.Inputs] is shared with the
// scheduler and with the sibling sub-jobs of a scattered step, which run concurrently.
func (m *PathMap) RewriteInputs(inputs map[string]any) map[string]any {
	rewritten := make(map[string]any, len(inputs))
	for name, value := range inputs {
		rewritten[name] = m.rewriteValue(value)
	}

	return rewritten
}

// isolate reports whether a listing entry must be placed as a copy that the tool's changes cannot
// escape from. A writable entry must, unless an InplaceUpdateRequirement has lifted exactly that
// isolation.
func (m *PathMap) isolate(writable bool) bool {
	return writable && !m.inplace
}

// stageAt plans one value at an already-resolved absolute target.
//
// The nil checks are not decoration. A FileOrDirectory is an interface, and cwlcore's union
// accessors hand back a typed nil pointer for a member that is not there —
// InitialWorkDirEntry.File() on an entry holding a Dirent, say — which is a non-nil interface
// wrapping nothing. Left unchecked that is a panic inside a handler goroutine.
func (m *PathMap) stageAt(value cwlcore.FileOrDirectory, target string, writable bool) error {
	file, isFile := value.(*cwlcore.File)
	if isFile && file != nil {
		return m.stageFile(file, target, writable)
	}

	dir, isDir := value.(*cwlcore.Directory)
	if isDir && dir != nil {
		return m.stageDirectory(dir, target, writable)
	}

	return fmt.Errorf("%w: %s is not a File or Directory this engine can place",
		ErrStageValue, cwlcore.TypeName(value))
}

// stageFile plans a File and, beside it, each of its secondary files.
//
// The bytes are looked for at whatever the value names, which is its `path` when it has one and the
// local path of a `file:` `location` when it does not; see [stageFields.ref]. A listing entry
// written directly into a document has only the location.
func (m *PathMap) stageFile(file *cwlcore.File, target string, writable bool) error {
	switch local := pathOf(file); {
	case local != "":
		m.add(PathMapping{Resolved: local, Target: target, Action: stageActions[m.isolate(writable)]}, file)
	case file.Contents.IsSet():
		m.add(PathMapping{Target: target, Contents: file.Contents.Value(), Action: StageWrite}, file)
	case file.Location != "":
		return fmt.Errorf("%w: %s is not on a filesystem this engine can read",
			ErrUnsupportedFeature, file.Location)
	default:
		return fmt.Errorf("%w: File %q has neither a path nor contents", ErrStageValue, file.Basename)
	}

	beside := filepath.Dir(target)

	for _, secondary := range file.SecondaryFiles {
		err := m.stageAt(secondary, filepath.Join(beside, basenameOf(secondary)), writable)
		if err != nil {
			return err
		}
	}

	return nil
}

// stageDirectory plans a Directory and, inside it, each entry of a literal's listing.
//
// A Directory that names a host path is placed whole, listing and all, so its entries need no
// placements of their own. That path is its `path` when it has one and the local path of a `file:`
// `location` when it does not, as it is for a File. A directory literal — no path, but a listing —
// is created empty and filled from the listing, which is the only way its contents can reach a disk.
func (m *PathMap) stageDirectory(dir *cwlcore.Directory, target string, writable bool) error {
	local := pathOf(dir)
	if local != "" {
		m.add(PathMapping{Resolved: local, Target: target, Action: stageActions[m.isolate(writable)]}, dir)

		return nil
	}

	if dir.Listing == nil {
		if dir.Location != "" {
			return fmt.Errorf("%w: %s is not on a filesystem this engine can read",
				ErrUnsupportedFeature, dir.Location)
		}

		return fmt.Errorf("%w: Directory %q has neither a path nor a listing", ErrStageValue, dir.Basename)
	}

	m.add(PathMapping{Target: target, Action: StageMkdir}, dir)

	for _, entry := range dir.Listing {
		err := m.stageAt(entry, filepath.Join(target, basenameOf(entry)), writable)
		if err != nil {
			return err
		}
	}

	return nil
}

// add records one placement, along with the two lookups that relocate an input object onto it.
//
// A host path already staged keeps its first target. CommandLineTool.yml: "If the same File or
// Directory appears more than once in the `InitialWorkDirRequirement` listing, the implementation
// must choose exactly one value for `path`; how this value is chosen is undefined".
func (m *PathMap) add(mapping PathMapping, value cwlcore.FileOrDirectory) {
	m.plan = append(m.plan, mapping)
	m.used[mapping.Target] = true

	if value != nil {
		m.byValue[value] = mapping.Target
	}

	if mapping.Resolved == "" || mapping.Resolved == mapping.Target {
		return
	}

	if _, claimed := m.byPath[mapping.Resolved]; !claimed {
		m.byPath[mapping.Resolved] = mapping.Target
	}
}

// targetIn resolves a document-supplied entry name against a base directory, rejecting anything that
// would put the entry somewhere else.
//
// An absolute name is not merely invalid, it is a feature this engine does not have: the
// specification permits one only when "the program is will run inside a software container where,
// from the perspective of the program, the root filesystem is not shared with any other user or
// running program", and container execution is exactly what [ErrUnsupportedFeature] covers.
func (m *PathMap) targetIn(base, name string) (string, error) {
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("%w: an absolute entryname (%q) requires container execution",
			ErrUnsupportedFeature, name)
	}

	if name == "" || !filepath.IsLocal(name) {
		return "", fmt.Errorf("%w: %q", ErrStagePath, name)
	}

	return filepath.Join(base, name), nil
}

// unique returns a path in the staging directory named after name, pushed into a numbered
// subdirectory until it collides with nothing already planned.
func (m *PathMap) unique(name string) string {
	if name == "" || !filepath.IsLocal(name) {
		name = literalName
	}

	target := filepath.Join(m.staging, name)
	for attempt := 1; m.used[target]; attempt++ {
		target = filepath.Join(m.staging, strconv.Itoa(attempt), name)
	}

	return target
}

// rewriteValue relocates one value, descending through the record and array shapes an input object
// can nest a filesystem value inside.
func (m *PathMap) rewriteValue(value any) any {
	switch typed := value.(type) {
	case *cwlcore.File:
		return m.rewriteObject(typed)
	case *cwlcore.Directory:
		return m.rewriteObject(typed)
	case []any:
		return m.rewriteList(typed)
	case []cwlcore.FileOrDirectory:
		return m.rewriteList(outWiden(typed))
	case map[string]any:
		return m.RewriteInputs(typed)
	default:
		return value
	}
}

// rewriteList relocates every element of an array-valued parameter.
func (m *PathMap) rewriteList(values []any) []any {
	rewritten := make([]any, 0, len(values))
	for _, value := range values {
		rewritten = append(rewritten, m.rewriteValue(value))
	}

	return rewritten
}

// rewriteObject relocates one filesystem value, or returns it unchanged when this map never placed
// it anywhere.
//
// A value is looked up by identity first and by host path second. Identity is what a literal has
// instead of a path; the host path is what lets an input parameter and a listing entry that name the
// same file — two different Go values, decoded independently — resolve to the one staged copy.
func (m *PathMap) rewriteObject(value cwlcore.FileOrDirectory) cwlcore.FileOrDirectory {
	target, found := m.byValue[value]
	if !found {
		target, found = m.byPath[pathOf(value)]
	}

	if !found {
		return value
	}

	return relocate(value, target)
}

// basenameOf returns the name a filesystem value is known by, deriving one from what the value
// names when it declared none.
//
// Process.yml, basename: "If not provided, the implementation must set this field based on the
// `location` field by taking the final path component after parsing `location` as an IRI." An
// InitialWorkDirRequirement entry written straight into a document — `iwd-fileobjs1` stages
// `{class: File, location: ../loadContents/inp-filelist.txt}` — arrives with nothing else, having
// been through salad's link resolution and no other processing, so this is the only thing that
// names it.
func basenameOf(value cwlcore.FileOrDirectory) string {
	fields := fieldsOf(value)
	if fields.basename != "" {
		return fields.basename
	}

	return fields.ref().name
}

// pathOf returns the host path a filesystem value names, or "" when it names none: a literal, or a
// resource on something that is not a local filesystem.
func pathOf(value cwlcore.FileOrDirectory) string {
	return fieldsOf(value).ref().local
}

// stageFields are the three strings a filesystem value can name itself with, read off whichever of
// the two types it is so that nothing below here has to know which.
type stageFields struct {
	basename string
	path     string
	location string
}

// fieldsOf reads the naming fields of a filesystem value. A value that is really a typed nil
// pointer names nothing; see [PathMap.stageAt].
func fieldsOf(value cwlcore.FileOrDirectory) stageFields {
	if file, ok := value.(*cwlcore.File); ok && file != nil {
		return stageFields{basename: file.Basename, path: file.Path, location: file.Location}
	}

	if dir, ok := value.(*cwlcore.Directory); ok && dir != nil {
		return stageFields{basename: dir.Basename, path: dir.Path, location: dir.Location}
	}

	return stageFields{}
}

// stageRef is what a filesystem value names: the host path holding its bytes, and the final
// component of the reference that named it.
type stageRef struct {
	// local is the absolute host path, or "" when the value names nothing on this filesystem.
	local string

	// name is the reference's final path component, which is what an absent basename is derived
	// from. It is set even when local is not, so that a remote resource still has a name.
	name string
}

// ref resolves what a value names, from its `path` when it has one and from its `location`
// otherwise.
//
// This is the derivation joborder_file.go's joResolveRef applies to a job order's values, and the
// two agree deliberately: `path` wins over `location`, because a path names a real filesystem while
// a location names a resource that need not be on one; a `file:` IRI yields both a local path and a
// name; and any other scheme yields a name alone, so that a remote resource is reported as the
// feature this engine lacks rather than as a value with nothing in it.
//
// The one difference is that there is no base URI to resolve against here. A document's locations
// were made absolute when it was loaded, so a reference that is still relative at this point cannot
// be resolved by guessing at a base, and yields a name without a path.
func (f stageFields) ref() stageRef {
	if f.path != "" {
		return stageRef{local: f.path, name: filepath.Base(f.path)}
	}

	parsed, err := url.Parse(f.location)
	if err != nil || parsed.Path == "" {
		return stageRef{}
	}

	name := path.Base(parsed.Path)
	if (parsed.Scheme != "" && parsed.Scheme != joSchemeFile) || !path.IsAbs(parsed.Path) {
		return stageRef{name: name}
	}

	return stageRef{local: filepath.Clean(parsed.Path), name: name}
}

// relocate returns a copy of a filesystem value placed at target, moving its secondary files beside
// it and its listing entries inside it.
//
// The name fields move with the value: CommandLineTool.yml says of a Dirent that "the `entryname`
// field overrides the value of `basename` of the File or Directory object", so a staged value is
// known by the name it was staged under, not by the one it arrived with.
func relocate(value cwlcore.FileOrDirectory, target string) cwlcore.FileOrDirectory {
	if file, ok := value.(*cwlcore.File); ok && file != nil {
		return relocateFile(file, target)
	}

	if dir, ok := value.(*cwlcore.Directory); ok && dir != nil {
		return relocateDirectory(dir, target)
	}

	return value
}

// relocateFile copies a File onto a new path, re-deriving every field the specification says the
// implementation must set from that path and carrying the content-describing fields across unchanged
// — the bytes have not changed, only where they are.
func relocateFile(file *cwlcore.File, target string) *cwlcore.File {
	basename := filepath.Base(target)
	parts := outSplitName(basename)

	moved := &cwlcore.File{
		Node:     file.Node,
		Location: outFileURI(target),
		Path:     target,
		Basename: basename,
		Dirname:  outDirname(target),
		Nameroot: parts.root,
		Nameext:  parts.ext,
		Checksum: file.Checksum,
		Format:   file.Format,
		Size:     file.Size,
		Contents: file.Contents,
	}

	beside := filepath.Dir(target)

	for _, secondary := range file.SecondaryFiles {
		moved.SecondaryFiles = append(moved.SecondaryFiles,
			relocate(secondary, filepath.Join(beside, basenameOf(secondary))))
	}

	return moved
}

// relocateDirectory copies a Directory onto a new path, moving its listing entries inside it. A nil
// listing stays nil: nobody read it, and inventing an empty one would assert the directory is empty.
func relocateDirectory(dir *cwlcore.Directory, target string) *cwlcore.Directory {
	moved := &cwlcore.Directory{
		Node:     dir.Node,
		Location: outFileURI(target),
		Path:     target,
		Basename: filepath.Base(target),
	}

	for _, entry := range dir.Listing {
		moved.Listing = append(moved.Listing, relocate(entry, filepath.Join(target, basenameOf(entry))))
	}

	return moved
}
