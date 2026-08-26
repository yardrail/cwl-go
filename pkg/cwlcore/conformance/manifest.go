package conformance

import (
	"net/url"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// Field names in a conformance_tests.yaml entry.
const (
	fieldTool       = "tool"
	fieldID         = "id"
	fieldTags       = "tags"
	fieldShouldFail = "should_fail"
)

// manifestEntry is what the corpus manifest says about one CWL document, merged across
// every test that names it. A single document is frequently referenced by several tests
// with different tags, and occasionally by both a passing and a should_fail test.
type manifestEntry struct {
	// ids are the conformance test ids that reference this document.
	ids []string
	// tags is the union of the feature tags of those tests.
	tags []string
	// alwaysFails is true when every referencing test expects a failure, which is the
	// manifest's strongest available signal that the document itself is meant to be
	// rejected.
	alwaysFails bool
}

// manifest maps a corpus-relative, slash-separated document path to what the test
// manifest says about it.
type manifest map[string]*manifestEntry

// loadManifest reads conformance_tests.yaml through pkg/salad, which resolves the seven
// $import-ed sub-suites in place, and indexes every entry by the document it names.
//
// Loading the manifest with our own loader is deliberate: the manifest is itself a
// Schema Salad document, so this dogfoods $import resolution against a file nobody wrote
// for us. Link checking is off because the manifest has no schema to link against.
func loadManifest(c *corpus) (manifest, error) {
	loader := salad.NewLoader(salad.WithSkipLinkCheck(true))

	abs, err := filepath.Abs(c.manifestPath())
	if err != nil {
		return nil, err
	}

	doc, err := loader.Load(abs)
	if err != nil {
		return nil, err
	}

	entries, ok := salad.AsSeq(doc.Root)
	if !ok {
		return nil, salad.Errorf(salad.SourceLine{File: abs}, "expected the test manifest to be a list")
	}

	root, err := filepath.Abs(c.root)
	if err != nil {
		return nil, err
	}

	return indexEntries(entries, root), nil
}

// indexEntries folds every manifest entry into a per-document index.
func indexEntries(entries *salad.SeqNode, root string) manifest {
	index := make(manifest, entries.Len())

	for _, item := range entries.All() {
		entry, ok := salad.AsMap(item)
		if !ok {
			continue
		}

		key, ok := toolKey(entry, root)
		if !ok {
			continue
		}

		record, seen := index[key]
		if !seen {
			record = &manifestEntry{alwaysFails: true}
			index[key] = record
		}

		mergeEntry(record, entry)
	}

	for _, record := range index {
		slices.Sort(record.ids)
		record.tags = dedupe(record.tags)
	}

	return index
}

// mergeEntry folds one manifest entry's id, tags and should_fail flag into record.
func mergeEntry(record *manifestEntry, entry *salad.MapNode) {
	id, ok := stringField(entry, fieldID)
	if ok {
		record.ids = append(record.ids, id)
	}

	record.tags = append(record.tags, tagList(entry)...)

	if boolField(entry, fieldShouldFail) {
		return
	}

	record.alwaysFails = false
}

// toolKey resolves an entry's "tool" reference to a corpus-relative, slash-separated
// path. The reference is relative to the document the entry was written in, which for an
// $import-ed sub-suite is not the top-level manifest -- so the entry node's own
// SourceLine is what it must be resolved against.
func toolKey(entry *salad.MapNode, root string) (string, bool) {
	tool, ok := stringField(entry, fieldTool)
	if !ok {
		return "", false
	}

	base := sourceDir(entry.Loc(), root)

	rel, err := filepath.Rel(root, filepath.Join(base, filepath.FromSlash(tool)))
	if err != nil {
		return "", false
	}

	return filepath.ToSlash(rel), true
}

// sourceDir is the directory holding the document a node came from, falling back to the
// corpus root when the node carries no usable location.
func sourceDir(loc salad.SourceLine, root string) string {
	file := localPath(loc.File)
	if file == "" {
		return root
	}

	if !filepath.IsAbs(file) {
		file = filepath.Join(root, file)
	}

	return filepath.Dir(file)
}

// localPath turns a document reference into a filesystem path, tolerating both the
// file:// URLs the loader normalizes to and bare paths.
func localPath(ref string) string {
	if ref == "" || !strings.Contains(ref, "://") {
		return filepath.FromSlash(ref)
	}

	parsed, err := url.Parse(ref)
	if err != nil || parsed.Scheme != "file" {
		return ""
	}

	return filepath.FromSlash(path.Clean(parsed.Path))
}

// stringField reads a string-valued field from a manifest entry.
func stringField(entry *salad.MapNode, key string) (string, bool) {
	node, ok := entry.Get(key)
	if !ok {
		return "", false
	}

	return salad.AsString(node)
}

// boolField reads a boolean-valued field, treating anything else as false.
func boolField(entry *salad.MapNode, key string) bool {
	node, ok := entry.Get(key)
	if !ok {
		return false
	}

	scalar, ok := salad.AsScalar(node)
	if !ok || !scalar.IsBool() {
		return false
	}

	return scalar.AsBool()
}

// tagList reads an entry's feature tags.
func tagList(entry *salad.MapNode) []string {
	node, ok := entry.Get(fieldTags)
	if !ok {
		return nil
	}

	seq, ok := salad.AsSeq(node)
	if !ok {
		return nil
	}

	tags := make([]string, 0, seq.Len())

	for _, item := range seq.All() {
		tag, ok := salad.AsString(item)
		if ok {
			tags = append(tags, tag)
		}
	}

	return tags
}

// dedupe returns the sorted distinct members of in.
func dedupe(in []string) []string {
	if len(in) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))

	for _, v := range in {
		_, dup := seen[v]
		if dup {
			continue
		}

		seen[v] = struct{}{}

		out = append(out, v)
	}

	slices.Sort(out)

	return out
}
