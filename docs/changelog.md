# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html) starting with the v1.0.0 release.

## [Unreleased]

### Added

- **Multi-spec batch generation** — the `-spec` flag now accepts directories (walked recursively, filtered to `.yaml`/`.yml`/`.json`), glob patterns (stdlib `filepath.Glob` syntax: `*`, `?`, `[...]`), and comma-separated combinations of files / globs / directories. Single-file and URL inputs are unchanged. New package `pkg/batch` derives per-spec `PackageName` / `OutDir` / `ClientImport` from each matched spec's filename stem (`-out gen -spec apis/` writes `gen/billingmcp/billingmcp.mcp.go` etc.). `-client-import` is treated as a base path and the slug is appended with `path.Join` so generated import lines use forward slashes on every OS. Per-spec failures are accumulated and reported at end, exit code rolls up to `3` (`exitGenerate`); single-spec mode keeps its previous fail-fast behaviour and exit codes. Slug collisions (e.g. `v1/api.yaml` and `v2/api.yaml`) are reported with all source paths before any file is written. The flags `-package` and `-emit-v3` are rejected in batch mode (`exitUsage=1`) because they would produce ambiguous or overwriting output. `-list` is supported and groups operations by spec with `=== <path> ===` headers.
- **`pkg/loader.ExpandSpecArg`** — new exported helper for callers that want to resolve a `-spec`-style argument into a deterministic, sorted, deduplicated list of `loader.SpecRef` values (file paths or `http(s)://` URLs). Empty matches are surfaced as errors quoting the offending entry.
- **`generator.MCPPackageSuffix`** — exported constant (`"mcp"`) so `pkg/batch` and any future caller can derive consistent package names without re-encoding the convention.
- **`x-mcp` extension filtering** — spec authors can now mark individual operations, path-items, or the whole document with an `x-mcp: false` extension to exclude operations from MCP tool generation, or `x-mcp: true` to opt back in. Precedence is operation > path-item > document > generator default. Excluded operations emit an `excluded-by-x-mcp` info diagnostic; unrecognised values (e.g. `x-mcp: "maybe"`) emit an `invalid-x-mcp-value` warning and fall through to the next level so typos surface loudly. Boolean and string forms are both accepted (`true`/`false`/`"true"`/`"false"`), and `json.RawMessage` is handled for forward-compatibility with legacy kin-openapi versions.
- **`-exclude-by-default` CLI flag** / **`Options.ExcludeByDefault`** — inverts the document-wide fallback: when set, only operations explicitly opted in with `x-mcp: true` are generated. Useful for large specs where the author wants to publish a small curated subset as MCP tools without rewriting the rest of the document.
- **`-force` CLI flag** / **`Options.Force`** — required to overwrite an existing `*.mcp.go` output file. Without it, an already-present file is a fatal error (exit code 3) rather than a silent overwrite, so accidental re-runs over hand-edited or committed output are caught. A directory at the output path is rejected even with `-force` — removing it would be more destructive than overwriting a file. The repository's own `make regen-examples` target now passes `-force` via the new `GEN_FLAGS` variable.

### Added — proxy mode

