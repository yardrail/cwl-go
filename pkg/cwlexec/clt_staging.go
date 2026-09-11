package cwlexec

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// InitialWorkDirRequirement planning and filesystem application.

// ErrStageEntry reports an invalid InitialWorkDirRequirement listing entry.
var ErrStageEntry = errors.New("invalid InitialWorkDirRequirement listing entry")

// StageInitialWorkDir plans the placements for an InitialWorkDirRequirement onto mapper.
// Call [PathMap.Apply] to execute the plan.
func StageInitialWorkDir(mapper *PathMap, scope *cwlcore.RequirementScope, inputs map[string]any,
	eval *cwlcore.Evaluator, rt cwlcore.RuntimeContext,
) error {
	if inplaceUpdate(scope) {
		mapper.AllowInplaceUpdate()
	}

	requirement, found := initialWorkDir(scope)
	if !found {
		return nil
	}

	stager := &workDirStager{
		mapper: mapper,
		eval:   eval,
		types: &outputCollector{
			tool:   nil,
			eval:   nil,
			scope:  nil,
			inputs: nil,
			roots:  nil,
			runtime: cwlcore.RuntimeContext{
				Cores:      nil,
				RAM:        nil,
				OutdirSize: nil,
				TmpdirSize: nil,
				ExitCode:   nil,
				Outdir:     "",
				Tmpdir:     "",
			},
			outdir:   mapper.Workdir(),
			outfs:    nil,
			outroot:  "",
			exitCode: 0,
		},
		ctx: &cwlcore.EvalContext{Inputs: outExpressionObject(inputs), Self: nil, Runtime: rt},
	}

	return stager.listing(requirement.Listing)
}

// initialWorkDir resolves the InitialWorkDirRequirement from scope (including hints).
func initialWorkDir(scope *cwlcore.RequirementScope) (*cwlcore.InitialWorkDirRequirement, bool) {
	if scope == nil {
		return nil, false
	}

	requirement, found, _ := scope.GetRequirement(cwlcore.ClassInitialWorkDirRequirement)
	if !found {
		return nil, false
	}

	typed, ok := requirement.(*cwlcore.InitialWorkDirRequirement)

	return typed, ok
}

// inplaceUpdate reports whether InplaceUpdateRequirement enables in-place updates.
func inplaceUpdate(scope *cwlcore.RequirementScope) bool {
	if scope == nil {
		return false
	}

	requirement, found, _ := scope.GetRequirement(cwlcore.ClassInplaceUpdateRequirement)
	if !found {
		return false
	}

	typed, ok := requirement.(*cwlcore.InplaceUpdateRequirement)

	return ok && typed.InplaceUpdate
}

// workDirStager carries the fixed context of one [StageInitialWorkDir] call.
type workDirStager struct {
	mapper *PathMap
	eval   *cwlcore.Evaluator
	types  *outputCollector    // Retypes expression results as cwlcore values.
	ctx    *cwlcore.EvalContext
}

// listing plans all entries of an InitialWorkDirRequirement listing.
func (s *workDirStager) listing(declared cwlcore.InitialWorkDirListing) error {
	if declared.Kind() == cwlcore.ValueExpression {
		return s.listingExpression(declared.Expression())
	}

	for index, entry := range declared.Entries() {
		err := s.entry(entry)
		if err != nil {
			return fmt.Errorf("InitialWorkDirRequirement listing entry %d: %w", index, err)
		}
	}

	return nil
}

// listingExpression plans a listing from a single expression that must produce an array.
func (s *workDirStager) listingExpression(expr cwlcore.Expression) error {
	value, err := s.eval.Eval(string(expr), s.ctx)
	if err != nil {
		return err
	}

	items, ok := value.([]any)
	if !ok {
		return fmt.Errorf("%w: a listing expression must produce an array, got %s",
			ErrStageEntry, cwlcore.TypeName(value))
	}

	for index, item := range items {
		err = s.value(item, "", false)
		if err != nil {
			return fmt.Errorf("InitialWorkDirRequirement listing entry %d: %w", index, err)
		}
	}

	return nil
}

// entry plans one listing entry, dispatching by union kind. Null entries are no-ops.
func (s *workDirStager) entry(declared cwlcore.InitialWorkDirEntry) error {
	switch declared.Kind() {
	case cwlcore.ValueDirent:
		return s.dirent(declared.Dirent())
	case cwlcore.ValueExpression:
		return s.expression(declared.Expression())
	case cwlcore.ValueFile:
		return s.object(declared.File(), "", false)
	case cwlcore.ValueDirectory:
		return s.object(declared.Directory(), "", false)
	case cwlcore.ValueList:
		return s.objects(declared.Objects(), "", false)
	default:
		return nil
	}
}

