# openapi-go-mcp

[![CI](https://github.com/dipjyotimetia/openapi-go-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/dipjyotimetia/openapi-go-mcp/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/dipjyotimetia/openapi-go-mcp.svg)](https://pkg.go.dev/github.com/dipjyotimetia/openapi-go-mcp)
[![Go Report Card](https://goreportcard.com/badge/github.com/dipjyotimetia/openapi-go-mcp)](https://goreportcard.com/report/github.com/dipjyotimetia/openapi-go-mcp)
[![Release](https://img.shields.io/github/v/release/dipjyotimetia/openapi-go-mcp)](https://github.com/dipjyotimetia/openapi-go-mcp/releases/latest)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

Generate a [Model Context Protocol (MCP)](https://modelcontextprotocol.io) server in Go from any OpenAPI 3.x or Swagger 2.0 specification. Every operation in the spec becomes an MCP tool that an LLM client (Claude, Cursor, VS Code, ...) can call.

`openapi-go-mcp` is the OpenAPI counterpart to [`redpanda-data/protoc-gen-go-mcp`](https://github.com/redpanda-data/protoc-gen-go-mcp).

## Contents

- [Features](#features)
- [Install](#install)
- [Quick start](#quick-start)
- [CLI reference](#cli-reference)
- [Guides](#guides)
- [Examples](#examples)
- [Documentation](#documentation)
- [Contributing](#contributing)
- [License](#license)

## Features

- **OpenAPI 3.0, 3.1 and Swagger 2.0 input.** Swagger 2.0 is converted automatically via [kin-openapi](https://github.com/getkin/kin-openapi).
- **Three ways to serve a spec:**
  - **Proxy mode** generates a runnable Go module that calls the upstream API directly, with auth taken from environment variables.
  - **Companion mode** generates a `*.mcp.go` file that delegates to your [`oapi-codegen`](https://github.com/oapi-codegen/oapi-codegen) typed client.
  - **Dynamic mode** (`pkg/dynamic`) registers tools from a spec at process startup, with no code generation.
- **Works with either MCP library.** Generated code targets a small `MCPServer` interface, with adapters for the official [`modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk) and [`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go). Switching is a one-line import change.
- **Tool schemas built from the spec.** Path, query, header and body parameters are grouped into one JSON Schema, with `$defs` for shared components. Recursive schemas are handled.
- **OpenAI compatibility mode.** `-openai-compat` emits a flattened, `$ref`-free schema that OpenAI's strict tool-call validator accepts.
- **Batch generation.** `-spec` accepts a directory, glob or comma-separated list. Each spec is written to its own package.
- **Curated exposure.** Use `x-mcp` to include or exclude operations, and `x-mcp-tool-name` to give a tool a stable name that doesn't depend on its `operationId`.
- **Deterministic output.** Iteration is sorted and output is gofmt'd, so regenerating gives reviewable diffs.

## Install

Every channel ships the same binary.

**Homebrew** (macOS, Linux)

```bash
brew install dipjyotimetia/tap/openapi-go-mcp
```

**Go** (1.26+)

```bash
go install github.com/dipjyotimetia/openapi-go-mcp/cmd/openapi-go-mcp@latest
```

**Container image** (linux/amd64, linux/arm64)

```bash
docker run --rm -v "$PWD":/workspace ghcr.io/dipjyotimetia/openapi-go-mcp:latest \
    -spec /workspace/petstore.yaml -out /workspace/mcp -package petmcp \
    -client-import github.com/example/petstore
```

**Pre-built binaries.** Download the archive for your OS and architecture from the [latest release](https://github.com/dipjyotimetia/openapi-go-mcp/releases/latest) and put `openapi-go-mcp` on your `PATH`.

Check the install with `openapi-go-mcp -version`. Generated code imports this module's runtime, so it needs Go 1.26+.

## Quick start

| Mode | You get | Use it when |
|---|---|---|
| [Proxy](#proxy-mode) | A complete, runnable Go module | You want an MCP server for an API with no Go code of your own |
| [Companion](#companion-mode) | A `*.mcp.go` file for your existing module | The MCP layer is part of a larger service, or you already use `oapi-codegen` |
| [Dynamic](#dynamic-mode) | Tools registered at startup, nothing generated | The spec is only known at runtime, or you don't want a codegen step |

### Proxy mode

```bash
openapi-go-mcp \
    -mode=proxy \
    -spec petstore.yaml \
    -out gen/petstore-mcp \
    -module github.com/me/petstore-mcp

cd gen/petstore-mcp
go mod tidy
go build

# Credentials come from env vars derived from the spec's securitySchemes.
# The generated README.md lists the variable for each scheme.
BEARER_TOKEN_BEARERAUTH=xxx ./petstore-mcp   # serves MCP over stdio
```

The output is a Go module containing `main.go`, `go.mod`, `<pkg>/<pkg>.mcp.go` and `README.md`. The upstream base URL defaults to the spec's `servers[0].url`; set `API_BASE_URL` to override it. The server validates tool inputs against the spec's schemas, signs upstream requests with the configured credentials, and returns HTTP responses (including non-2xx status and headers) as tool results.

### Companion mode

```bash
# 1. Generate the typed HTTP client.
oapi-codegen -generate types,client -package pet -o gen/pet/pet.gen.go petstore.yaml

# 2. Generate the MCP companion.
openapi-go-mcp \
    -spec petstore.yaml \
    -out gen/petmcp \
    -package petmcp \
    -client-import github.com/me/myrepo/gen/pet
```

```go
package main

import (
    "context"
    "log"

    "github.com/modelcontextprotocol/go-sdk/mcp"

    "github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime/gosdk"
    "github.com/me/myrepo/gen/pet"
    "github.com/me/myrepo/gen/petmcp"
)

func main() {
    client, err := pet.NewClientWithResponses("https://api.example.com")
    if err != nil {
        log.Fatal(err)
    }
    raw, s := gosdk.NewServer("petstore-mcp", "1.0.0")
    petmcp.RegisterSwaggerPetstoreClient(s, client)
    if err := raw.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
        log.Fatal(err)
    }
}
```

You own `main.go` and the HTTP transport, so retries, tracing and mTLS configured on your `oapi-codegen` client apply to every tool call. Auth is up to your code.

### Dynamic mode

```go
raw, s := gosdk.NewServer("petstore-mcp", "1.0.0")
if err := dynamic.Register(ctx, s, "petstore.yaml", dynamic.Config{}); err != nil {
    log.Fatal(err)
}
_ = raw.Run(ctx, &mcp.StdioTransport{})
```

`dynamic.Register` loads the spec once at startup and uses the same request handling as proxy mode. The source must be a local path or an HTTPS URL; a remote spec also requires `Config.BaseURL`. Treat the source as trusted deployment configuration, not user input. See the [`dynamic` package docs](https://pkg.go.dev/github.com/dipjyotimetia/openapi-go-mcp/pkg/dynamic) for all options.

## CLI reference

```
openapi-go-mcp [flags]

  -spec PATH              OpenAPI 3.x / Swagger 2.0 source. Accepts:
                            • a single file path
                            • an http(s):// URL
                            • a directory (recursively walked, .yaml/.yml/.json)
                            • a glob pattern (filepath.Glob: *, ?, [...])
                            • a comma-separated list of any of the above
                          When the value matches multiple specs, batch mode
                          is activated: each spec gets its own <slug>mcp/
                          subdirectory under -out. (required)
  -out DIR                output directory (default ./mcp). In batch mode this
                          is the base directory; each spec lands in <out>/<slug>mcp/
  -package NAME           Go package name (default derived from spec title).
                          Rejected in batch mode — packages are auto-derived
                          from filename stems instead.
  -client-import PATH     import path of the oapi-codegen output package
                          (required in companion mode, except with -list /
                          -emit-v3). In batch mode this is treated as a base
                          path and the slug is appended (forward-slash join).
  -client-type NAME       client interface name (default ClientWithResponsesInterface)
  -mode MODE              emission mode: companion (default) generates a
                          *.mcp.go file that delegates to your oapi-codegen
                          client; proxy generates a runnable Go module that
                          calls the upstream API directly (no oapi-codegen)
  -module PATH            module path for the generated go.mod. Required iff
                          -mode=proxy; rejected otherwise. In batch mode it is
                          a base path and each spec's slug is appended.
  -sdk NAME               MCP SDK the proxy scaffold's main.go imports:
                          gosdk (default, modelcontextprotocol/go-sdk) or
                          mark3labs (mark3labs/mcp-go). Ignored in companion mode.
  -runtime-version VERSION version of openapi-go-mcp to pin in a proxy scaffold.
                          Released binaries infer it; unversioned development
                          builds require this flag (or a local runtime replace).
  -name-prefix PREFIX     static prefix added to every tool name
  -openai-compat          emit OpenAI-tool-compatible JSON Schema
  -prefer-content-type CT pick this content type for the request body when an
                          operation declares multiple (overrides the default
                          JSON → form → multipart → octet → text → xml priority)
  -exclude-by-default     invert x-mcp filtering: only operations explicitly
                          opted in with `x-mcp: true` are generated (default
                          is to generate every operation unless excluded with
                          `x-mcp: false`)
  -force                  overwrite the generated *.mcp.go file if it exists;
                          without this, an existing file is a fatal error
  -list                   print the operations found in the spec and exit
  -emit-v3 PATH           write the spec as OpenAPI 3 YAML to PATH (Swagger 2.0 conversion helper)
  -warnings-as-errors     exit non-zero when any warning-level diagnostic fires
  -version                print version information and exit
```

## Guides

### Choosing which operations become tools

Set `x-mcp: false` on an operation, a path item or the document root to leave it out of the generated tools. `x-mcp: true` opts it back in. The most specific level wins (operation > path > document > CLI default). Excluded operations are reported as info diagnostics; an invalid value such as `x-mcp: maybe` is a warning.

```yaml
paths:
  /admin:
    x-mcp: false             # exclude every operation under /admin …
    delete:
      operationId: purgeAll
    get:
      operationId: listAdmins
      x-mcp: true            # … except this one
```

For a large spec where only a few operations should be exposed, pass `-exclude-by-default`. Nothing is generated unless it has `x-mcp: true`.

### Stable tool names

Set `x-mcp-tool-name` on an operation when its MCP name must stay the same across OpenAPI refactors. Values must match `^[a-z_][a-z0-9_-]{0,63}$`. Invalid or duplicate names fail generation.

```yaml
get:
  operationId: GetEquityResearchReport
  x-mcp-tool-name: get_equity_research_report
```

### Generating from many specs at once

Point `-spec` at a directory, glob or comma-separated list. Each matching spec is generated into its own subdirectory of `-out`.

```bash
# Every spec under apis/, recursively
openapi-go-mcp -spec apis/ -out gen -client-import github.com/acme/apis/gen -force

# Glob (filepath.Glob syntax; ** is not supported, use a directory for recursion)
openapi-go-mcp -spec 'apis/*.yaml' -out gen -client-import github.com/acme/apis/gen

# Mixed inputs
openapi-go-mcp -spec 'core/,extras/audit.yaml' -out gen -client-import example.com/g
```

Each spec's slug comes from its filename (`billing-api.yaml` → `billingapi`), and output goes to `<out>/<slug>mcp/<slug>mcp.mcp.go`. `-client-import` is treated as a base path with the slug appended, so `github.com/acme/apis/gen` becomes `github.com/acme/apis/gen/billing` for `billing.yaml`.

A failing spec doesn't stop the run. Every error is reported at the end and the process exits with code `3`. Slug collisions (for example `v1/api.yaml` and `v2/api.yaml`) are detected before any file is written. See [usage pattern 12](docs/usage-patterns.md#pattern-12--batch-generation-across-many-specs) for a full walkthrough.

### Swagger 2.0 with companion mode

`oapi-codegen` does not accept Swagger 2.0, so convert the spec first:

```bash
openapi-go-mcp -spec petstore-v2.json -emit-v3 petstore-v3.yaml
oapi-codegen -generate types,client -package pet -o gen/pet/pet.gen.go petstore-v3.yaml
openapi-go-mcp -spec petstore-v3.yaml -out gen/petmcp -package petmcp -client-import ...
```

`-emit-v3` also removes non-JSON response content types, which works around an oapi-codegen v2.7.0 issue with responses that declare several content types.

### Choosing an MCP library

The same generated code works with either library. Only the runtime adapter import changes:

| Library | Adapter | Server construction |
|---|---|---|
| [`modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk) (official) | `pkg/runtime/gosdk` | `raw, s := gosdk.NewServer(name, version)` |
| [`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go) | `pkg/runtime/mark3labs` | `raw, s := mark3labs.NewServer(name, version)` |

In proxy mode, choose with `-sdk gosdk` or `-sdk mark3labs`.

### Tool input schema

Each tool's input schema groups parameters by location:

```json
{
  "type": "object",
  "properties": {
    "path":   { "type": "object", "properties": { "petId": { "type": "integer" } }, "required": ["petId"] },
    "query":  { "type": "object", "properties": { "limit": { "type": "integer" } } },
    "header": { "type": "object", "properties": { "X-Trace-Id": { "type": "string" } } },
    "body":   { "$ref": "#/$defs/NewPet" }
  },
  "required": ["path", "body"],
  "$defs": { "NewPet": { ... } }
}
```

Empty groups are omitted. Shared schemas are copied into each tool's own `$defs`, so every tool schema is self-contained.

With `-openai-compat`, schemas are inlined (no `$ref`), `oneOf`/`anyOf`/`allOf` are flattened, and every object gets `additionalProperties: false`.

### Runtime options

Pass options when registering tools:

```go
import "github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime"

// Prefix every tool name, e.g. when registering the same API twice.
petmcp.RegisterSwaggerPetstoreClient(s, client, runtime.WithNamePrefix("staging"))

// Add a per-call property (e.g. a tenant ID) to every tool's input.
petmcp.RegisterSwaggerPetstoreClient(s, client, runtime.WithExtraProperties(
    runtime.ExtraProperty{Name: "tenant", Description: "Tenant ID", Required: true},
))
```

| Option | Effect |
|---|---|
| `WithNamePrefix` | Prepends `<prefix>_` to every tool name |
| `WithExtraProperties` | Adds properties to every tool schema and puts their values on the handler context |
| `WithHTTPClient` / `WithMTLSHTTPClient` | Supplies the HTTP client (or an mTLS client) for upstream calls |
| `WithRequestTimeout` | Sets a deadline for each tool call |
| `WithMaxResponseBytes` | Limits upstream response size (16 MiB by default in proxy and dynamic modes) |
| `WithServerVariables` | Fills in OpenAPI server URL template variables |
| `WithRequestAuthProvider` | Signs requests for OpenID Connect or custom auth schemes |
| `WithAllowInsecureAuth` | Allows credentials over plain HTTP (local development only) |

## Examples

| Directory | What it shows |
|---|---|
| [`examples/todos`](examples/todos) | **Start here.** A standalone HTTP backend plus an MCP proxy in front of it, with [client configs](examples/todos/README.md) for Claude Desktop, Claude Code, Cursor, VS Code and MCP Inspector |
| [`examples/petstore`](examples/petstore) | OpenAPI 3.0, JSON bodies, `go-sdk` backend |
| [`examples/petstore-mark3labs`](examples/petstore-mark3labs) | The same spec on the `mark3labs/mcp-go` backend |
| [`examples/swagger2-petstore`](examples/swagger2-petstore) | Swagger 2.0 input via `-emit-v3` |
| [`examples/library`](examples/library) | Swagger 2.0 end to end (load, convert, generate) |
| [`examples/users-api`](examples/users-api) | UUID path params, required headers, PUT / PATCH / DELETE |
| [`examples/complex`](examples/complex) | Recursive `$ref`, `oneOf` / `allOf`, enums, `date-time` / `uuid` formats |
| [`examples/non-json-bodies`](examples/non-json-bodies) | Form-urlencoded, multipart (base64 file fields), octet-stream, text/plain, XML |

## Documentation

- [Usage patterns](docs/usage-patterns.md): deployment recipes (stdio, remote HTTP, multi-tenant, auth, aggregation, batch)
- [Design decisions](docs/design-decisions.md): the non-obvious choices and why they were made
- [Architecture](docs/architecture.md): packages, pipeline and extension points
- [Changelog](docs/changelog.md)
- [API reference](https://pkg.go.dev/github.com/dipjyotimetia/openapi-go-mcp) on pkg.go.dev

## Contributing

Contributions are welcome. [docs/contributing.md](docs/contributing.md) covers dev setup, tests and code style. The short version:

```bash
make build   # build the CLI into ./bin
make test    # run the test suite
make lint    # run golangci-lint
```

Please read the [code of conduct](docs/code-of-conduct.md). Report security issues privately as described in the [security policy](docs/security.md), not in public issues.

## License

Apache License 2.0. See [LICENSE](LICENSE).

The project follows [Semantic Versioning](https://semver.org/) from v1.0.0.

Portions of `pkg/runtime` and `pkg/generator/naming.go` are adapted from [redpanda-data/protoc-gen-go-mcp](https://github.com/redpanda-data/protoc-gen-go-mcp) under the same license.
