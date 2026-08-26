package cwlcore

import (
	"embed"
	"strings"
)

// schemaFS holds the vendored CWL v1.2 Schema Salad schema — the root schema documents
// and the full transitive $import/$include closure they need to load, including the
// Markdown documentation targets. The snapshot is pinned to the upstream release tag
// recorded in schema/VERSION; see schema/README.md for the file set and the re-vendoring
// procedure.
//
// Paths inside the FS are rooted at "schema/", matching the on-disk layout, so the
// relative $import/$include references between the files resolve unchanged.
//
//go:embed schema
var schemaFS embed.FS

// schemaVersionRaw is the verbatim content of schema/VERSION, trailing newline included.
// Use SchemaVersion for the trimmed value.
//
//go:embed schema/VERSION
var schemaVersionRaw string

// SchemaVersion returns the upstream common-workflow-language/cwl-v1.2 release tag that
// the embedded schema snapshot was vendored from, for example "v1.2.1".
func SchemaVersion() string {
	return strings.TrimSpace(schemaVersionRaw)
}
