package cwlexec

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Globbing a finished tool's output directory.
//
// CommandLineTool.yml, CommandOutputBinding.glob: "Find files or directories relative to the
// output directory, using POSIX glob(3) pathname matching. If an array is provided, find files or
// directories that match any pattern in the array. If an expression is provided, the expression
// must return a string or an array of strings, which will then be evaluated as one or more glob
// patterns. Must only match and return files/directories which actually exist."
//
// Every path a pattern matches becomes a [cwlcore.File] or [cwlcore.Directory], with the derived
// name fields filled in, and — for a File — a size and a sha1 checksum read from the bytes
// themselves. The conformance harness recomputes both from disk when it compares an output, so a
// value that is merely plausible is a failure.

// Errors reported while globbing an output directory. They are wrapped with context, so callers
// should test them with [errors.Is].
var (
	// ErrGlobEscape reports a glob pattern that resolves to a path outside the output directory.
	//
	// CommandLineTool.yml, glob: "If the value of the glob is an absolute path pattern (it does
	// begin with a slash '/') then it must refer to a path within the output directory. It is an
	// error if any glob resolves to a path outside the output directory." This is a containment
	// property rather than a stylistic one: a tool that could name `../../etc/passwd` as its own
	// output would have the engine read, checksum and publish it.
	ErrGlobEscape = errors.New("glob pattern resolves outside the output directory")

	// ErrGlobSymlink reports a globbed path that is, or leads through, a symlink whose target is
	// under neither the output directory nor any directory an input came out of.
	//
	// CommandLineTool.yml, glob: "It is an error if a symlink in the output directory (or any
	// symlink in a chain of links) refers to any file or directory that is not under an input or
	// output directory." [ErrGlobEscape] is the same containment property applied to the pattern;
	// this one applies it to what the matched path turns out to point at, which a pattern spelled
	// entirely inside the output directory can still get wrong.
	ErrGlobSymlink = errors.New("globbed symlink leads outside the output and input directories")

	// ErrGlobPattern reports a glob pattern that is not valid glob(3) syntax.
	ErrGlobPattern = errors.New("malformed glob pattern")

	// ErrGlobValue reports a glob expression that evaluated to something other than a string or
	// an array of strings.
	ErrGlobValue = errors.New("glob expression did not produce a string or array of strings")

	// ErrContentsTooLarge reports a loadContents read of a file over the specification's 64 KiB
	// ceiling.
	//
	// CommandLineTool.yml, loadContents: "the file (or each file in the array) must be a UTF-8
	// text file 64 KiB or smaller ... If the size of the file is greater than 64 KiB, the
	// implementation must raise a fatal error." The v1.2 changelog records this as a deliberate
	// change: "When using `loadContents` it now must fail when attempting to load a file greater
	// than 64 KiB instead of silently truncating the data". Truncation is not an option.
	ErrContentsTooLarge = errors.New("loadContents: file is over the 64 KiB limit")

	// ErrContentsNotText reports a loadContents read of a file that is not valid UTF-8, which the
	// same sentence of the specification requires it to be.
	ErrContentsNotText = errors.New("loadContents: file is not UTF-8 text")
)

// globValues resolves the patterns against the output directory and builds a File or Directory
// value for every path they match, in pattern order and sorted within each pattern.
//
// Sorting is what makes the result reproducible: the order glob(3) reports matches in is not
// specified, and an output array whose order changed between two identical runs would make every
// downstream comparison — including the conformance harness's — unreliable. Patterns are not
// merged before sorting, so a document that writes two patterns gets the first pattern's matches
// ahead of the second's, which is the order it asked for.
func (c *outputCollector) globValues(
	patterns []string, binding *cwlcore.CommandOutputBinding,
) ([]cwlcore.FileOrDirectory, error) {
	collected := make([]cwlcore.FileOrDirectory, 0, len(patterns))

	for _, pattern := range patterns {
		values, err := c.globOne(pattern, binding)
		if err != nil {
			return nil, err
		}

		collected = append(collected, values...)
	}

	return collected, nil
}

