package cwlexec

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// ErrFilesystemEntry reports a member of a secondaryFiles or listing array that is not a File or
// Directory object. The schema types both fields as arrays of exactly those two, so anything else
// is a mistake in the expression that produced it.
var ErrFilesystemEntry = errors.New("secondaryFiles or listing entry is not a File or Directory")

// The field names of a File, a Directory and the class discriminator they share.
//
// This is the package's only copy: the job-order path reads the same names, and a second set would
// be a second thing to keep in step with the schema. pkg/cwlcore keeps an unexported copy of its
// own — it cannot import this package, and exporting thirteen constants to reach literal
// single-sourcing would widen its API for no behavioural gain — and TestFileFieldNamesMatchCwlcore
// asserts the two agree so that a divergence is a failing test rather than a silent one.
const (
	outKeyClass          = "class"
	outKeyLocation       = "location"
	outKeyPath           = "path"
	outKeyBasename       = "basename"
	outKeyDirname        = "dirname"
	outKeyNameroot       = "nameroot"
	outKeyNameext        = "nameext"
	outKeyChecksum       = "checksum"
	outKeySize           = "size"
	outKeyFormat         = "format"
	outKeyContents       = "contents"
	outKeySecondaryFiles = "secondaryFiles"
	outKeyListing        = "listing"
)

// The bridge between the engine's typed filesystem values and the shape a CWL expression sees.
//
// A File is a [*cwlcore.File] everywhere in this package: that is what [LoadJobOrder] produces and
// what [CollectOutputs] returns, and cwlcore's own documentation gives keeping one idea of what a
// File is as the reason the types exist at all. cwlcore's expression evaluator, though, reads
// objects as string-keyed maps — it has no knowledge of the Go types — so `self.basename` inside an
// outputEval would find nothing without a translation.
//
// That translation is [cwlcore.ToExpressionValue], and this package uses it rather than owning a
// second copy. Which fields appear is not cosmetic — an empty `size` is the size of an empty file,
// an empty `nameext` is the extension of a name that has none, and a nil `listing` means nobody
// read the directory — so two implementations of the rule are two answers a document can get for
// the same File depending on which side of the engine rendered it.

// outExpressionObject renders every field of an object, which is how a record input or a job's
// whole input object reaches an expression with its File values translated.
//
// It exists only because [cwlcore.EvalContext].Inputs is typed as a map: ToExpressionValue returns
// any, and unpacking that would need a type assertion at every call site. The rendering itself is
// entirely cwlcore's.
func outExpressionObject(object map[string]any) map[string]any {
	rendered := make(map[string]any, len(object))
	for key, value := range object {
		rendered[key] = cwlcore.ToExpressionValue(value)
	}

	return rendered
}

// outWiden views a list of filesystem values as the plain list an output port carries.
//
// This is a widening and not a rendering: the elements stay typed. It is what hands a globbed
// result to a path rewriter or to an output port, both of which want the values themselves.
func outWiden(values []cwlcore.FileOrDirectory) []any {
	widened := make([]any, 0, len(values))
	for _, value := range values {
		widened = append(widened, value)
	}

	return widened
}

// outTextField reads a string-valued field, treating an absent field and a non-string one alike as
// empty.
func outTextField(object map[string]any, key string) string {
	text, ok := object[key].(string)
	if !ok {
		return ""
	}

	return text
}

// retypeValue converts the File and Directory objects an expression produced back into the engine's
// typed representation, walking through lists and plain objects to reach them.
//
// The reference implementation does the same walk — visit_class(result, ("File", "Directory"), ...)
// — because an outputEval is free to return a File nested inside whatever structure it likes.
func (c *outputCollector) retypeValue(value any) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		return c.retypeObject(typed)
	case []any:
		return c.retypeList(typed)
	default:
		return value, nil
	}
}

// retypeList re-types every element of a list.
func (c *outputCollector) retypeList(values []any) ([]any, error) {
	retyped := make([]any, 0, len(values))

	for _, value := range values {
		item, err := c.retypeValue(value)
		if err != nil {
			return nil, err
		}

		retyped = append(retyped, item)
	}

	return retyped, nil
}

