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

// Object is an ordered, string-keyed mapping built for deterministic output.
//
// It exists instead of map[string]any because a dump is meant to be diffed.
// A Go map iterates randomly, and sorting it alphabetically would scatter the
// fields a reader wants together — a process's class, id and label would be
// separated by everything that happens to sort between them. Insertion order
// is both stable across runs and the order the author of the projection chose,
// so it is what Object keeps.
//
// The zero Object is a valid empty object.
type Object struct {
	entries []Entry
}

// initialEntries is how many entries a new Object has room for before it
// grows. Most dumped objects are a handful of fields.
const initialEntries = 8

// NewObject returns an empty Object ready to be populated with [Object.Set].
func NewObject() *Object {
	return &Object{entries: make([]Entry, 0, initialEntries)}
}

// Set records value under key and returns o, so that populating an object
// chains. Setting a key that is already present overwrites its value in place,
// leaving the key at its original position.
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

// SetString records value under key, unless value is empty, in which case the
// key is omitted entirely. An absent optional field is noise in a dump; a
// present-but-empty one invites the reader to wonder what emptied it.
func (o *Object) SetString(key, value string) *Object {
	if value == "" {
		return o
	}

	return o.Set(key, value)
}

// SetSlice records items under key, unless items is empty, in which case the
// key is omitted on the same terms as [Object.SetString].
func (o *Object) SetSlice(key string, items []any) *Object {
	if len(items) == 0 {
		return o
	}

	return o.Set(key, items)
}

// Entries returns the object's entries in insertion order. The result aliases
// the object's own storage and must not be modified.
func (o *Object) Entries() []Entry {
	return o.entries
}

// Len returns the number of entries in the object.
func (o *Object) Len() int {
	return len(o.entries)
}

// SortedKeys returns m's keys in sorted order. It is the one sanctioned way to
// read a Go map into a dump: map iteration order is random, so a rendered map
// is only reproducible if its keys are sorted first.
func SortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
