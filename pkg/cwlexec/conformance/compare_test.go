package conformance

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The bytes every File fixture holds, and their CWL checksum and size.
const (
	fixtureText     = "hello\n"
	fixtureChecksum = "sha1$f572d396fae9206628714fb2ce00f72e94f2258f"
	fixtureSize     = 6
	fixtureName     = "out.txt"

	keyBasename = "basename"
	outputName  = "out"

	// testValue is a placeholder scalar used where a test needs some value but not any
	// particular one.
	testValue = "value"
)

// mustNormalize puts a value through the JSON round trip the driver compares through, so
// that a test exercises the same representation a run does rather than a hand-built one.
func mustNormalize(t *testing.T, value any) any {
	t.Helper()

	out, err := normalize(value)
	if err != nil {
		t.Fatalf("normalizing %#v: %v", value, err)
	}

	return out
}

// writeFile drops text into a fresh directory and returns the file's path.
func writeFile(t *testing.T, text string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), fixtureName)

	err := os.WriteFile(path, []byte(text), 0o600)
	if err != nil {
		t.Fatalf("writing the fixture file: %v", err)
	}

	return path
}

// fileOutput is a reported File output for the file at path.
func fileOutput(path string) map[string]any {
	return map[string]any{
		keyClass:    classFile,
		keyLocation: path,
		keyPath:     path,
		keyBasename: fixtureName,
		keyChecksum: fixtureChecksum,
		keySize:     fixtureSize,
	}
}

// check runs one comparison and reports whether it matched.
func check(t *testing.T, expected, actual any) error {
	t.Helper()

	return compare(mustNormalize(t, expected), mustNormalize(t, actual))
}

func TestCompareAnyIsWildcard(t *testing.T) {
	t.Parallel()

	actuals := map[string]any{
		"a string":  "text",
		"a number":  42,
		"a float":   1.5,
		"a boolean": true,
		"null":      nil,
		"a list":    []any{1, 2},
		"an object": map[string]any{keyClass: classFile, keyLocation: "nowhere"},
	}

	for name, actual := range actuals {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := check(t, wildcard, actual)
			if err != nil {
				t.Errorf("Any did not match %s: %v", name, err)
			}
		})
	}

	t.Run("an absent field", func(t *testing.T) {
		t.Parallel()

		// The wildcard is tested before the "expected something, got nothing"
		// rule, so it matches a field the run never produced at all.
		err := check(t, map[string]any{outputName: wildcard}, make(map[string]any))
		if err != nil {
			t.Errorf("Any did not match an absent field: %v", err)
		}
	})
}

func TestCompareVerifiesChecksumFromDisk(t *testing.T) {
	t.Parallel()

	t.Run("bytes that match the declared checksum", func(t *testing.T) {
		t.Parallel()

		path := writeFile(t, fixtureText)

		expected := map[string]any{keyClass: classFile, keyLocation: fixtureName, keyChecksum: fixtureChecksum}

		err := check(t, expected, fileOutput(path))
		if err != nil {
			t.Errorf("a correct output object did not match: %v", err)
		}
	})

	t.Run("bytes that do not", func(t *testing.T) {
		t.Parallel()

		// The output object declares the checksum the suite asked for, and the
		// suite asked for the right one -- but the file holds something else. An
		// implementation that reported a digest it did not compute would pass a
		// harness that took the object's word for it.
		path := writeFile(t, "these are not the bytes you are looking for")

		expected := map[string]any{keyClass: classFile, keyLocation: fixtureName, keyChecksum: fixtureChecksum}

		err := check(t, expected, fileOutput(path))
		if err == nil {
			t.Error("a checksum contradicted by the file on disk was accepted")
		}
	})

	t.Run("a size the suite never asked about", func(t *testing.T) {
		t.Parallel()

		path := writeFile(t, fixtureText)

		actual := fileOutput(path)
		actual[keySize] = 1234

		// The expectation mentions neither checksum nor size, and the run is
		// still caught out: its own claim about the file is checked against the
		// file.
		err := check(t, map[string]any{keyClass: classFile, keyLocation: fixtureName}, actual)
		if err == nil {
			t.Error("a size contradicted by the file on disk was accepted")
		}
	})
}

