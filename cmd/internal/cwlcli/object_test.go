package cwlcli

import (
	"slices"
	"testing"
)

func TestObjectKeepsInsertionOrder(t *testing.T) {
	t.Parallel()

	o := NewObject().Set("zebra", 1).Set("apple", 2).Set("mango", 3)

	got := keysOf(o)
	want := []string{"zebra", "apple", "mango"}

	if !slices.Equal(got, want) {
		t.Errorf("keys = %v, want %v", got, want)
	}
}

func TestObjectSetOverwritesInPlace(t *testing.T) {
	t.Parallel()

	o := NewObject().Set("a", 1).Set("b", 2).Set("a", 3)

	if got := keysOf(o); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("keys = %v, want [a b]", got)
	}

	if o.Len() != 2 {
		t.Errorf("Len() = %d, want 2", o.Len())
	}

	if got := o.Entries()[0].Value; got != 3 {
		t.Errorf("a = %v, want 3", got)
	}
}

func TestObjectOmitsEmptyValues(t *testing.T) {
	t.Parallel()

	o := NewObject()
	o.SetString("present", "yes")
	o.SetString("absent", "")
	o.SetSlice("items", []any{1})
	o.SetSlice("empty", make([]any, 0))

	if got := keysOf(o); !slices.Equal(got, []string{"present", "items"}) {
		t.Errorf("keys = %v, want [present items]", got)
	}
}

func TestZeroObjectIsUsable(t *testing.T) {
	t.Parallel()

	var o Object

	o.Set("a", 1)

	if o.Len() != 1 {
		t.Errorf("Len() = %d, want 1", o.Len())
	}
}

func TestSortedKeys(t *testing.T) {
	t.Parallel()

	m := map[string]int{"c": 3, "a": 1, "b": 2}

	if got := SortedKeys(m); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("SortedKeys = %v, want [a b c]", got)
	}
}

// keysOf returns an object's keys in insertion order.
func keysOf(o *Object) []string {
	out := make([]string, 0, o.Len())
	for _, entry := range o.Entries() {
		out = append(out, entry.Key)
	}

	return out
}
