package main

import (
	"github.com/yardrail/cwl-go/cmd/internal/cwlcli"
	"github.com/yardrail/cwl-go/pkg/cwlcore"
)

// Typed-slice to []any conversions for renderers.

// stringItems converts a string slice for rendering.
func stringItems(items []string) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}

	return out
}

// intItems converts an int slice for rendering.
func intItems(items []int) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}

	return out
}

// expressionItems converts a slice of unevaluated expressions for rendering.
func expressionItems(items []cwlcore.Expression) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, string(item))
	}

	return out
}

// objectItems converts a slice of built objects for rendering.
func objectItems(items []*cwlcli.Object) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}

	return out
}
