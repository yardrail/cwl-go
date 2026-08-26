package cwlcore

import (
	"embed"
	"fmt"
	"sync"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// Choosing which vendored schema a document is validated against.
//
// There are three, one per CWL version this implementation can read, and the
// choice is made from the version the document declares. See update.go for what
// happens to a document once its own version has judged it.

// The references the embedded CWL schemas are loaded through. Each mount point
// is a synthetic file URL so that the relative $import references between the
// vendored documents resolve without touching the real filesystem.
//
// The three mount points are deliberately distinct. All three schema trees
// declare the same vocabulary IRIs and name their files alike, so a shared mount
// would make one version's relative $import resolve into another version's tree.
const (
	schemaMountURL = "file:///cwl-go/cwl-v1.2/"
	schemaRootRef  = "schema/CommonWorkflowLanguage.yml"

	schemaV11MountURL = "file:///cwl-go/cwl-v1.1/"
	schemaV11RootRef  = "schema-v1.1/CommonWorkflowLanguage.yml"

	schemaV10MountURL = "file:///cwl-go/cwl-v1.0/"
	schemaV10RootRef  = "schema-v1.0/CommonWorkflowLanguage.yml"
)

// schemaSet describes one vendored schema tree: the embedded file system it is
// read from, the synthetic URL that file system is mounted at, and the root
// schema document to load, spelled relative to the mount point.
type schemaSet struct {
	files    embed.FS
	mountURL string
	rootRef  string
}

// sourceURL is the absolute reference the set's root schema document is loaded
// through.
func (s schemaSet) sourceURL() string {
	return s.mountURL + s.rootRef
}

// The three vendored schema trees, one per CWL version this implementation can
// read. See embed.go for what each holds and schema*/README.md for the tag each
// is pinned to.
var (
	schemaSetV12 = schemaSet{files: schemaFS, mountURL: schemaMountURL, rootRef: schemaRootRef}
	schemaSetV11 = schemaSet{files: schemaV11FS, mountURL: schemaV11MountURL, rootRef: schemaV11RootRef}
	schemaSetV10 = schemaSet{files: schemaV10FS, mountURL: schemaV10MountURL, rootRef: schemaV10RootRef}
)

// The embedded schemas, each loaded and flattened at most once.
//
// Flattening a CWL schema costs tens of milliseconds and one run may need more
// than one of them: a v1.2 workflow may run a v1.0 tool, and each document is
// validated against the schema for the version it declares. Memoizing per
// version means a run pays for the versions it actually meets, once each, for
// the life of the process.
var (
	cwlSchemaV12 = sync.OnceValues(func() (*salad.LoadedSchema, error) { return loadEmbeddedSchema(schemaSetV12) })
	cwlSchemaV11 = sync.OnceValues(func() (*salad.LoadedSchema, error) { return loadEmbeddedSchema(schemaSetV11) })
	cwlSchemaV10 = sync.OnceValues(func() (*salad.LoadedSchema, error) { return loadEmbeddedSchema(schemaSetV10) })
)

// schemaFor returns the flattened schema a document declaring version should be
// validated against.
//
// A document that declares no version at all is validated against v1.2. That is
// not a guess about what it was written for: a process with no cwlVersion is one
// embedded in a document that has one, or one built in memory by a caller of
// this package, and in both cases v1.2 is the version this implementation's
// model describes.
//
// Anything else is refused rather than approximated, because validating a
// document against a schema it was not written for is a fail-open: the checks
// that would have caught the mismatch are exactly the ones that do not run.
func schemaFor(version string) (*salad.LoadedSchema, error) {
	switch version {
	case "", CWLVersionV12:
		return cwlSchemaV12()
	case CWLVersionV11:
		return cwlSchemaV11()
	case CWLVersionV10:
		return cwlSchemaV10()
	default:
		return nil, fmt.Errorf("%w: %q is not one of %s, %s or %s",
			ErrUnsupportedVersion, version, CWLVersionV10, CWLVersionV11, CWLVersionV12)
	}
}

// loadEmbeddedSchema loads and flattens one vendored schema out of its embedded
// file system, without touching the real filesystem or the network. Call the
// memoized cwlSchemaV1x instead; this is separate only so that the cost can be
// benchmarked without the memoization in the way.
func loadEmbeddedSchema(set schemaSet) (*salad.LoadedSchema, error) {
	return salad.LoadSchema(
		set.sourceURL(),
		salad.WithFetcher(salad.NewFSFetcher(set.files, set.mountURL)),
		salad.WithBaseURL(set.mountURL),
	)
}
