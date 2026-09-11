package cwlexec

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// ErrFilesystemEntry reports a secondaryFiles or listing entry that is not a File or Directory.
var ErrFilesystemEntry = errors.New("secondaryFiles or listing entry is not a File or Directory")

// File/Directory field name constants.
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

// Bridge between typed File/Directory values and expression-visible maps via [cwlcore.ToExpressionValue].

// outExpressionObject renders an object's fields via [cwlcore.ToExpressionValue] for expression use.
func outExpressionObject(object map[string]any) map[string]any {
	rendered := make(map[string]any, len(object))
	for key, value := range object {
		rendered[key] = cwlcore.ToExpressionValue(value)
	}

	return rendered
}

// outWiden widens []FileOrDirectory to []any.
func outWiden(values []cwlcore.FileOrDirectory) []any {
	widened := make([]any, 0, len(values))
	for _, value := range values {
		widened = append(widened, value)
	}

	return widened
}

// outTextField reads a string field, returning "" if absent or non-string.
func outTextField(object map[string]any, key string) string {
	text, ok := object[key].(string)
	if !ok {
		return ""
	}

	return text
}

// retypeValue converts expression-produced File/Directory maps back into typed values, recursively.
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

// retypeList re-types each element of a list.
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

// retypeObject re-types one object based on its class field.
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

