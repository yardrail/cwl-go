package conformance

import (
	"errors"
	"fmt"
	"maps"
	"slices"
)

// The output-object field names the comparison treats specially, and the two class values
// that select the filesystem rules.
const (
	keyClass    = "class"
	keyPath     = "path"
	keyLocation = "location"
	keyListing  = "listing"
	keyContents = "contents"
	keyChecksum = "checksum"
	keySize     = "size"

	classFile      = "File"
	classDirectory = "Directory"
)

// wildcard is the expected value that matches anything.
//
// It is the literal string "Any", tested before everything else and at every depth, so it
// stands in for a whole object as readily as for one field, and it matches an absent or
// null actual value as well as a present one.
const wildcard = "Any"

// errMismatch is the sentinel every comparison failure carries. The difference is
// described in the message; the sentinel is what lets a caller tell "the outputs differ"
// from "the run never produced any".
var errMismatch = errors.New("output object does not match")

// compare reports whether actual matches expected under cwltest's rules, returning nil
// when it does and an error naming the first difference when it does not.
//
// It is a transcription of cwltest's compare.py rather than an interpretation of it. The
// harness is only useful while it agrees with the authoritative one, so where the two
// could differ this follows cwltest -- including the parts of it that read oddly, each of
// which is called out where it appears.
func compare(expected, actual any) error {
	if expected == wildcard {
		return nil
	}

	// An expectation of anything at all is not met by a missing value. The check comes
	// before the type switch, so it also covers an expected object or list.
	if expected != nil && actual == nil {
		return fmt.Errorf("%w: expected %s, got null", errMismatch, render(expected))
	}

	switch typed := expected.(type) {
	case map[string]any:
		return compareObject(typed, actual)
	case []any:
		return compareList(typed, actual)
	default:
		if !equalScalar(expected, actual) {
			return fmt.Errorf("%w: expected %s, got %s", errMismatch, render(expected), render(actual))
		}

		return nil
	}
}

// compareObject dispatches on the expected object's class: a File and a Directory are
// compared against the filesystem, and anything else field by field.
func compareObject(expected map[string]any, actual any) error {
	object, ok := actual.(map[string]any)
	if !ok {
		return fmt.Errorf("%w: expected an object, got %s", errMismatch, render(actual))
	}

	switch classOf(expected) {
	case classFile:
		return compareFile(expected, object)
	case classDirectory:
		return compareDirectory(expected, object)
	default:
		return compareDict(expected, object)
	}
}

// compareList compares two arrays element-wise, lengths first.
func compareList(expected []any, actual any) error {
	items, ok := actual.([]any)
	if !ok {
		return fmt.Errorf("%w: expected a list, got %s", errMismatch, render(actual))
	}

	if len(expected) != len(items) {
		return fmt.Errorf("%w: expected %d items, got %d", errMismatch, len(expected), len(items))
	}

	for i := range expected {
		err := compare(expected[i], items[i])
		if err != nil {
			return fmt.Errorf("item %d: %w", i, err)
		}
	}

	return nil
}

// compareDict compares an ordinary object field by field.
//
// An expected field the actual object does not carry is compared against nothing, which
// fails unless the expectation is null or the wildcard. In the other direction a field the
// expectation never mentions is an error -- but only when it holds something: cwltest
// tolerates a spurious key whose value is null, and so does this.
//
// Keys are visited in sorted order purely so that the difference reported for an object
// with more than one is the same on every run. Go map iteration is randomised, and a
// harness whose failure message moves about is one nobody trusts.
func compareDict(expected, actual map[string]any) error {
	for _, key := range slices.Sorted(maps.Keys(expected)) {
		err := compare(expected[key], actual[key])
		if err != nil {
			return fmt.Errorf("field %q: %w", key, err)
		}
	}

	for _, key := range slices.Sorted(maps.Keys(actual)) {
		_, declared := expected[key]
		if declared || actual[key] == nil {
			continue
		}

		return fmt.Errorf("%w: unexpected key %q", errMismatch, key)
	}

	return nil
}

// classOf reads an object's class discriminator, answering "" when it carries none or
// carries a non-string one.
func classOf(object map[string]any) string {
	class, ok := object[keyClass].(string)
	if !ok {
		return ""
	}

	return class
}