- **`-mode=proxy` emission mode** — new first-class output: a runnable Go module (`main.go` + `go.mod` + `<pkg>/<pkg>.mcp.go` + `README.md`) that proxies MCP tool calls directly to the upstream HTTP API. No `oapi-codegen` step needed; the generated handlers build `*http.Request` objects via `http.NewRequestWithContext` and dispatch through `cfg.HTTPClient.Do`. Companion mode remains the default and is byte-for-byte unchanged (the golden test still passes).
- **`-module <import-path>` CLI flag** — required iff `-mode=proxy`. Becomes the `module` directive in the generated `go.mod`. In batch mode it's treated as a base path; each spec's slug is appended.
- **`-sdk={gosdk|mark3labs}` CLI flag** — picks which MCP SDK adapter the generated `main.go` imports. Defaults to `gosdk` (the official `modelcontextprotocol/go-sdk`). Ignored in companion mode.
- **`Options.Mode`, `Options.ModulePath`, `Options.SDK`** — library-level equivalents of the new flags.
- **Built-in authentication from `securitySchemes`** — proxy mode reads `components.securitySchemes` and emits one `applyAuth<Scheme>` helper per supported scheme. Credentials are read from environment variables at startup: `API_KEY_<NAME>` for `apiKey` schemes (any `in`: header / query / cookie), `BEARER_TOKEN_<NAME>` for `http` + `bearer`, `BASIC_AUTH_USERNAME_<NAME>` / `BASIC_AUTH_PASSWORD_<NAME>` for `http` + `basic`, `OAUTH2_ACCESS_TOKEN_<NAME>` for `oauth2` (treated as a pre-acquired Bearer; no token-exchange flow). Unsupported schemes (`openIdConnect`, `http` + `digest`, etc.) surface as `unsupported-security-scheme` warnings and are dropped from auth wiring rather than aborting the build.
- **`runtime.MissingCredentialError`** — typed error surfaced through the MCP `tools/call` response when a required env var is unset. The error names both the scheme and the env var so the user gets an actionable message instead of a silent upstream 401.
- **`runtime.ApplyAPIKey`, `runtime.ApplyBearer`, `runtime.ApplyBasic`** — public helpers used by the generated `applyAuth<Scheme>` functions; reusable from user code if needed.
- **`runtime.DecodeProxyParam`, `runtime.BuildProxyURL`, `runtime.EncodeJSONBody`, `runtime.EncodeFormBody`, `runtime.ReadResponseBody`** — new helpers consumed by the proxy template (and available to anyone constructing their own proxies on top of the runtime).
- **`generator.ParseSecuritySchemes` / `ResolveOperationSecurity`** — exported helpers that lower an OpenAPI 3 document's security model into the generator's `[]SecurityScheme` representation. Useful for tooling that wants to inspect a spec's auth surface without generating code.
- **E2E coverage** — `tests/e2e/cli_proxy_test.go` builds the generated scaffold, runs it as an MCP stdio server, and verifies (a) `initialize` returns a result; (b) a Bearer token in `BEARER_TOKEN_<NAME>` reaches the upstream `Authorization` header; (c) a missing required credential surfaces in the MCP error response with the env-var name.

### Fixed

- **Runtime options are now applied by generated handlers.** Companion and proxy handlers remove `WithExtraProperties` fields from arguments, place present values on `context.Context` via `ExtraProperty.ContextKey`, and apply `WithRequestTimeout` per tool call. Proxy handlers also expand `WithServerVariables` before building upstream URLs.
- **Proxy URL construction preserves base query strings correctly.** A server URL such as `https://api.example.com/v2?tenant=acme` now becomes `https://api.example.com/v2/<operation>?tenant=acme&...` instead of appending the operation path inside the query value.
- **Proxy path parameters use path-segment escaping.** Generated proxy code now escapes path placeholders with `url.PathEscape` semantics so spaces become `%20` rather than query-form `+`.
- **Generated path-parameter locals are collision-safe.** Distinct path parameter names that sanitise to the same Go identifier (`foo-bar` and `foo_bar`) now get deterministic suffixed local variable names instead of duplicate declarations.
- **`pkg/batch.Slug` rejects digit-leading stems.** A filename like `999.yaml` or `2024-api.yaml` previously slugged to `999`/`2024api` and produced a package name (`999mcp`) that fails to compile because Go identifiers cannot start with a digit. `Slug` now returns a clear error pointing at the offending filename so the user can rename it up front, matching the behaviour for the already-rejected empty-stem case.
- **`pkg/loader.ExpandSpecArg` no longer splits commas inside URL tokens.** Matrix parameters and OData `$select=a,b` values are common in real URLs; the previous split would yield bogus "matched no files" errors. The split heuristic is now "comma followed by whitespace separates entries"; a bare comma inside an `http(s)://` token is kept verbatim. A new unit test pins the behaviour.
- **CLI log prefixes prefer relative paths.** Batch-mode `=== <path> ===` headers and diagnostic prefixes now render the spec path relative to the working directory when that form is shorter and doesn't escape the tree, falling back to the absolute path otherwise. Underlying loader behaviour is unchanged — only the surface text is shorter.

