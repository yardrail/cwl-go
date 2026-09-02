package conformance

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// Tag names the fixtures use, named so that goconst does not see a literal repeated
// across the package's tests.
const (
	tagRequired = "required"
	tagWorkflow = "workflow"
	tagTool     = "command_line_tool"
)

// The two fixture manifests. The second is reached through $import, which is what makes
// the relative-path resolution interesting: "sub-tool.cwl" is written relative to the
// sub-suite, not to the top-level manifest that imports it.
const (
	fixtureManifest = `
- tool: tests/echo.cwl
  job: tests/echo-job.json
  output:
    out: hello
    count: 3
  id: inline_entry
  doc: An entry written in the top-level manifest
  tags: [ required, command_line_tool ]

- tool: tests/no-job.cwl
  id: entry_without_a_job
  doc: An entry that names no job order
  tags: [ required ]

- $import: sub/index.yaml
`

	fixtureSubSuite = `
- tool: sub-tool.cwl
  job: sub-job.json
  output:
    out:
      class: File
      checksum: sha1$deadbeef
      size: 12
  id: imported_entry
  doc: An entry written in an $import-ed sub-suite
  tags: [ workflow ]
  should_fail: true
`
)

// writeFixtureCorpus lays out a miniature corpus and returns its root.
func writeFixtureCorpus(t *testing.T) string {
	t.Helper()

	root := t.TempDir()

	err := os.MkdirAll(filepath.Join(root, "sub"), 0o750)
	if err != nil {
		t.Fatalf("creating the sub-suite directory: %v", err)
	}

	files := map[string]string{
		manifestName:     fixtureManifest,
		"sub/index.yaml": fixtureSubSuite,
	}

	for name, body := range files {
		err = os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(body), 0o600)
		if err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	return root
}

func TestManifestReadsJobAndOutput(t *testing.T) {
	t.Parallel()

	tests, err := LoadEntries(writeFixtureCorpus(t))
	if err != nil {
		t.Fatalf("LoadEntries: %v", err)
	}

	want := []Entry{
		{
			Output:     map[string]any{"out": "hello", "count": int64(3)},
			ID:         "inline_entry",
			Doc:        "An entry written in the top-level manifest",
			Tool:       "tests/echo.cwl",
			Job:        "tests/echo-job.json",
			Tags:       []string{tagRequired, tagTool},
			ShouldFail: false,
		},
		{
			Output:     nil,
			ID:         "entry_without_a_job",
			Doc:        "An entry that names no job order",
			Tool:       "tests/no-job.cwl",
			Job:        "",
			Tags:       []string{tagRequired},
			ShouldFail: false,
		},
		{
			Output: map[string]any{
				"out": map[string]any{
					"class":    "File",
					"checksum": "sha1$deadbeef",
					"size":     int64(12),
				},
			},
			ID:         "imported_entry",
			Doc:        "An entry written in an $import-ed sub-suite",
			Tool:       "sub/sub-tool.cwl",
			Job:        "sub/sub-job.json",
			Tags:       []string{tagWorkflow},
			ShouldFail: true,
		},
	}

	if !reflect.DeepEqual(tests, want) {
		t.Errorf("LoadEntries returned\n%#v\nwant\n%#v", tests, want)
	}
}

// writeCorpusManifest lays out a corpus root whose manifest is exactly body, with no
// sub-suites. It stands in for writeFixtureCorpus when a test wants a malformed or
// oddly-shaped manifest rather than the well-formed fixture.
func writeCorpusManifest(t *testing.T, body string) string {
	t.Helper()

	root := t.TempDir()

	err := os.WriteFile(filepath.Join(root, manifestName), []byte(body), 0o600)
	if err != nil {
		t.Fatalf("writing %s: %v", manifestName, err)
	}

	return root
}

