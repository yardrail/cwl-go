package cwlexec

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Filling in a tool's working directory.
//
// Two jobs live here. The first is turning an InitialWorkDirRequirement into placements on a
// [PathMap] — evaluating the expressions a listing is made of, and deciding what each entry means.
// The second is carrying a finished plan out against a real filesystem.
//
// They are separate because only the first is interesting to reason about and only the second needs
// a disk, so the whole of the requirement's semantics can be tested without one.

// ErrStageEntry reports an InitialWorkDirRequirement listing entry the specification does not allow:
// a listing expression that did not produce an array, a Dirent whose `entry` is text with no
// `entryname` to create it under, or an array of filesystem objects given an `entryname`.
var ErrStageEntry = errors.New("invalid InitialWorkDirRequirement listing entry")

// StageInitialWorkDir plans the placements the InitialWorkDirRequirement in scope calls for, adding
// them to mapper.
//
// inputs is the invocation's input object, eval the evaluator built for its requirement scope, and
// rt the runtime context; the listing, a Dirent's `entryname` and its `entry` may all be
// expressions, and all three are evaluated against those. A scope with no such requirement — and a
// nil scope — plans nothing and is not an error.
//
// Nothing is written here. Call [PathMap.Apply] to carry the plan out.
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
		types:  &outputCollector{outdir: mapper.Workdir()},
		ctx:    &cwlcore.EvalContext{Inputs: outExpressionObject(inputs), Runtime: rt},
	}

	return stager.listing(requirement.Listing)
}

// initialWorkDir resolves the InitialWorkDirRequirement in effect for a scope. A declaration in
// hints counts: staging files a tool asked for can only make more documents work, and refusing an
// advisory declaration would make one that a conforming engine runs fail here.
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

// inplaceUpdate reports whether an InplaceUpdateRequirement in scope has turned `inplaceUpdate` on.
//
// A declaration in hints counts, and the specification asks for exactly that: "Workflow authors
// should provide this in the `hints` section." It is safe to honour advisorily because the feature
// is an optimization rather than a semantic change — "the intent of this feature is that workflows
// produce the same results whether or not InplaceUpdateRequirement is supported by the
// implementation" — so a document that means it gets it, and one that does not is unaffected.
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
	// mapper is the plan being built.
	mapper *PathMap

	// eval evaluates the expressions a listing carries.
	eval *cwlcore.Evaluator

	// types converts the File and Directory objects an expression produced back into the
	// engine's typed values, resolving their relative paths against the working directory.
	types *outputCollector

	// ctx is the symbol environment every one of those expressions is evaluated against.
	ctx *cwlcore.EvalContext
}

// listing plans a whole InitialWorkDirRequirement listing, in either of the two forms the schema
// allows: a written-out array of entries, or a single expression that produces the array.
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

// listingExpression plans a listing written as one expression, whose result must be the array of
// entries the written-out form would have held.
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

// entry plans one written-out listing entry, dispatching on which member of the union it holds.
//
// An explicit null stages nothing: "Expressions may return null, in which case they have no effect."
// The same reading covers the unset kind, which a malformed entry decodes to.
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

// dirent plans one Dirent: its `entryname` names the result and its `entry` supplies it, and either
// may be an expression.
//
// The two are evaluated through different entry points, and that is the whole of what makes a staged
// file the size the document asked for. `entryname` is a document field, so it goes through
// [cwlcore.Evaluator.EvalString], which strips surrounding whitespace — around a filename that
// whitespace is punctuation. `entry` is file content, so it goes through
// [cwlcore.Evaluator.EvalContent], which does not: `entry: |` ends its text with a newline, and that
// newline is data. Conformance tests initial_workdir_trailingnl and
// escaping_expression_no_extra_quotes each measure one byte of it.
//
// A nil Dirent stages nothing. cwlcore's union accessor returns one for an entry whose kind says
// Dirent and whose payload is missing, which a malformed document can produce.
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

// expression plans one listing entry written as a bare expression rather than as a Dirent.
//
// Such an entry carries no `entryname`, so whatever it produces is staged read-only under its own
// basename — and text, which is the one shape that needs a name, is rejected by
// [workDirStager.contents]. Nothing it can legally produce is file content this engine writes out
// verbatim, so it is evaluated as the document field it is, whitespace stripped.
func (s *workDirStager) expression(expr cwlcore.Expression) error {
	value, err := s.eval.Eval(string(expr), s.ctx)
	if err != nil {
		return err
	}

	return s.value(value, "", false)
}

