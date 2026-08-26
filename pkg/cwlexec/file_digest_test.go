package cwlexec

import (
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// digestImportPath is the import every CWL checksum ultimately goes through.
const digestImportPath = "crypto/sha1"

// digestHome is the one file in this package allowed to import it.
const digestHome = "file_digest.go"

// TestDigestHelpersHaveOneImplementation is the guard on the consolidation that produced
// [outDigest]: the job-order path and the output-collection path each once carried a
// byte-identical copy of it, and the two are the sort of thing that drifts silently, because a
// checksum that disagrees with the one the other half computes shows up as a conformance failure
// somewhere else entirely.
//
// Counting imports rather than comparing bodies is deliberate. It is cheap, it needs no
// maintenance as the file grows, and a second implementation cannot appear without tripping it:
// there is no way to compute a CWL checksum without crypto/sha1.
//
// It also pins the premise the linter configuration rests on. gosec's G401 and G505 are excluded
// by a path pattern rather than at a call site, because this project bans //nolint; that exclusion
// is only as narrow as the number of files matching it, so the file this test names is the whole of
// what is exempt.
func TestDigestHelpersHaveOneImplementation(t *testing.T) {
	t.Parallel()

	importers := make([]string, 0, 1)

	for _, name := range goFilesInPackage(t) {
		if importsDigest(t, name) {
			importers = append(importers, name)
		}
	}

	if !slices.Equal(importers, []string{digestHome}) {
		t.Errorf("%s is imported by %v, want only %s", digestImportPath, importers, digestHome)
	}
}

// TestDigestFilesMatchTheLinterExclusion pins the other half of that premise: the exclusion is
// written as a path pattern, so a file named *_digest.go is exempt from G401 and G505 whether or
// not it computes a digest. There is exactly one in this package, and it is the one above.
func TestDigestFilesMatchTheLinterExclusion(t *testing.T) {
	t.Parallel()

	matched := make([]string, 0, 1)

	for _, name := range goFilesInPackage(t) {
		if strings.HasSuffix(name, "_digest.go") {
			matched = append(matched, name)
		}
	}

	if !slices.Equal(matched, []string{digestHome}) {
		t.Errorf("files matching *_digest.go: %v, want only %s", matched, digestHome)
	}
}

// goFilesInPackage lists this package's own Go sources, tests included and subdirectories
// excluded. pkg/cwlexec/conformance is a package of its own and computes its own digests, which is
// why the walk deliberately stops here.
func goFilesInPackage(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}

	names := make([]string, 0, len(entries))

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
			names = append(names, entry.Name())
		}
	}

	slices.Sort(names)

	return names
}

// importsDigest reports whether the named file imports [digestImportPath].
func importsDigest(t *testing.T, name string) bool {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parsing %s: %v", name, err)
	}

	for _, spec := range file.Imports {
		path, unquoteErr := strconv.Unquote(spec.Path.Value)
		if unquoteErr == nil && path == digestImportPath {
			return true
		}
	}

	return false
}
