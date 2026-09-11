package cwlcore

import (
	"embed"
	"strings"
)

// schemaFS holds the vendored CWL v1.2 Schema Salad schema.
//
//go:embed schema
var schemaFS embed.FS

// schemaV11FS holds the vendored CWL v1.1 schema.
//
//go:embed schema-v1.1
var schemaV11FS embed.FS

// schemaV10FS holds the vendored CWL v1.0 schema.
//
//go:embed schema-v1.0
var schemaV10FS embed.FS

// schemaVersionRaw is the raw content of schema/VERSION.
//
//go:embed schema/VERSION
var schemaVersionRaw string

// SchemaVersion returns the upstream cwl-v1.2 release tag (e.g. "v1.2.1").
func SchemaVersion() string {
	return strings.TrimSpace(schemaVersionRaw)
}
