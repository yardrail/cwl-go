# cwl-go

A feature-complete Go implementation of the [Common Workflow Language v1.2](https://www.commonwl.org/v1.2/)
(v1.2.1) and [Schema Salad v1.2](https://www.commonwl.org/v1.2/SchemaSalad.html) specs.

`cwl-go` is a general-purpose, spec-compliant CWL engine — it has no knowledge of any
downstream vocabulary. It exists as a standalone module so it can be depended on by
anything that needs to parse or run CWL documents.

## Prerequisites

- Go (see `go.mod` for the version)
- [`task`](https://taskfile.dev)
- [`golangci-lint`](https://golangci-lint.run) v2, [`go-arch-lint`](https://github.com/fe3dback/go-arch-lint),
  [`govulncheck`](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck),
  [`go.uber.org/nilaway`](https://github.com/uber-go/nilaway) — for `task lint`

## Package layout

```
pkg/
├── salad/     generic Schema Salad engine — zero knowledge of CWL
├── cwlcore/   CWL v1.2 typed object model, built on salad
│   └── schema/  vendored CWL v1.2 schema, go:embed'd into the binary
└── cwlexec/   runtime/scheduler, built on cwlcore
cmd/
├── cwl-run/       cwl-runner-compatible CLI entrypoint (drives the conformance suite)
├── cwl-validate/  validate a document against the embedded schema (dev/CI tool)
└── cwl-inspect/   dump the parsed intermediate representation for debugging
```

Dependencies form a strict DAG: `salad` → `cwlcore` → `cwlexec`, enforced by
`.go-arch-lint.yml` (`task lint` runs `go-arch-lint check`). `salad` stays fully
CWL-agnostic and is reusable outside CWL, mirroring upstream `schema_salad`.

This is scaffolding: the packages above are stubs (a package doc comment each). The
full design — public interfaces, the parsed-document value representation, the error
model, extension points for engines built on top of `cwl-go` — is tracked in
[yardrail/yardrail#91](https://github.com/yardrail/yardrail/issues/91).

## Testing & linting

```sh
task test   # go test ./...
task lint   # golangci-lint, go-arch-lint, govulncheck, nilaway
```

Both also run in CI (`.github/workflows/ci.yml`) on every pull request and push to
`main`.

## Generating code

`pkg/cwlcore/schema` will hold a vendored, `go:embed`'d snapshot of the upstream CWL
schema once it's added (see that directory's README). After adding or updating
generated code:

```sh
task generate
```

## Docs

`AGENTS.md` (and its `CLAUDE.md` symlink) are generated from
`scripts/gen-agents-md/AGENTS.md.tmpl` plus the current package docs and Taskfile
targets:

```sh
task docs:agents         # regenerate
task docs:agents:check   # fail if out of date (used in CI)
```
