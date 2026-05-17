# openapi-go-mcp

[![CI](https://github.com/dipjyotimetia/openapi-go-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/dipjyotimetia/openapi-go-mcp/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/dipjyotimetia/openapi-go-mcp.svg)](https://pkg.go.dev/github.com/dipjyotimetia/openapi-go-mcp)
[![Go Report Card](https://goreportcard.com/badge/github.com/dipjyotimetia/openapi-go-mcp)](https://goreportcard.com/report/github.com/dipjyotimetia/openapi-go-mcp)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

Generate a [Model Context Protocol (MCP)](https://modelcontextprotocol.io) server in Go from any OpenAPI 3.x or Swagger 2.0 specification. Each operation in the spec becomes an MCP tool; tool calls are forwarded to an [`oapi-codegen`](https://github.com/oapi-codegen/oapi-codegen) HTTP client.

`openapi-go-mcp` is the OpenAPI counterpart to [`redpanda-data/protoc-gen-go-mcp`](https://github.com/redpanda-data/protoc-gen-go-mcp).

## Features

- **OpenAPI 3.0, 3.1, and Swagger 2.0 input** — Swagger 2.0 is auto-converted via [kin-openapi](https://github.com/getkin/kin-openapi).
- **Companion to oapi-codegen** — the generated `*.mcp.go` imports the user's `oapi-codegen` package and delegates HTTP work to its typed `ClientWithResponsesInterface`. Zero glue code.
- **MCP-library-agnostic** — runtime targets a thin `MCPServer` interface; ship adapters for the official [`modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk) and [`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go). Switch backends by changing one import.
- **Tool-call schema built from the spec** — path / query / header / body grouped into a single JSON Schema with `$defs` for shared components; recursion-safe.
- **OpenAI compatibility mode** — `-openai-compat` flag emits a flattened, `$ref`-free schema suitable for OpenAI's strict tool-call schema validator.
- **Multi-spec batch generation** — `-spec` accepts a directory (recursive walk), glob pattern, or comma-separated list of any of those. Each matched spec is rendered into its own `<slug>mcp/` subdirectory under `-out`, with `PackageName` and `ClientImport` auto-derived from the filename stem.
- **Curated exposure with `x-mcp`** — spec authors can opt operations, path-items, or the whole document in or out of MCP tool generation; pair with `-exclude-by-default` for opt-in-only specs.
- **Runtime options** — `WithNamePrefix` (namespace tools when the same API is registered multiple times), `WithExtraProperties` (inject per-call context such as base URL or auth token).

## Install

Pick whichever fits your environment — every channel ships the same binary.

**Homebrew** (macOS, Linux)

```bash
brew install dipjyotimetia/tap/openapi-go-mcp
```

**Pre-built binaries** — download the archive matching your OS / arch from the [latest release](https://github.com/dipjyotimetia/openapi-go-mcp/releases/latest), unpack it, and put `openapi-go-mcp` on your `PATH`.

**Container image** (linux/amd64 + linux/arm64)

```bash
docker pull ghcr.io/dipjyotimetia/openapi-go-mcp:latest
docker run --rm -v "$PWD":/workspace ghcr.io/dipjyotimetia/openapi-go-mcp:latest \
    -spec /workspace/petstore.yaml -out /workspace/mcp -package petmcp \
    -client-import github.com/example/petstore
```

**From source** (Go 1.26+)

```bash
go install github.com/dipjyotimetia/openapi-go-mcp/cmd/openapi-go-mcp@latest
# or pin to a specific release
go install github.com/dipjyotimetia/openapi-go-mcp/cmd/openapi-go-mcp@v0.1.0
```

Verify with `openapi-go-mcp -version`. Generated code targets Go 1.23+.

## Quick start

Two emission modes. Pick **proxy** for a turnkey server you can `go build && ./server`, or **companion** to embed the MCP layer in an existing module alongside your own `oapi-codegen` clients.

### Proxy mode (zero-boilerplate runnable server)

```bash
# One command. Produces a complete Go module: main.go + go.mod + <pkg>/<pkg>.mcp.go + README.md.
openapi-go-mcp \
    -mode=proxy \
    -spec petstore.yaml \
    -out gen/petstore-mcp \
    -module github.com/me/petstore-mcp

cd gen/petstore-mcp
go mod tidy
go build

# Auth credentials come from environment variables derived from the spec's
# securitySchemes. The generated README.md lists the env var per scheme.
BEARER_TOKEN_BEARERAUTH=xxx ./petstore-mcp        # serves MCP on stdio
```

The spec's `servers[0].url` is the default upstream base URL; override with `API_BASE_URL`. The generated server validates inputs against the JSON Schema derived from the spec, signs the upstream request with credentials from env vars, and surfaces HTTP responses (including non-2xx status + headers) as MCP tool results.

### Companion mode (embed into an existing Go service)

```bash
# 1. Generate the oapi-codegen HTTP client for your OpenAPI spec.
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

    "github.com/modelcontextprotocol/go-sdk/mcp"

    "github.com/me/myrepo/gen/pet"
    "github.com/me/myrepo/gen/petmcp"
    "github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime/gosdk"
)

func main() {
    client, _ := pet.NewClientWithResponses("https://api.example.com")
    raw, s := gosdk.NewServer("petstore-mcp", "1.0.0")
    petmcp.RegisterSwaggerPetstoreClient(s, client)
    _ = raw.Run(context.Background(), &mcp.StdioTransport{})
}
```

You write `main.go`, you own the HTTP transport (custom retries, tracing, mTLS, etc. flow naturally through your `oapi-codegen` client), and auth is your code's responsibility. Use this mode when the MCP layer is one feature of a larger service binary.

## CLI

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
                          (required, except with -list / -emit-v3). In batch
                          mode this is treated as a base path and the slug
                          is appended (forward-slash join).
  -client-type NAME       client interface name (default ClientWithResponsesInterface)
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

### Filtering operations with `x-mcp`

Tag any operation, path-item, or the document root with `x-mcp: false` to keep
it out of the generated tool list; `x-mcp: true` opts it back in. The most
specific level wins (operation > path > document > CLI default). Excluded
operations show up as info diagnostics; typos like `x-mcp: maybe` become
warnings so they don't slip past review.

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

Pair with `-exclude-by-default` when only a small curated subset of a large
spec should be exposed: nothing is generated unless `x-mcp: true` appears
explicitly.

### Generating from many specs in one invocation

Point `-spec` at a directory, glob, or comma-separated list — every match is
rendered into its own subdirectory under `-out`:

```bash
# Recursive directory: every spec under apis/ becomes its own tool set
openapi-go-mcp \
    -spec apis/ \
    -out gen \
    -client-import github.com/acme/apis/gen \
    -force

# Glob (filepath.Glob syntax — no ** in v1; use a directory for recursion)
openapi-go-mcp -spec 'apis/*.yaml' -out gen -client-import github.com/acme/apis/gen

# Multiple folders / mixed inputs, comma-separated
openapi-go-mcp -spec 'core/,extras/audit.yaml' -out gen -client-import example.com/g
```

For each matched spec the generator derives a slug from the filename stem
(`billing-api.yaml → billingapi`) and writes
`<out>/<slug>mcp/<slug>mcp.mcp.go`. The `-client-import` value is treated as
a base path; the slug is appended with forward slashes, so
`github.com/acme/apis/gen` on `billing.yaml` becomes
`github.com/acme/apis/gen/billing` in the generated import line.

Failures don't stop the run: each spec is processed independently, every
error is reported at end, and the process exits with code `3` if any spec
failed. Slug collisions (e.g. `v1/api.yaml` and `v2/api.yaml` both → `api`)
are caught up front before any file is written.

See [`docs/usage-patterns.md`](docs/usage-patterns.md) Pattern 12 for the
full walkthrough.

## Swagger 2.0 workflow

`oapi-codegen` does not accept Swagger 2.0 input. Convert first:

```bash
openapi-go-mcp -spec petstore-v2.json -emit-v3 petstore-v3.yaml
oapi-codegen -generate types,client -package pet -o gen/pet/pet.gen.go petstore-v3.yaml
openapi-go-mcp -spec petstore-v3.yaml -out gen/petmcp -package petmcp -client-import ...
```

`-emit-v3` also prunes non-JSON content types from responses, which works around a known issue in oapi-codegen v2.7.0 with responses exposed under multiple content types.

## Choosing an MCP backend

The same generated `*.mcp.go` works with either backend — change only the runtime adapter import:

| Backend | Adapter | Server construction |
|---|---|---|
| [`modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk) (official) | `pkg/runtime/gosdk` | `raw, s := gosdk.NewServer(name, version)` |
| [`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go) | `pkg/runtime/mark3labs` | `raw, s := mark3labs.NewServer(name, version)` |

See [`examples/petstore`](examples/petstore) and [`examples/petstore-mark3labs`](examples/petstore-mark3labs).

## Examples

| Directory | What it demonstrates |
|---|---|
| [`examples/todos`](examples/todos) | **Canonical end-to-end demo** — two real binaries: `todos-server` (standalone HTTP backend with graceful shutdown + request logging + `/healthz`) and `todos-mcp` (MCP proxy that forwards every tool call over HTTP). Ships a [README](examples/todos/README.md) with MCP client configs for Claude Desktop, Claude Code, Cursor, VS Code, and Inspector. |
| [`examples/petstore`](examples/petstore) | OpenAPI 3.0, JSON bodies, `go-sdk` backend |
| [`examples/petstore-mark3labs`](examples/petstore-mark3labs) | Same spec on the `mark3labs/mcp-go` backend |
| [`examples/swagger2-petstore`](examples/swagger2-petstore) | Swagger 2.0 input via `-emit-v3` conversion |
| [`examples/users-api`](examples/users-api) | UUID path params, required headers, PUT / PATCH / DELETE |
| [`examples/library`](examples/library) | Swagger 2.0 end-to-end (load → convert → generate) |
| [`examples/complex`](examples/complex) | Recursive `$ref`, oneOf / allOf, enums, date-time / uuid formats |
| [`examples/non-json-bodies`](examples/non-json-bodies) | Form-urlencoded, multipart (with base64 file fields), octet-stream, text/plain, XML |

## Tool input schema shape

Each operation's MCP tool input schema groups parameters by location:

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

Empty groups are omitted. Shared schemas referenced through `$ref` are hoisted into a per-operation `$defs` to keep each tool's schema self-contained.

In `-openai-compat` mode the schema is inlined (no `$ref`), composition keywords (`oneOf`/`anyOf`/`allOf`) are flattened, and every object carries `additionalProperties: false`.

## Runtime options

```go
import "github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime"

// Prefix every tool name — useful when registering the same API twice.
petmcp.RegisterSwaggerPetstoreClient(s, client, runtime.WithNamePrefix("staging"))

// Inject per-call context properties (e.g. a tenant token) into every tool.
petmcp.RegisterSwaggerPetstoreClient(s, client, runtime.WithExtraProperties(
    runtime.ExtraProperty{Name: "tenant", Description: "Tenant ID", Required: true},
))
```

## Status

Pre-1.0. APIs may change between minor versions. Apache 2.0 licensed.

## Documentation

All docs live in [`docs/`](docs/). Start there:

- [Usage patterns](docs/usage-patterns.md) — deployment recipes (stdio, remote, multi-tenant, auth, aggregation, ...)
- [Design decisions](docs/design-decisions.md) — the non-obvious choices and why they exist
- [Architecture](docs/architecture.md) — packages, pipeline, extension points
- [Contributing](docs/contributing.md) — dev setup, testing, code style
- [Changelog](docs/changelog.md)
- [Security policy](docs/security.md)
- [Code of conduct](docs/code-of-conduct.md)

## License

Apache License 2.0 — see [LICENSE](LICENSE).

Portions of `pkg/runtime` and `pkg/generator/naming.go` are adapted from [redpanda-data/protoc-gen-go-mcp](https://github.com/redpanda-data/protoc-gen-go-mcp) under the same license.
