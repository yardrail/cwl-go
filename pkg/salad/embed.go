package salad

import "embed"

// metaschemaFS holds the vendored Schema Salad metaschema — metaschema.yml and the full
// transitive $import/$include closure it needs to load, including the Markdown
// documentation targets. The snapshot is pinned to the upstream revision recorded in
// metaschema/VERSION; see metaschema/README.md for the file set and the re-vendoring
// procedure.
//
// Paths inside the FS are rooted at "metaschema/", matching the on-disk layout, so the
// relative $import/$include references between the files resolve unchanged.
//
//go:embed metaschema
var metaschemaFS embed.FS
