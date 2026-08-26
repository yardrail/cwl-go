package conformance

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
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

func TestManifestIndexMergesEveryTestNamingADocument(t *testing.T) {
	t.Parallel()

	index := indexEntries([]Entry{
		{Tool: "tests/a.cwl", ID: "second", Tags: []string{tagWorkflow}, ShouldFail: true},
		{Tool: "tests/a.cwl", ID: "first", Tags: []string{tagWorkflow, tagRequired}},
		{Tool: "tests/b.cwl", ID: "third", Tags: []string{tagTool}, ShouldFail: true},
	})

	shared := index["tests/a.cwl"]
	if shared == nil {
		t.Fatal("tests/a.cwl is missing from the index")
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