func TestCompareSuffixMatchesLocation(t *testing.T) {
	t.Parallel()

	path := writeFile(t, fixtureText)

	tests := []struct {
		name     string
		location string
		match    bool
	}{
		{name: "a differing directory prefix", location: fixtureName, match: true},
		// The rule is a suffix *after a separator*, so an expectation naming the
		// whole path does not match it: "/tmp/x/out.txt" does not end with
		// "//tmp/x/out.txt". Surprising, and cwltest's.
		{name: "the whole path", location: path, match: false},
		{name: "a differing basename", location: "other.txt", match: false},
		{name: "a partial basename", location: "ut.txt", match: false},
		{name: "the wildcard", location: wildcard, match: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expected := map[string]any{keyClass: classFile, keyLocation: tt.location}

			err := check(t, expected, fileOutput(path))
			if (err == nil) != tt.match {
				t.Errorf("location %q: match = %v, want %v (%v)", tt.location, err == nil, tt.match, err)
			}
		})
	}
}

func TestCompareRejectsExtraKeys(t *testing.T) {
	t.Parallel()

	path := writeFile(t, fixtureText)

	t.Run("an extra top-level key", func(t *testing.T) {
		t.Parallel()

		err := check(t, map[string]any{outputName: "ok"}, map[string]any{outputName: "ok", "surplus": testValue})
		if err == nil {
			t.Error("an unexpected top-level key was accepted")
		}
	})

	t.Run("an extra top-level key holding null", func(t *testing.T) {
		t.Parallel()

		// cwltest tolerates a spurious key whose value is null, which is what
		// lets an engine emit a declared-but-unproduced output.
		err := check(t, map[string]any{outputName: "ok"}, map[string]any{outputName: "ok", "surplus": nil})
		if err != nil {
			t.Errorf("a null-valued extra key was rejected: %v", err)
		}
	})

	t.Run("an extra key inside a File", func(t *testing.T) {
		t.Parallel()

		// The exclusion is one-directional. An expectation names a checksum
		// without having to predict the nameroot, dirname and secondaryFiles the
		// engine reports beside it.
		expected := map[string]any{keyClass: classFile, keyLocation: fixtureName, keySize: fixtureSize}

		actual := fileOutput(path)
		actual["nameroot"] = outputName
		actual["nameext"] = ".txt"

		err := check(t, expected, actual)
		if err != nil {
			t.Errorf("extra keys inside a File were rejected: %v", err)
		}
	})
}

func TestCompareDirectoryListing(t *testing.T) {
	t.Parallel()

	path := writeFile(t, fixtureText)
	dir := filepath.Dir(path)

	directory := func() map[string]any {
		return map[string]any{
			keyClass:    classDirectory,
			keyLocation: dir,
			keyListing:  []any{fileOutput(path)},
		}
	}

	t.Run("an entry the listing holds", func(t *testing.T) {
		t.Parallel()

		expected := map[string]any{
			keyClass:   classDirectory,
			keyListing: []any{map[string]any{keyClass: classFile, keyBasename: fixtureName}},
		}

		err := check(t, expected, directory())
		if err != nil {
			t.Errorf("a listed entry was not found: %v", err)
		}
	})

	t.Run("an entry it does not", func(t *testing.T) {
		t.Parallel()

		expected := map[string]any{
			keyClass:   classDirectory,
			keyListing: []any{map[string]any{keyClass: classFile, keyBasename: "absent.txt"}},
		}

		err := check(t, expected, directory())
		if err == nil {
			t.Error("an entry missing from the listing was accepted")
		}
	})

	t.Run("a reported directory carrying no listing at all", func(t *testing.T) {
		t.Parallel()

		reported := directory()
		delete(reported, keyListing)

		expected := map[string]any{keyClass: classDirectory, keyListing: make([]any, 0)}

		err := check(t, expected, reported)
		if err == nil {
			t.Error("a Directory output with no listing was accepted")
		}
	})
}

