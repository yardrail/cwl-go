// Package conformance is the Stage 0 conformance sweep: every CWL document in the
// pinned common-workflow-language/cwl-v1.2 corpus is loaded through pkg/salad and
// decoded by pkg/cwlcore, and any unexpected failure fails the build.
//
// The sweep is opt-in and runs only when CWL_CONFORMANCE=1 is set, because the corpus
// is fetched from GitHub rather than vendored (it is far too large to check in). A
// developer running "go test ./..." offline therefore sees a skip, never a red build.
//
// Environment:
//
//	CWL_CONFORMANCE=1              enable the sweep (otherwise every test here skips)
//	CWL_CONFORMANCE_CORPUS=<dir>   use an existing corpus checkout instead of fetching
//	CWL_CONFORMANCE_CACHE=<dir>    override the download cache (default: <UserCacheDir>/cwl-go/conformance)
//
// The pass count and the ranked failure clusters are logged, so pass -v to see them:
//
//	CWL_CONFORMANCE=1 go test -v ./pkg/cwlcore/conformance/
//
// The corpus tag is not configurable: it is pinned to cwlcore.SchemaVersion(), the same
// upstream release the vendored schema was cut from. Sweeping a different tag than the
// schema was vendored from would measure two things at once.
package conformance