// globOne matches one pattern and builds a value for each path it matched.
//
// An empty pattern matches nothing at all rather than the output directory itself, which is what
// joining "" onto a directory would otherwise produce. An expression that declines to name a glob
// is how a document writes "collect nothing here", and reading that as "collect the whole output
// directory" would be the worst possible interpretation of it.
func (c *outputCollector) globOne(
	pattern string, binding *cwlcore.CommandOutputBinding,
) ([]cwlcore.FileOrDirectory, error) {
	if pattern == "" {
		return nil, nil
	}

	matches, err := c.globMatches(pattern)
	if err != nil {
		return nil, err
	}

	values := make([]cwlcore.FileOrDirectory, 0, len(matches))

	for _, local := range matches {
		value, err := c.collectMatch(local, binding)
		if err != nil {
			return nil, err
		}

		values = append(values, value)
	}

	return values, nil
}

// collectMatch builds the value for one matched path, once that path has been shown to lead
// somewhere the tool is allowed to publish from.
func (c *outputCollector) collectMatch(
	local string, binding *cwlcore.CommandOutputBinding,
) (cwlcore.FileOrDirectory, error) {
	err := c.checkRetrievable(local)
	if err != nil {
		return nil, err
	}

	return outCollectPath(local, binding)
}

// checkRetrievable rejects a matched path that leads outside everything the tool may publish from.
//
// CommandLineTool.yml, glob: "It is an error if a symlink in the output directory (or any symlink
// in a chain of links) refers to any file or directory that is not under an input or output
// directory." [filepath.EvalSymlinks] resolves the whole chain, which is what "any symlink in a
// chain" asks for, and the containment test is then the same lexical one
// [outputCollector.resolveGlob] applies to a pattern.
//
// Three ways of satisfying it, because there are three ways a legitimate match arises:
//
//   - the chain stays inside the output directory, which is the ordinary case and the whole of
//     what a tool writing its own outputs produces;
//   - the matched path *is* an input, or sits inside one. An implementation stages an input into
//     the output directory as a symlink to wherever the input really lives, so a tool that names a
//     staged input as its own output produces a link leading straight back out. The staged value's
//     `path` is the link, which is why the test is applied to the path as matched;
//   - the chain ends at an input, which is what a tool that follows an input's own path produces.
//
// The matched path itself is left exactly as it was found, because the same paragraph requires the
// collected value to keep the symlink's own basename: only where the link *leads* is in question.
func (c *outputCollector) checkRetrievable(local string) error {
	resolved, err := filepath.EvalSymlinks(local)
	if err != nil {
		return err
	}

	if outWithinDir(c.outroot, resolved) || c.fromInput(local) || c.fromInput(resolved) {
		return nil
	}

	return fmt.Errorf("%w: %q leads to %q, which is neither inside %q nor one of the inputs %s",
		ErrGlobSymlink, local, resolved, c.outroot, outQuoted(c.roots))
}

// fromInput reports whether local names one of the invocation's inputs, or something inside one.
func (c *outputCollector) fromInput(local string) bool {
	return slices.ContainsFunc(c.roots, func(root string) bool { return outWithinDir(root, local) })
}

// publishable reports whether local names a path this invocation may publish as it stands: the
// output directory or something inside it, or one of the paths the invocation brought into it.
//
// This is the containment rule applied to a path the *tool named*, which is the question a
// cwl.output.json asks; see [outputCollector.checkPublishable]. [outputCollector.checkRetrievable]
// asks a narrower one about a globbed path, because a glob pattern has already been shown to denote
// something inside the output directory and what remains in doubt is only where its symlinks lead.
// Both draw on the same root set, so a path one of them admits as an input the other does too.
func (c *outputCollector) publishable(local string) bool {
	return outWithinDir(c.outdir, local) || c.fromInput(local)
}

