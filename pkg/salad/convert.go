package salad

import (
	"math"
	"slices"
	"strconv"
)

// fromUint64 converts a uint64 into a scalar node, using [DecimalScalar] if it exceeds int64.
func fromUint64(v uint64, loc SourceLine) *ScalarNode {
	if v <= math.MaxInt64 {
		return NewIntNode(loc, int64(v))
	}

	// Twenty digits at most, which ParseDecimal always accepts.
	value, _ := ParseDecimal(strconv.FormatUint(v, decimalBase))

	return NewNumberNode(loc, value)
}

// ToAny converts a Node tree into plain Go values (map[string]any, []any, scalars).
// Key order is lost; walk the Node tree for order-sensitive data.
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

// FromAny converts plain Go values into a Node tree. Inverse of [ToAny].
// map[string]any keys are sorted; pass []MapEntry to preserve order.
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
	case Decimal:
		return NewNumberNode(loc, t), true
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

// fromAnyUnsigned converts the unsigned integer types.
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

// fromAnyMap converts a map[string]any into a *MapNode with sorted keys.
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
