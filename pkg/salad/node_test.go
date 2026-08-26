package salad

import (
	"math"
	"slices"
	"testing"
)

func TestMapNodePreservesInsertionOrder(t *testing.T) {
	t.Parallel()

	// A Go map would randomize this; the whole point of MapNode is that it does not.
	want := []string{"zebra", "apple", "mango", "banana", "cherry", "kiwi", "plum", "fig"}

	pairs := make([]MapEntry, 0, len(want))
	for _, k := range want {
		pairs = append(pairs, MapEntry{Key: k, Value: NewStringNode(SourceLine{}, k)})
	}

	m := NewMapNode(SourceLine{}, pairs)
	for range 50 {
		if got := m.Keys(); !slices.Equal(got, want) {
			t.Fatalf("Keys() = %v, want %v", got, want)
		}
	}

	iterated := make([]string, 0, m.Len())
	for k := range m.All() {
		iterated = append(iterated, k)
	}

	if !slices.Equal(iterated, want) {
		t.Errorf("All() yielded %v, want %v", iterated, want)
	}
}

func TestMapNodeDuplicateKeyKeepsFirstPosition(t *testing.T) {
	t.Parallel()

	m := NewMapNode(SourceLine{}, entries("a", "1", "b", "2", "a", "3"))
	if got, want := m.Keys(), []string{"a", "b"}; !slices.Equal(got, want) {
		t.Errorf("Keys() = %v, want %v", got, want)
	}

	if got, ok := AsString(mustGet(t, m, "a")); !ok || got != "3" {
		t.Errorf(`Get("a") = %q, want "3"`, got)
	}
}

func TestMapNodeGetAndHas(t *testing.T) {
	t.Parallel()

	m := NewMapNode(SourceLine{}, entries("a", "1"))
	if !m.Has("a") {
		t.Error(`Has("a") = false, want true`)
	}

	if m.Has("missing") {
		t.Error(`Has("missing") = true, want false`)
	}

	if n, ok := m.Get("missing"); ok || n != nil {
		t.Errorf(`Get("missing") = (%v, %v), want (nil, false)`, n, ok)
	}
}

func TestMapNodeWith(t *testing.T) {
	t.Parallel()

	base := NewMapNode(SourceLine{}, entries("a", "1", "b", "2"))
	updated := base.With(
		MapEntry{Key: "b", Value: NewStringNode(SourceLine{}, "two")},
		MapEntry{Key: "c", Value: NewStringNode(SourceLine{}, "3")},
	)

	if got, want := updated.Keys(), []string{"a", "b", "c"}; !slices.Equal(got, want) {
		t.Errorf("With() keys = %v, want %v", got, want)
	}

	if got, _ := AsString(mustGet(t, updated, "b")); got != "two" {
		t.Errorf("With() did not replace b, got %q", got)
	}

	if got, _ := AsString(mustGet(t, base, "b")); got != "2" {
		t.Errorf("With() mutated the receiver, b = %q", got)
	}
}

func TestMapNodeWithout(t *testing.T) {
	t.Parallel()

	base := NewMapNode(SourceLine{}, entries("a", "1", "b", "2", "c", "3"))
	trimmed := base.Without("a", "missing")

	if got, want := trimmed.Keys(), []string{"b", "c"}; !slices.Equal(got, want) {
		t.Errorf("Without() keys = %v, want %v", got, want)
	}

	if base.Len() != 3 {
		t.Errorf("Without() mutated the receiver, Len() = %d", base.Len())
	}
}

func TestMapNodeNilIsSafe(t *testing.T) {
	t.Parallel()

	var m *MapNode
	if m.Len() != 0 || m.Has("a") || len(m.Keys()) != 0 || len(m.Entries()) != 0 {
		t.Error("a nil MapNode should behave as empty")
	}

	if !m.Loc().IsZero() {
		t.Error("a nil MapNode should report a zero location")
	}

	for range m.All() {
		t.Error("a nil MapNode should yield nothing")
	}
}

func newTestSeq() *SeqNode {
	return NewSeqNode(
		SourceLine{File: testFile, Start: Position{Line: 1, Column: 1}},
		[]Node{
			NewIntNode(SourceLine{}, 1),
			NewStringNode(SourceLine{}, "two"),
			NewNullNode(SourceLine{}),
		},
	)
}