// outRootForms is how many forms of each root [outAllowedRoots] records: the declared one and the
// symlink-resolved one.
const outRootForms = 2

// outAllowedRoots is the set of paths the invocation brought into its working directory: every File
// and Directory the input object carries, wherever the staging that ran before the tool did leave
// it, plus everything an InitialWorkDirRequirement staged from.
//
// Both are "an input directory" in the sense [outputCollector.checkRetrievable] quotes. The engine
// stages either kind by symlinking the source into the output directory, so a tool that names one as
// its own output produces a link leading straight back out, and a rule that admitted only the input
// object would reject a value the specification requires an implementation to publish.
//
// Each is recorded twice, as it appears in the document and again with its symlinks resolved,
// because [outputCollector.checkRetrievable] compares it against both forms of a matched path.
// [slices.Compact] then drops the duplicate for the common case where the two are the same.
func outAllowedRoots(inputs map[string]any, scope *cwlcore.RequirementScope) []string {
	declared := outInputRoots(inputs, make([]string, 0, len(inputs)))
	declared = outStagedRoots(scope, declared)

	roots := make([]string, 0, len(declared)*outRootForms)
	for _, root := range declared {
		roots = append(roots, root, outResolvePath(root))
	}

	slices.Sort(roots)

	return slices.Compact(roots)
}

// outStagedRoots appends the host paths an InitialWorkDirRequirement stages from, which are the ones
// [StageInitialWorkDir] hands to the path mapper.
//
// Only the forms that name an existing resource contribute. A Dirent's content is *created* in the
// output directory rather than staged from anywhere, so it is already inside it and needs no root;
// an expression — a listing written as one, or an entry written as one — names nothing until it is
// evaluated, and re-evaluating it here to find out would run the document's expressions a second
// time to answer a containment question. Both therefore contribute nothing, which is the
// conservative direction: an output that then fails the containment test fails loudly.
func outStagedRoots(scope *cwlcore.RequirementScope, roots []string) []string {
	requirement, found := initialWorkDir(scope)
	if !found {
		return roots
	}

	// Entries is empty unless the listing is the written-out array, so the expression form needs
	// no separate arm.
	for _, entry := range requirement.Listing.Entries() {
		roots = outEntryRoots(entry, roots)
	}

	return roots
}

// outEntryRoots appends the paths one written-out listing entry stages from.
func outEntryRoots(entry cwlcore.InitialWorkDirEntry, roots []string) []string {
	switch entry.Kind() {
	case cwlcore.ValueFile:
		return outStagedRoot(entry.File(), roots)
	case cwlcore.ValueDirectory:
		return outStagedRoot(entry.Directory(), roots)
	case cwlcore.ValueList:
		for _, object := range entry.Objects() {
			roots = outStagedRoot(object, roots)
		}

		return roots
	default:
		// A Dirent, an expression, an explicit null, or the unset zero value a malformed entry
		// decodes to: none of them names a path to stage from.
		return roots
	}
}

// outStagedRoot appends the path one staged filesystem value occupies, if it occupies one. A literal
// and a resource on storage that is not a local filesystem occupy none.
func outStagedRoot(value cwlcore.FileOrDirectory, roots []string) []string {
	local := pathOf(value)
	if local == "" {
		return roots
	}

	return append(roots, local)
}

// outResolvePath resolves the symlinks in a path, leaving one it cannot resolve as it was: a path
// that is not on disk contains nothing either way, so there is nothing to report.
func outResolvePath(local string) string {
	resolved, err := filepath.EvalSymlinks(local)
	if err != nil {
		return local
	}

	return resolved
}

// outInputRoots collects the paths the filesystem values inside one input value occupy, appending
// them to roots.
func outInputRoots(value any, roots []string) []string {
	switch typed := value.(type) {
	case map[string]any:
		return outObjectRoots(typed, roots)
	case []any:
		for _, item := range typed {
			roots = outInputRoots(item, roots)
		}

		return roots
	default:
		return roots
	}
}

