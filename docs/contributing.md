# Contributing to openapi-go-mcp

Thanks for your interest in the project. This guide covers the dev loop, where to add features, and what to verify before opening a pull request.

## Dev setup

```bash
git clone https://github.com/dipjyotimetia/openapi-go-mcp
cd openapi-go-mcp
go mod download
```

Requirements:

- Go 1.23 or newer
- `oapi-codegen` (for regenerating example clients): `go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest`

## Common tasks

### Run the test suite

```bash
go test ./... -race
```

The golden generator test (`pkg/generator/golden_test.go`) compares output to `testdata/golden/petstore.mcp.go.golden`. When the generator legitimately changes output, refresh the golden:

```bash
UPDATE_GOLDEN=1 go test ./pkg/generator/...
```

Review the diff against `git status` before committing.

### Lint

```bash
go vet ./...
golangci-lint run                # installs from .golangci.yml
```

### Build the CLI

```bash
go build -o bin/openapi-go-mcp ./cmd/openapi-go-mcp
```

### Regenerate the example outputs

```bash
# Petstore v3 (go-sdk + mark3labs share the same gen output)
oapi-codegen -config examples/petstore/gen/pet/oapi.yaml \
    examples/petstore/petstore.yaml
go run ./cmd/openapi-go-mcp \
    -spec examples/petstore/petstore.yaml \
    -out examples/petstore/gen/petmcp \
    -package petmcp \
    -client-import github.com/dipjyotimetia/openapi-go-mcp/examples/petstore/gen/pet

# Swagger 2.0 — convert first, then run oapi-codegen
go run ./cmd/openapi-go-mcp \
    -spec testdata/petstore-v2.json \
    -emit-v3 examples/swagger2-petstore/petstore-v3.yaml
oapi-codegen -config examples/swagger2-petstore/gen/pet/oapi.yaml \
    examples/swagger2-petstore/petstore-v3.yaml
go run ./cmd/openapi-go-mcp \
    -spec examples/swagger2-petstore/petstore-v3.yaml \
    -out examples/swagger2-petstore/gen/petmcp \
    -package petmcp \
    -client-import github.com/dipjyotimetia/openapi-go-mcp/examples/swagger2-petstore/gen/pet
```

### Smoke-test an example as an MCP server

```bash
(printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoketest","version":"0.0.1"}}}'
 sleep 1
 printf '%s\n' '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}'
 printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
 sleep 2) | go run ./examples/petstore 2>/dev/null | head
```

## Project layout

See [architecture.md](architecture.md) for the package map, data-flow, and extension points.

## Coding conventions

- **Idiomatic Go**: short receivers; no stutter (`generator.Generator` is wrong); errors wrapped with `%w`; `errors.Is` / `errors.As` for inspection.
- **Documentation**: every exported identifier has a Go doc comment that starts with the identifier name.
- **Comments**: explain **why**, not **what**. Don't narrate the change or reference the PR.
- **Tests**: prefer table-driven tests for converters and parsers. Always add a regression test for a bug fix.
- **Imports**: standard library first, third party, then this module's packages. `goimports` sorts these automatically.
- **No dead code**: if a field is set but never read, remove it.

## Submitting a pull request

1. Open an issue first for non-trivial changes — easier to align on direction than to redo a PR.
2. Branch from `main`. Keep the PR focused; small PRs land faster.
3. Make sure the following all pass locally:
   - `go test ./... -race`
   - `go vet ./...`
   - `golangci-lint run`
   - The smoke test for whichever example your change touches.
4. Update [changelog.md](changelog.md) under the `## Unreleased` heading.
5. Update relevant docs (README, architecture.md) if you changed behaviour or added a feature.

## Adding a new MCP backend

1. Create `pkg/runtime/<libname>/server.go`.
2. Implement `NewServer(name, version string, opts...) (*<RawServerType>, runtime.MCPServer)` and `Wrap(raw *<RawServerType>) runtime.MCPServer`.
3. The adapter's `AddTool` must translate `runtime.Tool` and `runtime.ToolHandler` into the target library's idiom.
4. Add an example under `examples/<libname>-petstore` that reuses the existing generator output.
5. Smoke-test as above.

## Adding a new schema-mode flag

1. Add a field on `generator.Options`.
2. Thread it through `Render` → `CollectOperations` → `NewSchemaConverter`.
3. Implement the transformation in `pkg/generator/schema.go` and, if it affects the envelope (`root`, `path`/`query`/`header` groups), in `buildInputSchema` in `pkg/generator/operation.go`.
4. Add a test alongside `TestRender_OpenAICompat_PetstoreSchemas` that asserts the new invariants on the rendered output.

## Reporting bugs / requesting features

Use the GitHub issue templates. Bug reports should include the spec (or a minimised excerpt), the command you ran, the expected behaviour, and the observed behaviour.

## Code of conduct

This project follows the [Contributor Covenant](code-of-conduct.md).

## License

By contributing, you agree that your contributions are licensed under the Apache License 2.0.
