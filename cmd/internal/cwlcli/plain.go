package cwlcli

// Plain converts Go containers to renderer shapes: maps become sorted [Object]s, slices are converted recursively.
func Plain(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return plainObject(t)
	case []any:
		return plainSlice(t)
	default:
		return v
	}
}

// plainObject converts a map into an Object with sorted keys.
func plainObject(m map[string]any) *Object {
	o := NewObject()
	for _, key := range SortedKeys(m) {
		o.Set(key, Plain(m[key]))
	}

	return o
}

// plainSlice converts a slice element by element, preserving its order.
func plainSlice(items []any) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, Plain(item))
	}

	return out
}
