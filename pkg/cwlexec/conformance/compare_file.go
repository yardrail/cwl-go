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

// fileOwnKeys are the fields the File comparison handles itself. Every other field the
// expectation carries is compared as an ordinary value.
var fileOwnKeys = map[string]bool{
	keyPath:     true,
	keyLocation: true,
	keyListing:  true,
	keyContents: true,
	keyChecksum: true,
	keySize:     true,
}

// compareFile applies cwltest's File rules: the location is matched by suffix and the file
// must be there, declared contents are read back, a File's checksum and size are
// recomputed from disk, and whatever else the expectation names is compared field by
// field.
//
// There is deliberately no check for keys the expectation does not mention. The
// extra-key rule is one-directional: an ordinary object may not carry a field the
// expectation is silent about, a File may carry as many as the engine cares to report.
// That is what lets an expectation name a checksum without also having to predict a
// nameroot, a dirname and a secondaryFiles array.
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

	// Only a File is measured. A Directory has no bytes of its own, and cwltest keys
	// this off the *actual* object's class, so an expectation that says File about
	// something the engine reported as a Directory fails on the location instead.
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

// compareDirectory applies cwltest's Directory rules.
//
// The actual object must be a Directory and must carry a listing, whether or not the
// expectation mentions one -- cwltest calls that field mandatory in so many words. Every
// entry the expectation lists must match some entry of the actual listing, in any order,
// and the actual listing may hold more. The directory then goes through the File
// comparison, which is where its location is checked.
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

// compareListing matches every entry the expected listing names against some entry of the
// actual one.
//
// An expected Directory that declares no listing at all is a mismatch. cwltest reads the
// field unguarded and turns the resulting KeyError into a comparison failure, so an
// expectation written without one can never pass there; reporting it as a match here would
// be this harness inventing a pass.
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

// compareLocation matches the location the run reported against the one expected, and
// confirms it is really there.
//
// The match is by suffix, because the expectation names a file the run was free to put
// anywhere: "output.txt" matches "/tmp/xyz/output.txt". A location with no separator in it
// at all is matched whole, which is the only way such an expectation could ever be met.
func compareLocation(expected, actual map[string]any) error {
	expectedKey := locationKey(expected, actual)
	if expectedKey == "" {
		return nil
	}

	// Which field is read on each side is decided independently, so an expected
	// location may well be compared against a reported path. That is cwltest's
	// behaviour and it is what makes the suffix rule work at all: the two fields carry
	// the same trailing components.
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

// locationKey picks the field of the expectation the location is compared through, and
// answers "" when it declares neither.
//
// It also reproduces cwltest's one mutation of the output object: an expectation written
// as a path, against a run that reported only a location, copies the location into path
// first. The copy is not cosmetic -- the checksum, size and contents checks all open
// whatever path holds afterwards, so leaving it out would change which file they read.
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

// checkExists confirms the reported location names something of the class the run said it
// was. A File where a directory sits, or the reverse, is as much a mismatch as nothing at
// all being there.
func checkExists(where, class string) error {
	info, err := os.Stat(localPath(where))
	if err != nil || info.IsDir() != (class == classDirectory) {
		return fmt.Errorf("%w: %s does not exist", errMismatch, where)
	}

	return nil
}

// compareContents reads the file back and compares its text against the expectation.
func compareContents(expected any, actual map[string]any) error {
	// cwltest opens actual["path"] directly here rather than going through its
	// filesystem accessor, so a run that reported only a location has nothing to open
	// -- except that locationKey has by then copied the location across.
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

// localPath turns a reported location into a filesystem path, tolerating both the file
// URLs an engine may emit and bare paths. An http or https URL is returned unchanged and
// will simply fail to stat, which is cwltest's behaviour for one too.
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
