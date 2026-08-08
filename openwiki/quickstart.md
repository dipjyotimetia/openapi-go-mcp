---
type: Guide
title: Getting started with openapi-go-mcp
description: Generate an MCP server layer from OpenAPI 3.x or Swagger 2.0 using companion mode for existing Go services or proxy mode for a standalone server.
tags: [getting-started, cli, code-generation, mcp]
openwiki:
  roles: [workflow, repository]
  source_paths: [cmd/openapi-go-mcp/main.go, pkg/generator/generator.go]
  validation_commands: [go build ./cmd/openapi-go-mcp]
---

# Getting started

`openapi-go-mcp` turns each included operation in an OpenAPI 3.x or Swagger 2.0 document into an MCP tool. It supports two output models:

| Mode | Use when | Output | HTTP and authentication |
|---|---|---|---|
| Companion (default) | MCP is part of an existing Go application | One `<package>.mcp.go` file | Your `oapi-codegen` typed client and application own transport and auth |
| Proxy (`-mode=proxy`) | You want a runnable MCP server from a spec | A Go module with `main.go`, `go.mod`, `README.md`, and generated package | Generated handlers issue HTTP requests; proxy scaffold derives auth configuration from the spec |

## Prerequisites

- Go 1.26 or newer to build this repository and its runtime module.
- An OpenAPI 3.0/3.1 or Swagger 2.0 specification.
- For companion mode, `oapi-codegen` and a generated client package exposing `ClientWithResponsesInterface` (or the interface named with `-client-type`).

Install the CLI from source:

```bash
go install github.com/dipjyotimetia/openapi-go-mcp/cmd/openapi-go-mcp@latest
```

## Companion mode

First generate the typed client, then generate its MCP companion:

```bash
oapi-codegen -generate types,client -package pet -o gen/pet/pet.gen.go petstore.yaml

openapi-go-mcp \
  -spec petstore.yaml \
  -out gen/petmcp \
  -package petmcp \
  -client-import github.com/me/myrepo/gen/pet
```

In your program, create the typed client, choose an MCP adapter, and register the generated tools. The generated package targets `runtime.MCPServer`, so choosing the supported Go SDK adapter is independent of code generation.

```go
client, _ := pet.NewClientWithResponses("https://api.example.com")
raw, server := gosdk.NewServer("petstore-mcp", "1.0.0")
petmcp.RegisterSwaggerPetstoreClient(server, client)
_ = raw.Run(context.Background(), &mcp.StdioTransport{})
```

Companion mode requires `-client-import`; it intentionally does not generate `main.go` or configure credentials.

## Proxy mode

Generate a standalone module without an `oapi-codegen` step:

```bash
openapi-go-mcp \
  -mode=proxy \
  -spec petstore.yaml \
  -out gen/petstore-mcp \
  -module github.com/me/petstore-mcp

cd gen/petstore-mcp
go mod tidy
go build
./petstore-mcp
```

`-module` is required in proxy mode. Use `-sdk=mark3labs` to scaffold the `mark3labs/mcp-go` adapter instead of the default `gosdk` adapter. The generated README identifies environment variables derived from declared security schemes. The first OpenAPI server URL is the upstream default; `API_BASE_URL` can override it.

## Inspect before generating

List operations without writing output:

```bash
openapi-go-mcp -spec petstore.yaml -list
```

Convert Swagger 2.0 to v3 YAML for a downstream tool such as `oapi-codegen`:

```bash
openapi-go-mcp -spec petstore-v2.json -emit-v3 petstore-v3.yaml
```

`-emit-v3` is a single-spec operation and cannot be combined with proxy mode.

## Common controls

- `-force` permits replacing an existing generated `*.mcp.go` file; without it, generation fails rather than overwriting output.
- `-name-prefix` adds a static prefix to generated tool names.
- `-openai-compat` emits the constrained schema form needed by OpenAI strict tool validation.
- `-prefer-content-type` overrides request-body content-type selection for operations with several body media types.
- `-warnings-as-errors` makes warning diagnostics fail the invocation.

See [generation workflow](workflows/generation.md) for multi-spec input and inclusion rules, and [runtime model](architecture/runtime.md) for tool invocation behavior.
