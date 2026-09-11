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

// Plans where a tool sees its files. [PathMap.Apply] in clt_staging.go carries the plan out.
// Three paths per placement: Resolved (host source), Target (tool sees), Host (where Apply writes).

// StageAction says how one mapped value is placed at its target path.
type StageAction string

const (
	// StageLink places a read-only value, possibly as a symlink.
	StageLink StageAction = "link"

	// StageCopy places a writable copy of the value.
	StageCopy StageAction = "copy"

	// StageWrite creates a file from literal bytes.
	StageWrite StageAction = "write"

	// StageMkdir creates an empty directory.
	StageMkdir StageAction = "mkdir"
)

var (
	// ErrStagePath reports an entry name that escapes the working directory.
	ErrStagePath = errors.New("staged entry name is not a relative path inside the working directory")

	// ErrStageValue reports a File/Directory with neither a path nor literal content.
	ErrStageValue = errors.New("value cannot be staged")
)

// Permissions for staged entries.
const (
	stageDirPerm  = 0o700
	stageFilePerm = 0o600
)

// literalName is the default name for unnamed file literals.
const literalName = "literal"

// stageActions maps writable flag to placement action.
var stageActions = map[bool]StageAction{true: StageCopy, false: StageLink}

// PathMapping is one planned placement: what to put where, and how.
type PathMapping struct {
	Resolved string      // host path of source bytes, or "" for literals
	Target   string      // path the tool sees
	Host     string      // host path Apply writes to, or "" if bind-mounted
	Contents string      // literal text for StageWrite
	Action   StageAction // how Target is produced
	Writable bool        // tool may modify this entry
}

// PathMap is the staging plan for one invocation. Not safe for concurrent use.
type PathMap struct {
	byPath      map[string]string                  // host path → target
	byValue     map[cwlcore.FileOrDirectory]string // value identity → target
	used        map[string]bool                    // claimed targets
	plan        []PathMapping                      // ordered placements
	workdir     string                             // output directory (tool-side)
	staging     string                             // staging directory (tool-side)
	hostWorkdir string                             // host-side workdir
	hostStaging string                             // host-side staging
	resolver    OutputResolver                     // nil means local filesystem
	inplace     bool                               // InplaceUpdateRequirement active
	contained   bool                               // running in a container
	absolute    bool                               // allow absolute entry targets
}

// NewPathMap returns an empty path map for a host invocation (Host == Target).
func NewPathMap(workdir, staging string) *PathMap {
	return &PathMap{
		byPath:      make(map[string]string),
		byValue:     make(map[cwlcore.FileOrDirectory]string),
		used:        make(map[string]bool),
		plan:        make([]PathMapping, 0),
		workdir:     workdir,
		staging:     staging,
		hostWorkdir: workdir,
		hostStaging: staging,
		resolver:    nil,
		inplace:     false,
		contained:   false,
		absolute:    false,
	}
}

// Workdir returns the output directory this map stages into.
func (m *PathMap) Workdir() string {
	return m.workdir
}

// AllowInplaceUpdate permits writable entries to be linked instead of copied.
func (m *PathMap) AllowInplaceUpdate() {
	m.inplace = true
}

// Plan returns the ordered placements. The slice must not be modified.
func (m *PathMap) Plan() []PathMapping {
	return m.plan
}

// Target returns the staged target for a host path, if any.
func (m *PathMap) Target(resolved string) (string, bool) {
	target, found := m.byPath[resolved]

	return target, found
}

// Stage plans a File or Directory placement in the working directory under name.
func (m *PathMap) Stage(value cwlcore.FileOrDirectory, name string, writable bool) error {
	target, err := m.targetIn(m.workdir, name)
	if err != nil {
		return err
	}

	return m.stageAt(value, target, writable)
}