// dirent plans one Dirent entry. entryname is whitespace-trimmed; entry preserves content exactly.
func (s *workDirStager) dirent(declared *cwlcore.Dirent) error {
	if declared == nil {
		return nil
	}

	name, err := s.eval.EvalString(string(declared.Entryname), s.ctx)
	if err != nil {
		return err
	}

	value, err := s.eval.EvalContent(string(declared.Entry), s.ctx)
	if err != nil {
		return err
	}

	return s.value(value, name, declared.Writable)
}

// expression plans a bare expression entry (no entryname, staged under its own basename).
func (s *workDirStager) expression(expr cwlcore.Expression) error {
	value, err := s.eval.Eval(string(expr), s.ctx)
	if err != nil {
		return err
	}

	return s.value(value, "", false)
}

// value plans an evaluated entry: text becomes a file, objects are staged, null is skipped.
func (s *workDirStager) value(value any, name string, writable bool) error {
	if value == nil {
		return nil
	}

	if text, ok := value.(string); ok {
		return s.contents(name, text)
	}

	typed, err := s.types.retypeValue(value)
	if err != nil {
		return err
	}

	switch object := typed.(type) {
	case cwlcore.FileOrDirectory:
		return s.object(object, name, writable)
	case []any:
		return s.list(object, name, writable)
	default:
		return s.serialized(name, value)
	}
}

// list plans an array-valued entry. Non-filesystem-object arrays are serialized to JSON.
func (s *workDirStager) list(values []any, name string, writable bool) error {
	objects := make([]cwlcore.FileOrDirectory, 0, len(values))

	for _, value := range values {
		object, ok := value.(cwlcore.FileOrDirectory)
		if !ok {
			return s.serialized(name, values)
		}

		objects = append(objects, object)
	}

	return s.objects(objects, name, writable)
}

// objects plans an array of filesystem objects. An entryname on an array is an error.
func (s *workDirStager) objects(values []cwlcore.FileOrDirectory, name string, writable bool) error {
	if name != "" {
		return fmt.Errorf("%w: entryname %q cannot name an array of %d filesystem objects",
			ErrStageEntry, name, len(values))
	}

	for _, value := range values {
		err := s.object(value, "", writable)
		if err != nil {
			return err
		}
	}

	return nil
}

// object plans one File or Directory under the given name or its own basename.
func (s *workDirStager) object(value cwlcore.FileOrDirectory, name string, writable bool) error {
	if name == "" {
		name = basenameOf(value)
	}

	if name == "" {
		return fmt.Errorf("%w: a staged %s with no basename needs an entryname",
			ErrStageEntry, cwlcore.TypeName(value))
	}

	return s.mapper.Stage(value, name, writable)
}

// contents plans a literal text file. Requires an entryname.
func (s *workDirStager) contents(name, text string) error {
	if name == "" {
		return fmt.Errorf("%w: an entry evaluating to text requires an entryname", ErrStageEntry)
	}

	_, err := s.mapper.StageContents(name, text)

	return err
}

// serialized plans a JSON file from a non-text, non-filesystem value.
func (s *workDirStager) serialized(name string, value any) error {
	return s.contents(name, cwlcore.EncodeJSON(cwlcore.ToExpressionValue(value)))
}

// Apply executes the staging plan against real filesystems. Idempotent for resumed runs.
func (m *PathMap) Apply(outFS, stageFS WriteFS) error {
	for index := range m.plan {
		mapping := &m.plan[index]

		err := m.applyMapping(mapping, outFS, stageFS)
		if err != nil {
			return fmt.Errorf("staging %q: %w", mapping.Host, err)
		}
	}

	return nil
}

// fsResolution pairs a target filesystem with the relative path within it.
type fsResolution struct {
	fs  WriteFS
	rel string
}

// resolvePlacement resolves the target FS and relative path, creating parent dirs.
func (m *PathMap) resolvePlacement(host string, outFS, stageFS WriteFS) (fsResolution, error) {
	parent, resolveErr := m.resolveFS(filepath.Dir(host), outFS, stageFS)
	if resolveErr != nil {
		return fsResolution{fs: nil, rel: ""}, resolveErr
	}

	mkdirErr := parent.fs.MkdirAll(parent.rel, stageDirPerm)
	if mkdirErr != nil {
		return fsResolution{fs: nil, rel: ""}, mkdirErr
	}

	return m.resolveFS(host, outFS, stageFS)
}

