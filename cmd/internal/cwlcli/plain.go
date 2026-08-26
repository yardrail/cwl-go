package cwlcli

// Plain converts a value made of ordinary Go containers into the renderers'
// own shapes: a map[string]any becomes an [Object] with its keys sorted, and a
// []any is converted element by element. Anything else is returned unchanged.
//
// It is the boundary for values that arrive as untyped Go data — chiefly
// salad.ToAny output, which is how the model hands over a node it kept
// verbatim. Two things need doing to such a value before it can be rendered.
// A Go map iterates randomly, so it has to be given a fixed order or the dump
// stops being diffable; and the text renderer would otherwise print a bare map
// through fmt as "map[a:1 b:2]", which is not the shape the rest of the dump
// is written in.
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
