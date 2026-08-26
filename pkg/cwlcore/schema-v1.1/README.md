# cwlcore/schema-v1.1

This directory holds a vendored snapshot of the upstream
[`common-workflow-language/cwl-v1.1`](https://github.com/common-workflow-language/cwl-v1.1)
schema, `go:embed`'d into the binary so schema loading is fully offline and
reproducible.

The snapshot is pinned to the tag recorded in [`VERSION`](VERSION) — currently `v1.1.0`
(commit `4feec74019b56dc5c51be905a208ff90797661de`), the only official CWL v1.1 release.
Files are byte-for-byte copies of the upstream tree with their relative paths preserved,
because the `$import`/`$include` references between them are relative.

## Why a second schema tree exists at all

A document declaring `cwlVersion: v1.1` is validated against *this* schema, not against
v1.2's. v1.2 widened `ResourceRequirement` to accept fractional cores and added
`WorkflowStep.when`, so validating a v1.1 document against the v1.2 schema silently
accepts syntax its declared version never had. Once it validates here,
`pkg/cwlcore/update.go` rewrites it into v1.2 form, which is what the rest of this
package decodes.

## File set

The snapshot is the transitive `$import`/`$include` closure of the root schema
documents, nothing more and nothing less:

- Root schemas: `CommonWorkflowLanguage.yml` (the full CWL v1.1 schema),
  `CommandLineTool.yml`, `Workflow.yml`, `Process.yml`, and
  `CommandLineTool-standalone.yml`. There is no `Operation.yml`: `Operation` is a v1.2
  addition.
- Documentation targets pulled in by `$include`: `concepts.md`, `contrib.md`,
  `intro.md`, `invocation.md`. These are not optional — the Schema Salad loader resolves
  `$include` eagerly and fails if they are absent.
- `salad/schema_salad/metaschema/metaschema_base.yml`, which `Process.yml` `$import`s by
  that exact relative path. It is the Schema Salad base metaschema *as vendored inside
  cwl-v1.1 at this tag*, and it deliberately differs from the copies in
  `../schema/salad/` and `../../salad/metaschema/`. Do not deduplicate the three: each
  copy is what its own CWL version validates against.
- `LICENSE.txt`, the upstream Apache-2.0 license covering these files.

Upstream also carries `index.md`, `cwl-runner.cwl` and the v1.1 conformance corpus.
None of them is reachable from `CommonWorkflowLanguage.yml`, so none of them is
vendored.

## Re-vendoring

Same procedure as [`../schema/README.md`](../schema/README.md):

1. `git clone --branch <tag> https://github.com/common-workflow-language/cwl-v1.1`
2. Copy the root `.yml` files plus every `$import`/`$include` target, keeping relative
   paths intact.
3. Write the new tag into `VERSION`.
4. Verify the closure is complete: grep the vendored tree for `$import` and `$include`
   and confirm every referenced path resolves locally.
5. Run `go test ./pkg/cwlcore/...` — `embed_test.go` asserts the expected files are
   present, non-empty, and that nothing unexpected has crept in.
