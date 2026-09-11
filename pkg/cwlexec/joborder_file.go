package cwlexec

import (
	"context"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// Normalising File and Directory values from a job order into [cwlcore.File]/[cwlcore.Directory].

// joFileFields and joDirectoryFields are the accepted fields for each filesystem value type.
var (
	joFileFields = []string{
		outKeyClass, outKeyLocation, outKeyPath, outKeyBasename, outKeyDirname, outKeyNameroot, outKeyNameext,
		outKeyChecksum, outKeySize, outKeyFormat, outKeyContents, outKeySecondaryFiles,
	}

	joDirectoryFields = []string{outKeyClass, outKeyLocation, outKeyPath, outKeyBasename, outKeyListing}
)

// fileValue converts a value declared as `type: File`.
func (l *joLoader) fileValue(ctx context.Context, n salad.Node, v *joValueCtx) (any, *salad.Error) {
	m, ok := salad.AsMap(n)
	if !ok || joClassOf(m) != cwlcore.ClassFile {
		return nil, joTypeErr(n, v)
	}

	file, err := l.normalizeFile(ctx, m, v)
	if err != nil {
		return nil, err
	}

	bad := l.checkFormat(file, v)
	if bad != nil {
		return nil, bad
	}

	return file, nil
}

// directoryValue converts a value declared as `type: Directory`.
func (l *joLoader) directoryValue(ctx context.Context, n salad.Node, v *joValueCtx) (any, *salad.Error) {
	m, ok := salad.AsMap(n)
	if !ok || joClassOf(m) != cwlcore.ClassDirectory {
		return nil, joTypeErr(n, v)
	}

	dir, err := l.normalizeDirectory(ctx, m, v)
	if err != nil {
		return nil, err
	}

	return dir, nil
}

// normalizeFile turns a File mapping into a fully-populated [cwlcore.File].
func (l *joLoader) normalizeFile(ctx context.Context, m *salad.MapNode, v *joValueCtx) (*cwlcore.File, *salad.Error) {
	stopped := joCancelled(ctx, m, v)
	if stopped != nil {
		return nil, stopped
	}

	file, err := l.fileShell(m, v)
	if err != nil {
		return nil, err
	}

	measured := joMeasure(file, m, v)
	if measured != nil {
		return nil, measured
	}

	secondary, _, err := l.entries(ctx, m, outKeySecondaryFiles, v)
	if err != nil {
		return nil, err
	}

	file.SecondaryFiles = secondary

	return file, nil
}

// fileShell builds every field of a File that can be derived without touching a disk.
func (l *joLoader) fileShell(m *salad.MapNode, v *joValueCtx) (*cwlcore.File, *salad.Error) {
	unknown := joCheckKeys(m, joFileFields, "File field")
	if unknown != nil {
		return nil, unknown
	}

	reader := &joFieldReader{m: m, err: nil, path: v.path}
	ref := joResolveRef(reader, v.base)
	contents := reader.optText(outKeyContents)
	basename := reader.text(outKeyBasename)
	format := l.vocab.expandFormat(reader.text(outKeyFormat))

	if reader.err != nil {
		return nil, reader.err
	}

	bad := joCheckFileIdentity(m, v, &ref, contents)
	if bad != nil {
		return nil, bad
	}

	if basename == "" {
		basename = ref.name
	}

	parts := outSplitName(basename)

	return &cwlcore.File{
		Node:           m,
		Location:       ref.location,
		Path:           ref.local,
		Basename:       basename,
		Dirname:        outDirname(ref.local),
		Nameroot:       parts.root,
		Nameext:        parts.ext,
		Checksum:       "",
		Format:         format,
		Size:           cwlcore.OptInt{},
		Contents:       contents,
		SecondaryFiles: nil,
	}, nil
}

// joMeasure fills in a File's size, checksum, and optionally contents from disk.
func joMeasure(file *cwlcore.File, m *salad.MapNode, v *joValueCtx) *salad.Error {
	if file.Path == "" {
		outMeasureLiteral(file)

		return nil
	}

	stats, err := outDigest(file.Path)
	if err != nil {
		return salad.Errorf(m.Loc(), "%s: cannot read file: %v", v.path, err)
	}

	file.Size = cwlcore.NewOptInt(stats.size)
	file.Checksum = stats.checksum

	return joLoadContents(file, m, v, &stats)
}

// joLoadContents populates a File's contents field when loadContents is set.
func joLoadContents(file *cwlcore.File, m *salad.MapNode, v *joValueCtx, stats *outFileStats) *salad.Error {
	if !v.loadContents {
		return nil
	}

	if stats.size > joMaxContentsBytes {
		return salad.Errorf(m.Loc(),
			"%s: loadContents is set but the file is %d bytes, over the %d byte limit",
			v.path, stats.size, joMaxContentsBytes)
	}

	file.Contents = cwlcore.NewOptString(string(stats.head))

	return nil
}

// joCheckFileIdentity requires a File to have location, path, or contents (within 64 KiB).
func joCheckFileIdentity(m *salad.MapNode, v *joValueCtx, ref *joFileRef, contents cwlcore.OptString) *salad.Error {
	if ref.location == "" && !contents.IsSet() {
		return salad.Errorf(m.Loc(), "%s: a File must supply location, path, or contents", v.path)
	}

	if size := len(contents.Value()); size > joMaxContentsBytes {
		return salad.Errorf(m.Loc(),
			"%s: contents is %d bytes, over the %d byte limit the specification places on a file literal",
			v.path, size, joMaxContentsBytes)
	}

	return nil
}

// normalizeDirectory turns a Directory mapping into a [cwlcore.Directory].
func (l *joLoader) normalizeDirectory(
	ctx context.Context, m *salad.MapNode, v *joValueCtx,
) (*cwlcore.Directory, *salad.Error) {
	stopped := joCancelled(ctx, m, v)
	if stopped != nil {
		return nil, stopped
	}

	unknown := joCheckKeys(m, joDirectoryFields, "Directory field")
	if unknown != nil {
		return nil, unknown
	}

	reader := &joFieldReader{m: m, err: nil, path: v.path}
	ref := joResolveRef(reader, v.base)
	basename := reader.text(outKeyBasename)

	if reader.err != nil {
		return nil, reader.err
	}

	listing, err := l.directoryListing(ctx, m, &ref, v)
	if err != nil {
		return nil, err
	}

	if basename == "" {
		basename = ref.name
	}

	return &cwlcore.Directory{
		Node: m, Location: ref.location, Path: ref.local, Basename: basename, Listing: listing,
	}, nil
}

// directoryListing resolves a Directory's listing from the supplied value or disk.
func (l *joLoader) directoryListing(
	ctx context.Context, m *salad.MapNode, ref *joFileRef, v *joValueCtx,
) ([]cwlcore.FileOrDirectory, *salad.Error) {
	listing, supplied, err := l.entries(ctx, m, outKeyListing, v)
	if err != nil {
		return nil, err
	}

	if ref.location == "" && !supplied {
		return nil, salad.Errorf(m.Loc(), "%s: a Directory must supply location, path, or listing", v.path)
	}

	info, missing := joStatDirectory(ref.local, m, v)
	if missing != nil {
		return nil, missing
	}

	if supplied || info == nil {
		return listing, nil
	}

	return joReadListing(ref.local, v.listing, m, v)
}

// joReadListing reads a Directory's listing from disk at the depth loadListing requests.
func joReadListing(
	local string, mode cwlcore.LoadListingEnum, m *salad.MapNode, v *joValueCtx,
) ([]cwlcore.FileOrDirectory, *salad.Error) {
	if mode == "" || mode == cwlcore.LoadListingNone {
		return nil, nil
	}

	dir, err := outCollectDirectory(local, mode, NewLocalDirFS(local), local)
	if err != nil {
		return nil, salad.Errorf(m.Loc(), "%s: reading the directory listing: %v", v.path, err)
	}

	return dir.Listing, nil
}

// entries converts a `listing` or `secondaryFiles` field, reporting whether it was present.
func (l *joLoader) entries(
	ctx context.Context, m *salad.MapNode, key string, v *joValueCtx,
) ([]cwlcore.FileOrDirectory, bool, *salad.Error) {
	node, ok := m.Get(key)
	if !ok || salad.IsNull(node) {
		return nil, false, nil
	}

	items := []salad.Node{node}
	if seq, isSeq := salad.AsSeq(node); isSeq {
		items = seq.Items()
	}

	nested := v.at("."+key, cwlcore.TypeRef{})
	values := make([]cwlcore.FileOrDirectory, 0, len(items))

	for i, item := range items {
		value, err := l.entry(ctx, item, nested.item(i, cwlcore.TypeRef{}))
		if err != nil {
			return nil, false, err
		}

		values = append(values, value)
	}

	return values, true, nil
}

// entry converts one File or Directory member of a listing or secondaryFiles list.
func (l *joLoader) entry(ctx context.Context, n salad.Node, v *joValueCtx) (cwlcore.FileOrDirectory, *salad.Error) {
	m, ok := salad.AsMap(n)
	if !ok {
		return nil, salad.Errorf(joNodeLoc(n),
			"%s: expected a mapping with class: File or class: Directory, but found %s", v.path, salad.NodeKind(n))
	}

	switch joClassOf(m) {
	case cwlcore.ClassFile:
		return l.normalizeFile(ctx, m, v)
	case cwlcore.ClassDirectory:
		return l.normalizeDirectory(ctx, m, v)
	default:
		return nil, salad.Errorf(m.Loc(), "%s: a filesystem value must declare class: File or class: Directory", v.path)
	}
}

// joFileRef is a resolved location: absolute IRI, local path, and basename.
type joFileRef struct {
	location string
	local    string
	name     string
}

// joResolveRef resolves a File/Directory's location against base. `path` takes precedence over `location`.
func joResolveRef(r *joFieldReader, base string) joFileRef {
	if local := r.text(outKeyPath); local != "" {
		return joRefAt(joUnwrapFileIRI(local), base)
	}

	location := r.text(outKeyLocation)
	if location == "" {
		return joFileRef{location: "", local: "", name: ""}
	}

	parsed, err := url.Parse(location)
	if err != nil {
		r.err = salad.Errorf(joNodeLoc(r.node(outKeyLocation)),
			"%s: location %q is not a valid IRI: %v", r.path, location, err)

		return joFileRef{location: "", local: "", name: ""}
	}

	if parsed.Scheme != "" && parsed.Scheme != joSchemeFile {
		return joFileRef{location: location, local: "", name: path.Base(parsed.Path)}
	}

	return joRefAt(parsed.Path, base)
}

// joRefAt builds a reference to a local filesystem path, which may be relative to base.
func joRefAt(local, base string) joFileRef {
	abs := outAbsolutize(local, base)

	return joFileRef{location: outFileURI(abs), local: abs, name: filepath.Base(abs)}
}

// joUnwrapFileIRI extracts the filesystem path from a `path` field, unwrapping file: IRIs.
func joUnwrapFileIRI(ref string) string {
	parsed, err := url.Parse(ref)
	if err != nil || parsed.Scheme != joSchemeFile || parsed.Path == "" {
		return ref
	}

	return parsed.Path
}

// joStatDirectory stats local and returns nil FileInfo if local is empty or non-local.
func joStatDirectory(local string, m *salad.MapNode, v *joValueCtx) (fs.FileInfo, *salad.Error) {
	if local == "" {
		return nil, nil
	}

	info, err := os.Stat(local)
	if err != nil {
		return nil, salad.Errorf(m.Loc(), "%s: %v", v.path, err)
	}

	if !info.IsDir() {
		return nil, salad.Errorf(m.Loc(), "%s: %s is not a directory", v.path, local)
	}

	return info, nil
}

// joCancelled reports ctx's cancellation as an error located at the value being normalised.
func joCancelled(ctx context.Context, m *salad.MapNode, v *joValueCtx) *salad.Error {
	err := ctx.Err()
	if err != nil {
		return salad.Errorf(m.Loc(), "%s: %v", v.path, err)
	}

	return nil
}

// joFieldReader reads string fields from a filesystem value, collecting the first error.
type joFieldReader struct {
	m    *salad.MapNode
	err  *salad.Error
	path string
}

// text reads a string field, returning "" when the field is absent.
func (r *joFieldReader) text(key string) string {
	return r.optText(key).Value()
}

// optText reads a string field, distinguishing absent from empty.
func (r *joFieldReader) optText(key string) cwlcore.OptString {
	if r.err != nil {
		return cwlcore.OptString{}
	}

	node, ok := r.m.Get(key)
	if !ok {
		return cwlcore.OptString{}
	}

	text, ok := salad.AsString(node)
	if !ok {
		r.err = salad.Errorf(
			joNodeLoc(node),
			"%s: %s must be a string, but found %s",
			r.path,
			key,
			salad.NodeKind(node),
		)

		return cwlcore.OptString{}
	}

	return cwlcore.NewOptString(text)
}

// node returns the value node of a field, or nil if absent.
func (r *joFieldReader) node(key string) salad.Node {
	node, _ := r.m.Get(key)

	return node
}
