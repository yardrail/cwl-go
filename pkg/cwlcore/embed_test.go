package cwlcore

import (
	"embed"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// vendoredSchema describes one embedded schema tree for the assertions below: the file
// system it lives in, the directory its paths are rooted at, the upstream tag it is
// pinned to, and the complete set of files it is expected to hold.
type vendoredSchema struct {
	files embed.FS
	root  string
	tag   string
	names []string
}

// vendoredSchemas is every embedded schema tree, with the complete expected content of
// each: the root schema documents, every $include'd Markdown documentation target, the
// Schema Salad base metaschema that Process.yml $imports by relative path, the upstream
// license, and the vendoring metadata.
//
// The v1.0 and v1.1 trees hold no Operation.yml. Operation is a v1.2 addition, so its
// absence is part of what those trees are for.
var vendoredSchemas = []vendoredSchema{
	{
		files: schemaFS,
		root:  "schema",
		tag:   "v1.2.1",
		names: []string{
			"schema/CommandLineTool-standalone.yml",
			"schema/CommandLineTool.yml",
			"schema/CommonWorkflowLanguage.yml",
			"schema/LICENSE.txt",
			"schema/Operation.yml",
			"schema/Process.yml",
			"schema/README.md",
			"schema/VERSION",
			"schema/Workflow.yml",
			"schema/concepts.md",
			"schema/contrib.md",
			"schema/intro.md",
			"schema/invocation.md",
			"schema/salad/schema_salad/metaschema/metaschema_base.yml",
		},
	},
	{
		files: schemaV11FS,
		root:  "schema-v1.1",
		tag:   "v1.1.0",
		names: []string{
			"schema-v1.1/CommandLineTool-standalone.yml",
			"schema-v1.1/CommandLineTool.yml",
			"schema-v1.1/CommonWorkflowLanguage.yml",
			"schema-v1.1/LICENSE.txt",
			"schema-v1.1/Process.yml",
			"schema-v1.1/README.md",
			"schema-v1.1/VERSION",
			"schema-v1.1/Workflow.yml",
			"schema-v1.1/concepts.md",
			"schema-v1.1/contrib.md",
			"schema-v1.1/intro.md",
			"schema-v1.1/invocation.md",
			"schema-v1.1/salad/schema_salad/metaschema/metaschema_base.yml",
		},
	},
	{
		files: schemaV10FS,
		root:  "schema-v1.0",
		tag:   "v1.0.2",
		names: []string{
			"schema-v1.0/CommandLineTool-standalone.yml",
			"schema-v1.0/CommandLineTool.yml",
			"schema-v1.0/CommonWorkflowLanguage.yml",
			"schema-v1.0/LICENSE.txt",
			"schema-v1.0/Process.yml",
			"schema-v1.0/README.md",
			"schema-v1.0/VERSION",
			"schema-v1.0/Workflow.yml",
			"schema-v1.0/concepts.md",
			"schema-v1.0/contrib.md",
			"schema-v1.0/intro.md",
			"schema-v1.0/invocation.md",
			"schema-v1.0/salad/schema_salad/metaschema/metaschema_base.yml",
		},
	},
}

// schemaRefPattern matches a Schema Salad $import or $include reference in either the YAML
// spelling ("$import: foo.yml", "{$include: foo.md}") or the JSON spelling
// ('{"$import": "foo.yml"}'), capturing the referenced path.
var schemaRefPattern = regexp.MustCompile(`["']?\$(?:import|include)["']?\s*:\s*["']?([^"'}\s,]+)`)

func TestSchemaFSContainsExpectedFiles(t *testing.T) {
	t.Parallel()

	for _, tree := range vendoredSchemas {
		t.Run(tree.root, func(t *testing.T) {
			t.Parallel()
			checkPresent(t, tree)
		})
	}
}

// checkPresent reports every expected file of one tree that is missing or empty.
func checkPresent(t *testing.T, tree vendoredSchema) {
	t.Helper()

	for _, name := range tree.names {
		data, err := tree.files.ReadFile(name)
		if err != nil {
			t.Errorf("expected embedded file %s: %v", name, err)

			continue
		}

		if len(data) == 0 {
			t.Errorf("embedded file %s is empty", name)
		}
	}
}

// TestSchemaFSHasNoUnexpectedFiles fails on an extra file as well as on a missing one.
// That is the half that keeps a vendored tree from drifting: copying one file too many
// out of an upstream checkout is exactly as much of a mistake as copying one too few,
// and only the equality catches it.
func TestSchemaFSHasNoUnexpectedFiles(t *testing.T) {
	t.Parallel()

	for _, tree := range vendoredSchemas {
		t.Run(tree.root, func(t *testing.T) {
			t.Parallel()

			got := embeddedFiles(t, tree)
			if !slices.Equal(got, tree.names) {
				t.Errorf("embedded schema tree = %v, want %v", got, tree.names)
			}
		})
	}
}

func TestSchemaVersionMatchesVersionFile(t *testing.T) {
	t.Parallel()

	for _, tree := range vendoredSchemas {
		t.Run(tree.root, func(t *testing.T) {
			t.Parallel()

			raw, err := tree.files.ReadFile(path.Join(tree.root, "VERSION"))
			if err != nil {
				t.Fatalf("reading %s/VERSION: %v", tree.root, err)
			}

			got := strings.TrimSpace(string(raw))
			if got != tree.tag {
				t.Errorf("%s/VERSION = %q, want %q", tree.root, got, tree.tag)
			}
		})
	}
}

// TestSchemaVersionReportsTheV12Tag pins the one tag the package reports at runtime.
func TestSchemaVersionReportsTheV12Tag(t *testing.T) {
	t.Parallel()

	got := SchemaVersion()
	if got != vendoredSchemas[0].tag {
		t.Errorf("SchemaVersion() = %q, want %q", got, vendoredSchemas[0].tag)
	}
}

// TestSchemaFSImportClosureIsComplete guards the property that makes the snapshots usable
// offline: every $import/$include target reachable from an embedded schema file is itself
// embedded in the same tree, so the loader never has to reach outside the FS -- and never
// reaches into another version's tree, which shares its file names.
func TestSchemaFSImportClosureIsComplete(t *testing.T) {
	t.Parallel()

	for _, tree := range vendoredSchemas {
		t.Run(tree.root, func(t *testing.T) {
			t.Parallel()
			checkClosure(t, tree)
		})
	}
}

// checkClosure reports every $import/$include in one tree that leaves it.
func checkClosure(t *testing.T, tree vendoredSchema) {
	t.Helper()

	for _, name := range embeddedFiles(t, tree) {
		if !strings.HasSuffix(name, ".yml") {
			continue
		}

		data, err := tree.files.ReadFile(name)
		if err != nil {
			t.Fatalf("read embedded file %s: %v", name, err)
		}

		checkRefs(t, tree, name, string(data))
	}
}

// checkRefs reports every $import/$include reference in the body of file name that does
// not resolve to another file embedded in the same tree.
func checkRefs(t *testing.T, tree vendoredSchema, name, body string) {
	t.Helper()

	for _, match := range schemaRefPattern.FindAllStringSubmatch(body, -1) {
		target := resolveRef(name, match[1])
		if target == "" {
			continue
		}

		_, err := fs.Stat(tree.files, target)
		if err != nil {
			t.Errorf("%s references %q, which resolves to unvendored %s", name, match[1], target)
		}
	}
}

// resolveRef resolves a $import/$include reference found in file name to a path within the
// embedded FS, returning "" for references that need no local file (remote URLs and
// fragment-only references).
func resolveRef(name, ref string) string {
	target, _, _ := strings.Cut(ref, "#")
	if target == "" || strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return ""
	}

	return path.Join(path.Dir(name), target)
}

// embeddedFiles returns every file in one embedded schema tree, sorted by path.
func embeddedFiles(t *testing.T, tree vendoredSchema) []string {
	t.Helper()

	var files []string

	walk := func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !entry.IsDir() {
			files = append(files, name)
		}

		return nil
	}

	err := fs.WalkDir(tree.files, tree.root, walk)
	if err != nil {
		t.Fatalf("walk embedded schema tree %s: %v", tree.root, err)
	}

	slices.Sort(files)

	return files
}