func TestCompareNumbersAcrossTheIntFloatDivide(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		expected any
		actual   any
		match    bool
	}{
		{name: "an integer and a whole float", expected: 1, actual: 1.0, match: true},
		{name: "two integers", expected: 42, actual: 42, match: true},
		{name: "an integer and a different one", expected: 42, actual: 43, match: false},
		{name: "a boolean and one", expected: true, actual: 1, match: true},
		{name: "a boolean and zero", expected: true, actual: 0, match: false},
		{name: "false and zero", expected: false, actual: 0, match: true},
		{name: "false and one", expected: false, actual: 1, match: false},
		{name: "a fraction", expected: 0.5, actual: 0.5, match: true},
		{name: "a string and a number", expected: "1", actual: 1, match: false},
		{name: "a huge integer and itself", expected: 1e42, actual: 1e42, match: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := check(t, tt.expected, tt.actual)
			if (err == nil) != tt.match {
				t.Errorf("match = %v, want %v (%v)", err == nil, tt.match, err)
			}
		})
	}
}

func TestCompareRejectsAMissingScalarField(t *testing.T) {
	t.Parallel()

	// A non-wildcard expectation is not met by a field the run never produced: the
	// "expected something, got null" branch, distinct from the wildcard case in
	// TestCompareAnyIsWildcard where an absent field always matches.
	err := check(t, map[string]any{outputName: testValue}, make(map[string]any))
	if err == nil {
		t.Error("a missing field was accepted against a non-wildcard expectation")
	}
}

func TestCompareObjectRejectsANonObjectActual(t *testing.T) {
	t.Parallel()

	err := check(t, map[string]any{outputName: testValue}, "a string")
	if err == nil {
		t.Error("a non-object actual was accepted against an object expectation")
	}
}

func TestCompareDigestFailures(t *testing.T) {
	t.Parallel()

	t.Run("neither a path nor a location", func(t *testing.T) {
		t.Parallel()

		err := compareDigest(make(map[string]any), map[string]any{keyClass: classFile})
		if err == nil {
			t.Error("a File with no path or location was accepted")
		}
	})

	t.Run("a path that does not exist", func(t *testing.T) {
		t.Parallel()

		actual := map[string]any{keyClass: classFile, keyPath: filepath.Join(t.TempDir(), "gone.txt")}

		err := compareDigest(make(map[string]any), actual)
		if err == nil {
			t.Error("a File whose path does not exist was accepted")
		}
	})
}

func TestDigestPathFallsBackToLocation(t *testing.T) {
	t.Parallel()

	where, ok := digestPath(map[string]any{keyLocation: "x"})
	if !ok || where != "x" {
		t.Errorf("digestPath = %q, %v, want %q, true", where, ok, "x")
	}
}

func TestDigestOfErrors(t *testing.T) {
	t.Parallel()

	t.Run("a path that does not exist", func(t *testing.T) {
		t.Parallel()

		_, err := digestOf(filepath.Join(t.TempDir(), "gone.txt"))
		if err == nil {
			t.Error("digestOf accepted a path that does not exist")
		}
	})

	t.Run("a directory", func(t *testing.T) {
		t.Parallel()

		// Reading an open directory fd fails with EISDIR on Linux, which is what
		// exercises the errors.Join(copyErr, closeErr) branch.
		_, err := digestOf(t.TempDir())
		if err == nil {
			t.Error("digestOf accepted a directory")
		}
	})
}

func TestCompareDirectoryRejectsANonDirectoryActual(t *testing.T) {
	t.Parallel()

	expected := map[string]any{keyClass: classDirectory}

	tests := map[string]map[string]any{
		"a File":          {keyClass: classFile},
		"no class at all": {},
	}

	for name, actual := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := compareDirectory(expected, actual)
			if err == nil {
				t.Errorf("%s was accepted as a Directory", name)
			}
		})
	}
}

func TestCompareListingRejectsNonListValues(t *testing.T) {
	t.Parallel()

	t.Run("an expected value that is not a list", func(t *testing.T) {
		t.Parallel()

		err := compareListing(make(map[string]any), make([]any, 0))
		if err == nil {
			t.Error("a non-list expected listing was accepted")
		}
	})

	t.Run("an actual value that is not a list", func(t *testing.T) {
		t.Parallel()

		err := compareListing(make([]any, 0), "not a list")
		if err == nil {
			t.Error("a non-list actual listing was accepted")
		}
	})
}