func TestLoadManifestWrapsAMalformedManifest(t *testing.T) {
	t.Parallel()

	root := writeCorpusManifest(t, "- tool: [unterminated\n")

	_, err := loadManifest(&corpus{root: root})
	if err == nil {
		t.Fatal("loadManifest accepted a malformed manifest")
	}
}

func TestReadEntriesLoaderError(t *testing.T) {
	t.Parallel()

	root := writeCorpusManifest(t, "- tool: [unterminated\n")

	_, err := readEntries(filepath.Join(root, manifestName), root)
	if err == nil {
		t.Fatal("readEntries accepted a document pkg/salad cannot parse")
	}
}

func TestReadEntriesRejectsAManifestThatIsNotAList(t *testing.T) {
	t.Parallel()

	root := writeCorpusManifest(t, "tool: tests/a.cwl\n")

	_, err := readEntries(filepath.Join(root, manifestName), root)
	if err == nil {
		t.Fatal("readEntries accepted a manifest whose root is a mapping, not a list")
	}

	var se *salad.Error
	if !errors.As(err, &se) {
		t.Fatalf("readEntries error = %v (%T), want a *salad.Error", err, err)
	}
}

func TestCollectEntriesSkipsNonMapAndToollessItems(t *testing.T) {
	t.Parallel()

	loc := salad.SourceLine{}
	scalarItem := salad.NewStringNode(loc, "not a map")
	toollessItem := salad.NewMapNode(loc, []salad.MapEntry{{Key: fieldID, Value: salad.NewStringNode(loc, "x")}})
	seq := salad.NewSeqNode(loc, []salad.Node{scalarItem, toollessItem})

	got := collectEntries(seq, "/root")
	if len(got) != 0 {
		t.Errorf("collectEntries = %+v, want no entries", got)
	}
}

func TestNewEntryReportsFalseWhenToolIsMissing(t *testing.T) {
	t.Parallel()

	loc := salad.SourceLine{}
	entry := salad.NewMapNode(loc, []salad.MapEntry{{Key: fieldID, Value: salad.NewStringNode(loc, "x")}})

	got, ok := newEntry(entry, "/root")
	if ok {
		t.Errorf("newEntry ok = true for an entry with no tool field, got %+v", got)
	}

	if !reflect.DeepEqual(got, Entry{}) {
		t.Errorf("newEntry = %+v, want the zero Entry", got)
	}
}

func TestPathFieldReportsFalseForANonStringValue(t *testing.T) {
	t.Parallel()

	loc := salad.SourceLine{}
	entry := salad.NewMapNode(loc, []salad.MapEntry{{Key: fieldTool, Value: salad.NewIntNode(loc, 3)}})

	_, ok := pathField(entry, fieldTool, "/root")
	if ok {
		t.Error("pathField ok = true for a non-string field value")
	}
}

func TestSourceDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		loc  salad.SourceLine
		root string
		want string
	}{
		{
			name: "no usable location falls back to the corpus root",
			loc:  salad.SourceLine{},
			root: filepath.FromSlash("/corpus"),
			want: filepath.FromSlash("/corpus"),
		},
		{
			name: "a file URL is resolved",
			loc:  salad.SourceLine{File: "file:///corpus/sub/index.yaml"},
			root: filepath.FromSlash("/corpus"),
			want: filepath.FromSlash("/corpus/sub"),
		},
		{
			name: "a bare absolute path",
			loc:  salad.SourceLine{File: filepath.FromSlash("/corpus/sub/index.yaml")},
			root: filepath.FromSlash("/corpus"),
			want: filepath.FromSlash("/corpus/sub"),
		},
		{
			name: "a bare relative path is joined against the root",
			loc:  salad.SourceLine{File: filepath.FromSlash("sub/index.yaml")},
			root: filepath.FromSlash("/corpus"),
			want: filepath.FromSlash("/corpus/sub"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := sourceDir(tt.loc, tt.root)
			if got != tt.want {
				t.Errorf("sourceDir(%+v, %q) = %q, want %q", tt.loc, tt.root, got, tt.want)
			}
		})
	}
}

