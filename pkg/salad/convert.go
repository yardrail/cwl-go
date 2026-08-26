package salad

import (
	"math"
	"slices"
)

// fromUint64 converts an unsigned integer into a scalar node, widening values
// that do not fit in an int64 into floats.
func fromUint64(v uint64, loc SourceLine) *ScalarNode {
	if v > math.MaxInt64 {
		return NewFloatNode(loc, float64(v))
	}

	return NewIntNode(loc, int64(v))
}

// ToAny converts a Node tree into plain Go values, for JSON round-trips and for
// deep-equality assertions in tests.
//
// A *MapNode becomes a map[string]any, a *SeqNode becomes a []any, and a
// *ScalarNode becomes nil, bool, int64, float64 or string. A nil Node becomes nil.
//
// Key order is lost, because Go maps are unordered. Never round-trip
// order-significant data (record fields, enum symbols, identifier maps) through
// ToAny; walk the Node tree instead.
func ToAny(n Node) any {
	switch v := n.(type) {
	case *MapNode:
		out := make(map[string]any, v.Len())
		for key, val := range v.All() {
			out[key] = ToAny(val)
		}

		return out
	case *SeqNode:
		out := make([]any, 0, v.Len())
		for _, item := range v.All() {
			out = append(out, ToAny(item))
		}

		return out
	case *ScalarNode:
		return v.Value()
	default:
		return nil
	}
}

// FromAny converts plain Go values into a Node tree, attaching loc to every node
// it creates. It is the inverse of ToAny and the entry point for values that
// arrive from encoding/json.
//
// Accepted inputs are nil, bool, string, the signed and unsigned integer types,
// float32/float64, []any, []MapEntry, map[string]any, and any existing Node
// (returned unchanged). Anything else is an error.
//
// Because Go maps are unordered, the keys of a map[string]any are sorted
// lexicographically so that conversion is deterministic. Pass a []MapEntry when
// the original order matters.
func FromAny(v any, loc SourceLine) (Node, error) {
	if v == nil {
		return NewNullNode(loc), nil
	}

	if n, ok := v.(Node); ok {
		return n, nil
	}

	if n, ok := fromAnyScalar(v, loc); ok {
		return n, nil
	}

	return fromAnyContainer(v, loc)
}

// fromAnyScalar converts the non-numeric and floating-point scalar types.
func fromAnyScalar(v any, loc SourceLine) (Node, bool) {
	switch t := v.(type) {
	case bool:
		return NewBoolNode(loc, t), true
	case string:
		return NewStringNode(loc, t), true
	case float32:
		return NewFloatNode(loc, float64(t)), true
	case float64:
		return NewFloatNode(loc, t), true
	default:
		return fromAnySigned(v, loc)
	}
}

// fromAnySigned converts the signed integer types.
func fromAnySigned(v any, loc SourceLine) (Node, bool) {
	switch t := v.(type) {
	case int:
		return NewIntNode(loc, int64(t)), true
	case int8:
		return NewIntNode(loc, int64(t)), true
	case int16:
		return NewIntNode(loc, int64(t)), true
	case int32:
		return NewIntNode(loc, int64(t)), true
	case int64:
		return NewIntNode(loc, t), true
	default:
		return fromAnyUnsigned(v, loc)
	}
}

// fromAnyUnsigned converts the unsigned integer types. Values above [math.MaxInt64]
// become floats, matching how the YAML adapter widens oversized integers.
func fromAnyUnsigned(v any, loc SourceLine) (Node, bool) {
	switch t := v.(type) {
	case uint:
		return fromUint64(uint64(t), loc), true
	case uint8:
		return NewIntNode(loc, int64(t)), true
	case uint16:
		return NewIntNode(loc, int64(t)), true
	case uint32:
		return NewIntNode(loc, int64(t)), true
	case uint64:
		return fromUint64(t, loc), true
	default:
		return nil, false
	}
}

// fromAnyContainer converts the supported composite types.
func fromAnyContainer(v any, loc SourceLine) (Node, error) {
	switch t := v.(type) {
	case []any:
		return fromAnySlice(t, loc)
	case []MapEntry:
		return NewMapNode(loc, t), nil
	case map[string]any:
		return fromAnyMap(t, loc)
	default:
		return nil, Errorf(loc, "cannot convert a Go value of type %T into a salad node", v)
	}
}

// fromAnySlice converts a []any into a *SeqNode.
func fromAnySlice(items []any, loc SourceLine) (Node, error) {
	out := make([]Node, 0, len(items))
	for _, item := range items {
		n, err := FromAny(item, loc)
		if err != nil {
			return nil, err
		}

		out = append(out, n)
	}

	return NewSeqNode(loc, out), nil
}

// fromAnyMap converts a map[string]any into a *MapNode with lexicographically
// sorted keys.
func fromAnyMap(m map[string]any, loc SourceLine) (Node, error) {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	entries := make([]MapEntry, 0, len(keys))
	for _, key := range keys {
		n, err := FromAny(m[key], loc)
		if err != nil {
			return nil, err
		}

		entries = append(entries, MapEntry{Key: key, Value: n})
	}

	return NewMapNode(loc, entries), nil
}
