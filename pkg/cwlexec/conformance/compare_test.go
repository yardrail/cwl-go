package conformance

import (
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

		err := check(t, map[string]any{outputName: "ok"}, map[string]any{outputName: "ok", "surplus": "value"})
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
