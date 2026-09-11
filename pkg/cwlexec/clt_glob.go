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

// Globbing a finished tool's output directory to collect File/Directory values.

// Errors reported while globbing an output directory. Use [errors.Is] to test.
var (
	// ErrGlobEscape reports a glob pattern resolving outside the output directory.
	ErrGlobEscape = errors.New("glob pattern resolves outside the output directory")
	// ErrGlobSymlink reports a symlink leading outside output and input directories.
	ErrGlobSymlink = errors.New("globbed symlink leads outside the output and input directories")
	// ErrGlobPattern reports invalid glob(3) syntax.
	ErrGlobPattern = errors.New("malformed glob pattern")
	// ErrGlobValue reports a glob expression that is not a string or string array.
	ErrGlobValue = errors.New("glob expression did not produce a string or array of strings")
	// ErrContentsTooLarge reports a loadContents file over 64 KiB.
	ErrContentsTooLarge = errors.New("loadContents: file is over the 64 KiB limit")
	// ErrContentsNotText reports a loadContents file that is not valid UTF-8.
	ErrContentsNotText = errors.New("loadContents: file is not UTF-8 text")
)

// globValues matches patterns against the output directory and builds
// File/Directory values, sorted within each pattern.
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

// globOne matches one pattern. An empty pattern matches nothing.
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

// collectMatch builds a value for one matched path after checking containment.
func (c *outputCollector) collectMatch(
	local string, binding *cwlcore.CommandOutputBinding,
) (cwlcore.FileOrDirectory, error) {
	err := c.checkRetrievable(local)
	if err != nil {
		return nil, err
	}

	return outCollectPath(local, binding)
}

// checkRetrievable rejects a matched path whose symlink chain leads outside the output and input directories.
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

// fromInput reports whether local is under one of the invocation's input roots.
func (c *outputCollector) fromInput(local string) bool {
	return slices.ContainsFunc(c.roots, func(root string) bool { return outWithinDir(root, local) })
}

// publishable reports whether local is under the output directory or an input root.
func (c *outputCollector) publishable(local string) bool {
	return outWithinDir(c.outdir, local) || c.fromInput(local)
}

// outRootForms is how many forms of each root are recorded: declared and symlink-resolved.
const outRootForms = 2

// outAllowedRoots collects all input and staged paths (both declared and symlink-resolved) for containment checks.
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

// outStagedRoots appends host paths from InitialWorkDirRequirement entries.
func outStagedRoots(scope *cwlcore.RequirementScope, roots []string) []string {
	requirement, found := initialWorkDir(scope)
	if !found {
		return roots
	}

	for _, entry := range requirement.Listing.Entries() {
		roots = outEntryRoots(entry, roots)
	}

	return roots
}

// outEntryRoots appends paths from one listing entry.
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
		return roots
	}
}

// outStagedRoot appends the local path of a staged value, if it has one.
func outStagedRoot(value cwlcore.FileOrDirectory, roots []string) []string {
	local := pathOf(value)
	if local == "" {
		return roots
	}

	return append(roots, local)
}

// outResolvePath resolves symlinks, returning the original path on error.
func outResolvePath(local string) string {
	resolved, err := filepath.EvalSymlinks(local)
	if err != nil {
		return local
	}

	return resolved
}

// outInputRoots recursively collects filesystem paths from an input value.
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

// outObjectRoots collects the path of an object and its nested fields.
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

// outInputRoot returns the path of a File or Directory object, or "" if not applicable.
func outInputRoot(object map[string]any) string {
	class := outTextField(object, outKeyClass)
	if class != cwlcore.ClassFile && class != cwlcore.ClassDirectory {
		return ""
	}

	return outTextField(object, outKeyPath)
}

// globMatches returns sorted paths matching one pattern.
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

// resolveGlob makes a pattern absolute within the output directory, or rejects it as an escape.
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

// outWithinDir reports whether local is dir or a descendant of dir.
func outWithinDir(dir, local string) bool {
	return local == dir || strings.HasPrefix(local, dir+string(filepath.Separator))
}

// globPatterns expands a binding's glob entries into patterns.
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

// globPattern expands one glob entry (literal or expression).
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

// outGlobStrings normalizes a glob expression result. Null means no patterns.
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

// outGlobStringList converts a []any of strings to []string.
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

// outCollectPath builds a File or Directory value for a matched path, following symlinks for stat.
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

// outCollectFile builds a File value with size, checksum, and optionally contents from disk.
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

// outMeasureFile builds a File value with size and checksum but no contents.
func outMeasureFile(local string) (*cwlcore.File, error) {
	return outCollectFile(
		local,
		&cwlcore.CommandOutputBinding{OutputEval: "", LoadListing: "", Glob: nil, LoadContents: false},
	)
}

// outWithContents populates File.Contents from the digested bytes, enforcing the 64 KiB and UTF-8 constraints.
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

// outListingDepth controls directory walk depth.
type outListingDepth uint8

const (
	// outShallowWalk reads one level.
	outShallowWalk outListingDepth = iota

	// outDeepWalk reads the full tree.
	outDeepWalk
)

// outCollectDirectory builds a Directory value with listing depth per loadListing. nil listing means unread.
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

// outListDirectory builds a Directory with its listing read from disk. walked prevents symlink cycles.
func outListDirectory(local string, depth outListingDepth, walked []fs.FileInfo) (*cwlcore.Directory, error) {
	entries, err := os.ReadDir(local)
	if err != nil {
		return nil, err
	}

	dir := outNewDirectory(local)
	listing := make([]cwlcore.FileOrDirectory, 0, len(entries))

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

// outListingEntry builds one directory listing entry. Deep-walked subdirs get their own listing.
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

// outAlreadyWalked detects symlink cycles using [os.SameFile].
func outAlreadyWalked(info fs.FileInfo, walked []fs.FileInfo) bool {
	for _, seen := range walked {
		if os.SameFile(info, seen) {
			return true
		}
	}

	return false
}

// outNewFile builds a File value from a path with derived name fields.
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

// Completing directory listings at the engine boundary. Done once on output, not during
// collection, so in-flight values reflect current disk state rather than frozen snapshots.

// outFillListings ensures every Directory in value has a listing. Errors are silently ignored.
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
	}
}

// outFillEntries completes listings for each entry.
func outFillEntries(entries []cwlcore.FileOrDirectory) {
	for _, entry := range entries {
		outFillListings(entry)
	}
}

// outFillDirectory completes one Directory. Existing listings are kept and descended into.
func outFillDirectory(dir *cwlcore.Directory) {
	if dir.Listing != nil {
		outFillEntries(dir.Listing)

		return
	}

	dir.Listing = outReadListing(dir.Path)
}

// outReadListing reads the full tree under local, returning nil on error.
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

// outNewDirectory builds a Directory value from a path.
func outNewDirectory(local string) *cwlcore.Directory {
	return &cwlcore.Directory{
		Node:     nil,
		Location: outFileURI(local),
		Path:     local,
		Basename: filepath.Base(local),
		Listing:  nil,
	}
}
