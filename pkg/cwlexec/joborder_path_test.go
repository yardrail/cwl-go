package cwlexec

import (
	"path/filepath"
	"testing"
)

// jobPathReads is a plausible absolute path, named because the table below repeats it in both the
// wrapped and the bare spelling and goconst counts occurrences package-wide.
const jobPathReads = "/data/reads.cram"

// TestFilePathWrittenAsAFileIRIResolvesToTheSameFile pins the second spelling a `path` reaches the
// loader in. Process.yml gives File.path and Directory.path a jsonldPredicate of `_type: "@id"`, so
// pkg/salad resolves the field as a link wherever a document is loaded against the schema: a `path`
// written inside a parameter's `default` arrives as an absolute `file:` IRI rather than as the
// relative path its author wrote. Both spellings must name the same file.
func TestFilePathWrittenAsAFileIRIResolvesToTheSameFile(t *testing.T) {
	t.Parallel()

	fixtures := jobFixtures(t)
	want := filepath.Join(fixtures, "files", "hello.txt")

	src := "f: {class: File, path: 'file://" + want + "'}"

	values := jobMustParse(t, t.TempDir(), src, jobFileTool())

	file := jobFileValue(t, values, "f")
	if file.Path != want {
		t.Errorf("path = %q, want %q", file.Path, want)
	}

	if file.Checksum != jobSumHello {
		t.Errorf("checksum = %q, want the hello.txt digest", file.Checksum)
	}
}

// TestUnwrapFileIRI covers the spellings a `path` field can hold, including the ones that must be
// left exactly as written so that the caller rebases them against the document directory.
func TestUnwrapFileIRI(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		ref  string
		want string
	}{
		{name: "file IRI", ref: "file://" + jobPathReads, want: jobPathReads},
		{name: "escaped file IRI", ref: "file:///data/a%20b.txt", want: "/data/a b.txt"},
		{name: "relative path", ref: "files/hello.txt", want: "files/hello.txt"},
		{name: "absolute path", ref: jobPathReads, want: jobPathReads},
		{name: "opaque file reference", ref: "file:hello.txt", want: "file:hello.txt"},
		{name: "another scheme", ref: "https://example.com/x", want: "https://example.com/x"},
		{name: "unparseable", ref: "file://\x7f/x", want: "file://\x7f/x"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := joUnwrapFileIRI(tc.ref); got != tc.want {
				t.Errorf("joUnwrapFileIRI(%q) = %q, want %q", tc.ref, got, tc.want)
			}
		})
	}
}
