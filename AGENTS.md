# AGENTS.md

This file provides guidance to Codex (Codex.ai/code) when working with code in this repository.

## What this project is

`openapi-go-mcp` is a CLI code generator that consumes an OpenAPI 3.x or Swagger 2.0 spec and emits a Go file (`*.mcp.go`) which registers every operation in the spec as an MCP (Model Context Protocol) tool. Generated code delegates HTTP work to a user-supplied `oapi-codegen` typed client. It is the OpenAPI counterpart to `protoc-gen-go-mcp`.

The generator does not own the HTTP client — the user runs `oapi-codegen` to produce the typed client, then runs this tool to produce the MCP companion alongside it.

## Commands

Standard dev loop (see `Makefile` for full list):

```bash
make build           # builds CLI into ./bin/openapi-go-mcp
make test            # go test ./...
make test-race       # go test ./... -race -count=1
make vet             # go vet ./...
make lint            # golangci-lint run (config in .golangci.yml)
make fmt             # gofmt -s -w .
make regen-examples  # regenerates every example's oapi-codegen + mcp output (requires `oapi-codegen` on PATH)
make smoke           # boots petstore example over stdio, calls initialize + tools/list
```

Run a single test:

```bash
go test ./pkg/generator/ -run TestRender_OpenAICompat_PetstoreSchemas -v
```

Refresh the generator golden file when output legitimately changes (then review the diff before committing):

```bash
UPDATE_GOLDEN=1 go test ./pkg/generator/...
```

End-to-end tests live in `tests/e2e/`. They exercise the generated example servers via the MCP stdio protocol; running them requires the example clients to already be generated (`make regen-examples` if you've changed the generator).

CI runs on Go 1.26.x (matching the `go 1.26` directive in `go.mod`). Generated code itself only relies on standard-library features that have been available since Go 1.23, so downstream consumers can still compile the output against 1.23+.

## Architecture

Detailed package layout, data flow diagrams, and extension points are in `docs/architecture.md` — read it before making non-trivial changes to the generator or runtime. Deployment recipes are in `docs/usage-patterns.md`; the rationale behind non-obvious choices (companion codegen, grouped input schema, per-operation `$defs`, default-vs-OpenAI-strict schema dialect, JSON-only bodies) is in `docs/design-decisions.md` — consult it before changing those defaults. The short version:

```
cmd/openapi-go-mcp/  CLI entry point + batch orchestration loop
pkg/loader/              Spec ingestion: OpenAPI 3.x direct, Swagger 2.0 via openapi2conv; ExpandSpecArg for glob/dir/comma input
pkg/batch/               Per-spec option derivation (slug → PackageName/OutDir/ClientImport), collision detection
pkg/generator/           Operation collection, JSON Schema conversion, text/template → gofmt
pkg/runtime/             MCPServer interface + decoders + ApplyConfig (library-agnostic)
pkg/runtime/gosdk/       Adapter for modelcontextprotocol/go-sdk
pkg/runtime/mark3labs/   Adapter for mark3labs/mcp-go
examples/                One end-to-end demo per backend / per spec dialect
testdata/                Spec fixtures + golden generator output
tests/e2e/               Black-box tests that drive the example servers over stdio; CLI integration tests
```

Two decoupling boundaries do most of the architectural work:

1. **`runtime.MCPServer` interface** — generated code only calls `AddTool(Tool, ToolHandler)`. The choice of MCP library is a one-line import swap (`gosdk.NewServer` ↔ `mark3labs.NewServer`); no regeneration. To add a new backend: create `pkg/runtime/<libname>/` exporting `NewServer` and `Wrap`. The generator never changes.

2. **Per-operation `SchemaConverter`** — each operation gets its own converter so each tool's `$defs` are self-contained. A `nameByPtr` map is shared across converters within one `CollectOperations` call to avoid O(P·S) rebuild cost.

The generator pipeline is: `loader.Load` → `generator.CollectOperations` (walks paths × methods, sorted) → `generator.Render` (text/template + gofmt) → writes `<out>/<pkg>.mcp.go`. Determinism (sorted iteration, gofmt, golden test) is a hard requirement — reviews depend on it.

**Batch mode** sits in front of this pipeline rather than changing it. `loader.ExpandSpecArg` resolves the `-spec` value into a sorted, deduplicated list of `SpecRef`s; `batch.PlanFor` derives per-spec `generator.Options` from each filename stem (`PackageName=<slug>mcp`, `OutDir=<out>/<slug>mcp`, `ClientImport=<base>/<slug>` joined with forward slashes); `batch.DetectCollisions` aborts before any write if two specs share a slug. The single-spec pipeline then runs once per plan. Per-spec failures don't stop the run — they're accumulated and the process exits with `exitGenerate` (`3`) at the end so CI sees every failing spec in one run. `-package` and `-emit-v3` are rejected in batch mode (`exitUsage`); `-list` groups output under `=== <path> ===` headers per spec.

### Generated handler shape

For every operation the generator emits an `AddTool` call wrapping a closure that:
1. Decodes path/query/header/body args via `runtime.DecodeBody`, `DecodePathParam`, etc.
2. Calls the typed `<Op>WithResponse(ctx, ...)` method on the `oapi-codegen` client.
3. Returns the response body via `runtime.NewToolResultJSON`.

Argument order to the typed client follows oapi-codegen's deterministic convention: `ctx`, positional path params, `*<Op>Params` (only when query/header params exist), typed body (only when a request body exists), `reqEditors...`.

### Schema modes

Default mode produces draft-07-compatible JSON Schema with `$defs` for shared components. `-openai-compat` (the `OpenAICompat` field on `generator.Options`) produces a flattened, `$ref`-free schema: `oneOf`/`anyOf` collapse to the first branch, `allOf` is shallow-merged, every object gets `additionalProperties: false`. When adding a new schema-mode flag, thread it through `Render` → `CollectOperations` → `NewSchemaConverter`, and if it affects envelope objects (`root`, `path`/`query`/`header` groups) also update `buildInputSchema` in `pkg/generator/operation.go`.

### Spec ingestion

`loader.Load` detects Swagger 2.0 by the top-level `swagger: 2.0` and converts via `kin-openapi/openapi2conv`. All loaded specs pass `openapi3.Validate`. External `$ref`s resolve against the spec file's directory. `WriteV3YAMLJSONOnly` (the `-emit-v3` flag) prunes non-JSON content types on a deep clone to work around a known oapi-codegen v2.7.0 issue with responses exposed under multiple content types — the original document is not mutated.

Request bodies support `application/json`, `application/x-www-form-urlencoded`, `multipart/form-data`, `application/octet-stream`, `text/*`, and any other content type as a raw-string / base64 fallback. When an operation declares multiple, the generator picks deterministically in that priority order. Only `application/json` response bodies are decoded; non-JSON responses are surfaced as raw bytes.

## Conventions

- Lint config (`.golangci.yml`) enables `errcheck`, `govet`, `staticcheck`, `revive`, `gocritic`, `gosec`, etc. `gocritic`'s `ifElseChain` is intentionally disabled. `goimports` `local-prefixes` is set to this module's path — import groups are stdlib, third-party, then this module.
- The golden test (`pkg/generator/golden_test.go`) guards generator output format. Any generator change must either preserve the golden or update it with `UPDATE_GOLDEN=1` and a reviewed diff.
- Update `docs/changelog.md` under `## Unreleased` for user-visible changes.
- The runtime package has a `Tool`/`MCPServer`/`Config` shape adapted from `redpanda-data/protoc-gen-go-mcp` (Apache-2.0) — keep the attribution comment intact when editing those files.
