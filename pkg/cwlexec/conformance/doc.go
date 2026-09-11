// Package conformance is the in-process CWL conformance driver: runs the pinned
// cwl-v1.2 suite through pkg/cwlexec and compares outputs by cwltest's rules.
//
// Opt-in via CWL_CONFORMANCE=1. Set CWL_CONFORMANCE_CORPUS=<dir> for a local
// checkout, CWL_CONFORMANCE_CACHE=<dir> to override the cache, or CWLTEST=<path>
// for the cwltest binary.
package conformance