### Added — robustness pass (Phase 1–4)

- **`runtime.NewToolResultFromHTTP(status, header, body, fallbackContentType)`** — canonical wrapper that preserves the upstream HTTP status code and a curated allowlist of response headers (`Location`, `ETag`, `Last-Modified`, `Cache-Control`, `Content-Type`, `Content-Disposition`, `Content-Language`, `Retry-After`, `WWW-Authenticate`, `Link`, plus up to 32 `X-*` headers) on `*CallToolResult`. JSON bodies surface as `StructuredContent` (unchanged shape); `text/*` bodies surface as `{"contentType","text"}`; binary bodies surface as `{"contentType","base64"}`; 204 / empty bodies surface as success with no `StructuredContent`; non-2xx responses become `IsError=true` with a `{"status","headers","body"}` envelope. Generated handlers now call this helper for every operation, so MCP clients can distinguish 201 + `Location` from 200, 304 from 200, etc.
- **`CallToolResult.StatusCode` + `CallToolResult.Headers`** — additive struct fields. Zero values when not from an HTTP round-trip; existing handlers that build the struct directly continue to compile.
- **Cookie parameter support** — `in: cookie` parameters are now first-class: exposed in the tool's input schema under a `cookie` group, decoded via `runtime.DecodeCookieParam`, and forwarded to the upstream client through a new `runtime.CookieRequestEditor` that satisfies oapi-codegen's `RequestEditorFn`.
- **`runtime.WithHTTPClient`, `runtime.WithRequestTimeout`, `runtime.WithServerVariables`** — option groundwork user code can call from `Register*` opts to customise the upstream HTTP client, set a per-tool-call deadline, and supply substitutions for OpenAPI server-URL templates.
- **`runtime.SubstituteServerVariables(template, vars)`** — helper to expand `{name}` placeholders in OpenAPI server URLs at runtime.
- **`runtime.DecodeArguments`** — shared argument decoder used by both adapters so the gosdk and mark3labs paths surface identical error semantics on malformed input.
- **`runtime.BuildHTTPMeta` + `runtime.HTTPMetaKey`** — adapters serialise the new HTTP status + headers onto the underlying SDK's `_meta` channel (go-sdk `Meta`, mark3labs `Meta.AdditionalFields`) under `openapi-go-mcp/http`, so MCP clients can read both regardless of which SDK is wired up.
- **`ExtraProperty.Type`** — extra-property declarations may now request `number`, `integer`, or `boolean` shapes in addition to the previous string-only path. Unknown types fall back to string rather than emit invalid schema.
- **Structured generator diagnostics** — `generator.CollectOperations` and `generator.Generate` now return `[]Diagnostic` alongside the error. Stable `Code` values (`dropped-callback`, `unsupported-parameter-style`, `shadowed-parameter`, `dropped-server-variables`, `dropped-security-requirement`, `content-type-header-override`, …) make findings machine-readable; the legacy `Options.Warnings` stream is preserved.
- **`-warnings-as-errors` CLI flag** — exit code `4` when any warning-level diagnostic fires, useful for failing CI on spec regressions.
- **Distinct CLI exit codes** — `0` ok / `1` usage / `2` bad input / `3` generation failure / `4` warnings-as-errors. CI pipelines can branch on the code.
- **URL-based spec loading** — `loader.Load` now dispatches `http(s)://` paths to `loader.LoadFromURL(ctx, url, opts...)`, which enforces a 32 MiB body cap and a 30 s timeout (both `URLLoadOption`-configurable). The CLI's `-spec` flag accepts URLs without any extra ceremony.
- **`loader.URLLoadOption` knobs** — `WithHTTPClient`, `WithMaxBodySize`, `WithTimeout` for custom transports / proxies / mTLS / auth headers.
- **Collision detection at codegen time** — `Render` rejects `ClientImport` paths whose base segment collides with Go reserved words or with packages the generated file already imports (`context`, `json`, `runtime`), and refuses operations whose `ToolName`s mangle to the same const identifier (`get-pet` vs `get_pet` would have silently produced duplicate decls).
- **CWD-robust e2e tests** — `tests/e2e/cli_test.go` now walks up looking for `go.mod` instead of assuming a fixed depth.
- **`make smoke-all`** — exercises both the gosdk and mark3labs backends so adapter parity is verified at the protocol layer.

