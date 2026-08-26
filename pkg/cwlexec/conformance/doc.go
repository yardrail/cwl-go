// Package conformance is the in-process CWL conformance driver: it reads the pinned
// cwl-v1.2 suite through the manifest reader in pkg/cwlcore/conformance and runs every
// entry through [github.com/yardrail/cwl-go/pkg/cwlexec], comparing each output object by
// cwltest's own rules.
//
// It exists for the development loop. The authoritative check is still cwltest, which is
// what CI runs and what the project self-certifies against; but cwltest needs Python, a
// virtualenv and a built binary, and it forks a process per test. This driver needs none
// of that, so the whole suite is one "go test" away while a feature is being written.
//
// Because it is a second harness over the same corpus it is only useful while it agrees
// with the first, and TestInProcessMatchesCwltest asserts exactly that: the pass, fail and
// skip *sets* must be equal, not merely the counts, since two offsetting mistakes would
// cancel in a count. When they disagree, cwltest is right and this driver has a bug.
//
// The suite is opt-in and runs only when CWL_CONFORMANCE=1 is set, because the corpus is
// fetched rather than vendored. Unlike the Stage 0 sweep this package never downloads it:
// it uses a corpus that is already unpacked and skips when there is none, so nothing here
// touches the network.
//
// Environment:
//
//	CWL_CONFORMANCE=1              enable the suite (otherwise every test here skips)
//	CWL_CONFORMANCE_CORPUS=<dir>   use an existing corpus checkout
//	CWL_CONFORMANCE_CACHE=<dir>    where the Stage 0 sweep unpacked one (default: <UserCacheDir>/cwl-go/conformance)
//	CWLTEST=<path>                 the cwltest executable the comparison test runs (default: the one on PATH)
//
// The corpus tag is pinned to cwlcore.SchemaVersion(), the release the vendored schema was
// cut from, on the same reasoning as the Stage 0 sweep: running a different tag than the
// schema was vendored from would measure two things at once.
package conformance