// applyMapping executes one placement. No-host mappings are executor bind-mounts.
func (m *PathMap) applyMapping(mapping *PathMapping, outFS, stageFS WriteFS) error {
	if mapping.Host == "" || mapping.Resolved == mapping.Host {
		return nil
	}

	resolved, err := m.resolvePlacement(mapping.Host, outFS, stageFS)
	if err != nil {
		return err
	}

	switch mapping.Action {
	case StageMkdir:
		return resolved.fs.MkdirAll(resolved.rel, stageDirPerm)
	case StageWrite:
		return replaceWithFS(resolved.fs, resolved.rel, func() error {
			w, createErr := resolved.fs.Create(resolved.rel)
			if createErr != nil {
				return createErr
			}

			_, writeErr := io.WriteString(w, mapping.Contents)

			return errors.Join(writeErr, w.Close())
		})
	case StageLink:
		return replaceWithFS(resolved.fs, resolved.rel, func() error {
			return m.placeLinkFS(mapping, resolved.fs, resolved.rel)
		})
	case StageCopy:
		return replaceWithFS(resolved.fs, resolved.rel, func() error {
			return copyToFS(mapping.Resolved, resolved.fs, resolved.rel)
		})
	default:
		return fmt.Errorf("%w: unknown staging action %q", ErrStageValue, mapping.Action)
	}
}

// resolveFS maps an absolute host path to its WriteFS and relative path.
func (m *PathMap) resolveFS(host string, outFS, stageFS WriteFS) (fsResolution, error) {
	if strings.HasPrefix(host, m.hostStaging+string(filepath.Separator)) || host == m.hostStaging {
		rel, relErr := filepath.Rel(m.hostStaging, host)
		if relErr != nil {
			return fsResolution{fs: nil, rel: ""}, relErr
		}

		return fsResolution{fs: stageFS, rel: filepath.ToSlash(rel)}, nil
	}

	rel, relErr := filepath.Rel(m.hostWorkdir, host)
	if relErr != nil {
		return fsResolution{fs: nil, rel: ""}, relErr
	}

	return fsResolution{fs: outFS, rel: filepath.ToSlash(rel)}, nil
}

// placeLinkFS symlinks on host; creates a mount point under containers (relinked after exit).
func (m *PathMap) placeLinkFS(mapping *PathMapping, dst WriteFS, rel string) error {
	if !m.contained {
		return dst.Symlink(mapping.Resolved, rel)
	}

	return mountPointFS(mapping.Resolved, dst, rel)
}

// mountPointFS creates an empty file or directory for bind-mounting. Kind must match source.
func mountPointFS(source string, dst WriteFS, rel string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return dst.MkdirAll(rel, stageDirPerm)
	}

	w, err := dst.Create(rel)
	if err != nil {
		return err
	}

	return w.Close()
}

// Relink replaces container mount points with symlinks after the container exits.
// No-op for host invocations.
func (m *PathMap) Relink(outFS, stageFS WriteFS) error {
	if !m.contained {
		return nil
	}

	for index := range m.plan {
		mapping := &m.plan[index]

		if mapping.Action != StageLink || mapping.Host == "" {
			continue
		}

		resolved, err := m.resolveFS(mapping.Host, outFS, stageFS)
		if err != nil {
			return fmt.Errorf("relinking %q: %w", mapping.Host, err)
		}

		err = replaceWithFS(resolved.fs, resolved.rel, func() error {
			return resolved.fs.Symlink(mapping.Resolved, resolved.rel)
		})
		if err != nil {
			return fmt.Errorf("relinking %q: %w", mapping.Host, err)
		}
	}

	return nil
}

// replaceWithFS removes any existing target then runs place. Makes re-runs idempotent.
func replaceWithFS(dst WriteFS, rel string, place func() error) error {
	err := dst.Remove(rel)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	return place()
}

// copyToFS copies source into dst at rel, preserving permissions.
func copyToFS(source string, dst WriteFS, rel string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return copyTreeFS(source, dst, rel)
	}

	return copyFileFS(source, dst, rel, info.Mode().Perm())
}

// copyTreeFS copies a directory and everything under it into a [WriteFS].
func copyTreeFS(source string, dst WriteFS, rel string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}

	err = dst.MkdirAll(rel, stageDirPerm)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		childSrc := filepath.Join(source, entry.Name())
		childRel := rel + "/" + entry.Name()

		err = copyEntryFS(childSrc, dst, childRel, entry)
		if err != nil {
			return err
		}
	}

	return nil
}

// copyEntryFS copies one member of a directory tree into a [WriteFS].
func copyEntryFS(source string, dst WriteFS, rel string, entry fs.DirEntry) error {
	if entry.IsDir() {
		return copyTreeFS(source, dst, rel)
	}

	return copyPathFS(source, dst, rel)
}

// copyPathFS copies one file into a [WriteFS], taking its mode from the source.
func copyPathFS(source string, dst WriteFS, rel string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}

	return copyFileFS(source, dst, rel, info.Mode().Perm())
}

// copyFileFS reads from a host path and writes through a [WriteFS].
func copyFileFS(source string, dst WriteFS, rel string, _ fs.FileMode) error {
	src, err := os.Open(filepath.Clean(source))
	if err != nil {
		return err
	}
	defer src.Close()

	w, err := dst.Create(rel)
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(w, src)

	return errors.Join(copyErr, w.Close())
}