// retypeObject re-types one object, which is a File or a Directory when its class says so and an
// ordinary record otherwise.
func (c *outputCollector) retypeObject(object map[string]any) (any, error) {
	switch outTextField(object, outKeyClass) {
	case cwlcore.ClassFile:
		return c.retypeFile(object)
	case cwlcore.ClassDirectory:
		return c.retypeDirectory(object)
	default:
		return c.retypeFields(object)
	}
}

// retypeFields re-types every field of a plain object.
func (c *outputCollector) retypeFields(object map[string]any) (map[string]any, error) {
	retyped := make(map[string]any, len(object))

	for key, value := range object {
		field, err := c.retypeValue(value)
		if err != nil {
			return nil, err
		}

		retyped[key] = field
	}

	return retyped, nil
}

// retypeFile builds a File from the object an expression produced, deriving the fields the
// specification says the implementation must set and measuring the file when the expression did not
// give a checksum or a size.
func (c *outputCollector) retypeFile(object map[string]any) (*cwlcore.File, error) {
	ref := c.deriveRef(object)
	parts := outSplitName(ref.basename)

	file := &cwlcore.File{
		Location: ref.location,
		Path:     ref.local,
		Basename: ref.basename,
		Dirname:  outDirname(ref.local),
		Nameroot: parts.root,
		Nameext:  parts.ext,
		Checksum: outTextField(object, outKeyChecksum),
		Format:   outTextField(object, outKeyFormat),
	}

	if contents, ok := object[outKeyContents].(string); ok {
		file.Contents = cwlcore.NewOptString(contents)
	}

	if size, ok := outNumber(object[outKeySize]); ok {
		file.Size = cwlcore.NewOptInt(size)
	}

	secondary, err := c.retypeEntries(object[outKeySecondaryFiles])
	if err != nil {
		return nil, err
	}

	file.SecondaryFiles = secondary
	outRemeasure(file)

	return file, nil
}

// outRemeasure fills in a File's size and checksum when the expression that produced it supplied
// neither and the bytes are reachable.
//
// A File that names a path is measured from disk, and a file literal from its own contents; a File
// whose path does not exist is left as it is rather than rejected, because an expression may
// legitimately describe a file some later stage will create.
func outRemeasure(file *cwlcore.File) {
	if file.Checksum != "" && file.Size.IsSet() {
		return
	}

	if file.Path == "" {
		outMeasureLiteral(file)

		return
	}

	stats, err := outDigest(file.Path)
	if err != nil {
		return
	}

	file.Size = cwlcore.NewOptInt(stats.size)
	file.Checksum = stats.checksum
}

// retypeDirectory builds a Directory from the object an expression produced.
func (c *outputCollector) retypeDirectory(object map[string]any) (*cwlcore.Directory, error) {
	ref := c.deriveRef(object)

	listing, err := c.retypeEntries(object[outKeyListing])
	if err != nil {
		return nil, err
	}

	return &cwlcore.Directory{
		Location: ref.location,
		Path:     ref.local,
		Basename: ref.basename,
		Listing:  listing,
	}, nil
}

// retypeEntries re-types a secondaryFiles or listing field, which is a list of File and Directory
// values and nothing else. An absent field stays nil, preserving the distinction between a listing
// nobody read and an empty one.
func (c *outputCollector) retypeEntries(value any) ([]cwlcore.FileOrDirectory, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, nil
	}

	entries := make([]cwlcore.FileOrDirectory, 0, len(items))

	for _, item := range items {
		entry, err := c.retypeEntry(item)
		if err != nil {
			return nil, err
		}

		entries = append(entries, entry)
	}

	return entries, nil
}

// retypeEntry re-types one member of a secondaryFiles or listing list.
func (c *outputCollector) retypeEntry(value any) (cwlcore.FileOrDirectory, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: got %s", ErrFilesystemEntry, cwlcore.TypeName(value))
	}

	if outTextField(object, outKeyClass) == cwlcore.ClassDirectory {
		return c.retypeDirectory(object)
	}

	return c.retypeFile(object)
}

