package conformance

import (
	"errors"
	"fmt"
	"maps"
	"slices"
)

// Field names and class values used by the comparison.
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
const wildcard = "Any"

// errMismatch is the sentinel for comparison failures.
var errMismatch = errors.New("output object does not match")

// compare reports whether actual matches expected under cwltest's rules.
func compare(expected, actual any) error {
	if expected == wildcard {
		return nil
	}

	// Any non-wildcard expectation fails on a missing value.
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

// compareObject dispatches by class: File and Directory use filesystem rules, others field-by-field.
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

// compareDict compares an ordinary object field by field. Keys visited in sorted order for determinism.
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

// classOf reads the "class" field, or "".
func classOf(object map[string]any) string {
	class, ok := object[keyClass].(string)
	if !ok {
		return ""
	}

	return class
}
