package cwlcore

import (
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// wantSchemaVersion is the upstream cwl-v1.2 tag this snapshot is pinned to.
const wantSchemaVersion = "v1.2.1"

// wantSchemaFiles is the complete expected content of the embedded schema tree: the root
// schema documents, every $include'd Markdown documentation target, the Schema Salad base
// metaschema that Process.yml $imports by relative path, and the vendoring metadata.
var wantSchemaFiles = []string{
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
}

// schemaRefPattern matches a Schema Salad $import or $include reference in either the YAML
// spelling ("$import: foo.yml", "{$include: foo.md}") or the JSON spelling
// ('{"$import": "foo.yml"}'), capturing the referenced path.
var schemaRefPattern = regexp.MustCompile(`["']?\$(?:import|include)["']?\s*:\s*["']?([^"'}\s,]+)`)

func TestSchemaFSContainsExpectedFiles(t *testing.T) {
	t.Parallel()

	for _, name := range wantSchemaFiles {
		data, err := schemaFS.ReadFile(name)
		if err != nil {
			t.Errorf("expected embedded file %s: %v", name, err)

			continue
		}

		if len(data) == 0 {
			t.Errorf("embedded file %s is empty", name)
		}
	}
}

func TestSchemaFSHasNoUnexpectedFiles(t *testing.T) {
	t.Parallel()

	got := embeddedFiles(t)
	if !slices.Equal(got, wantSchemaFiles) {
		t.Errorf("embedded schema tree = %v, want %v", got, wantSchemaFiles)
	}
}

func TestSchemaVersionMatchesVersionFile(t *testing.T) {
	t.Parallel()

	got := SchemaVersion()
	if got != wantSchemaVersion {
		t.Errorf("SchemaVersion() = %q, want %q", got, wantSchemaVersion)
	}
}

// TestSchemaFSImportClosureIsComplete guards the property that makes the snapshot usable
// offline: every $import/$include target reachable from the embedded schema files is
// itself embedded, so the loader never has to reach outside the FS.
func TestSchemaFSImportClosureIsComplete(t *testing.T) {
	t.Parallel()

	for _, name := range embeddedFiles(t) {
		if !strings.HasSuffix(name, ".yml") {
			continue
		}

		data, err := schemaFS.ReadFile(name)
		if err != nil {
			t.Fatalf("read embedded file %s: %v", name, err)
		}

		checkRefs(t, name, string(data))
	}
}

// checkRefs reports every $import/$include reference in the body of file name that does
// not resolve to another embedded file.
func checkRefs(t *testing.T, name, body string) {
	t.Helper()

	for _, match := range schemaRefPattern.FindAllStringSubmatch(body, -1) {
		target := resolveRef(name, match[1])
		if target == "" {
			continue
		}

		_, err := fs.Stat(schemaFS, target)
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

// embeddedFiles returns every file in the embedded schema tree, sorted by path.
func embeddedFiles(t *testing.T) []string {
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

	err := fs.WalkDir(schemaFS, "schema", walk)
	if err != nil {
		t.Fatalf("walk embedded schema tree: %v", err)
	}

	slices.Sort(files)

	return files
}
