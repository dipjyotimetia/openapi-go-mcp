# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html) starting with the v1.0.0 release.

## [Unreleased]

### Added

- **`-prefer-content-type` flag** — when an operation declares multiple request content types, override the default JSON → form → multipart → octet → text → xml priority by naming the spec content type to use. Falls back to the priority order when the preferred type isn't declared on a given op.
- **OpenAPI `discriminator` description hint** — schemas that carry a `discriminator` (with optional `mapping`) now surface the property name and mapping keys in the description, so callers can pick the right branch without the source spec.
- **Multipart `encoding[field]` metadata** — per-part `Content-Type` overrides declared on a multipart body's `encoding` map are now propagated through `runtime.RequestFilePart` to each file part the runtime writes.
- **Nested multipart binary fields** — `format:binary` leaves inside nested objects are detected, rewritten to base64 strings, and extracted from the surrounding object at request time. The residual object (after extraction) is sent as a JSON form field; if extraction leaves it empty, the field is omitted.
- **Content-Type header parameter collision warning** — when a spec declares a `Content-Type` header parameter alongside a non-JSON body, the generator now writes a warning to `Options.Warnings` (defaults to stderr) noting the parameter will be overridden by the body's content type.
- **Non-JSON response decoding** — the generator now picks a wrapper per operation's primary 2xx response: `NewToolResultJSON` for JSON (unchanged), `NewToolResultText` for `text/*`, and a new `NewToolResultBinary([]byte, contentType string) *CallToolResult` for `application/octet-stream`, `application/xml`, and other raw responses. Binary bodies are base64-encoded into `Text` and surfaced as `{"contentType","base64"}` in `StructuredContent`.
- **CI `examples` job** — runs `make regen-examples` with pinned oapi-codegen v2.7.0 on every push, then `git diff --exit-code` so an unsynced generator change fails CI before merge.
- **`examples/todos` end-to-end example** — fully self-contained demo: the binary starts an in-memory HTTP backend (`backend.go`), points an oapi-codegen client at it, and serves the generated MCP layer over stdio. Covers GET/POST/PUT/DELETE with path params, query params, and JSON request bodies in one `go run ./examples/todos`. `TODOS_BASE_URL` overrides the embedded backend. Ships a dedicated `examples/todos/README.md` with copy-pasteable MCP client configs for Claude Desktop, Claude Code, Cursor, VS Code, and MCP Inspector.

### Changed

- **`runtime.BuildMultipartBody` signature** — second argument is now `[]runtime.RequestFilePart` instead of `[]string`. Generated code is updated automatically; out-of-tree callers must migrate. (Pre-1.0; no compatibility shim.)
- **`generator.CollectOperations` signature** — now takes `Options` instead of a `bool openAICompat`, threading `PreferContentType` and `Warnings` through alongside the dialect flag.
- **GoReleaser config migrated** — `dockers` + `docker_manifests` → single `dockers_v2` multi-platform block; `brews` → `homebrew_casks` (the v2 successor for CLI tools). Local `goreleaser check` is now deprecation-warning-free.
- **`oapi-codegen` configs in `examples/*/gen/*/oapi.yaml`** — output paths now name the destination explicitly (`examples/.../gen/foo/foo.gen.go`) so `make regen-examples` works regardless of cwd quirks. Previously some configs wrote `foo.gen.go` to the repo root.

### Fixed

- `make regen-examples` is now idempotent: a second run produces no diff. Previously the oapi-codegen step landed some `*.gen.go` files at the repo root because the `output:` field was a bare filename interpreted relative to cwd.
- **`Dockerfile` multi-platform COPY** — `dockers_v2` stages binaries under `<os>/<arch>/<binary>` in the build context, but the Dockerfile did a flat `COPY openapi-gen-go-mcp …` and the release docker build failed with `"/openapi-gen-go-mcp": not found`. The Dockerfile now declares `ARG TARGETOS` / `ARG TARGETARCH` (auto-populated by BuildKit) and copies from `${TARGETOS}/${TARGETARCH}/openapi-gen-go-mcp`. Verified locally with `docker buildx build --platform linux/amd64,linux/arm64` against a reproduced goreleaser staging layout.

## [0.1.0] — 2026-05-16

Initial public release.

### Added

