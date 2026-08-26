package salad

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// wantMetaschemaVersion is the upstream schema_salad commit this snapshot is pinned to.
const wantMetaschemaVersion = "c163bf94efe45b4b4aeff3d702cf5bfaa8fa92ba"

// saladFeatures are the per-feature chapters of the Schema Salad specification. Each has a
// documentation .yml in the metaschema plus a _schema/_src/_proc example triplet.
var saladFeatures = []string{
	"field_name",
	"ident_res",
	"link_res",
	"vocab_res",
	"map_res",
	"typedsl_res",
	"sfdsl_res",
}

// exampleKinds are the three roles in a Schema Salad example triplet: the mini-schema, the
// input document, and the expected resolved output.
var exampleKinds = []string{"schema", "src", "proc"}

// metaschemaRefPattern matches a Schema Salad $import or $include reference in either the
// YAML spelling ("$import: foo.yml", "{$include: foo.md}") or the JSON spelling
// ('{"$import": "foo.yml"}'), capturing the referenced path.
var metaschemaRefPattern = regexp.MustCompile(`["']?\$(?:import|include)["']?\s*:\s*["']?([^"'}\s,]+)`)

// examplesDir holds the Salad spec example triplets, vendored as conformance fixtures.
const examplesDir = "testdata/examples"

func TestMetaschemaFSContainsExpectedFiles(t *testing.T) {
	t.Parallel()

	for _, name := range wantMetaschemaFiles() {
		data, err := metaschemaFS.ReadFile(name)
		if err != nil {
			t.Errorf("expected embedded file %s: %v", name, err)

			continue
		}

		if len(data) == 0 {
			t.Errorf("embedded file %s is empty", name)
		}
	}
}

func TestMetaschemaFSHasNoUnexpectedFiles(t *testing.T) {
	t.Parallel()

	got := embeddedFiles(t)

	want := wantMetaschemaFiles()
	if !slices.Equal(got, want) {
		t.Errorf("embedded metaschema tree = %v, want %v", got, want)
	}
}

func TestMetaschemaVersionFileMatchesPin(t *testing.T) {
	t.Parallel()

	data, err := metaschemaFS.ReadFile("metaschema/VERSION")
	if err != nil {
		t.Fatalf("read embedded VERSION: %v", err)
	}

	got := strings.TrimSpace(string(data))
	if got != wantMetaschemaVersion {
		t.Errorf("metaschema/VERSION = %q, want %q", got, wantMetaschemaVersion)
	}
}

// TestMetaschemaFSImportClosureIsComplete guards the property that makes the snapshot
// usable offline: every $import/$include target reachable from the embedded metaschema is
// itself embedded, so the loader never has to reach outside the FS.
func TestMetaschemaFSImportClosureIsComplete(t *testing.T) {
	t.Parallel()

	for _, name := range embeddedFiles(t) {
		if !strings.HasSuffix(name, ".yml") {
			continue
		}

		data, err := metaschemaFS.ReadFile(name)
		if err != nil {
			t.Fatalf("read embedded file %s: %v", name, err)
		}

		checkRefs(t, name, string(data))
	}
}

// TestExampleTripletsArePresent checks the vendored Salad spec example triplets, which the
// conformance tests replay: seven features times schema/src/proc, plus the base metaschema
// that typedsl_res_schema.yml imports.
func TestExampleTripletsArePresent(t *testing.T) {
	t.Parallel()

	want := []string{"metaschema_base.yml"}

	for _, feature := range saladFeatures {
		for _, kind := range exampleKinds {
			want = append(want, feature+"_"+kind+".yml")
		}
	}

	slices.Sort(want)

	entries, err := os.ReadDir(examplesDir)
	if err != nil {
		t.Fatalf("read %s: %v", examplesDir, err)
	}

	var got []string

	for _, entry := range entries {
		info, statErr := os.Stat(filepath.Join(examplesDir, entry.Name()))
		if statErr != nil {
			t.Fatalf("stat example fixture %s: %v", entry.Name(), statErr)
		}

		if info.Size() == 0 {
			t.Errorf("example fixture %s is empty", entry.Name())
		}

		got = append(got, entry.Name())
	}

	if !slices.Equal(got, want) {
		t.Errorf("%s = %v, want %v", examplesDir, got, want)
	}
}

// wantMetaschemaFiles is the complete expected content of the embedded metaschema tree:
// the metaschema itself, every $include'd Markdown and per-feature chapter with its
// example triplet, and the vendoring metadata.
func wantMetaschemaFiles() []string {
	files := []string{
		"metaschema/LICENSE.txt",
		"metaschema/README.md",
		"metaschema/VERSION",
		"metaschema/import_include.md",
		"metaschema/metaschema.yml",
		"metaschema/metaschema_base.yml",
		"metaschema/salad.md",
	}

	for _, feature := range saladFeatures {
		files = append(files, "metaschema/"+feature+".yml")
		for _, kind := range exampleKinds {
			files = append(files, "metaschema/"+feature+"_"+kind+".yml")
		}
	}

	slices.Sort(files)

	return files
}

// checkRefs reports every $import/$include reference in the body of file name that does
// not resolve to another embedded file.
func checkRefs(t *testing.T, name, body string) {
	t.Helper()

	for _, match := range metaschemaRefPattern.FindAllStringSubmatch(body, -1) {
		target := resolveRef(name, match[1])
		if target == "" {
			continue
		}

		_, err := fs.Stat(metaschemaFS, target)
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

// embeddedFiles returns every file in the embedded metaschema tree, sorted by path.
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

	err := fs.WalkDir(metaschemaFS, "metaschema", walk)
	if err != nil {
		t.Fatalf("walk embedded metaschema tree: %v", err)
	}

	slices.Sort(files)

	return files
}
