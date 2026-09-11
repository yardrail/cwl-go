package salad

import "embed"

// metaschemaFS holds the vendored Schema Salad metaschema and its transitive closure.
//
//go:embed metaschema
var metaschemaFS embed.FS