- **CLI `openapi-gen-go-mcp`** — reads OpenAPI 3.0 / 3.1 / Swagger 2.0 specs and generates a `*.mcp.go` file per spec. Each operation becomes an MCP tool whose handler forwards to an `oapi-codegen` `ClientWithResponsesInterface`.
- **Request body kinds** — `application/json`, `application/x-www-form-urlencoded`, `multipart/form-data` (binary fields accepted as base64), `application/octet-stream`, `text/*`, and `application/xml` + raw-string fallback for any other content type. When an operation declares multiple, the generator picks deterministically (JSON → form → multipart → octet → text → xml → first). Three new runtime helpers — `BuildMultipartBody`, `BuildBase64BytesBody`, `BuildStringBody` — handle the encoding.
- **Format-aware Go types for path parameters** — `format: uuid` / `email` / `date` produce typed wrappers (`openapi_types.UUID`, `openapi_types.Email`, `openapi_types.Date`); `format: date-time` produces `time.Time`. Required extra imports are emitted automatically.
- **`pkg/loader`** — spec ingestion with auto-conversion of Swagger 2.0 via `kin-openapi/openapi2conv`. Exports `Load`, `WriteV3YAMLJSONOnly`, `IsJSONContentType`.
- **`pkg/generator`** — operation walk, JSON-Schema conversion (draft-07 compatible, recursion-safe via `$defs`), `text/template` driven Go-source emission with gofmt post-pass.
- **`pkg/runtime`** — MCP-library-agnostic types (`MCPServer`, `Tool`, `CallToolRequest`, `CallToolResult`), JSON decode helpers (`DecodePathParam`, `DecodeBody`, `DecodeParamsCombined`), functional options (`WithNamePrefix`, `WithExtraProperties`).
- **`pkg/runtime/gosdk`** — adapter for the official `modelcontextprotocol/go-sdk`.
- **`pkg/runtime/mark3labs`** — adapter for `mark3labs/mcp-go`. Generated code is unchanged when switching between the two.
- **`-openai-compat` flag** — emits OpenAI-tool-compatible JSON Schema (no `$ref`, no `oneOf`/`anyOf`/`allOf`, `additionalProperties:false` on every object).
- **`-emit-v3` flag** — converts a Swagger 2.0 spec to OpenAPI 3 YAML, pruning non-JSON content types from response bodies. Works around an oapi-codegen v2.7.0 quirk with responses exposed under multiple content types. Request bodies are preserved so downstream oapi-codegen emits the matching Formdata / Multipart / WithBody helpers.
- **`-list` flag** — print operations in the spec and exit.
- **`-version` flag** — print build metadata (GoReleaser-injected `version` / `commit` / `date`, falling back to `runtime/debug.BuildInfo`).
- **Examples** — `petstore` (go-sdk), `petstore-mark3labs`, `swagger2-petstore`, `users-api`, `library` (v2 → v3 end-to-end), `complex` (recursive `$ref` / oneOf / allOf / enums / formats), `non-json-bodies` (every non-JSON request kind).
- **Distribution channels** — pre-built binaries on every tagged release (darwin/linux/windows × amd64/arm64), Homebrew formula via `dipjyotimetia/homebrew-tap`, and a multi-arch container image at `ghcr.io/dipjyotimetia/openapi-gen-go-mcp`. Driven by `.goreleaser.yml`; release workflow runs on `v*` tag push.

### Tests

- Golden test for generator output (`pkg/generator/golden_test.go`); refresh via `UPDATE_GOLDEN=1`.
- End-to-end OpenAI-compat invariants test running across the petstore and non-JSON-bodies fixtures (`pkg/generator/openai_compat_test.go`).
- Loader unit tests including `TestPruneNonJSONContent_KeepsRequestBodiesPrunesResponses` and `TestLoad_Swagger2_FormDataConverts`.
- Runtime unit tests for every body builder (`BuildMultipartBody` covers form fields, file fields, multiple-files deterministic ordering, non-string file rejection, missing body; `BuildBase64BytesBody` / `BuildStringBody` cover the happy + wrong-type + missing paths).
- Stdio end-to-end tests across 5 fixtures (`internal/e2e`):
  - **Petstore v3** — basic JSON body and primitive path/query (gosdk + mark3labs adapter parity).
  - **Users API v3** — UUID path params, multi-path params, required headers, PUT / PATCH / DELETE, no-param operations, bad-UUID error path.
  - **Library Swagger 2.0** — full v2 → v3 → oapi-codegen → MCP pipeline.
  - **Complex Schemas** — recursive `$ref` in `$defs`, oneOf, allOf, enums, date-time/uuid formats, nullable.
  - **Non-JSON bodies** — form / multipart (with file part byte-level verification) / octet (base64 round-trip) / text/plain / XML.
- CLI integration tests (`internal/e2e/cli_test.go`): build sanity, `-list`, missing-flag exit, `-emit-v3` round-trip with response-only pruning, generated-file structural invariants.

### Known limitations

- Only `application/json` response bodies are decoded into structured JSON; non-JSON responses are surfaced as raw bytes via `NewToolResultJSON` (the `Text` field is populated, but `StructuredContent` may be malformed for non-JSON payloads).
- Multipart binary-field rewrite covers only top-level properties; nested binary leaves are not detected.
- Multipart `encoding[field]` metadata (per-part content-type, custom headers, style) is ignored — every file part is sent as `application/octet-stream`.
- A spec header parameter named `Content-Type` is silently overridden by oapi-codegen's `<Op>WithBodyWithResponse` for non-JSON request bodies.
- Streaming responses (SSE, chunked) surface as raw bytes; no first-class streaming support yet.
- No dynamic (no-codegen, reflection-based) registration path yet. Tracked in `TODO.md`.
- `discriminator` is dropped during schema conversion — JSON Schema has no direct equivalent.

[Unreleased]: https://github.com/dipjyotimetia/openapi-gen-go-mcp/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/dipjyotimetia/openapi-gen-go-mcp/releases/tag/v0.1.0