// outRef is a filesystem value's identity once the derivable parts have been filled in.
type outRef struct {
	// location is the absolute IRI naming the resource.
	location string

	// local is the filesystem path the location names, empty for a resource that is not on a
	// local filesystem and for a literal.
	local string

	// basename is the value's name without any leading path.
	basename string
}

// deriveRef completes a filesystem value's location, path and basename from whichever of the three
// the expression supplied.
//
// A relative reference resolves against the output directory, which is the directory the
// expression's own inputs came out of. A location naming any scheme but file: keeps its location
// and gets no local path: fetching a remote resource is a staging concern, and pretending its IRI
// is a path would produce a checksum of nothing.
func (c *outputCollector) deriveRef(object map[string]any) outRef {
	ref := outRef{
		location: outTextField(object, outKeyLocation),
		local:    outTextField(object, outKeyPath),
		basename: outTextField(object, outKeyBasename),
	}

	if ref.local == "" {
		ref.local = c.localPath(ref.location)
	} else {
		ref.local = outAbsolutize(ref.local, c.outdir)
	}

	if ref.local != "" {
		ref.location = outFileURI(ref.local)
	}

	if ref.basename == "" && ref.local != "" {
		ref.basename = filepath.Base(ref.local)
	}

	return ref
}

// localPath returns the absolute filesystem path a location names, and "" when it names a resource
// on some other kind of storage.
func (c *outputCollector) localPath(location string) string {
	parsed, err := url.Parse(location)
	if err != nil || (parsed.Scheme != "" && parsed.Scheme != joSchemeFile) {
		return ""
	}

	if parsed.Path == "" {
		return ""
	}

	return outAbsolutize(parsed.Path, c.outdir)
}

// The four derivations a filesystem value's fields come out of: a basename split at its extension
// boundary, a path turned into an IRI, a path's directory component, and a relative path resolved
// against a base.
//
// These are the package's only copies. A job order's File and a globbed File are the same value
// read from the same filesystem, and the specification derives their fields by the same rules, so a
// second set of these would be a second answer to "what is this file's nameext" — which is the
// question `nameroot + nameext == basename` makes it impossible to be wrong about twice.

// outNameParts is a basename divided at its extension boundary.
type outNameParts struct {
	// root is the nameroot: everything before the final period.
	root string

	// ext is the nameext: the final period and what follows it, or "".
	ext string
}

// outSplitName splits a basename into nameroot and nameext.
//
// Process.yml, nameroot: "The basename root such that `nameroot + nameext == basename`, and
// `nameext` is empty or begins with a period and contains at most one period. For the purposes of
// path splitting leading periods on the basename are ignored; a basename of `.cshrc` will have a
// nameroot of `.cshrc`."
//
// So: skip the run of leading periods, split what remains at its *last* period, and hand the
// leading periods back to the root. ".cshrc" splits as (".cshrc", ""), "a.b.c" as ("a.b", ".c"),
// ".gitignore.bak" as (".gitignore", ".bak") and "README" as ("README", ""). That is the rule
// Python's os.path.splitext implements, which is what the reference implementation calls.
func outSplitName(basename string) outNameParts {
	dots := len(basename) - len(strings.TrimLeft(basename, "."))

	last := strings.LastIndexByte(basename[dots:], '.')
	if last < 0 {
		return outNameParts{root: basename}
	}

	return outNameParts{root: basename[:dots+last], ext: basename[dots+last:]}
}

// outFileURI renders an absolute local path as a file:// IRI, percent-escaping as the URL syntax
// requires.
func outFileURI(local string) string {
	uri := url.URL{Scheme: joSchemeFile, Path: local}

	return uri.String()
}

// outDirname returns the directory component of a local path, and "" when there is no path.
//
// Process.yml, dirname: "The implementation must set this field based on the value of `path` prior
// to evaluating parameter references or expressions in a CommandLineTool document." A value with no
// path — a file literal, or a remote location — therefore has no dirname.
func outDirname(local string) string {
	if local == "" {
		return ""
	}

	return filepath.Dir(local)
}