// value plans whatever an entry evaluated to, by the four rules CommandLineTool.yml gives Dirent's
// `entry` field: text becomes a file, a File or Directory object is staged, an array of them is
// staged element by element, null does nothing, and anything else is serialized to JSON and becomes
// a file.
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

// list plans an array-valued entry. The array members must all be filesystem objects, because the
// only other thing an array can mean is a value to serialize, which the array as a whole already
// covers.
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

// objects plans an array of filesystem objects, each under its own basename.
//
// CommandLineTool.yml is explicit that an `entryname` here is a mistake rather than something to
// apply to one of them: it is "Invalid when `entry` evaluates to an array of File or Directory
// objects".
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

// object plans one File or Directory, under the entry name when there is one and under its own
// basename otherwise.
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

// contents plans a file of literal text, which is the one entry shape the specification requires an
// `entryname` for: it is "Required when `entry` evaluates to file contents only".
func (s *workDirStager) contents(name, text string) error {
	if name == "" {
		return fmt.Errorf("%w: an entry evaluating to text requires an entryname", ErrStageEntry)
	}

	_, err := s.mapper.StageContents(name, text)

	return err
}

// serialized plans a file holding the JSON rendering of a value that is neither text nor a
// filesystem object: "If the value is an expression that evaluates to some other array, number, or
// object not consisting of File or Directory objects, a new file must be created with the value
// serialized to JSON text as the file contents."
//
// The specification asks that the serialization "should match the behavior of string interpolation
// of Parameter references", so it is [cwlcore.EncodeJSON] that renders it — the very function the
// evaluator interpolates with, exported for exactly this. That layout is Python's json.dumps, ", "
// between entries and ": " after a key, because the conformance suite was authored against cwltool;
// iwd-jsondump1 compares such a file byte for byte, and 9999 array elements come to 9998 spaces that
// Go's compact encoding would not have written. A second implementation of the layout here would be
// a second thing to keep in step with the suite.
//
// It is never reached with a string, which [workDirStager.value] has already routed to
// [workDirStager.contents] as the file literal it is. [cwlcore.ToExpressionValue] runs first for the
// same reason the evaluator runs it before interpolating a resolved reference: an array that mixes
// filesystem objects with something else arrives here still holding typed values, and this way there
// is one definition of what a File looks like once written down rather than one per package.
func (s *workDirStager) serialized(name string, value any) error {
	return s.contents(name, cwlcore.EncodeJSON(cwlcore.ToExpressionValue(value)))
}

// Apply carries out the plan against a real filesystem, in the order the placements were made.
//
// It is always *this* host's filesystem, which is why every placement is carried out at its Host
// rather than at its Target: under a container the two are paths in different namespaces, and only
// one of them is a path this process can write to. See the pathmap.go package comment.
//
// It is safe to re-run over a directory a previous attempt half-filled, which is what a resumed
// invocation needs: every placement replaces whatever is at its target rather than expecting it to
// be absent.
func (m *PathMap) Apply() error {
	for index := range m.plan {
		mapping := &m.plan[index]

		err := m.applyMapping(mapping)
		if err != nil {
			return fmt.Errorf("staging %q: %w", mapping.Host, err)
		}
	}

	return nil
}

// applyMapping carries out one placement, on this host.
//
// A placement with no Host is one the executor places for itself by mounting Resolved at Target, and
// there is nothing here to do for it.
func (m *PathMap) applyMapping(mapping *PathMapping) error {
	if mapping.Host == "" || mapping.Resolved == mapping.Host {
		return nil
	}

	err := os.MkdirAll(filepath.Dir(mapping.Host), stageDirPerm)
	if err != nil {
		return err
	}

	switch mapping.Action {
	case StageMkdir:
		return os.MkdirAll(mapping.Host, stageDirPerm)
	case StageWrite:
		return replaceWith(mapping.Host, func() error {
			return os.WriteFile(mapping.Host, []byte(mapping.Contents), stageFilePerm)
		})
	case StageLink:
		return replaceWith(mapping.Host, func() error {
			return m.placeLink(mapping)
		})
	case StageCopy:
		return replaceWith(mapping.Host, func() error {
			return copyTo(mapping.Resolved, mapping.Host)
		})
	default:
		return fmt.Errorf("%w: unknown staging action %q", ErrStageValue, mapping.Action)
	}
}