// outObjectRoots collects the path one object occupies, and then those of every field it carries —
// which is how the entries of a listing and of a secondaryFiles array are reached.
func outObjectRoots(object map[string]any, roots []string) []string {
	root := outInputRoot(object)
	if root != "" {
		roots = append(roots, root)
	}

	for _, field := range object {
		roots = outInputRoots(field, roots)
	}

	return roots
}

// outInputRoot returns the path one input value occupies. An object that is not a File or a
// Directory, or that names no local path, occupies none: a record field that happens to be called
// `path` is not a filesystem location, and a literal has nowhere to be yet.
func outInputRoot(object map[string]any) string {
	class := outTextField(object, outKeyClass)
	if class != cwlcore.ClassFile && class != cwlcore.ClassDirectory {
		return ""
	}

	return outTextField(object, outKeyPath)
}

// globMatches returns the existing paths one pattern names, sorted.
func (c *outputCollector) globMatches(pattern string) ([]string, error) {
	resolved, err := c.resolveGlob(pattern)
	if err != nil {
		return nil, err
	}

	matches, err := filepath.Glob(resolved)
	if err != nil {
		return nil, fmt.Errorf("%w %q: %w", ErrGlobPattern, pattern, err)
	}

	slices.Sort(matches)

	return matches, nil
}

// resolveGlob turns one declared pattern into an absolute pattern inside the output directory, or
// reports that it escapes.
//
// A relative pattern is resolved against the output directory, and an absolute one is accepted
// only when it already names something inside it — which is exactly what the specification says
// an absolute glob must do. Cleaning happens before the containment test, so "sub/../../secret"
// is rejected on the path it actually denotes rather than on the way it was spelled.
//
// Symlinks are deliberately not resolved. The specification says a globbed symlink keeps its own
// basename, so the link's own location is the one that has to be inside the output directory; a
// link whose target lies elsewhere is a staging concern, not a containment breach here.
func (c *outputCollector) resolveGlob(pattern string) (string, error) {
	resolved := filepath.Join(c.outdir, pattern)
	if filepath.IsAbs(pattern) {
		resolved = filepath.Clean(pattern)
	}

	if !outWithinDir(c.outdir, resolved) {
		return "", fmt.Errorf("%w: %q denotes %q, which is not inside %q",
			ErrGlobEscape, pattern, resolved, c.outdir)
	}

	return resolved, nil
}

// outWithinDir reports whether local names dir itself or something beneath it, comparing cleaned
// paths lexically.
func outWithinDir(dir, local string) bool {
	return local == dir || strings.HasPrefix(local, dir+string(filepath.Separator))
}

// globPatterns expands an output binding's declared glob entries into the patterns to match.
func (c *outputCollector) globPatterns(binding *cwlcore.CommandOutputBinding) ([]string, error) {
	patterns := make([]string, 0, len(binding.Glob))

	for _, declared := range binding.Glob {
		expanded, err := c.globPattern(declared)
		if err != nil {
			return nil, err
		}

		patterns = append(patterns, expanded...)
	}

	return patterns, nil
}

// globPattern expands one declared glob entry, which is either a literal pattern or an expression
// producing one or several.
func (c *outputCollector) globPattern(declared cwlcore.Expression) ([]string, error) {
	text := string(declared)
	if !cwlcore.NeedsParsing(text) {
		return []string{text}, nil
	}

	value, err := c.eval.Eval(text, c.context(nil))
	if err != nil {
		return nil, err
	}

	return outGlobStrings(value)
}

// outGlobStrings normalizes what a glob expression evaluated to.
//
// A null result contributes no patterns, matching the reference implementation, so a conditional
// glob can be written as an expression that returns null when it has nothing to collect.
func outGlobStrings(value any) ([]string, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case string:
		return []string{typed}, nil
	case []any:
		return outGlobStringList(typed)
	default:
		return nil, fmt.Errorf("%w: got %s", ErrGlobValue, cwlcore.TypeName(value))
	}
}

