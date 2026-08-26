# cwlcore/schema-v1.0

This directory holds a vendored snapshot of the upstream CWL **v1.0** schema, taken
from the `v1.0/` subtree of
[`common-workflow-language/common-workflow-language`](https://github.com/common-workflow-language/common-workflow-language)
and `go:embed`'d into the binary so schema loading is fully offline and reproducible.

The snapshot is pinned to the tag recorded in [`VERSION`](VERSION) — currently `v1.0.2`
(commit `a062055fddcc7d7d9dbc53d28288e3ccb9a800d8`), the last official CWL v1.0 point
release. Files are byte-for-byte copies of the upstream tree with their relative paths
preserved, because the `$import`/`$include` references between them are relative.

Note the repository slug: CWL v1.1 and v1.2 each live in a repository of their own
(`cwl-v1.1`, `cwl-v1.2`), but there is no `cwl-v1.0` repository — v1.0 was released from
the original monorepo, under a `v1.0/` directory.

## Why a second schema tree exists at all

A document declaring `cwlVersion: v1.0` is validated against *this* schema, not against
v1.2's. That is the whole point: v1.2 widened several fields (`ResourceRequirement`
accepts fractional cores, `WorkflowStep` gained `when`, `secondaryFiles` became a
record), so validating an old document against the new schema silently accepts syntax
its declared version never had. Once it validates here, `pkg/cwlcore/update.go` rewrites
it into v1.2 form, which is what the rest of this package decodes.

## File set

The snapshot is the transitive `$import`/`$include` closure of the root schema
documents, nothing more and nothing less:

- Root schemas: `CommonWorkflowLanguage.yml` (the full CWL v1.0 schema),
  `CommandLineTool.yml`, `Workflow.yml`, `Process.yml`, and
  `CommandLineTool-standalone.yml`. There is no `Operation.yml`: `Operation` is a v1.2
  addition.
- Documentation targets pulled in by `$include`: `concepts.md`, `contrib.md`,
  `intro.md`, `invocation.md`. These are not optional — the Schema Salad loader resolves
  `$include` eagerly and fails if they are absent.
- `salad/schema_salad/metaschema/metaschema_base.yml`, which `Process.yml` `$import`s by
  that exact relative path. It is the Schema Salad base metaschema *as vendored inside
  the CWL v1.0 release*, and it deliberately differs from the copies in
  `../schema/salad/` and `../../salad/metaschema/`. Do not deduplicate the three: each
  copy is what its own CWL version validates against.
- `LICENSE.txt`, the upstream Apache-2.0 license covering these files.

Upstream's `v1.0/` directory also carries `UserGuide.yml`, `userguide-intro.md`,
`examples/` and the v1.0 conformance corpus. None of them is reachable from
`CommonWorkflowLanguage.yml`, so none of them is vendored.

## Re-vendoring

Same procedure as [`../schema/README.md`](../schema/README.md):

1. `git clone --branch <tag> https://github.com/common-workflow-language/common-workflow-language`
2. Copy the root `.yml` files from `v1.0/` plus every `$import`/`$include` target,
   keeping relative paths intact.
3. Write the new tag into `VERSION`.
4. Verify the closure is complete: grep the vendored tree for `$import` and `$include`
   and confirm every referenced path resolves locally.
5. Run `go test ./pkg/cwlcore/...` — `embed_test.go` asserts the expected files are
   present, non-empty, and that nothing unexpected has crept in.