// placeLink places a [StageLink] mapping, which is the one placement whose answer differs between
// a host invocation and a contained one.
//
// On this host the link *is* the placement: it puts the original bytes at the planned path without
// copying them, which is what "link" means and what makes staging a large input free.
//
// Inside a container the same link would be a trap, and the reason is a property of bind mounts
// rather than of anything in the specification. The executor mounts Resolved at Target, and a
// container engine resolves a mount's target before it mounts there: a symbolic link at that path
// is *followed*, so the engine creates the link's destination — a path on this host, spelled inside
// the container — and mounts there instead. That destination is created as root, and if it falls
// under one of the directories mounted whole it is created on this filesystem, leaving a root-owned
// file this process can neither read back nor remove. So what the link's place is taken by is an
// empty mount point of the same kind as the bytes, which the engine mounts over and which belongs
// to this process either way.
//
// The link is not lost, only deferred: [PathMap.Relink] restores it once the container has exited
// and the mount is gone. This is cwltool's relink_initialworkdir, arrived at from the same
// constraint.
func (m *PathMap) placeLink(mapping *PathMapping) error {
	if !m.contained {
		return os.Symlink(mapping.Resolved, mapping.Host)
	}

	return mountPoint(mapping.Resolved, mapping.Host)
}

// mountPoint creates an empty file or directory at host for source to be bind-mounted over. A
// directory can only be mounted onto a directory and a file only onto a file, so the kind has to
// match.
func mountPoint(source, host string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return os.MkdirAll(host, stageDirPerm)
	}

	file, err := os.OpenFile(host, os.O_CREATE|os.O_EXCL|os.O_WRONLY, stageFilePerm)
	if err != nil {
		return err
	}

	return file.Close()
}

// Relink replaces the mount points a contained invocation staged with the symbolic links a host
// invocation would have placed to begin with. It is called once the tool has exited, when the
// mounts that stood in for those links are gone.
//
// Without it every linked input is an empty file on this host, and output collection reads exactly
// that: a document whose output globs a file it also staged — an InitialWorkDirRequirement entry it
// then names as an output, which is the ordinary shape of an in-place update — would publish zero
// bytes. cwltool's relink_initialworkdir does the same thing for the same reason.
//
// It is a no-op without a container, where [PathMap.Apply] placed the links already.
func (m *PathMap) Relink() error {
	if !m.contained {
		return nil
	}

	for index := range m.plan {
		mapping := &m.plan[index]

		if mapping.Action != StageLink || mapping.Host == "" {
			continue
		}

		err := replaceWith(mapping.Host, func() error {
			return os.Symlink(mapping.Resolved, mapping.Host)
		})
		if err != nil {
			return fmt.Errorf("relinking %q: %w", mapping.Host, err)
		}
	}

	return nil
}

// replaceWith clears whatever is already at target and then runs place.
//
// Clearing first is what makes a re-run idempotent, and it is also the only way to link over a
// target that already exists: [os.Symlink] refuses one, and writing through a stale link left by an
// earlier attempt would modify the file it points at rather than replace it.
func replaceWith(target string, place func() error) error {
	err := os.RemoveAll(target)
	if err != nil {
		return err
	}

	return place()
}

// copyTo copies a file or a whole directory tree to target, preserving the permission bits so that
// a staged program is still executable.
func copyTo(source, target string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return copyTree(source, target)
	}

	return copyFile(source, target, info.Mode().Perm())
}

// copyTree copies a directory and everything under it.
//
// The walk is written by hand rather than with [filepath.WalkDir] so that every failure it can
// report is one this engine can actually produce and test: an unreadable directory, a dangling
// symlink, an unreadable file. WalkDir would fold all three into one callback error and add two
// more that cannot happen.
func copyTree(source, target string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}

	err = os.MkdirAll(target, stageDirPerm)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		err = copyEntry(filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name()), entry)
		if err != nil {
			return err
		}
	}

	return nil
}

// copyEntry copies one member of a directory tree.
func copyEntry(source, target string, entry fs.DirEntry) error {
	if entry.IsDir() {
		return copyTree(source, target)
	}

	return copyPath(source, target)
}

// copyPath copies one file, taking its mode from the file itself. A symbolic link is followed, so a
// link to something that is not there is a failure rather than a broken link in the copy.
func copyPath(source, target string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}

	return copyFile(source, target, info.Mode().Perm())
}

// copyFile copies one file's bytes, creating the destination with perm.
func copyFile(source, target string, perm fs.FileMode) error {
	src, err := os.Open(filepath.Clean(source))
	if err != nil {
		return err
	}

	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return errors.Join(err, src.Close())
	}

	_, copied := io.Copy(dst, src)

	return errors.Join(copied, dst.Close(), src.Close())
}