func TestBoolField(t *testing.T) {
	t.Parallel()

	loc := salad.SourceLine{}

	tests := []struct {
		name  string
		value salad.Node
		want  bool
	}{
		{name: "a true bool", value: salad.NewBoolNode(loc, true), want: true},
		{name: "a false bool", value: salad.NewBoolNode(loc, false), want: false},
		{name: "not a scalar", value: salad.NewSeqNode(loc, []salad.Node{salad.NewBoolNode(loc, true)}), want: false},
		{name: "a scalar that is not a bool", value: salad.NewStringNode(loc, "yes"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			entry := salad.NewMapNode(loc, []salad.MapEntry{{Key: fieldShouldFail, Value: tt.value}})

			got := boolField(entry, fieldShouldFail)
			if got != tt.want {
				t.Errorf("boolField = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("field absent", func(t *testing.T) {
		t.Parallel()

		entry := salad.NewMapNode(loc, nil)
		if boolField(entry, fieldShouldFail) {
			t.Error("boolField = true for an absent field")
		}
	})
}

func TestTagList(t *testing.T) {
	t.Parallel()

	loc := salad.SourceLine{}

	t.Run("field absent", func(t *testing.T) {
		t.Parallel()

		entry := salad.NewMapNode(loc, nil)
		if got := tagList(entry); got != nil {
			t.Errorf("tagList = %v, want nil", got)
		}
	})

	t.Run("field present but not a sequence", func(t *testing.T) {
		t.Parallel()

		entry := salad.NewMapNode(loc, []salad.MapEntry{{Key: fieldTags, Value: salad.NewStringNode(loc, "foo")}})
		if got := tagList(entry); got != nil {
			t.Errorf("tagList = %v, want nil", got)
		}
	})

	t.Run("a non-string item is skipped", func(t *testing.T) {
		t.Parallel()

		seq := salad.NewSeqNode(loc, []salad.Node{salad.NewStringNode(loc, tagRequired), salad.NewIntNode(loc, 3)})
		entry := salad.NewMapNode(loc, []salad.MapEntry{{Key: fieldTags, Value: seq}})

		got := tagList(entry)
		if !reflect.DeepEqual(got, []string{tagRequired}) {
			t.Errorf("tagList = %v, want [%s]", got, tagRequired)
		}
	})
}

func TestDedupeOfNothing(t *testing.T) {
	t.Parallel()

	if got := dedupe(nil); got != nil {
		t.Errorf("dedupe(nil) = %v, want nil", got)
	}

	if got := dedupe(make([]string, 0)); got != nil {
		t.Errorf("dedupe(empty) = %v, want nil", got)
	}
}

func TestManifestIndexMergesEveryTestNamingADocument(t *testing.T) {
	t.Parallel()

	index := indexEntries([]Entry{
		{Tool: testDoc, ID: "second", Tags: []string{tagWorkflow}, ShouldFail: true},
		{Tool: testDoc, ID: "first", Tags: []string{tagWorkflow, tagRequired}},
		{Tool: "tests/b.cwl", ID: "third", Tags: []string{tagTool}, ShouldFail: true},
	})

	shared := index[testDoc]
	if shared == nil {
		t.Fatal("testDoc is missing from the index")
	}

	if !reflect.DeepEqual(shared.ids, []string{"first", "second"}) {
		t.Errorf("ids = %v, want [first second]", shared.ids)
	}

	if !reflect.DeepEqual(shared.tags, []string{tagRequired, tagWorkflow}) {
		t.Errorf("tags = %v, want [%s %s]", shared.tags, tagRequired, tagWorkflow)
	}

	if shared.alwaysFails {
		t.Error("alwaysFails is true although one referencing test expects a pass")
	}

	if !index["tests/b.cwl"].alwaysFails {
		t.Error("alwaysFails is false although the only referencing test is should_fail")
	}
}
