package salad

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"
)

func TestToAny(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want any
		node Node
		name string
	}{
		{name: nameNothing, node: nil, want: nil},
		{name: nameNull, node: NewNullNode(SourceLine{}), want: nil},
		{name: nameBoolean, node: NewBoolNode(SourceLine{}, true), want: true},
		{name: nameInt, node: NewIntNode(SourceLine{}, 42), want: int64(42)},
		{name: nameFloat, node: NewFloatNode(SourceLine{}, 1.5), want: 1.5},
		{name: nameString, node: NewStringNode(SourceLine{}, "s"), want: "s"},
		{
			name: nameSequence,
			node: NewSeqNode(SourceLine{}, []Node{NewIntNode(SourceLine{}, 1), NewNullNode(SourceLine{})}),
			want: []any{int64(1), nil},
		},
		{
			name: nameMapping,
			node: NewMapNode(SourceLine{}, entries("b", "2", "a", "1")),
			want: map[string]any{"a": "1", "b": "2"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := ToAny(tc.node); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ToAny() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestFromAnyScalars(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input any
		want  any
		name  string
	}{
		{name: "nil", input: nil, want: nil},
		{name: nameBoolean, input: false, want: false},
		{name: nameString, input: "s", want: "s"},
		{name: nameInt, input: 5, want: int64(5)},
		{name: "int8", input: int8(5), want: int64(5)},
		{name: "int16", input: int16(5), want: int64(5)},
		{name: "int32", input: int32(5), want: int64(5)},
		{name: "int64", input: int64(5), want: int64(5)},
		{name: "uint", input: uint(5), want: int64(5)},
		{name: "uint8", input: uint8(5), want: int64(5)},
		{name: "uint16", input: uint16(5), want: int64(5)},
		{name: "uint32", input: uint32(5), want: int64(5)},
		{name: "uint64", input: uint64(5), want: int64(5)},
		{name: "huge uint64 widens", input: uint64(math.MaxUint64), want: float64(math.MaxUint64)},
		{name: "float32", input: float32(0.5), want: 0.5},
		{name: nameFloat, input: 0.5, want: 0.5},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			n, err := FromAny(tc.input, SourceLine{})
			if err != nil {
				t.Fatalf("FromAny(%#v) failed: %v", tc.input, err)
			}

			if got := ToAny(n); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("FromAny(%#v) -> ToAny = %#v, want %#v", tc.input, got, tc.want)
			}
		})
	}
}

func TestFromAnyAttachesLocation(t *testing.T) {
	t.Parallel()

	loc := SourceLine{File: "gen", Start: Position{Line: 9, Column: 2}}

	n, err := FromAny(map[string]any{"a": []any{1}}, loc)
	if err != nil {
		t.Fatalf("FromAny failed: %v", err)
	}

	if n.Loc() != loc {
		t.Errorf("root Loc() = %v, want %v", n.Loc(), loc)
	}

	inner := mustGet(t, mustMap(t, n), "a")
	if inner.Loc() != loc {
		t.Errorf("nested Loc() = %v, want %v", inner.Loc(), loc)
	}
}

func TestFromAnySortsMapKeys(t *testing.T) {
	t.Parallel()

	// Go map iteration is randomized, so FromAny must impose a deterministic order.
	input := map[string]any{"delta": 1, "amber": 2, "charlie": 3, "bravo": 4}
	want := []string{"amber", "bravo", "charlie", "delta"}

	for range 20 {
		n, err := FromAny(input, SourceLine{})
		if err != nil {
			t.Fatalf("FromAny failed: %v", err)
		}

		if got := mustMap(t, n).Keys(); !slices.Equal(got, want) {
			t.Fatalf("Keys() = %v, want %v", got, want)
		}
	}
}

func TestFromAnyEntriesPreserveOrder(t *testing.T) {
	t.Parallel()

	n, err := FromAny(entries("zulu", "amber"), SourceLine{})
	if err != nil {
		t.Fatalf("FromAny failed: %v", err)
	}

	if got, want := mustMap(t, n).Keys(), []string{"zulu"}; !slices.Equal(got, want) {
		t.Errorf("Keys() = %v, want %v", got, want)
	}
}

func TestFromAnyPassesNodesThrough(t *testing.T) {
	t.Parallel()

	original := NewStringNode(SourceLine{File: "orig"}, "x")

	n, err := FromAny(original, SourceLine{File: "other"})
	if err != nil {
		t.Fatalf("FromAny failed: %v", err)
	}

	if n != Node(original) {
		t.Error("FromAny should return an existing Node unchanged")
	}
}

func TestFromAnyRejectsUnsupported(t *testing.T) {
	t.Parallel()

	loc := SourceLine{File: "gen", Start: Position{Line: 1, Column: 1}}

	_, err := FromAny(struct{ A int }{}, loc)
	if err == nil {
		t.Fatal("FromAny should reject an unsupported type")
	}

	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("error is %T, want *Error", err)
	}

	if se.Loc != loc {
		t.Errorf("error Loc = %v, want %v", se.Loc, loc)
	}
}

func TestFromAnyPropagatesNestedErrors(t *testing.T) {
	t.Parallel()

	_, seqErr := FromAny([]any{make(chan int)}, SourceLine{})
	if seqErr == nil {
		t.Error("FromAny should propagate an error from a sequence element")
	}

	_, mapErr := FromAny(map[string]any{"a": make(chan int)}, SourceLine{})
	if mapErr == nil {
		t.Error("FromAny should propagate an error from a map value")
	}
}

func TestJSONRoundTrip(t *testing.T) {
	t.Parallel()

	src := `{"class":"Thing","inputs":[{"id":"a"}],"ratio":0.25,"flag":true,"none":null}`

	var decoded any

	err := json.Unmarshal([]byte(src), &decoded)
	if err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	n, err := FromAny(decoded, SourceLine{File: "inline.json"})
	if err != nil {
		t.Fatalf("FromAny failed: %v", err)
	}

	out, err := json.Marshal(ToAny(n))
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var reparsed any

	err = json.Unmarshal(out, &reparsed)
	if err != nil {
		t.Fatalf("json.Unmarshal of the round-tripped value failed: %v", err)
	}

	if !reflect.DeepEqual(reparsed, decoded) {
		t.Errorf("round trip changed the value:\n got %#v\nwant %#v", reparsed, decoded)
	}
}

func TestParseThenToAnyMatchesJSON(t *testing.T) {
	t.Parallel()

	// The YAML adapter and encoding/json must agree on the shape of a document,
	// modulo integers, which salad keeps as int64 rather than widening to float64.
	n := mustParse(t, "inline.json", `{"a":"x","b":[1,2],"c":{"d":true},"e":null}`)

	want := map[string]any{
		"a": "x",
		"b": []any{int64(1), int64(2)},
		"c": map[string]any{"d": true},
		"e": nil,
	}
	if got := ToAny(n); !reflect.DeepEqual(got, want) {
		t.Errorf("ToAny(Parse(json)) = %#v, want %#v", got, want)
	}
}
