package cwlcli

import "testing"

func TestPlainSortsMapKeys(t *testing.T) {
	t.Parallel()

	got := Text(Plain(map[string]any{"c": 3, "a": 1, "b": 2}))
	want := "a: 1\nb: 2\nc: 3"

	if got != want {
		t.Errorf("Plain = %q, want %q", got, want)
	}
}

func TestPlainConvertsNestedContainers(t *testing.T) {
	t.Parallel()

	value := map[string]any{
		"list": []any{map[string]any{"z": 1, "a": 2}},
	}

	got := Text(Plain(value))
	want := "list:\n  - a: 2\n    z: 1"

	if got != want {
		t.Errorf("Plain = %q, want %q", got, want)
	}
}

func TestPlainLeavesScalarsAlone(t *testing.T) {
	t.Parallel()

	for _, value := range []any{nil, "s", 1, 1.5, true} {
		if got := Plain(value); got != value {
			t.Errorf("Plain(%v) = %v, want it unchanged", value, got)
		}
	}
}
