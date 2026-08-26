package cwlexec

import (
	"context"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/yardrail/cwl-go/pkg/cwlcore"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// Normalising the two filesystem values.
//
// A File or Directory arrives from a job file as a mapping with a handful of author-supplied
// fields and leaves as a fully-populated [cwlcore.File] or [cwlcore.Directory]: an absolute
// file:// location, an absolute local path, the four derived name fields, and — for a File — a
// size and a sha1 checksum read from the bytes themselves.

// The field names of a File, of a Directory, and the class discriminator they share.
const (
	joKeyClass          = "class"
	joKeyLocation       = "location"
	joKeyPath           = "path"
	joKeyBasename       = "basename"
	joKeyDirname        = "dirname"
	joKeyNameroot       = "nameroot"
	joKeyNameext        = "nameext"
	joKeyChecksum       = "checksum"
	joKeySize           = "size"
	joKeyFormat         = "format"
	joKeyContents       = "contents"
	joKeySecondaryFiles = "secondaryFiles"
	joKeyListing        = "listing"
)

// joFileFields and joDirectoryFields are the fields each filesystem value may carry.
//
// Directory's set is much smaller, and deliberately so: the vendored schema gives it only class,
// location, path, basename and listing. It has no size, no checksum, no format and no
// secondaryFiles, so a Directory can neither carry nor satisfy a format constraint.
//
// The five derived File fields — dirname, nameroot, nameext, checksum and size — are accepted
// but never read. The specification says the implementation "must set" each of them from the
// resource, and cwltest re-derives size and checksum from disk when it compares outputs, so a
// value written by hand in a job order would at best duplicate what is computed here and at
// worst contradict it.
var (
	joFileFields = []string{
		joKeyClass, joKeyLocation, joKeyPath, joKeyBasename, joKeyDirname, joKeyNameroot, joKeyNameext,
		joKeyChecksum, joKeySize, joKeyFormat, joKeyContents, joKeySecondaryFiles,
	}

	joDirectoryFields = []string{joKeyClass, joKeyLocation, joKeyPath, joKeyBasename, joKeyListing}
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

	secondary, _, err := l.entries(ctx, m, joKeySecondaryFiles, v)
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

	reader := &joFieldReader{m: m, path: v.path}
	ref := joResolveRef(reader, v.base)
	contents := reader.optText(joKeyContents)
	basename := reader.text(joKeyBasename)
	format := l.vocab.expandFormat(reader.text(joKeyFormat))

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

	parts := joSplitBasename(basename)

	return &cwlcore.File{
		Node:     m,
		Location: ref.location,
		Path:     ref.local,
		Basename: basename,
		Dirname:  joDirnameOf(ref.local),
		Nameroot: parts.root,
		Nameext:  parts.ext,
		Format:   format,
		Contents: contents,
	}, nil
}

// joCheckFileIdentity enforces the two rules that decide whether a File mapping names anything at
// all: it must have a location, a path or contents, and its contents must fit in 64 KiB.
//
// Process.yml, File: "If no `location` or `path` is specified, a file object must specify
// `contents` with the UTF-8 text content of the file. This is a 'file literal'. ... The maximum
// size of `contents` is 64 kilobytes".
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

// normalizeDirectory turns a Directory mapping into a fully-populated [cwlcore.Directory].
//
// An absent `listing` is read from disk only when the declaring parameter's `loadListing` asks
// for it; under the `no_listing` default it stays nil. That is not the same as an empty
// directory: nil means nobody read the listing, so a later consumer still can, whereas an empty
// slice would assert that the directory has no entries.
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

	reader := &joFieldReader{m: m, path: v.path}
	ref := joResolveRef(reader, v.base)
	basename := reader.text(joKeyBasename)

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

// directoryListing settles a Directory's listing, and with it the two checks that need to know
// whether the job order supplied one: a Directory names nothing at all unless it has a location,
// a path or a listing, and one that does name a local path must name a directory that exists.
func (l *joLoader) directoryListing(
	ctx context.Context, m *salad.MapNode, ref *joFileRef, v *joValueCtx,
) ([]cwlcore.FileOrDirectory, *salad.Error) {
	listing, supplied, err := l.entries(ctx, m, joKeyListing, v)
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

	return joReadListing(ref.local, info, v.listing, m, v)
}

// joReadListing materialises a Directory's listing from disk at the depth loadListing asks for.
//
// Process.yml, LoadListingEnum: `no_listing` means "Do not load the directory listing",
// `shallow_listing` that "Only load the top level listing ... but do not recurse into
// subdirectories", and `deep_listing` that the implementation must "Load the directory listing
// and recursively load all subdirectories as well".
//
// The walk itself is the one the output side already uses, which is deliberate: an input
// Directory and an output Directory are the same value read from the same filesystem, and two
// walks would be two chances to disagree about entry order, about the size and checksum a listed
// File carries, or about what to do with a symlink that points back up the tree.
func joReadListing(
	local string, info fs.FileInfo, mode cwlcore.LoadListingEnum, m *salad.MapNode, v *joValueCtx,
) ([]cwlcore.FileOrDirectory, *salad.Error) {
	if mode == "" || mode == cwlcore.LoadListingNone {
		return nil, nil
	}

	dir, err := outCollectDirectory(local, info, mode)
	if err != nil {
		return nil, salad.Errorf(m.Loc(), "%s: reading the directory listing: %v", v.path, err)
	}

	return dir.Listing, nil
}

// entries converts a `listing` or a `secondaryFiles` field, reporting whether the field was
// present at all so that an absent listing stays nil and an explicitly empty one does not.
//
// A single mapping is accepted where a list is declared. Job files are written by hand and the
// schema's own one-or-many convention makes the shorthand the natural thing to write; nothing is
// lost by accepting it, since the element type is unchanged either way.
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

// entry converts one member of a listing or a secondaryFiles list, which the schema types as
// `File | Directory` and nothing else.
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

// joFileRef is a resolved location: the absolute IRI, the local path it names when there is one,
// and the final segment of the reference, which is what an absent basename is derived from.
type joFileRef struct {
	location string
	local    string
	name     string
}

// joResolveRef resolves a File or Directory's location against base, an absolute directory.
//
// `path` wins over `location` when both are given, which is the precedence the specification
// states for cwl.output.json and the only one that makes sense for an input object too: path
// names a real filesystem, location names a resource that need not be on one.
//
// A location with no scheme is a relative reference and resolves against base — the job file's
// directory for a value the job supplied, the process document's for one that came from a
// `default`. A `file:` IRI is already absolute and yields both a location and a local path. Any
// other scheme, http or s3 or otherwise, is carried through untouched with no local path, so its
// basename is still derived but no size or checksum is computed: fetching a remote resource is a
// staging concern, not a loading one.
func joResolveRef(r *joFieldReader, base string) joFileRef {
	if local := r.text(joKeyPath); local != "" {
		return joRefAt(joUnwrapFileIRI(local), base)
	}

	location := r.text(joKeyLocation)
	if location == "" {
		return joFileRef{}
	}

	parsed, err := url.Parse(location)
	if err != nil {
		r.err = salad.Errorf(joNodeLoc(r.node(joKeyLocation)),
			"%s: location %q is not a valid IRI: %v", r.path, location, err)

		return joFileRef{}
	}

	if parsed.Scheme != "" && parsed.Scheme != joSchemeFile {
		return joFileRef{location: location, name: path.Base(parsed.Path)}
	}

	return joRefAt(parsed.Path, base)
}

// joRefAt builds a reference to a local filesystem path, which may be relative to base.
func joRefAt(local, base string) joFileRef {
	abs := joAbsolutize(local, base)

	return joFileRef{location: joFileURI(abs), local: abs, name: filepath.Base(abs)}
}

// joUnwrapFileIRI returns the filesystem path a `path` field names.
//
// The field is a local host path as an author writes it, but it does not always reach here that
// way: Process.yml gives File.path and Directory.path a jsonldPredicate of `_type: "@id"`, so
// pkg/salad resolves the field as a link wherever a document is loaded against the schema. A `path`
// written inside a parameter's `default` therefore arrives as an absolute `file:` IRI, while the
// same field in a job file — parsed with [salad.Parse] and no schema — arrives exactly as written.
//
// Both spellings must name the same file, so a `file:` IRI is unwrapped back into the path it
// carries and anything else is returned untouched. An opaque `file:` reference, which carries no
// path at all, is left alone rather than collapsed to the empty string that would make the value
// look like a file literal.
func joUnwrapFileIRI(ref string) string {
	parsed, err := url.Parse(ref)
	if err != nil || parsed.Scheme != joSchemeFile || parsed.Path == "" {
		return ref
	}

	return parsed.Path
}

// joAbsolutize resolves a possibly-relative filesystem path against base.
func joAbsolutize(p, base string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}

	return filepath.Join(base, p)
}

