package cwlcore

import (
	"embed"
	"fmt"
	"sync"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// Schema selection: one vendored schema per CWL version (v1.0, v1.1, v1.2).

// Synthetic file URLs for loading embedded schemas.
// Each version gets a distinct mount to avoid cross-version $import collisions.
const (
	schemaMountURL = "file:///cwl-go/cwl-v1.2/"
	schemaRootRef  = "schema/CommonWorkflowLanguage.yml"

	schemaV11MountURL = "file:///cwl-go/cwl-v1.1/"
	schemaV11RootRef  = "schema-v1.1/CommonWorkflowLanguage.yml"

	schemaV10MountURL = "file:///cwl-go/cwl-v1.0/"
	schemaV10RootRef  = "schema-v1.0/CommonWorkflowLanguage.yml"
)

// schemaSet describes one vendored schema tree.
type schemaSet struct {
	files    embed.FS
	mountURL string
	rootRef  string
}

// sourceURL is the absolute URL of the root schema document.
func (s schemaSet) sourceURL() string {
	return s.mountURL + s.rootRef
}

// The three vendored schema trees.
var (
	schemaSetV12 = schemaSet{files: schemaFS, mountURL: schemaMountURL, rootRef: schemaRootRef}
	schemaSetV11 = schemaSet{files: schemaV11FS, mountURL: schemaV11MountURL, rootRef: schemaV11RootRef}
	schemaSetV10 = schemaSet{files: schemaV10FS, mountURL: schemaV10MountURL, rootRef: schemaV10RootRef}
)

// Each schema loaded and flattened at most once.
var (
	cwlSchemaV12 = sync.OnceValues(func() (*salad.LoadedSchema, error) { return loadEmbeddedSchema(schemaSetV12) })
	cwlSchemaV11 = sync.OnceValues(func() (*salad.LoadedSchema, error) { return loadEmbeddedSchema(schemaSetV11) })
	cwlSchemaV10 = sync.OnceValues(func() (*salad.LoadedSchema, error) { return loadEmbeddedSchema(schemaSetV10) })
)

// schemaFor returns the schema for the given CWL version.
// Empty version defaults to v1.2; unknown versions return ErrUnsupportedVersion.
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

// loadEmbeddedSchema loads and flattens one vendored schema from its [embed.FS].
func loadEmbeddedSchema(set schemaSet) (*salad.LoadedSchema, error) {
	return salad.LoadSchema(
		set.sourceURL(),
		salad.WithFetcher(salad.NewFSFetcher(set.files, set.mountURL)),
		salad.WithBaseURL(set.mountURL),
	)
}