func TestCompareLocationErrors(t *testing.T) {
	t.Parallel()

	t.Run("a reported location that is not a string", func(t *testing.T) {
		t.Parallel()

		expected := map[string]any{keyLocation: fixtureName}
		actual := map[string]any{keyLocation: 123}

		err := compareLocation(expected, actual)
		if err == nil {
			t.Error("a non-string reported location was accepted")
		}
	})

	t.Run("an expected location that does not exist on disk", func(t *testing.T) {
		t.Parallel()

		expected := map[string]any{keyLocation: fixtureName}
		actual := map[string]any{keyLocation: filepath.Join(t.TempDir(), fixtureName)}

		err := compareLocation(expected, actual)
		if err == nil {
			t.Error("a location that does not exist on disk was accepted")
		}
	})
}

func TestCompareLocationTrimsATrailingSlashFromADirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	expected := map[string]any{keyLocation: filepath.Base(dir)}
	actual := map[string]any{keyClass: classDirectory, keyLocation: dir + "/"}

	err := compareLocation(expected, actual)
	if err != nil {
		t.Fatalf("compareLocation: %v", err)
	}

	if actual[keyLocation] != dir {
		t.Errorf("actual[keyLocation] = %q, want the trailing slash trimmed to %q", actual[keyLocation], dir)
	}
}

func TestCheckSuffixMatchesALocationWithNoSeparatorAtAll(t *testing.T) {
	t.Parallel()

	err := checkSuffix(fixtureName, fixtureName)
	if err != nil {
		t.Errorf("checkSuffix rejected an exact match with no separator: %v", err)
	}
}

func TestLocationKeyCopiesLocationIntoPath(t *testing.T) {
	t.Parallel()

	expected := map[string]any{keyPath: "wanted.txt"}
	actual := map[string]any{keyLocation: "reported.txt"}

	key := locationKey(expected, actual)
	if key != keyPath {
		t.Errorf("locationKey = %q, want %q", key, keyPath)
	}

	if actual[keyPath] != "reported.txt" {
		t.Errorf(`actual[keyPath] = %v, want "reported.txt"`, actual[keyPath])
	}
}

func TestCheckSuffixRejectsANonStringExpectedLocation(t *testing.T) {
	t.Parallel()

	err := checkSuffix(123, "/tmp/out.txt")
	if err == nil {
		t.Error("a non-string expected location was accepted")
	}
}

func TestCheckExistsErrors(t *testing.T) {
	t.Parallel()

	t.Run("a location that does not exist", func(t *testing.T) {
		t.Parallel()

		err := checkExists(filepath.Join(t.TempDir(), "gone.txt"), classFile)
		if err == nil {
			t.Error("a location that does not exist was accepted")
		}
	})

	t.Run("a class mismatch", func(t *testing.T) {
		t.Parallel()

		path := writeFile(t, fixtureText)

		err := checkExists(path, classDirectory)
		if err == nil {
			t.Error("a File reported as a Directory was accepted")
		}
	})
}

func TestCompareContentsRejectsAnActualWithNoPath(t *testing.T) {
	t.Parallel()

	err := compareContents(fixtureText, map[string]any{keyClass: classFile, keyLocation: fixtureName})
	if !errors.Is(err, errMismatch) {
		t.Errorf("compareContents = %v, want it to wrap errMismatch", err)
	}
}

func TestCompareListsMatchElementWise(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		expected any
		actual   any
		match    bool
	}{
		{name: "equal lists", expected: []any{1, "a"}, actual: []any{1, "a"}, match: true},
		{name: "a shorter actual", expected: []any{1, "a"}, actual: []any{1}, match: false},
		{name: "a longer actual", expected: []any{1}, actual: []any{1, "a"}, match: false},
		{name: "a reordered actual", expected: []any{1, 2}, actual: []any{2, 1}, match: false},
		{name: "a list against an object", expected: []any{1}, actual: map[string]any{"0": 1}, match: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := check(t, tt.expected, tt.actual)
			if (err == nil) != tt.match {
				t.Errorf("match = %v, want %v (%v)", err == nil, tt.match, err)
			}
		})
	}
}