### Changed — robustness pass (Phase 1–4)

- **`runtime.HandleError` JSON-marshal fallback is no longer recursive** — when the inner payload fails to encode, the helper synthesises a fixed-shape error string with `fmt.Sprintf` and returns; previously the fallback could re-enter the encoder.
- **`runtime.ToolError`** gained a `Cause error` field and an `Unwrap()` method, so callers can `errors.Is`/`errors.As` to inspect the underlying parse/decode failure. All call sites in `pkg/runtime/http.go` were updated to populate it.
- **Adapter parity** — `pkg/runtime/gosdk` and `pkg/runtime/mark3labs` route through the shared `DecodeArguments` helper, so the same MCP `arguments` payload yields the same `*runtime.CallToolResult` regardless of which backend is loaded. The gosdk adapter no longer surfaces malformed-input parses as protocol errors; both now produce an `IsError` tool result the LLM can self-correct from.
- **`generator.CollectOperations` signature** — now returns `([]Operation, []Diagnostic, error)`. Internal callers (`Render`, `Generate`) plumb the diagnostics through; the CLI prints them grouped by severity. The `Options.Warnings` `io.Writer` continues to receive a free-form line per finding for backwards compatibility.
- **`generator.Generate` signature** — now returns `([]Diagnostic, error)`.
- **Generated handlers** — every operation's success path now reads `resp.HTTPResponse.Header` via a small `headerOf(*http.Response)` helper emitted into the file and routes through `runtime.NewToolResultFromHTTP`. Out-of-tree code that pattern-matched on the previous `runtime.NewToolResultJSON(resp.Body)`/`NewToolResultBinary`/`NewToolResultText` lines must rerun the generator.
- **`callArgs` template helper** — returns `(string, error)` instead of panicking on an unhandled body kind; `Render` now surfaces the failure cleanly.
- **Loader file-read errors include the absolute path + CWD** so relative-path confusion is debuggable from a single line.
- **CLAUDE.md** — Go-version reference updated to match `go.mod` (1.26.x) and the current CI matrix.

### Earlier (Phase 0)

- **`examples/todos` split into two binaries** — the example is now `examples/todos/server` (a real `net/http` service with `-addr` flag, request log middleware, `/healthz`, and graceful `SIGINT`/`SIGTERM` shutdown) and `examples/todos/mcp` (an MCP proxy that connects via HTTP). The MCP proxy reads `TODOS_BASE_URL` (default `http://localhost:8080`), pings `/healthz` once at startup with a 2 s timeout, and logs a non-fatal warning if unreachable. The previous in-process `httptest.Server` bundling is removed in favour of running the two halves separately, matching how the pattern is actually deployed. The README documents the two-terminal workflow and updated MCP-host configs.