// outAbsolutize resolves a possibly-relative filesystem path against base.
func outAbsolutize(local, base string) string {
	if filepath.IsAbs(local) {
		return filepath.Clean(local)
	}

	return filepath.Join(base, local)
}

// outNumber widens the numeric shapes an expression can produce into an int64, for the one numeric
// field a File carries.
func outNumber(value any) (int64, bool) {
	switch typed := value.(type) {
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case float64:
		return int64(typed), true
	default:
		return 0, false
	}
}

// Giving a collected value the name it declares.
//
// Process.yml, File.path: "The leaf name of this file (the final path component)... the final path
// component must match the value of `basename`." A globbed value satisfies that by construction —
// its basename *is* the final component of the path it was matched at — so nothing here fires for
// one. An expression is a different matter: an outputEval or a secondaryFiles expression may return
// a File that keeps the path it was handed and declares a different name, which is precisely how a
// document asks for an output to be renamed. `rename-outputs.cwl` does it to a secondary file,
// producing `.../secondary_file_test.accessory` under the name `secondary_file_test.txt.accessory`,
// and the two must be made to agree before anything downstream sees the value.
//
// They are made to agree by moving the file, not by rewriting the name: the name is what the
// document asked for, and the path is the thing the engine controls. The bytes do not change, so
// size and checksum stay as they were and nothing is re-measured; only the five fields derived from
// the path are recomputed.
//
// The pass runs once the value is final — after outputEval and after secondaryFiles, the two things
// that can introduce a mismatch — and before it is published. A value whose basename already agrees
// with its path is not touched, which is what keeps this from becoming a copy-everything pass: it
// moves exactly the files a document asked to have moved, and in the ordinary case moves none.

// ErrOutputRename reports an output value that could not be given the basename it declares.
var ErrOutputRename = errors.New("cannot give an output the basename it declares")

// relocate gives every File and Directory a collected value carries the name its own basename
// declares, moving it on disk wherever the two disagree.
func (c *outputCollector) relocate(value any) error {
	if file, ok := value.(*cwlcore.File); ok && file != nil {
		return c.relocateFile(file)
	}

	if dir, ok := value.(*cwlcore.Directory); ok && dir != nil {
		return c.relocateDirectory(dir)
	}

	switch typed := value.(type) {
	case []any:
		return c.relocateEach(typed)
	case map[string]any:
		return c.relocateFields(typed)
	default:
		return nil
	}
}

// relocateEach relocates every member of a list.
func (c *outputCollector) relocateEach(values []any) error {
	for _, value := range values {
		err := c.relocate(value)
		if err != nil {
			return err
		}
	}

	return nil
}

// relocateFields relocates every field of a record.
func (c *outputCollector) relocateFields(object map[string]any) error {
	for _, value := range object {
		err := c.relocate(value)
		if err != nil {
			return err
		}
	}

	return nil
}

// relocateEntries relocates every member of a secondaryFiles or listing array. This is the arm that
// carries `rename-outputs.cwl`: the value being renamed is a secondary, not the parameter itself.
func (c *outputCollector) relocateEntries(entries []cwlcore.FileOrDirectory) error {
	for _, entry := range entries {
		err := c.relocate(entry)
		if err != nil {
			return err
		}
	}

	return nil
}

// relocateFile moves one File to the name it declares, and then its own secondary files.
func (c *outputCollector) relocateFile(file *cwlcore.File) error {
	target := outMisnamed(file.Path, file.Basename)
	if target != "" {
		local, err := c.moveTo(file.Path, target)
		if err != nil {
			return err
		}

		outRepath(file, local)
	}

	return c.relocateEntries(file.SecondaryFiles)
}

// relocateDirectory moves one Directory to the name it declares, and then its listing.
//
// Everything the directory holds moves with it, so a listing that was already read describes paths
// that no longer exist. Rebasing it is what keeps those entries pointing at the bytes they always
// pointed at, and it happens before the entries are themselves relocated, so a listing entry that
// *also* declares a different name is renamed inside the directory's new home rather than its old
// one.
func (c *outputCollector) relocateDirectory(dir *cwlcore.Directory) error {
	target := outMisnamed(dir.Path, dir.Basename)
	if target != "" {
		local, err := c.moveTo(dir.Path, target)
		if err != nil {
			return err
		}

		outRebaseEntries(dir.Listing, dir.Path, local)
		dir.Location, dir.Path = outFileURI(local), local
	}

	return c.relocateEntries(dir.Listing)
}

