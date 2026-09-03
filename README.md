# cwl-go

[![Go Reference](https://pkg.go.dev/badge/github.com/yardrail/cwl-go.svg)](https://pkg.go.dev/github.com/yardrail/cwl-go)
[![CI](https://github.com/yardrail/cwl-go/actions/workflows/ci.yml/badge.svg)](https://github.com/yardrail/cwl-go/actions/workflows/ci.yml)

A Go implementation of [CWL v1.2](https://www.commonwl.org/v1.2/) and
[Schema Salad v1.2](https://www.commonwl.org/v1.2/SchemaSalad.html).

`cwl-go` is a spec-compliant CWL engine that can parse, validate, and execute
CWL documents. It ships as a standalone Go module with no baked-in downstream
vocabulary, so it can be imported by anything that needs to work with CWL.

## Features

- **100% CWL v1.2 conformance** — 378/378 tests passing, including the
  required subset
- **CWL v1.0, v1.1, and v1.2** — documents declaring an older version are
  validated against that version's schema, then upgraded to v1.2 for execution
- **Full execution engine** — scatter (all three methods), conditionals
  (`when:`), subworkflows, Docker containers, CWL expressions, loops
- **Suspension and resume** — durable workflow execution with serializable run
  state
- **Standalone Schema Salad engine** — `pkg/salad` is CWL-agnostic and
  reusable for any Schema Salad-defined schema

## Installation

### CLI tools

```sh
go install github.com/yardrail/cwl-go/cmd/cwl-run@latest
go install github.com/yardrail/cwl-go/cmd/cwl-validate@latest
go install github.com/yardrail/cwl-go/cmd/cwl-inspect@latest
```

### As a library

```sh
go get github.com/yardrail/cwl-go
```

## CLI tools

### cwl-run

A [cwl-runner](https://www.commonwl.org/v1.2/CommandLineTool.html#Executing_CWL_documents_and_tools)-compatible
entrypoint: execute a CWL document and print the output object to stdout.
This is the binary the `cwltest` conformance harness drives.

```sh
cwl-run workflow.cwl job.json
cwl-run -outdir results/ workflow.cwl job.json
cwl-run -no-container tool.cwl          # skip Docker, run commands on the host
```

### cwl-validate

Validate one or more CWL documents against the embedded schema for the CWL
version they declare. Useful in CI pipelines and editor integrations.

```sh
cwl-validate tool.cwl workflow.cwl
cwl-validate -strict tool.cwl           # promote advisory diagnostics to errors
```

`-strict` is recommended for CI — without it, typo'd field names are silently
discarded per the CWL spec.

### cwl-inspect

Dump the parsed intermediate representation of a CWL document at any stage of
the loading pipeline. Output is deterministic (byte-identical across runs), so
diffs work.

```sh
cwl-inspect tool.cwl                    # default: typed cwlcore model (JSON)
cwl-inspect -stage parsed tool.cwl      # raw YAML parse tree
cwl-inspect -stage resolved tool.cwl    # after $import/$include and identifier resolution
cwl-inspect -stage graph tool.cwl       # all top-level processes
cwl-inspect -stage scope tool.cwl       # requirements/hints with provenance
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for how to build, test, lint, and run
the conformance suite.