## [0.1.1] — 2026-05-16

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
- **`Dockerfile` multi-platform COPY** — `dockers_v2` stages binaries under `<os>/<arch>/<binary>` in the build context, but the Dockerfile did a flat `COPY openapi-go-mcp …` and the release docker build failed with `"/openapi-go-mcp": not found`. The Dockerfile now declares `ARG TARGETOS` / `ARG TARGETARCH` (auto-populated by BuildKit) and copies from `${TARGETOS}/${TARGETARCH}/openapi-go-mcp`. Verified locally with `docker buildx build --platform linux/amd64,linux/arm64` against a reproduced goreleaser staging layout.

## [0.1.0] — 2026-05-16

Initial public release.

### Added

- **CLI `openapi-go-mcp`** — reads OpenAPI 3.0 / 3.1 / Swagger 2.0 specs and generates a `*.mcp.go` file per spec. Each operation becomes an MCP tool whose handler forwards to an `oapi-codegen` `ClientWithResponsesInterface`.
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
- **Distribution channels** — pre-built binaries on every tagged release (darwin/linux/windows × amd64/arm64), Homebrew formula via `dipjyotimetia/homebrew-tap`, and a multi-arch container image at `ghcr.io/dipjyotimetia/openapi-go-mcp`. Driven by `.goreleaser.yml`; release workflow runs on `v*` tag push.

### Tests

- Golden test for generator output (`pkg/generator/golden_test.go`); refresh via `UPDATE_GOLDEN=1`.
- End-to-end OpenAI-compat invariants test running across the petstore and non-JSON-bodies fixtures (`pkg/generator/openai_compat_test.go`).
- Loader unit tests including `TestPruneNonJSONContent_KeepsRequestBodiesPrunesResponses` and `TestLoad_Swagger2_FormDataConverts`.
- Runtime unit tests for every body builder (`BuildMultipartBody` covers form fields, file fields, multiple-files deterministic ordering, non-string file rejection, missing body; `BuildBase64BytesBody` / `BuildStringBody` cover the happy + wrong-type + missing paths).
- Stdio end-to-end tests across 5 fixtures (`tests/e2e`):
  - **Petstore v3** — basic JSON body and primitive path/query (gosdk + mark3labs adapter parity).
  - **Users API v3** — UUID path params, multi-path params, required headers, PUT / PATCH / DELETE, no-param operations, bad-UUID error path.
  - **Library Swagger 2.0** — full v2 → v3 → oapi-codegen → MCP pipeline.
  - **Complex Schemas** — recursive `$ref` in `$defs`, oneOf, allOf, enums, date-time/uuid formats, nullable.
  - **Non-JSON bodies** — form / multipart (with file part byte-level verification) / octet (base64 round-trip) / text/plain / XML.
- CLI integration tests (`tests/e2e/cli_test.go`): build sanity, `-list`, missing-flag exit, `-emit-v3` round-trip with response-only pruning, generated-file structural invariants.

### Known limitations

- Only `application/json` response bodies are decoded into structured JSON; non-JSON responses are surfaced as raw bytes via `NewToolResultJSON` (the `Text` field is populated, but `StructuredContent` may be malformed for non-JSON payloads).
- Multipart binary-field rewrite covers only top-level properties; nested binary leaves are not detected.
- Multipart `encoding[field]` metadata (per-part content-type, custom headers, style) is ignored — every file part is sent as `application/octet-stream`.
- A spec header parameter named `Content-Type` is silently overridden by oapi-codegen's `<Op>WithBodyWithResponse` for non-JSON request bodies.
- Streaming responses (SSE, chunked) surface as raw bytes; no first-class streaming support yet.
- No dynamic (no-codegen, reflection-based) registration path yet. Tracked in `TODO.md`.
- `discriminator` is dropped during schema conversion — JSON Schema has no direct equivalent.

[Unreleased]: https://github.com/dipjyotimetia/openapi-go-mcp/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/dipjyotimetia/openapi-go-mcp/releases/tag/v0.1.1
[0.1.0]: https://github.com/dipjyotimetia/openapi-go-mcp/releases/tag/v0.1.0