// outGlobStringList normalizes the array form of a glob expression's result.
func outGlobStringList(values []any) ([]string, error) {
	patterns := make([]string, 0, len(values))

	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%w: array holds %s", ErrGlobValue, cwlcore.TypeName(value))
		}

		patterns = append(patterns, text)
	}

	return patterns, nil
}

// outCollectPath builds the File or Directory value for one matched path.
//
// The class is decided by a stat that follows symlinks, so a link to a file is a File with the
// target's bytes and the link's own basename, which is what CommandLineTool.yml describes: "the
// expected behavior is for the resulting File/Directory object to take the `basename` (and
// corresponding `nameroot` and `nameext`) of the symlink".
func outCollectPath(local string, binding *cwlcore.CommandOutputBinding) (cwlcore.FileOrDirectory, error) {
	info, err := os.Stat(local)
	if err != nil {
		return nil, err
	}

	if info.IsDir() {
		return outCollectDirectory(local, info, binding.LoadListing)
	}

	return outCollectFile(local, binding)
}

// outCollectFile builds a File value with its size and checksum read from disk, and its contents too
// when the binding asked for them.
//
// Process.yml, size: "It must be computed from the resource and made available to expressions."
// Size, checksum and the loadContents bytes all come from one pass over the file, so no two of
// them can ever describe different content.
func outCollectFile(local string, binding *cwlcore.CommandOutputBinding) (*cwlcore.File, error) {
	stats, err := outDigest(local)
	if err != nil {
		return nil, err
	}

	file := outNewFile(local)
	file.Size = cwlcore.NewOptInt(stats.size)
	file.Checksum = stats.checksum

	if !binding.LoadContents {
		return file, nil
	}

	return outWithContents(file, &stats)
}

// outMeasureFile builds a File value for local with its size and checksum read from disk and no
// contents, which is what a directory listing entry and a secondary file get.
func outMeasureFile(local string) (*cwlcore.File, error) {
	return outCollectFile(
		local,
		&cwlcore.CommandOutputBinding{OutputEval: "", LoadListing: "", Glob: nil, LoadContents: false},
	)
}

// outWithContents puts a file's leading bytes into the value a loadContents binding asks for.
//
// An empty file yields an OptString that is set and empty rather than unset: "" is the whole
// content of a zero-byte file, and an expression asking for `self.contents` must see it rather
// than find the field missing.
func outWithContents(file *cwlcore.File, stats *outFileStats) (*cwlcore.File, error) {
	if stats.size > joMaxContentsBytes {
		return nil, fmt.Errorf("%w: %s is %d bytes, over %d",
			ErrContentsTooLarge, file.Path, stats.size, joMaxContentsBytes)
	}

	if !utf8.Valid(stats.head) {
		return nil, fmt.Errorf("%w: %s", ErrContentsNotText, file.Path)
	}

	file.Contents = cwlcore.NewOptString(string(stats.head))

	return file, nil
}

// outListingDepth is how far a directory walk descends, which is what the two reading settings of
// loadListing amount to once no_listing has been dealt with.
type outListingDepth uint8

const (
	// outShallowWalk reads one directory and names its subdirectories without reading them.
	outShallowWalk outListingDepth = iota

	// outDeepWalk reads every directory it reaches.
	outDeepWalk
)

// outCollectDirectory builds a Directory value, reading its listing as deeply as loadListing asks.
//
// Process.yml gives loadListing three settings and a default of no_listing, and under that
// default the Listing stays nil. That is not the same as an empty directory: nil records that
// nothing read the listing, so a later consumer can still go and read it, whereas an empty slice
// would assert that the directory has no entries.
func outCollectDirectory(
	local string, info fs.FileInfo, mode cwlcore.LoadListingEnum,
) (*cwlcore.Directory, error) {
	switch mode {
	case cwlcore.LoadListingShallow:
		return outListDirectory(local, outShallowWalk, nil)
	case cwlcore.LoadListingDeep:
		return outListDirectory(local, outDeepWalk, []fs.FileInfo{info})
	default:
		return outNewDirectory(local), nil
	}
}

