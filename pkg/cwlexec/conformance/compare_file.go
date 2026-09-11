package conformance

import (
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// fileOwnKeys are the fields handled by File-specific comparison logic.
var fileOwnKeys = map[string]bool{
	keyPath:     true,
	keyLocation: true,
	keyListing:  true,
	keyContents: true,
	keyChecksum: true,
	keySize:     true,
}

// compareFile applies cwltest's File rules: suffix-match location, re-verify digest, compare remaining fields.
func compareFile(expected, actual map[string]any) error {
	err := compareLocation(expected, actual)
	if err != nil {
		return err
	}

	contents, declared := expected[keyContents]
	if declared {
		err = compareContents(contents, actual)
		if err != nil {
			return err
		}
	}

	if classOf(actual) == classFile {
		err = compareDigest(expected, actual)
		if err != nil {
			return err
		}
	}

	return compareRemainingKeys(expected, actual)
}

// compareRemainingKeys compares the expected fields the File rules do not own.
func compareRemainingKeys(expected, actual map[string]any) error {
	for _, key := range slices.Sorted(maps.Keys(expected)) {
		if fileOwnKeys[key] {
			continue
		}

		err := compare(expected[key], actual[key])
		if err != nil {
			return fmt.Errorf("field %q: %w", key, err)
		}
	}

	return nil
}

// compareDirectory applies cwltest's Directory rules: mandatory listing, subset match, then File comparison.
func compareDirectory(expected, actual map[string]any) error {
	if classOf(actual) != classDirectory {
		return fmt.Errorf("%w: expected an object with class Directory, got %s", errMismatch, render(actual))
	}

	found, present := actual[keyListing]
	if !present {
		return fmt.Errorf("%w: listing is a mandatory field of a Directory output", errMismatch)
	}

	err := compareListing(expected[keyListing], found)
	if err != nil {
		return err
	}

	return compareFile(expected, actual)
}

// compareListing checks that every expected listing entry matches some actual entry.
func compareListing(expected, actual any) error {
	want, ok := expected.([]any)
	if !ok {
		return fmt.Errorf("%w: the expected Directory declares no listing to match", errMismatch)
	}

	got, ok := actual.([]any)
	if !ok {
		return fmt.Errorf("%w: the reported listing is not a list", errMismatch)
	}

	for _, item := range want {
		if !listingContains(got, item) {
			return fmt.Errorf("%w: %s is not in the reported listing", errMismatch, render(item))
		}
	}

	return nil
}

// listingContains reports whether any entry of the listing matches want.
func listingContains(listing []any, want any) bool {
	for _, item := range listing {
		if compare(want, item) == nil {
			return true
		}
	}

	return false
}

// compareLocation suffix-matches the reported location and confirms it exists on disk.
func compareLocation(expected, actual map[string]any) error {
	expectedKey := locationKey(expected, actual)
	if expectedKey == "" {
		return nil
	}

	actualKey := keyLocation
	if _, ok := actual[keyPath]; ok {
		actualKey = keyPath
	}

	where, ok := actual[actualKey].(string)
	if !ok {
		return fmt.Errorf("%w: the reported %s is %s, not a string", errMismatch, actualKey, render(actual[actualKey]))
	}

	class := classOf(actual)
	if class == classDirectory {
		where = strings.TrimRight(where, "/")
		actual[actualKey] = where
	}

	err := checkExists(where, class)
	if err != nil {
		return err
	}

	return checkSuffix(expected[expectedKey], where)
}

// locationKey picks the location field to compare, copying location into path if needed.
func locationKey(expected, actual map[string]any) string {
	_, wantsPath := expected[keyPath]
	if wantsPath {
		_, has := actual[keyPath]
		if !has {
			actual[keyPath] = actual[keyLocation]
		}

		return keyPath
	}

	_, wantsLocation := expected[keyLocation]
	if wantsLocation {
		return keyLocation
	}

	return ""
}

// checkSuffix applies the suffix rule to one reported location.
func checkSuffix(expected any, actual string) error {
	if expected == wildcard {
		return nil
	}

	want, ok := expected.(string)
	if !ok {
		return fmt.Errorf("%w: the expected location %s is not a string", errMismatch, render(expected))
	}

	if strings.HasSuffix(actual, "/"+want) {
		return nil
	}

	if !strings.Contains(actual, "/") && want == actual {
		return nil
	}

	return fmt.Errorf("%w: %q does not end with %q", errMismatch, actual, want)
}

// checkExists confirms the reported location exists with the expected file/dir type.
func checkExists(where, class string) error {
	info, err := os.Stat(localPath(where))
	if err != nil || info.IsDir() != (class == classDirectory) {
		return fmt.Errorf("%w: %s does not exist", errMismatch, where)
	}

	return nil
}

// compareContents reads the file back and compares against the expectation.
func compareContents(expected any, actual map[string]any) error {
	where, ok := actual[keyPath].(string)
	if !ok {
		return fmt.Errorf("%w: contents were expected but the run reported no path", errMismatch)
	}

	data, err := os.ReadFile(filepath.Clean(where))
	if err != nil {
		return fmt.Errorf("%w: reading %s: %w", errMismatch, where, err)
	}

	if !equalScalar(expected, string(data)) {
		return fmt.Errorf("%w: %s holds %s, not the expected %s",
			errMismatch, where, render(string(data)), render(expected))
	}

	return nil
}

// localPath converts a location (file:// URL or bare path) to a filesystem path.
func localPath(ref string) string {
	if !strings.Contains(ref, "://") {
		return filepath.FromSlash(ref)
	}

	parsed, err := url.Parse(ref)
	if err != nil || parsed.Scheme != "file" {
		return ref
	}

	return filepath.FromSlash(parsed.Path)
}
