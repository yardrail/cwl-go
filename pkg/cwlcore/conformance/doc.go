// Package conformance is the Stage 0 conformance sweep: loads every CWL document
// in the pinned cwl-v1.2 corpus through pkg/salad and pkg/cwlcore.
//
// Opt-in via CWL_CONFORMANCE=1. Set CWL_CONFORMANCE_CORPUS=<dir> for a local
// checkout, or CWL_CONFORMANCE_CACHE=<dir> to override the download cache.
package conformance
