// Package cwlcli holds the plumbing the cwl-go developer command-line tools
// share: a deterministic ordered value model, JSON and text renderers for it,
// one place where an error becomes something a human can act on, and the
// version banner.
//
// It is internal to cmd/ on purpose. Nothing here is part of the library
// surface — pkg/salad, pkg/cwlcore and pkg/cwlexec must never import it — and
// it exists only so that cwl-validate and cwl-inspect present one flag style
// and one error rendering rather than two.
//
// Determinism is the design constraint. The primary use of a dump is diffing
// two runs of it, so every value that reaches a renderer has a fixed order:
// Object preserves insertion order rather than hashing its keys, and the one
// place a Go map is read — [SortedKeys] — sorts before iterating.
package cwlcli