// retypeFile builds a typed File from an expression-produced map, measuring from disk if needed.
func (c *outputCollector) retypeFile(object map[string]any) (*cwlcore.File, error) {
	ref := c.deriveRef(object)
	parts := outSplitName(ref.basename)

	file := &cwlcore.File{
		Node:           nil,
		Location:       ref.location,
		Path:           ref.local,
		Basename:       ref.basename,
		Dirname:        outDirname(ref.local),
		Nameroot:       parts.root,
		Nameext:        parts.ext,
		Checksum:       outTextField(object, outKeyChecksum),
		Format:         outTextField(object, outKeyFormat),
		Size:           cwlcore.OptInt{},
		Contents:       cwlcore.OptString{},
		SecondaryFiles: nil,
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
	outRemeasure(file, c.outfs, c.outdir)

	return file, nil
}

// outRemeasure fills in missing size/checksum from the output FS or literal contents.
func outRemeasure(file *cwlcore.File, fsys fs.FS, outdir string) {
	if file.Checksum != "" && file.Size.IsSet() {
		return
	}

	if file.Path == "" {
		outMeasureLiteral(file)

		return
	}

	rel, relErr := filepath.Rel(outdir, file.Path)
	if relErr != nil || !filepath.IsLocal(rel) {
		return
	}

	stats, err := outDigestFS(fsys, filepath.ToSlash(rel))
	if err != nil {
		return
	}

	file.Size = cwlcore.NewOptInt(stats.size)
	file.Checksum = stats.checksum
}

// retypeDirectory builds a typed Directory from an expression-produced map.
func (c *outputCollector) retypeDirectory(object map[string]any) (*cwlcore.Directory, error) {
	ref := c.deriveRef(object)

	listing, err := c.retypeEntries(object[outKeyListing])
	if err != nil {
		return nil, err
	}

	return &cwlcore.Directory{
		Node:     nil,
		Location: ref.location,
		Path:     ref.local,
		Basename: ref.basename,
		Listing:  listing,
	}, nil
}

// retypeEntries re-types a secondaryFiles or listing array. Nil stays nil.
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

// retypeEntry re-types one secondaryFiles or listing entry.
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

// outRef is a filesystem value's resolved identity.
type outRef struct {
	location string // absolute IRI
	local    string // filesystem path, or "" for non-local/literal
	basename string // final path component
}

// deriveRef completes location, path, and basename from whichever the expression supplied.
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

// localPath extracts a local filesystem path from a file: location, or "" for non-local.
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

// outNameParts is a basename split into root and extension.
type outNameParts struct {
	root string // everything before the final period
	ext  string // final period and suffix, or ""
}

// outSplitName splits a basename into nameroot and nameext, skipping leading periods.
func outSplitName(basename string) outNameParts {
	dots := len(basename) - len(strings.TrimLeft(basename, "."))

	last := strings.LastIndexByte(basename[dots:], '.')
	if last < 0 {
		return outNameParts{root: basename, ext: ""}
	}

	return outNameParts{root: basename[:dots+last], ext: basename[dots+last:]}
}

// outFileURI renders an absolute path as a file:// URI.
func outFileURI(local string) string {
	uri := url.URL{
		Scheme:      joSchemeFile,
		Opaque:      "",
		User:        nil,
		Host:        "",
		Path:        local,
		RawPath:     "",
		OmitHost:    false,
		ForceQuery:  false,
		RawQuery:    "",
		Fragment:    "",
		RawFragment: "",
	}

	return uri.String()
}

// outDirname returns the directory component of a path, or "" if no path.
func outDirname(local string) string {
	if local == "" {
		return ""
	}

	return filepath.Dir(local)
}

// outAbsolutize resolves a relative path against base.
func outAbsolutize(local, base string) string {
	if filepath.IsAbs(local) {
		return filepath.Clean(local)
	}

	return filepath.Join(base, local)
}

// outNumber converts numeric types to int64.
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

// Relocating collected values: when basename disagrees with path, the file is moved on disk.

// ErrOutputRename reports a failed output rename.
var ErrOutputRename = errors.New("cannot give an output the basename it declares")

// relocate moves Files/Directories on disk when their basename disagrees with their path.
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

// relocateEntries relocates each member of a secondaryFiles or listing array.
func (c *outputCollector) relocateEntries(entries []cwlcore.FileOrDirectory) error {
	for _, entry := range entries {
		err := c.relocate(entry)
		if err != nil {
			return err
		}
	}

	return nil
}

// relocateFile moves a File to its declared basename, then relocates its secondary files.
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

// relocateDirectory moves a Directory to its declared basename, rebasing its listing paths.
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

// outMisnamed returns the target path if basename disagrees with path, or "" if they agree.
func outMisnamed(local, basename string) string {
	if local == "" || basename == "" || filepath.Base(local) == basename {
		return ""
	}

	return filepath.Join(filepath.Dir(local), basename)
}

// moveTo renames source within outdir, or copies it in if source is outside outdir.
func (c *outputCollector) moveTo(source, target string) (string, error) {
	if outWithinDir(c.outdir, source) {
		relOld := c.relOutPath(source)
		relNew := c.relOutPath(target)

		return target, outMoveError(c.outfs.Rename(relOld, relNew), source, target)
	}

	inside := filepath.Join(c.outdir, filepath.Base(target))
	relInside := c.relOutPath(inside)

	return inside, outMoveError(copyToFS(source, c.outfs, relInside), source, inside)
}

// outMoveError wraps a move error with source and target paths.
func outMoveError(err error, source, target string) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("%w: %q to %q: %w", ErrOutputRename, source, target, err)
}

// outRepath updates a File's path-derived fields after a move. Basename is unchanged.
func outRepath(file *cwlcore.File, local string) {
	parts := outSplitName(file.Basename)

	file.Location = outFileURI(local)
	file.Path = local
	file.Dirname = outDirname(local)
	file.Nameroot = parts.root
	file.Nameext = parts.ext
}

// outRebaseEntries rewrites paths of entries after a directory rename.
func outRebaseEntries(entries []cwlcore.FileOrDirectory, from, to string) {
	for _, entry := range entries {
		outRebaseEntry(entry, from, to)
	}
}

// outRebaseEntry rewrites one entry's path and recurses into its children.
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

// outRebasePath replaces the from prefix with to, returning whether it changed.
func outRebasePath(local, from, to string) (string, bool) {
	if !outWithinDir(from, local) {
		return local, false
	}

	return filepath.Join(to, strings.TrimPrefix(local, from)), true
}

const outNoNames = "none"

// outQuoted renders patterns for error messages.
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
