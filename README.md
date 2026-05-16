# openapi-gen-go-mcp

[![CI](https://github.com/dipjyotimetia/openapi-gen-go-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/dipjyotimetia/openapi-gen-go-mcp/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/dipjyotimetia/openapi-gen-go-mcp.svg)](https://pkg.go.dev/github.com/dipjyotimetia/openapi-gen-go-mcp)
[![Go Report Card](https://goreportcard.com/badge/github.com/dipjyotimetia/openapi-gen-go-mcp)](https://goreportcard.com/report/github.com/dipjyotimetia/openapi-gen-go-mcp)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

Generate a [Model Context Protocol (MCP)](https://modelcontextprotocol.io) server in Go from any OpenAPI 3.x or Swagger 2.0 specification. Each operation in the spec becomes an MCP tool; tool calls are forwarded to an [`oapi-codegen`](https://github.com/oapi-codegen/oapi-codegen) HTTP client.

`openapi-gen-go-mcp` is the OpenAPI counterpart to [`redpanda-data/protoc-gen-go-mcp`](https://github.com/redpanda-data/protoc-gen-go-mcp).

## Features

- **OpenAPI 3.0, 3.1, and Swagger 2.0 input** — Swagger 2.0 is auto-converted via [kin-openapi](https://github.com/getkin/kin-openapi).
- **Companion to oapi-codegen** — the generated `*.mcp.go` imports the user's `oapi-codegen` package and delegates HTTP work to its typed `ClientWithResponsesInterface`. Zero glue code.
- **MCP-library-agnostic** — runtime targets a thin `MCPServer` interface; ship adapters for the official [`modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk) and [`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go). Switch backends by changing one import.
- **Tool-call schema built from the spec** — path / query / header / body grouped into a single JSON Schema with `$defs` for shared components; recursion-safe.
- **OpenAI compatibility mode** — `-openai-compat` flag emits a flattened, `$ref`-free schema suitable for OpenAI's strict tool-call schema validator.
- **Runtime options** — `WithNamePrefix` (namespace tools when the same API is registered multiple times), `WithExtraProperties` (inject per-call context such as base URL or auth token).

## Install

Pick whichever fits your environment — every channel ships the same binary.

**Homebrew** (macOS, Linux)

```bash
brew install dipjyotimetia/tap/openapi-gen-go-mcp
```

**Pre-built binaries** — download the archive matching your OS / arch from the [latest release](https://github.com/dipjyotimetia/openapi-gen-go-mcp/releases/latest), unpack it, and put `openapi-gen-go-mcp` on your `PATH`.

**Container image** (linux/amd64 + linux/arm64)

```bash
docker pull ghcr.io/dipjyotimetia/openapi-gen-go-mcp:latest
docker run --rm -v "$PWD":/workspace ghcr.io/dipjyotimetia/openapi-gen-go-mcp:latest \
    -spec /workspace/petstore.yaml -out /workspace/mcp -package petmcp \
    -client-import github.com/example/petstore
```

**From source** (Go 1.26+)

```bash
go install github.com/dipjyotimetia/openapi-gen-go-mcp/cmd/openapi-gen-go-mcp@latest
# or pin to a specific release
go install github.com/dipjyotimetia/openapi-gen-go-mcp/cmd/openapi-gen-go-mcp@v0.1.0
```

Verify with `openapi-gen-go-mcp -version`. Generated code targets Go 1.23+.

## Quick start

```bash
# 1. Generate the oapi-codegen HTTP client for your OpenAPI spec.
oapi-codegen -generate types,client -package pet -o gen/pet/pet.gen.go petstore.yaml

# 2. Generate the MCP companion.
openapi-gen-go-mcp \
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
    "github.com/dipjyotimetia/openapi-gen-go-mcp/pkg/runtime/gosdk"
)

func main() {
    client, _ := pet.NewClientWithResponses("https://api.example.com")
    raw, s := gosdk.NewServer("petstore-mcp", "1.0.0")
    petmcp.RegisterSwaggerPetstoreClient(s, client)
    _ = raw.Run(context.Background(), &mcp.StdioTransport{})
}
```

## CLI

```
openapi-gen-go-mcp [flags]

  -spec PATH              OpenAPI 3.x or Swagger 2.0 file (required)
  -out DIR                output directory (default ./mcp)
  -package NAME           Go package name (default derived from spec title)
  -client-import PATH     import path of the oapi-codegen output package (required, except with -list / -emit-v3)
  -client-type NAME       client interface name (default ClientWithResponsesInterface)
  -name-prefix PREFIX     static prefix added to every tool name
  -openai-compat          emit OpenAI-tool-compatible JSON Schema
  -list                   print the operations found in the spec and exit
  -emit-v3 PATH           write the spec as OpenAPI 3 YAML to PATH (Swagger 2.0 conversion helper)
  -version                print version information and exit
```

## Swagger 2.0 workflow

`oapi-codegen` does not accept Swagger 2.0 input. Convert first:

```bash
openapi-gen-go-mcp -spec petstore-v2.json -emit-v3 petstore-v3.yaml
oapi-codegen -generate types,client -package pet -o gen/pet/pet.gen.go petstore-v3.yaml
openapi-gen-go-mcp -spec petstore-v3.yaml -out gen/petmcp -package petmcp -client-import ...
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
import "github.com/dipjyotimetia/openapi-gen-go-mcp/pkg/runtime"

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
