# cwlcore/schema

This directory holds a vendored snapshot of the upstream
[`common-workflow-language/cwl-v1.2`](https://github.com/common-workflow-language/cwl-v1.2)
schema (`Process.yml`, `Workflow.yml`, etc.), pinned to a released tag and
`go:embed`'d into the binary so schema loading is fully offline and
reproducible.

Not vendored yet — this is scaffolding only. When the schema is vendored, add
a `VERSION` file recording the upstream tag the snapshot was taken from.
Upgrading to a newer CWL point release should be a deliberate, reviewable PR:
re-vendor the `.yml` files and bump `VERSION`, rather than fetching `main` at
build or run time.