// outListDirectory builds a Directory value whose listing is read from disk.
//
// walked carries the directories already on the branch being walked, which is how a deep walk
// terminates: a symlink pointing back up the tree would otherwise be followed for ever.
func outListDirectory(local string, depth outListingDepth, walked []fs.FileInfo) (*cwlcore.Directory, error) {
	entries, err := os.ReadDir(local)
	if err != nil {
		return nil, err
	}

	dir := outNewDirectory(local)
	listing := make([]cwlcore.FileOrDirectory, 0, len(entries))

	// os.ReadDir sorts its result by filename, which for these entries is the basename, so the
	// listing is already in the order the reference implementation sorts it into.
	for _, entry := range entries {
		value, err := outListingEntry(filepath.Join(local, entry.Name()), depth, walked)
		if err != nil {
			return nil, err
		}

		listing = append(listing, value)
	}

	dir.Listing = listing

	return dir, nil
}

// outListingEntry builds one entry of a directory listing. A subdirectory reached during a deep walk
// gets a listing of its own; one reached during a shallow walk gets a nil listing, which reads as
// "not read" rather than "empty".
func outListingEntry(
	local string, depth outListingDepth, walked []fs.FileInfo,
) (cwlcore.FileOrDirectory, error) {
	info, err := os.Stat(local)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		return outMeasureFile(local)
	}

	if depth != outDeepWalk || outAlreadyWalked(info, walked) {
		return outNewDirectory(local), nil
	}

	return outListDirectory(local, outDeepWalk, append(walked, info))
}

// outAlreadyWalked reports whether info names a directory already on the branch being walked, which
// is what a symlink pointing back up the tree produces. Identity is compared with [os.SameFile]
// rather than by path, because two paths that reach the same directory through different links
// are the same directory.
func outAlreadyWalked(info fs.FileInfo, walked []fs.FileInfo) bool {
	for _, seen := range walked {
		if os.SameFile(info, seen) {
			return true
		}
	}

	return false
}

// outNewFile builds the File fields that follow from the path alone.
func outNewFile(local string) *cwlcore.File {
	basename := filepath.Base(local)
	parts := outSplitName(basename)

	return &cwlcore.File{
		Node:           nil,
		Location:       outFileURI(local),
		Path:           local,
		Basename:       basename,
		Dirname:        outDirname(local),
		Nameroot:       parts.root,
		Nameext:        parts.ext,
		Checksum:       "",
		Format:         "",
		Size:           cwlcore.OptInt{},
		Contents:       cwlcore.OptString{},
		SecondaryFiles: nil,
	}
}

