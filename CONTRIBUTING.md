# Contributing to cwl-go

## Prerequisites

- Go (see `go.mod` for the minimum version)
- [`task`](https://taskfile.dev)
- For `task lint`:
  [`golangci-lint`](https://golangci-lint.run) v2,
  [`go-arch-lint`](https://github.com/fe3dback/go-arch-lint),
  [`govulncheck`](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck),
  [`go.uber.org/nilaway`](https://github.com/uber-go/nilaway)

## Package layout

```
pkg/
├── salad/     generic Schema Salad engine — zero knowledge of CWL
├── cwlcore/   CWL v1.2 typed object model, built on salad
│   └── schema/  vendored CWL v1.0–v1.2 schemas, go:embed'd into the binary
└── cwlexec/   execution engine and scheduler, built on cwlcore
cmd/
├── cwl-run/       cwl-runner-compatible CLI entrypoint (drives the conformance suite)
├── cwl-validate/  validate a document against the embedded schema
└── cwl-inspect/   dump the parsed intermediate representation for debugging
```

Dependencies form a strict DAG: `salad` → `cwlcore` → `cwlexec`, enforced by
`.go-arch-lint.yml`. `salad` stays fully CWL-agnostic and is reusable outside
CWL, mirroring upstream `schema_salad`.

## Building

```sh
task build   # builds cwl-run, cwl-validate, cwl-inspect into bin/
```

## Testing and linting

```sh
task test         # go test ./...
task test:race    # go test -race ./...
task test:cover   # test with coverage and per-package summary
task lint         # golangci-lint, go-arch-lint, govulncheck, nilaway
```

Both `test` and `lint` run in CI (`.github/workflows/ci.yml`) on every pull
request and push to `main`.

## Conformance testing

The conformance suite runs against the pinned
[cwl-v1.2](https://github.com/common-workflow-language/cwl-v1.2) corpus.

```sh
task test:conformance                     # Stage 0: parse/validate sweep
task test:conformance:inprocess           # Stage 1 via in-process driver (no Python needed)
task test:conformance:inprocess:compare   # assert in-process driver matches cwltest
task test:conformance:run                 # Stage 1 via cwltest (requires Python + cwltest)
task test:conformance:run:update          # rewrite the Stage 1 ratchet from a run
```

Results are ratcheted: the ratchet files record every passing test ID, so a
one-for-one swap (new pass replacing a different old pass) is still caught.

## Generating code

```sh
task generate   # go generate ./...
```

## Docs

`AGENTS.md` (and its `CLAUDE.md` symlink) are generated from
`scripts/gen-agents-md/AGENTS.md.tmpl` plus the current package docs and
Taskfile targets:

```sh
task docs:agents         # regenerate
task docs:agents:check   # fail if out of date (used in CI)
```
