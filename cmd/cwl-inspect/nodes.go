package main

import (
	"github.com/yardrail/cwl-go/cmd/internal/cwlcli"
	"github.com/yardrail/cwl-go/pkg/salad"
)

// Keys for dumped nodes.
const (
	keyKind    = "kind"
	keyLoc     = "loc"
	keyValue   = "value"
	keyEntries = "entries"
	keyItems   = "items"
	keyKey     = "key"
)

// nodeObject dumps a salad node tree with source lines and key order preserved.
func nodeObject(n salad.Node) *cwlcli.Object {
	o := cwlcli.NewObject()
	o.Set(keyKind, salad.NodeKind(n))

	if n == nil {
		return o
	}

	o.SetString(keyLoc, n.Loc().String())

	if mapping, ok := salad.AsMap(n); ok {
		return o.Set(keyEntries, mapEntries(mapping))
	}

	if seq, ok := salad.AsSeq(n); ok {
		return o.Set(keyItems, seqItems(seq))
	}

	if scalar, ok := salad.AsScalar(n); ok {
		return o.Set(keyValue, scalar.Value())
	}

	return o
}

// mapEntries dumps a mapping's entries in document order.
func mapEntries(m *salad.MapNode) []any {
	entries := m.Entries()
	out := make([]any, 0, len(entries))

	for _, entry := range entries {
		out = append(out, cwlcli.NewObject().
			Set(keyKey, entry.Key).
			Set(keyValue, nodeObject(entry.Value)))
	}

	return out
}

// seqItems dumps a sequence's items in order.
func seqItems(s *salad.SeqNode) []any {
	items := s.Items()
	out := make([]any, 0, len(items))

	for _, item := range items {
		out = append(out, nodeObject(item))
	}

	return out
}
