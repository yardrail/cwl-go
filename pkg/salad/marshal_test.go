package salad

import (
	"strings"
	"testing"
)

func TestMarshal_map(t *testing.T) {
	t.Parallel()

	n := NewMapNode(noLoc, []MapEntry{
		{Key: keyName, Value: NewStringNode(noLoc, "Log")},
		{Key: keyType, Value: NewStringNode(noLoc, "record")},
		{Key: "documentRoot", Value: NewBoolNode(noLoc, true)},
	})

	b, err := Marshal(n)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got := string(b)
	for _, want := range []string{"name: Log", "type: record", "documentRoot: true"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestMarshal_seq(t *testing.T) {
	t.Parallel()

	n := NewSeqNode(noLoc, []Node{
		NewStringNode(noLoc, "a"),
		NewStringNode(noLoc, "b"),
		NewIntNode(noLoc, 42),
	})

	b, err := Marshal(n)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got := string(b)
	for _, want := range []string{"- a", "- b", "- 42"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestMarshal_nested(t *testing.T) {
	t.Parallel()

	inner := NewMapNode(noLoc, []MapEntry{
		{Key: keyType, Value: NewStringNode(noLoc, "enum")},
		{Key: keyName, Value: NewStringNode(noLoc, "Kind")},
	})

	n := NewMapNode(noLoc, []MapEntry{
		{Key: keyName, Value: NewStringNode(noLoc, "field")},
		{Key: keyType, Value: inner},
	})

	b, err := Marshal(n)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got := string(b)
	if !strings.Contains(got, "type:") || !strings.Contains(got, "name: Kind") {
		t.Errorf("unexpected output:\n%s", got)
	}
}

func TestMarshal_scalars(t *testing.T) {
	t.Parallel()

	n := NewMapNode(noLoc, []MapEntry{
		{Key: "null_val", Value: NewNullNode(noLoc)},
		{Key: "bool_val", Value: NewBoolNode(noLoc, true)},
		{Key: "int_val", Value: NewIntNode(noLoc, 99)},
		{Key: "float_val", Value: NewFloatNode(noLoc, 3.14)},
		{Key: "str_val", Value: NewStringNode(noLoc, "hello")},
	})

	b, err := Marshal(n)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got := string(b)
	for _, want := range []string{"null_val: null", "bool_val: true", "int_val: 99", "float_val: 3.14", "str_val: hello"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestMarshal_nil(t *testing.T) {
	t.Parallel()

	b, err := Marshal(nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if !strings.Contains(string(b), "null") {
		t.Errorf("nil node should marshal as null, got %q", string(b))
	}
}

func TestMarshal_roundTrip(t *testing.T) {
	t.Parallel()

	original := NewMapNode(noLoc, []MapEntry{
		{Key: keyName, Value: NewStringNode(noLoc, "Test")},
		{Key: "count", Value: NewIntNode(noLoc, 3)},
		{Key: "items", Value: NewSeqNode(noLoc, []Node{
			NewStringNode(noLoc, "a"),
			NewStringNode(noLoc, "b"),
		})},
	})

	b, err := Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	parsed, err := Parse(testFile, b)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	m := mustMap(t, parsed)
	if v, _ := AsString(mustGet(t, m, keyName)); v != "Test" {
		t.Errorf("name = %q, want Test", v)
	}

	seq := mustSeq(t, mustGet(t, m, "items"))
	if seq.Len() != 2 {
		t.Errorf("items len = %d, want 2", seq.Len())
	}
}

func TestMarshal_keyOrder(t *testing.T) {
	t.Parallel()

	n := NewMapNode(noLoc, []MapEntry{
		{Key: "z_last", Value: NewStringNode(noLoc, "1")},
		{Key: "a_first", Value: NewStringNode(noLoc, "2")},
		{Key: "m_middle", Value: NewStringNode(noLoc, "3")},
	})

	b, err := Marshal(n)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got := string(b)
	zIdx := strings.Index(got, "z_last")
	aIdx := strings.Index(got, "a_first")
	mIdx := strings.Index(got, "m_middle")

	if zIdx > aIdx || aIdx > mIdx {
		t.Errorf("key order not preserved: z=%d a=%d m=%d\n%s", zIdx, aIdx, mIdx, got)
	}
}

var noLoc SourceLine
