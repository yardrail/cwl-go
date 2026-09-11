package cwlcli

import (
	"maps"
	"slices"
)

// Entry is one key/value pair of an [Object], in insertion order.
type Entry struct {
	// Value is the entry's value: a scalar, a slice, or a nested *Object.
	Value any
	// Key is the entry's name.
	Key string
}

// Object is an ordered, string-keyed mapping for deterministic output.
type Object struct {
	entries []Entry
}

// initialEntries is the initial capacity for a new Object.
const initialEntries = 8

// NewObject returns an empty Object ready to be populated with [Object.Set].
func NewObject() *Object {
	return &Object{entries: make([]Entry, 0, initialEntries)}
}

// Set records value under key and returns o for chaining.
func (o *Object) Set(key string, value any) *Object {
	for i := range o.entries {
		if o.entries[i].Key == key {
			o.entries[i].Value = value

			return o
		}
	}

	o.entries = append(o.entries, Entry{Key: key, Value: value})

	return o
}

// SetString records value under key, omitting empty strings.
func (o *Object) SetString(key, value string) *Object {
	if value == "" {
		return o
	}

	return o.Set(key, value)
}

// SetSlice records items under key, omitting empty slices.
func (o *Object) SetSlice(key string, items []any) *Object {
	if len(items) == 0 {
		return o
	}

	return o.Set(key, items)
}

// Entries returns the object's entries in insertion order.
func (o *Object) Entries() []Entry {
	return o.entries
}

// Len returns the number of entries in the object.
func (o *Object) Len() int {
	return len(o.entries)
}

// SortedKeys returns m's keys in sorted order.
func SortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
