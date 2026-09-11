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
	fieldJob        = "job"
	fieldOutput     = "output"
	fieldID         = "id"
	fieldDoc        = "doc"
	fieldTags       = "tags"
	fieldShouldFail = "should_fail"
)

// Entry is one conformance manifest entry: document to run, job order, and expected output.
type Entry struct {
	Output     any      // expected output, or nil
	ID         string   // conformance test id
	Doc        string   // one-line description
	Tool       string   // CWL document path (corpus-relative)
	Job        string   // job order path, or empty
	Tags       []string // feature tags
	ShouldFail bool
}

// LoadEntries reads the conformance manifest and returns one [Entry] per test.
func LoadEntries(root string) ([]Entry, error) {
	return readEntries(filepath.Join(root, manifestName), root)
}

// manifestEntry is per-document metadata merged from all referencing tests.
type manifestEntry struct {
	ids         []string // test ids referencing this document
	tags        []string // union of feature tags
	alwaysFails bool     // true if every referencing test expects failure
}

// manifest maps a corpus-relative, slash-separated document path to what the test
// manifest says about it.
type manifest map[string]*manifestEntry

// loadManifest reads the corpus manifest and indexes every entry by the document it names.
func loadManifest(c *corpus) (manifest, error) {
	tests, err := readEntries(c.manifestPath(), c.root)
	if err != nil {
		return nil, err
	}

	return indexEntries(tests), nil
}

// readEntries reads conformance_tests.yaml through pkg/salad, resolving $imports.
func readEntries(manifestPath, root string) ([]Entry, error) {
	loader := salad.NewLoader(salad.WithSkipLinkCheck(true))

	abs, err := filepath.Abs(manifestPath)
	if err != nil {
		return nil, err
	}

	doc, err := loader.Load(abs)
	if err != nil {
		return nil, err
	}

	entries, ok := salad.AsSeq(doc.Root)
	if !ok {
		return nil, salad.Errorf(
			salad.SourceLine{
				File:  abs,
				Start: salad.Position{Line: 0, Column: 0, Offset: 0},
				End:   salad.Position{Line: 0, Column: 0, Offset: 0},
			},
			"expected the test manifest to be a list",
		)
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	return collectEntries(entries, absRoot), nil
}

// collectEntries renders each manifest entry, skipping any that names no document.
func collectEntries(entries *salad.SeqNode, root string) []Entry {
	tests := make([]Entry, 0, entries.Len())

	for _, item := range entries.All() {
		entry, ok := salad.AsMap(item)
		if !ok {
			continue
		}

		test, ok := newEntry(entry, root)
		if !ok {
			continue
		}

		tests = append(tests, test)
	}

	return tests
}

// newEntry renders one manifest entry. Returns false if no tool is named.
func newEntry(entry *salad.MapNode, root string) (Entry, bool) {
	tool, ok := pathField(entry, fieldTool, root)
	if !ok {
		return Entry{Output: nil, ID: "", Doc: "", Tool: "", Job: "", Tags: nil, ShouldFail: false}, false
	}

	job, _ := pathField(entry, fieldJob, root)

	return Entry{
		Output:     outputValue(entry),
		ID:         stringOrEmpty(entry, fieldID),
		Doc:        stringOrEmpty(entry, fieldDoc),
		Tool:       tool,
		Job:        job,
		Tags:       tagList(entry),
		ShouldFail: boolField(entry, fieldShouldFail),
	}, true
}

// indexEntries folds every test into a per-document index.
func indexEntries(tests []Entry) manifest {
	index := make(manifest, len(tests))

	for i := range tests {
		test := &tests[i]

		record, seen := index[test.Tool]
		if !seen {
			record = &manifestEntry{ids: nil, tags: nil, alwaysFails: true}
			index[test.Tool] = record
		}

		mergeEntry(record, test)
	}

	for _, record := range index {
		slices.Sort(record.ids)
		record.tags = dedupe(record.tags)
	}

	return index
}

// mergeEntry folds one test's id, tags and should_fail flag into record.
func mergeEntry(record *manifestEntry, test *Entry) {
	if test.ID != "" {
		record.ids = append(record.ids, test.ID)
	}

	record.tags = append(record.tags, test.Tags...)

	if test.ShouldFail {
		return
	}

	record.alwaysFails = false
}

// pathField resolves a document reference to a corpus-relative path.
func pathField(entry *salad.MapNode, key, root string) (string, bool) {
	ref, ok := stringField(entry, key)
	if !ok {
		return "", false
	}

	base := sourceDir(entry.Loc(), root)

	rel, err := filepath.Rel(root, filepath.Join(base, filepath.FromSlash(ref)))
	if err != nil {
		return "", false
	}

	return filepath.ToSlash(rel), true
}

// outputValue reads an entry's expected output object, or nil.
func outputValue(entry *salad.MapNode) any {
	node, ok := entry.Get(fieldOutput)
	if !ok {
		return nil
	}

	return salad.ToAny(node)
}

// sourceDir returns the directory of the document a node came from.
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

// localPath converts a document reference (file:// URL or bare path) to a filesystem path.
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

// stringOrEmpty reads a string field, returning "" if absent.
func stringOrEmpty(entry *salad.MapNode, key string) string {
	value, _ := stringField(entry, key)

	return value
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
