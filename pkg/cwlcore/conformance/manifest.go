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

// Entry is one entry of the cwl-v1.2 conformance manifest, in the shape a harness needs to
// run it: which document to run, against which job order, and what the run must produce.
//
// Tool and Job are corpus-relative, slash-separated paths. Job is empty when the entry
// names none, which means the process runs against an empty input object.
//
// Output is the expected output object as plain Go values, and is nil both when the entry
// declares no output and when it declares a null one. cwltest draws no distinction between
// those either -- it reads the field with dict.get, which answers None for both -- so
// neither does this.
type Entry struct {
	// Output is the expected output object, or nil when the entry declares none.
	Output any
	// ID is the entry's conformance test id, such as "cl_basic_generation".
	ID string
	// Doc is the entry's one-line description.
	Doc string
	// Tool is the CWL document the test runs.
	Tool string
	// Job is the job order it runs against, empty when the entry names none.
	Job string
	// Tags are the entry's feature tags, as written. An entry carrying none is treated
	// as required by cwltest; that policy belongs to the harness, not to the reader.
	Tags []string
	// ShouldFail records an entry that passes only when the run fails.
	ShouldFail bool
}

// LoadEntries reads the conformance manifest at the root of a cwl-v1.2 corpus checkout and
// returns one [Entry] per entry, in manifest order.
//
// It is exported so that the in-process execution driver in pkg/cwlexec/conformance reads
// the suite through this reader rather than through a second parser of its own. Two
// parsers of the same file is how the in-process numbers and cwltest's would quietly come
// to disagree about which tests exist, which is precisely what that driver is checked
// against.
func LoadEntries(root string) ([]Entry, error) {
	return readEntries(filepath.Join(root, manifestName), root)
}

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

// loadManifest reads the corpus manifest and indexes every entry by the document it names.
func loadManifest(c *corpus) (manifest, error) {
	tests, err := readEntries(c.manifestPath(), c.root)
	if err != nil {
		return nil, err
	}

	return indexEntries(tests), nil
}

// readEntries reads conformance_tests.yaml through pkg/salad, which resolves the seven
// $import-ed sub-suites in place, and renders every entry as an [Entry].
//
// Loading the manifest with our own loader is deliberate: the manifest is itself a
// Schema Salad document, so this dogfoods $import resolution against a file nobody wrote
// for us. Link checking is off because the manifest has no schema to link against.
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
		return nil, salad.Errorf(salad.SourceLine{File: abs}, "expected the test manifest to be a list")
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

// newEntry renders one manifest entry, reporting false for an entry that names no document
// to run and so cannot be a test.
func newEntry(entry *salad.MapNode, root string) (Entry, bool) {
	tool, ok := pathField(entry, fieldTool, root)
	if !ok {
		return Entry{}, false
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
			record = &manifestEntry{alwaysFails: true}
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

// pathField resolves an entry's document reference to a corpus-relative, slash-separated
// path. The reference is relative to the document the entry was written in, which for an
// $import-ed sub-suite is not the top-level manifest -- so the entry node's own
// SourceLine is what it must be resolved against.
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

// outputValue reads an entry's expected output object as plain Go values, which is nil
// both for an absent field and for an explicitly null one.
func outputValue(entry *salad.MapNode) any {
	node, ok := entry.Get(fieldOutput)
	if !ok {
		return nil
	}

	return salad.ToAny(node)
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

// stringOrEmpty reads a string-valued field, answering "" when it is absent or is not a
// string. It is for the descriptive fields, where absence and emptiness mean the same
// thing to every caller.
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