// joFileURI renders an absolute local path as a file:// IRI, percent-escaping as the URL syntax
// requires.
func joFileURI(p string) string {
	uri := url.URL{Scheme: joSchemeFile, Path: p}

	return uri.String()
}

// joDirnameOf returns the directory component of a local path, and "" when there is no path.
//
// Process.yml, dirname: "The implementation must set this field based on the value of `path`
// prior to evaluating parameter references or expressions in a CommandLineTool document". A
// value with no path — a file literal, or a remote location — therefore has no dirname.
func joDirnameOf(local string) string {
	if local == "" {
		return ""
	}

	return filepath.Dir(local)
}

// joNameParts is a basename split into the two halves the specification requires, kept together so
// that they are always derived from the same basename by the same rule.
type joNameParts struct {
	root string
	ext  string
}

// joSplitBasename splits a basename into nameroot and nameext.
//
// Process.yml, nameroot: "The basename root such that `nameroot + nameext == basename`, and
// `nameext` is empty or begins with a period and contains at most one period. For the purposes
// of path splitting leading periods on the basename are ignored; a basename of `.cshrc` will
// have a nameroot of `.cshrc`".
//
// So: skip the run of leading periods, split what remains at its *last* period, and give the
// leading periods back to the root. ".cshrc" splits as (".cshrc", ""), "a.b.c" as ("a.b", ".c"),
// ".gitignore.bak" as (".gitignore", ".bak"), and "README" as ("README", ""). This is the same
// rule Python's os.path.splitext implements, which is what the reference implementation calls.
func joSplitBasename(basename string) joNameParts {
	dots := len(basename) - len(strings.TrimLeft(basename, "."))

	last := strings.LastIndexByte(basename[dots:], '.')
	if last < 0 {
		return joNameParts{root: basename}
	}

	return joNameParts{root: basename[:dots+last], ext: basename[dots+last:]}
}

// joStatDirectory reports an error unless local names an existing directory, and returns what the
// stat found so that a listing walk does not have to repeat it. An empty local is a directory
// literal or a remote location: there is nothing to stat, so the result is a nil FileInfo and no
// error.
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

// joFieldReader reads the string-valued fields of a filesystem value, remembering the first
// failure instead of returning one per call.
//
// It exists so that the four or five fields a File is built from can be read as a block and
// checked once, which keeps the construction readable; every read after a failure is a no-op, so
// the first diagnostic is the one reported.
type joFieldReader struct {
	m    *salad.MapNode
	err  *salad.Error
	path string
}

// text reads a string field, returning "" when the field is absent.
func (r *joFieldReader) text(key string) string {
	return r.optText(key).Value()
}

// optText reads a string field, distinguishing an absent field from an empty one. `contents` is
// why that distinction matters: "" is an empty file literal that must be created on disk, not an
// unread file.
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

// node returns the value node of a field, or nil when the field is absent. It exists so that a
// diagnostic can be located at the offending field rather than at the whole mapping.
func (r *joFieldReader) node(key string) salad.Node {
	node, _ := r.m.Get(key)

	return node
}
