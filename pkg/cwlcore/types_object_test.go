package cwlcore

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestFileAndDirectoryClass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		object FileOrDirectory
		name   string
		want   string
	}{
		{name: ClassFile, object: &File{}, want: ClassFile},
		{name: ClassDirectory, object: &Directory{}, want: ClassDirectory},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.object.Class(); got != tc.want {
				t.Errorf("Class() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFileAndDirectoryAreRecursive(t *testing.T) {
	t.Parallel()

	// The two types are mutually recursive through File.SecondaryFiles and
	// Directory.Listing, and a type switch over FileOrDirectory is how a
	// consumer walks the tree.
	index := &File{Basename: "reads.bam.bai"}
	reads := &File{Basename: "reads.bam", SecondaryFiles: []FileOrDirectory{index}}
	dir := &Directory{Basename: "run", Listing: []FileOrDirectory{reads, &Directory{Basename: "logs"}}}

	got := countObjects(dir)
	if got.files != 2 || got.dirs != 2 {
		t.Errorf("walk found %d files and %d directories, want 2 and 2", got.files, got.dirs)
	}
}

// objectCounts is what countObjects tallies.
type objectCounts struct {
	files int
	dirs  int
}

// add returns the sum of two tallies.
func (c objectCounts) add(other objectCounts) objectCounts {
	return objectCounts{files: c.files + other.files, dirs: c.dirs + other.dirs}
}

// countObjects walks a filesystem value and counts what it finds, exercising
// both recursive edges.
func countObjects(object FileOrDirectory) objectCounts {
	switch v := object.(type) {
	case *File:
		counts := objectCounts{files: 1}
		for _, child := range v.SecondaryFiles {
			counts = counts.add(countObjects(child))
		}

		return counts
	case *Directory:
		counts := objectCounts{dirs: 1}
		for _, child := range v.Listing {
			counts = counts.add(countObjects(child))
		}

		return counts
	default:
		return objectCounts{}
	}
}

func TestFileZeroValue(t *testing.T) {
	t.Parallel()

	// A File whose size and contents were never supplied must not look like
	// an empty file literal.
	var file File

	if file.Size.IsSet() || file.Contents.IsSet() {
		t.Error("the zero File claims a size or contents")
	}

	if file.SecondaryFiles != nil || file.Node != nil {
		t.Error("the zero File carries secondary files or a node")
	}

	// A nil Listing means "fetch it from Location", not "empty directory".
	var dir Directory
	if dir.Listing != nil {
		t.Error("the zero Directory carries a listing")
	}
}

// TestFilesystemValueFieldsMatchSchema checks, field by field, that File and
// Directory carry exactly what the vendored schema declares — no more, and
// crucially no less. It uses reflection rather than a hand-written list of Go
// field names so that adding a field to the struct without adding it to the
// schema, or the reverse, fails here rather than silently dropping a CWL
// feature at decode time.
func TestFilesystemValueFieldsMatchSchema(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value  any
		record string
	}{
		{record: ClassFile, value: File{}},
		{record: ClassDirectory, value: Directory{}},
	}

	declared := recordFieldSets(t)

	for _, tc := range tests {
		t.Run(tc.record, func(t *testing.T) {
			t.Parallel()

			want, ok := declared[tc.record]
			if !ok {
				t.Fatalf("the vendored schema declares no %q record", tc.record)
			}

			assertSameNames(t, tc.record+" field", want, documentFieldNames(tc.value))
		})
	}
}

// documentFieldNames returns the document keys a value's exported Go fields
// correspond to, sorted. Node is skipped because it holds the source node
// rather than a document field, and "class" is added because it is a schema
// field answered by the Class method rather than stored.
func documentFieldNames(value any) []string {
	typ := reflect.TypeOf(value)

	names := make([]string, 0, typ.NumField()+1)
	names = append(names, "class")

	for field := range typ.Fields() {
		name := field.Name
		if name == "Node" {
			continue
		}

		names = append(names, strings.ToLower(name[:1])+name[1:])
	}

	slices.Sort(names)

	return names
}

// recordFieldSets maps each vendored record's local name to its declared field
// names, sorted.
func recordFieldSets(t *testing.T) map[string][]string {
	t.Helper()

	sets := make(map[string][]string, 64)

	for _, rec := range schemaRecords(t) {
		name, ok := recordName(rec)
		if !ok {
			continue
		}

		if fields := recordFieldNames(rec); len(fields) > 0 {
			slices.Sort(fields)
			sets[name] = fields
		}
	}

	return sets
}

func TestFilesystemClassSymbolsMatchSchema(t *testing.T) {
	t.Parallel()

	want := map[string]string{ClassFile: ClassFile, ClassDirectory: ClassDirectory}

	for _, rec := range schemaRecords(t) {
		name, ok := recordName(rec)
		if !ok || want[name] == "" {
			continue
		}

		symbol, found := classFieldSymbol(rec)
		if !found {
			t.Errorf("schema record %q pins no class symbol", name)

			continue
		}

		if symbol != want[name] {
			t.Errorf("%s: schema pins class to %q, the model uses %q", name, symbol, want[name])
		}
	}
}