// outMisnamed returns the path a value ought to occupy when its declared basename disagrees with the
// final component of the path it holds, and "" when there is nothing to do.
//
// A value with no path names nothing this engine can move — a file literal, or a resource on storage
// that is not a local filesystem — and one with no basename has declared no name to be held to.
func outMisnamed(local, basename string) string {
	if local == "" || basename == "" || filepath.Base(local) == basename {
		return ""
	}

	return filepath.Join(filepath.Dir(local), basename)
}

// moveTo puts whatever is at source where target says it belongs, and reports where it ended up.
//
// A source inside the output directory is renamed in place, which is the whole of the ordinary case:
// it is the tool's own output, the engine owns it, and a rename within one directory costs nothing
// whatever the file's size. A source outside it is *copied* into the output directory instead. The
// run does not own that file — it is an input, or something an expression reached for — and renaming
// it would take it away from whoever else is still using it under its real name.
func (c *outputCollector) moveTo(source, target string) (string, error) {
	if outWithinDir(c.outdir, source) {
		return target, outMoveError(os.Rename(source, target), source, target)
	}

	inside := filepath.Join(c.outdir, filepath.Base(target))

	return inside, outMoveError(copyTo(source, inside), source, inside)
}

// outMoveError names the move a failure was reported for, and passes a success through untouched.
func outMoveError(err error, source, target string) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("%w: %q to %q: %w", ErrOutputRename, source, target, err)
}

// outRepath gives a File the fields that follow from the path it now occupies.
//
// Basename is left alone. It is what the move was for, and it is already the final component of
// local; nameroot and nameext are split out of it rather than out of the path, which is the same
// thing said the way Process.yml says it.
func outRepath(file *cwlcore.File, local string) {
	parts := outSplitName(file.Basename)

	file.Location = outFileURI(local)
	file.Path = local
	file.Dirname = outDirname(local)
	file.Nameroot = parts.root
	file.Nameext = parts.ext
}

// outRebaseEntries rewrites the paths of everything inside a directory that has just been renamed.
// The entries did not move relative to it, so only the leading part of each path changes.
func outRebaseEntries(entries []cwlcore.FileOrDirectory, from, to string) {
	for _, entry := range entries {
		outRebaseEntry(entry, from, to)
	}
}

// outRebaseEntry rewrites one entry's path, and then those of everything it holds.
func outRebaseEntry(entry cwlcore.FileOrDirectory, from, to string) {
	if file, ok := entry.(*cwlcore.File); ok && file != nil {
		local, moved := outRebasePath(file.Path, from, to)
		if moved {
			outRepath(file, local)
		}

		outRebaseEntries(file.SecondaryFiles, from, to)

		return
	}

	dir, ok := entry.(*cwlcore.Directory)
	if !ok || dir == nil {
		return
	}

	local, moved := outRebasePath(dir.Path, from, to)
	if moved {
		dir.Location, dir.Path = outFileURI(local), local
	}

	outRebaseEntries(dir.Listing, from, to)
}

// outRebasePath replaces the leading from of local with to, and reports whether it did. A path that
// was never inside from did not move with it: a secondary file living beside the directory, or a
// value naming a resource on storage this engine cannot move, stays where it is.
func outRebasePath(local, from, to string) (string, bool) {
	if !outWithinDir(from, local) {
		return local, false
	}

	return filepath.Join(to, strings.TrimPrefix(local, from)), true
}

// outNoNames is what a list of names renders as when it holds none.
const outNoNames = "none"

// outQuoted renders a list of glob patterns for an error message.
func outQuoted(patterns []string) string {
	if len(patterns) == 0 {
		return outNoNames
	}

	quoted := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		quoted = append(quoted, strconv.Quote(pattern))
	}

	return strings.Join(quoted, ", ")
}