func TestSeqNodeBasics(t *testing.T) {
	t.Parallel()

	s := newTestSeq()
	if s.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", s.Len())
	}

	if s.Loc().File != testFile {
		t.Errorf("Loc() = %v, want a location in %s", s.Loc(), testFile)
	}

	if got, ok := AsString(s.At(1)); !ok || got != "two" {
		t.Errorf("At(1) = %q, want \"two\"", got)
	}
}

func TestSeqNodeAtOutOfRange(t *testing.T) {
	t.Parallel()

	s := newTestSeq()
	if s.At(-1) != nil || s.At(3) != nil {
		t.Error("out-of-range At() should return nil")
	}
}

func TestSeqNodeAll(t *testing.T) {
	t.Parallel()

	s := newTestSeq()

	indexes := make([]int, 0, s.Len())
	for i := range s.All() {
		indexes = append(indexes, i)
	}

	if !slices.Equal(indexes, []int{0, 1, 2}) {
		t.Errorf("All() yielded %v, want [0 1 2]", indexes)
	}
}

func TestSeqNodeNilIsSafe(t *testing.T) {
	t.Parallel()

	var s *SeqNode
	if s.Len() != 0 || s.At(0) != nil || len(s.Items()) != 0 || !s.Loc().IsZero() {
		t.Error("a nil SeqNode should behave as empty")
	}
}

func TestSeqNodeCopiesItems(t *testing.T) {
	t.Parallel()

	items := []Node{NewIntNode(SourceLine{}, 1)}
	s := NewSeqNode(SourceLine{}, items)
	items[0] = NewIntNode(SourceLine{}, 99)

	first, ok := AsScalar(s.At(0))
	if !ok {
		t.Fatal("At(0) is not a scalar")
	}

	if got, _ := first.AsInt(); got != 1 {
		t.Errorf("NewSeqNode aliased its input slice, At(0) = %d", got)
	}
}

// scalarCase describes one expected reading of a ScalarNode.
type scalarCase struct {
	boxed   any
	node    *ScalarNode
	name    string
	str     string
	render  string
	kind    ScalarKind
	asFloat float64
	numeric bool
}

func checkScalar(t *testing.T, tc *scalarCase) {
	t.Helper()

	if got := tc.node.Kind(); got != tc.kind {
		t.Errorf("Kind() = %v, want %v", got, tc.kind)
	}

	if got, want := tc.node.IsNull(), tc.kind == NullScalar; got != want {
		t.Errorf("IsNull() = %v, want %v", got, want)
	}

	if got, ok := tc.node.AsString(); ok != (tc.kind == StringScalar) || got != tc.str {
		t.Errorf("AsString() = (%q, %v), want %q", got, ok, tc.str)
	}

	if got, ok := tc.node.AsFloat(); ok != tc.numeric || (tc.numeric && got != tc.asFloat) {
		t.Errorf("AsFloat() = (%v, %v), want (%v, %v)", got, ok, tc.asFloat, tc.numeric)
	}

	if got := tc.node.String(); got != tc.render {
		t.Errorf("String() = %q, want %q", got, tc.render)
	}

	if got := tc.node.Value(); got != tc.boxed {
		t.Errorf("Value() = %#v, want %#v", got, tc.boxed)
	}
}

func TestScalarNodeAccessors(t *testing.T) {
	t.Parallel()

	tests := []scalarCase{
		{
			name: nameNull, node: NewNullNode(SourceLine{}), kind: NullScalar,
			render: nameNull, boxed: nil,
		},
		{
			name: nameBoolean, node: NewBoolNode(SourceLine{}, true), kind: BoolScalar,
			render: "true", boxed: true,
		},
		{
			name: nameInt, node: NewIntNode(SourceLine{}, -7), kind: IntScalar,
			render: "-7", boxed: int64(-7), asFloat: -7, numeric: true,
		},
		{
			name: nameFloat, node: NewFloatNode(SourceLine{}, 2.5), kind: FloatScalar,
			render: "2.5", boxed: 2.5, asFloat: 2.5, numeric: true,
		},
		{
			name: nameString, node: NewStringNode(SourceLine{}, "hi"), kind: StringScalar,
			str: "hi", render: "hi", boxed: "hi",
		},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkScalar(t, tc)
		})
	}
}