// Materialize plans a File or Directory in the staging directory under a unique name.
// Skips values already in place (correct path, basename, and secondary file layout) on host.
func (m *PathMap) Materialize(value cwlcore.FileOrDirectory) error {
	if !m.contained && stagedInPlace(value) {
		return nil
	}

	return m.stageAt(value, m.unique(basenameOf(value)), false)
}

// stagedInPlace reports whether a value is already at its correct path and name.
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

// namedAs reports whether the file at local matches the value's declared basename.
func namedAs(value cwlcore.FileOrDirectory, local string) bool {
	basename := fieldsOf(value).basename

	return basename == "" || basename == filepath.Base(local)
}

// StageContents plans a literal text file in the working directory under name.
func (m *PathMap) StageContents(name, contents string) (string, error) {
	target, err := m.targetIn(m.workdir, name)
	if err != nil {
		return "", err
	}

	m.add(
		&PathMapping{Resolved: "", Target: target, Host: "", Contents: contents, Action: StageWrite, Writable: false},
		nil,
	)

	return target, nil
}

// RewriteInputs returns a copy of inputs with File/Directory paths rewritten to staged locations.
func (m *PathMap) RewriteInputs(inputs map[string]any) map[string]any {
	rewritten := make(map[string]any, len(inputs))
	for name, value := range inputs {
		rewritten[name] = m.rewriteValue(value)
	}

	return rewritten
}

// isolate reports whether a writable entry must be copied (not linked).
func (m *PathMap) isolate(writable bool) bool {
	return writable && !m.inplace
}

// stageAt plans one value at an absolute target path.
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

