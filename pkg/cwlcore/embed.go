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

// schemaV11FS holds the vendored CWL v1.1 schema, on the same terms as schemaFS.
//
// It is a separate tree rather than a diff against v1.2 because the two describe the
// same vocabulary differently: a v1.1 document is validated against what v1.1 declared,
// which is the only way "this document uses syntax its declared version did not have"
// can be reported at all. See schema-v1.1/README.md.
//
//go:embed schema-v1.1
var schemaV11FS embed.FS

// schemaV10FS holds the vendored CWL v1.0 schema, on the same terms as schemaV11FS.
// See schema-v1.0/README.md.
//
//go:embed schema-v1.0
var schemaV10FS embed.FS

// schemaVersionRaw is the verbatim content of schema/VERSION, trailing newline included.
// Use SchemaVersion for the trimmed value.
//
//go:embed schema/VERSION
var schemaVersionRaw string

// SchemaVersion returns the upstream common-workflow-language/cwl-v1.2 release tag that
// the embedded schema snapshot was vendored from, for example "v1.2.1".
//
// It names the v1.2 snapshot alone. The v1.0 and v1.1 snapshots are pinned to tags of
// their own — recorded in schema-v1.0/VERSION and schema-v1.1/VERSION — and exist to
// validate documents declaring those versions before they are upgraded; v1.2 is what
// this implementation runs, so it is the one worth reporting.
func SchemaVersion() string {
	return strings.TrimSpace(schemaVersionRaw)
}
