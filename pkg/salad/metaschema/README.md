# salad/metaschema

This directory holds a vendored snapshot of the Schema Salad metaschema from
[`common-workflow-language/schema_salad`](https://github.com/common-workflow-language/schema_salad)
(`src/schema_salad/metaschema/`), `go:embed`'d into the binary so that loading
the metaschema is fully offline and reproducible.

The snapshot is pinned to the commit recorded in [`VERSION`](VERSION) —
currently `c163bf94efe45b4b4aeff3d702cf5bfaa8fa92ba`, the revision this
project's design docs are written against. Files are byte-for-byte copies of
the upstream tree with their relative paths preserved, because the
`$import`/`$include` references between them are relative.

## File set

The snapshot is the transitive `$import`/`$include` closure of `metaschema.yml`
(32 files):

- `metaschema.yml` and `metaschema_base.yml`, the metaschema itself.
- `salad.md` and `import_include.md`, documentation targets pulled in by
  `$include`. These are not optional — the loader resolves `$include` eagerly
  and fails if they are absent.
- The seven per-feature documentation chapters `field_name.yml`,
  `ident_res.yml`, `link_res.yml`, `vocab_res.yml`, `map_res.yml`,
  `typedsl_res.yml`, `sfdsl_res.yml`, each of which `$include`s its own
  `_schema` / `_src` / `_proc` example triplet (21 further files).
- `LICENSE.txt`, the upstream Apache-2.0 license covering these files.

The same 21 example files are also vendored under `pkg/salad/testdata/examples`
(with `metaschema_base.yml`, which `typedsl_res_schema.yml` imports), where they
serve as executable conformance fixtures rather than embedded documentation.

Note that this snapshot is *newer* than the Schema Salad copy vendored inside
`pkg/cwlcore/schema/salad/`, which is frozen at whatever cwl-v1.2 shipped at its
release tag. The two are intentionally independent.

## Re-vendoring

Upgrading should be a deliberate, reviewable PR, never a fetch at build or run
time:

1. `git clone https://github.com/common-workflow-language/schema_salad` and
   check out the target commit or release tag.
2. Copy `src/schema_salad/metaschema/metaschema.yml` plus every
   `$import`/`$include` target, keeping relative paths intact.
3. Write the new commit SHA or tag into `VERSION`.
4. Verify the closure is complete: grep the vendored tree for `$import` and
   `$include` and confirm every referenced path resolves locally.
5. Run `go test ./pkg/salad/...` — `embed_test.go` asserts the expected files
   are present and non-empty.
