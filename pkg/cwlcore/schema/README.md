# cwlcore/schema

This directory holds a vendored snapshot of the upstream
[`common-workflow-language/cwl-v1.2`](https://github.com/common-workflow-language/cwl-v1.2)
schema (`Process.yml`, `Workflow.yml`, etc.), pinned to a released tag and
`go:embed`'d into the binary so schema loading is fully offline and
reproducible.

The snapshot is pinned to the tag recorded in [`VERSION`](VERSION) — currently
`v1.2.1` (commit `ae6899d159b5d62411f5f16d797f1d8e2176c5ba`), the latest
official CWL v1.2 release. Files are byte-for-byte copies of the upstream tree
with their relative paths preserved, because the `$import`/`$include`
references between them are relative.

## File set

The snapshot is the transitive `$import`/`$include` closure of the root schema
documents, nothing more and nothing less:

- Root schemas: `CommonWorkflowLanguage.yml` (the full CWL v1.2 schema),
  `CommandLineTool.yml`, `Workflow.yml`, `Operation.yml`, `Process.yml`, and
  `CommandLineTool-standalone.yml`.
- Documentation targets pulled in by `$include`: `concepts.md`, `contrib.md`,
  `intro.md`, `invocation.md`. These are not optional — the Schema Salad loader
  resolves `$include` eagerly and fails if they are absent.
- `salad/schema_salad/metaschema/metaschema_base.yml`, which `Process.yml`
  `$import`s by that exact relative path. It is the Schema Salad base
  metaschema *as vendored inside cwl-v1.2 at this tag*, and it deliberately
  differs from the newer snapshot in `pkg/salad/metaschema` (which tracks
  schema-salad upstream and has since gained `MapSchema`/`UnionSchema`). Do not
  deduplicate the two: this copy is what CWL v1.2.1 validates against.
- `LICENSE.txt`, the upstream Apache-2.0 license covering these files.

## Re-vendoring

Upgrading to a newer CWL point release should be a deliberate, reviewable PR,
never a fetch at build or run time:

1. `git clone --branch <tag> https://github.com/common-workflow-language/cwl-v1.2`
2. Copy the root `.yml` files plus every `$import`/`$include` target, keeping
   relative paths intact.
3. Write the new tag into `VERSION`.
4. Verify the closure is complete: grep the vendored tree for `$import` and
   `$include` and confirm every referenced path resolves locally.
5. Run `go test ./pkg/cwlcore/...` — `embed_test.go` asserts the expected files
   are present and non-empty.