// stageFile plans a File and its secondary files.
func (m *PathMap) stageFile(file *cwlcore.File, target string, writable bool) error {
	switch local := pathOf(file); {
	case local != "":
		m.add(&PathMapping{
			Resolved: local, Target: target, Host: "", Contents: "",
			Action: stageActions[m.isolate(writable)], Writable: writable,
		}, file)
	case file.Contents.IsSet():
		m.add(&PathMapping{
			Resolved: "", Target: target, Host: "", Contents: file.Contents.Value(),
			Action: StageWrite, Writable: writable,
		}, file)
	case file.Location != "":
		return fmt.Errorf("%w: %s is not on a filesystem this engine can read",
			ErrUnsupportedLocationScheme, file.Location)
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

// stageDirectory plans a Directory. Existing directories are placed whole; literals are created.
func (m *PathMap) stageDirectory(dir *cwlcore.Directory, target string, writable bool) error {
	local := pathOf(dir)
	if local != "" {
		m.add(&PathMapping{
			Resolved: local, Target: target, Host: "", Contents: "",
			Action: stageActions[m.isolate(writable)], Writable: writable,
		}, dir)

		return nil
	}

	if dir.Listing == nil {
		if dir.Location != "" {
			return fmt.Errorf("%w: %s is not on a filesystem this engine can read",
				ErrUnsupportedLocationScheme, dir.Location)
		}

		return fmt.Errorf("%w: Directory %q has neither a path nor a listing", ErrStageValue, dir.Basename)
	}

	m.add(
		&PathMapping{Resolved: "", Target: target, Host: "", Contents: "", Action: StageMkdir, Writable: writable},
		dir,
	)

	for _, entry := range dir.Listing {
		err := m.stageAt(entry, filepath.Join(target, basenameOf(entry)), writable)
		if err != nil {
			return err
		}
	}

	return nil
}

// add records a placement and updates the path/value lookups.
func (m *PathMap) add(mapping *PathMapping, value cwlcore.FileOrDirectory) {
	mapping.Host = m.hostFor(mapping.Target, mapping.Action)

	m.plan = append(m.plan, *mapping)
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

// unique returns a collision-free path in the staging directory.
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

// rewriteValue relocates one value, descending into records and arrays.
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

// rewriteList relocates every element of an array.
func (m *PathMap) rewriteList(values []any) []any {
	rewritten := make([]any, 0, len(values))
	for _, value := range values {
		rewritten = append(rewritten, m.rewriteValue(value))
	}

	return rewritten
}

// rewriteObject relocates one File/Directory, looking up by identity then by path.
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

// basenameOf returns the value's basename, deriving it from location if not set.
func basenameOf(value cwlcore.FileOrDirectory) string {
	fields := fieldsOf(value)
	if fields.basename != "" {
		return fields.basename
	}

	return fields.ref().name
}

// pathOf returns the local host path of a File/Directory, or "" for literals/remote resources.
func pathOf(value cwlcore.FileOrDirectory) string {
	return fieldsOf(value).ref().local
}

// stageFields holds the naming fields of a File or Directory.
type stageFields struct {
	basename string
	path     string
	location string
}

// fieldsOf reads the naming fields of a File or Directory.
func fieldsOf(value cwlcore.FileOrDirectory) stageFields {
	if file, ok := value.(*cwlcore.File); ok && file != nil {
		return stageFields{basename: file.Basename, path: file.Path, location: file.Location}
	}

	if dir, ok := value.(*cwlcore.Directory); ok && dir != nil {
		return stageFields{basename: dir.Basename, path: dir.Path, location: dir.Location}
	}

	return stageFields{basename: "", path: "", location: ""}
}

// stageRef is the resolved host path and filename of a value's reference.
type stageRef struct {
	local string // absolute host path, or ""
	name  string // final path component
}

// ref resolves path (preferred) or location to a local path and name.
func (f stageFields) ref() stageRef {
	if f.path != "" {
		return stageRef{local: f.path, name: filepath.Base(f.path)}
	}

	parsed, err := url.Parse(f.location)
	if err != nil || parsed.Path == "" {
		return stageRef{local: "", name: ""}
	}

	name := path.Base(parsed.Path)
	if (parsed.Scheme != "" && parsed.Scheme != joSchemeFile) || !path.IsAbs(parsed.Path) {
		return stageRef{local: "", name: name}
	}

	return stageRef{local: filepath.Clean(parsed.Path), name: name}
}

// relocate returns a copy of a File/Directory placed at target.
func relocate(value cwlcore.FileOrDirectory, target string) cwlcore.FileOrDirectory {
	if file, ok := value.(*cwlcore.File); ok && file != nil {
		return relocateFile(file, target)
	}

	if dir, ok := value.(*cwlcore.Directory); ok && dir != nil {
		return relocateDirectory(dir, target)
	}

	return value
}

// relocateFile copies a File to a new path, re-deriving path-based fields.
func relocateFile(file *cwlcore.File, target string) *cwlcore.File {
	basename := filepath.Base(target)
	parts := outSplitName(basename)

	moved := &cwlcore.File{
		Node:           file.Node,
		Location:       outFileURI(target),
		Path:           target,
		Basename:       basename,
		Dirname:        outDirname(target),
		Nameroot:       parts.root,
		Nameext:        parts.ext,
		Checksum:       file.Checksum,
		Format:         file.Format,
		Size:           file.Size,
		Contents:       file.Contents,
		SecondaryFiles: nil,
	}

	beside := filepath.Dir(target)

	for _, secondary := range file.SecondaryFiles {
		moved.SecondaryFiles = append(moved.SecondaryFiles,
			relocate(secondary, filepath.Join(beside, basenameOf(secondary))))
	}

	return moved
}

// relocateDirectory copies a Directory to a new path with its listing.
func relocateDirectory(dir *cwlcore.Directory, target string) *cwlcore.Directory {
	moved := &cwlcore.Directory{
		Node:     dir.Node,
		Location: outFileURI(target),
		Path:     target,
		Basename: filepath.Base(target),
		Listing:  nil,
	}

	for _, entry := range dir.Listing {
		moved.Listing = append(moved.Listing, relocate(entry, filepath.Join(target, basenameOf(entry))))
	}

	return moved
}