func TestScalarNodeAsIntRejectsFloats(t *testing.T) {
	t.Parallel()

	// Schema Salad distinguishes int/long from float/double, so a whole-numbered
	// float must not silently read back as an integer.
	if _, ok := NewFloatNode(SourceLine{}, 3.0).AsInt(); ok {
		t.Error("AsInt() accepted a float scalar")
	}

	if got, ok := NewIntNode(SourceLine{}, 3).AsFloat(); !ok || got != 3 {
		t.Errorf("AsFloat() on an int = (%v, %v), want (3, true)", got, ok)
	}
}

func TestScalarNodeBool(t *testing.T) {
	t.Parallel()

	yes := NewBoolNode(SourceLine{}, true)
	if !yes.IsBool() || !yes.AsBool() {
		t.Error("a true boolean scalar should report IsBool and AsBool")
	}

	no := NewBoolNode(SourceLine{}, false)
	if !no.IsBool() || no.AsBool() {
		t.Error("a false boolean scalar should report IsBool but not AsBool")
	}

	other := NewIntNode(SourceLine{}, 1)
	if other.IsBool() || other.AsBool() {
		t.Error("an int scalar should be neither IsBool nor AsBool")
	}
}

func TestScalarNodeNilIsNull(t *testing.T) {
	t.Parallel()

	var s *ScalarNode
	if !s.IsNull() || s.Kind() != NullScalar || s.Value() != nil || !s.Loc().IsZero() {
		t.Error("a nil ScalarNode should behave as null")
	}
}

func TestScalarKindString(t *testing.T) {
	t.Parallel()

	want := map[ScalarKind]string{
		NullScalar: nameNull, BoolScalar: nameBoolean, IntScalar: nameInt,
		FloatScalar: nameFloat, StringScalar: nameString, ScalarKind(99): nameUnknown,
	}
	for kind, name := range want {
		if got := kind.String(); got != name {
			t.Errorf("ScalarKind(%d).String() = %q, want %q", kind, got, name)
		}
	}
}

func TestAsMapAndAsSeq(t *testing.T) {
	t.Parallel()

	m := NewMapNode(SourceLine{}, nil)
	s := NewSeqNode(SourceLine{}, nil)

	if got, ok := AsMap(m); !ok || got != m {
		t.Error("AsMap failed on a MapNode")
	}

	if _, ok := AsMap(s); ok {
		t.Error("AsMap accepted a SeqNode")
	}

	if got, ok := AsSeq(s); !ok || got != s {
		t.Error("AsSeq failed on a SeqNode")
	}

	if _, ok := AsSeq(m); ok {
		t.Error("AsSeq accepted a MapNode")
	}
}

func TestAsScalarAndAsString(t *testing.T) {
	t.Parallel()

	m := NewMapNode(SourceLine{}, nil)
	str := NewStringNode(SourceLine{}, "x")

	if got, ok := AsScalar(str); !ok || got != str {
		t.Error("AsScalar failed on a scalar")
	}

	if _, ok := AsScalar(m); ok {
		t.Error("AsScalar accepted a MapNode")
	}

	if got, ok := AsString(str); !ok || got != "x" {
		t.Error("AsString failed on a string scalar")
	}

	if _, ok := AsString(m); ok {
		t.Error("AsString accepted a MapNode")
	}
}

func TestIsNull(t *testing.T) {
	t.Parallel()

	if !IsNull(nil) {
		t.Error("IsNull(nil) should be true")
	}

	if !IsNull(NewNullNode(SourceLine{})) {
		t.Error("IsNull(null scalar) should be true")
	}

	if IsNull(NewStringNode(SourceLine{}, "x")) {
		t.Error("IsNull(string) should be false")
	}
}

func TestNodeKind(t *testing.T) {
	t.Parallel()

	cases := map[string]Node{
		nameMapping:  NewMapNode(SourceLine{}, nil),
		nameSequence: NewSeqNode(SourceLine{}, nil),
		nameString:   NewStringNode(SourceLine{}, "x"),
		nameFloat:    NewFloatNode(SourceLine{}, math.Pi),
		nameNothing:  nil,
	}
	for want, n := range cases {
		if got := NodeKind(n); got != want {
			t.Errorf("NodeKind(%v) = %q, want %q", n, got, want)
		}
	}
}