// Completing the listings of a value on its way out of the engine.
//
// Process.yml gives loadListing a default of no_listing and says it decides how a Directory's
// listing is loaded "for use by expressions" — so wherever it appears it governs what an expression
// sees, and nothing more. What is *published* is a different question, and the specification answers
// it in Process.yml's description of `listing`: "If `listing` is not provided, the implementation
// must have some way of fetching the Directory listing at runtime based on the `location` field." A
// run's output object is exactly the place that guarantee runs out — the caller may relocate the
// directory, upload it to storage with no directory concept, or discard the container it lived in —
// so a Directory leaving the engine without its listing is a Directory whose contents nobody can
// recover. The conformance harness makes the same judgement and treats `listing` as a mandatory
// field of every Directory it is shown.
//
// # Why this happens once, at the boundary, and not when a tool collects an output
//
// Because a listing written onto a value is not a cache, it is the value. The same Process.yml
// sentence says the location is consulted only "if `listing` is not provided", so whatever is
// written here becomes the authoritative answer to what that directory contains, for every consumer
// that ever reads it.
//
// That is fine for a value nothing will touch again, and wrong for one still in flight.
// InplaceUpdateRequirement is the case that proves it: tests/inpdir_update_wf.cwl has step1 create a
// directory, step2 modify that same directory on disk, and step3 read it with
// `loadListing: shallow_listing`. Completing step1's output at collection time froze the empty
// listing it had at that moment onto the value, and step3 — correctly declining to overwrite a
// listing that had been provided — then reported the directory as empty, hours of disk activity
// notwithstanding. Deferring the completion to the boundary leaves the intermediate value saying
// what is true of it, which is that nobody has read its listing, so every later consumer that asks
// for one goes and reads the directory as it stands.
//
// The alternative — completing at collection time and re-reading at the far end whenever a consumer
// asks for a listing mode — was rejected because it cannot tell the two kinds of listing apart. A
// listing a *document* wrote is a claim the specification makes authoritative, and a Directory
// literal's listing is the only description of it there is; re-reading on request would silently
// replace both with whatever happened to be on disk. Deferring the write instead means the only
// non-nil listings inside a run are the ones a document or an expression put there, and DECIDED-16's
// nil-versus-supplied distinction survives untouched.
//
// The pass only ever *completes* a value: a listing already set is kept, and descended into so that
// its own subdirectories are completed too.

// outFillListings gives every Directory a collected value carries the listing it must be published
// with.
//
// Failure is not reported. A Directory an expression named but that is not on disk keeps the nil
// listing it had, on the same terms as [outRemeasure] leaving an unreadable File unmeasured: the
// value may legitimately describe something a later stage will create, and refusing to publish a
// tool's whole output over it would be the wrong trade.
func outFillListings(value any) {
	switch typed := value.(type) {
	case *cwlcore.Directory:
		if typed != nil {
			outFillDirectory(typed)
		}
	case *cwlcore.File:
		if typed != nil {
			outFillEntries(typed.SecondaryFiles)
		}
	case []any:
		for _, item := range typed {
			outFillListings(item)
		}
	case map[string]any:
		for _, field := range typed {
			outFillListings(field)
		}
	default:
		// A string, a number, a boolean or a null: nothing that can carry a Directory.
	}
}

// outFillEntries completes every member of a listing or a secondaryFiles array.
func outFillEntries(entries []cwlcore.FileOrDirectory) {
	for _, entry := range entries {
		outFillListings(entry)
	}
}

// outFillDirectory completes one Directory.
//
// A listing that is already set is descended into rather than replaced, and one that is read here
// is not descended into again — it was read to the bottom already, and the only nil listings it can
// contain are the ones [outListDirectory] leaves at a symlink loop, which is precisely where
// descending again would not terminate.
func outFillDirectory(dir *cwlcore.Directory) {
	if dir.Listing != nil {
		outFillEntries(dir.Listing)

		return
	}

	dir.Listing = outReadListing(dir.Path)
}

// outReadListing reads the whole tree under local, and returns nil when there is no directory there
// to read or reading it fails.
func outReadListing(local string) []cwlcore.FileOrDirectory {
	info, err := os.Stat(local)
	if err != nil || !info.IsDir() {
		return nil
	}

	read, err := outListDirectory(local, outDeepWalk, []fs.FileInfo{info})
	if err != nil {
		return nil
	}

	return read.Listing
}

// outNewDirectory builds the Directory fields that follow from the path alone.
//
// There are only three. The vendored schema gives Directory class, location, path, basename and
// listing and nothing else — no size, no checksum, no format, no secondaryFiles — so a Directory
// carries no derived name fields either.
func outNewDirectory(local string) *cwlcore.Directory {
	return &cwlcore.Directory{
		Node:     nil,
		Location: outFileURI(local),
		Path:     local,
		Basename: filepath.Base(local),
		Listing:  nil,
	}
}
