# Architecture

This document describes the structure of `openapi-go-mcp` — its packages, the data flow from OpenAPI spec to generated Go source, and the extension points where new MCP backends or schema dialects can be added.

## Goals

1. **Two ergonomics tiers for two audiences.** *Companion mode* (default) emits a single `*.mcp.go` alongside the user's `oapi-codegen` output for teams embedding MCP in an existing service binary. *Proxy mode* (`-mode=proxy`) emits a full runnable Go module — `main.go` + `go.mod` + `<pkg>/<pkg>.mcp.go` + `README.md` — so a user with just a spec gets `go build && ./server` out of the box.
2. **Library-agnostic runtime**: generated code targets a small interface (`runtime.MCPServer`) so the choice of MCP server library is a build-time import swap, not a regeneration. The `-sdk` flag picks which adapter the proxy scaffold imports.
3. **Spec-input flexibility**: OpenAPI 3.0, 3.1, and Swagger 2.0 specs are accepted via a single loader. Swagger 2.0 is converted in-process; users never write a separate conversion step.
4. **Deterministic, reviewable output**: generated source is gofmt-clean, operation order is sorted, and a golden test guards companion-mode output byte-for-byte. The companion golden file is the regression net — any accidental change to a shared helper that touches its bytes fails CI.

## Package layout

```
cmd/openapi-go-mcp/       # CLI entry point + batch orchestration loop
pkg/loader/               # OpenAPI 3.x + Swagger 2.0 ingestion; ExpandSpecArg
pkg/batch/                # Per-spec option derivation, slug rules, collision detection
pkg/generator/            # Operation collection, schema conversion, Go source emission
pkg/generator/security.go # Spec securitySchemes → SecurityScheme; env-var derivation (proxy mode)
pkg/generator/scaffold.go # main.go + go.mod + README emission (proxy mode)
pkg/runtime/              # MCP-library-agnostic types (MCPServer, Tool, helpers)
pkg/runtime/auth.go       # ApplyAPIKey / ApplyBearer / ApplyBasic + MissingCredentialError
pkg/runtime/proxy.go      # DecodeProxyParam / BuildProxyURL / EncodeJSON|FormBody (proxy mode)
pkg/runtime/gosdk/        # Adapter for modelcontextprotocol/go-sdk
pkg/runtime/mark3labs/    # Adapter for mark3labs/mcp-go
examples/                 # End-to-end demos (one per MCP backend, one for Swagger 2.0)
tests/e2e/                # Black-box CLI + stdio MCP integration tests
testdata/                 # Spec fixtures + golden generator output
```

## Emission modes

The generator runs in one of two modes, selected by `-mode` / `Options.Mode`:

- **`companion`** (default, byte-for-byte stable, golden-test-guarded). Emits a single `<pkg>.mcp.go` file. The user supplies an `oapi-codegen` typed client, writes `main.go`, and wires authentication themselves. Use this mode when the MCP layer is one feature of a larger service binary.
- **`proxy`** (`-mode=proxy`). Emits a runnable Go module — `main.go` + `go.mod` + `<pkg>/<pkg>.mcp.go` + `README.md`. Handlers construct `*http.Request` objects directly via the runtime helpers and dispatch through `cfg.HTTPClient.Do`. Authentication is wired automatically from the spec's `components.securitySchemes` using environment variables (see [`usage-patterns.md`](usage-patterns.md#pattern-13--standalone-proxy-server-zero-boilerplate)). No `oapi-codegen` step needed.

Both modes share schema conversion, parameter decoding, response wrapping, `x-mcp` filtering, batch orchestration, and the MCP-library adapters. Only request construction and the auth helpers diverge — companion mode delegates to the typed client; proxy mode walks the operation and builds the request inline.

## Data flow

The pipeline is conceptually single-spec — `loader.Load → CollectOperations →
Render → write` — and that pipeline runs unchanged whether the user supplied
one spec or one hundred. Multi-spec runs are an orchestration layer in front
of it: `ExpandSpecArg` resolves the `-spec` argument into a list of
`SpecRef`s, `batch.PlanFor` lifts each into a `(doc, generator.Options)`
pair, and the CLI loops the single-spec pipeline once per pair.

```
-spec value (file | URL | glob | directory | comma-list)
       │
       ▼
┌──────────────────────────────────────┐
│ loader.ExpandSpecArg                 │   Resolves -spec into []SpecRef:
│   isHTTPURL → URL passthrough        │   URL passthrough, filepath.Glob for
│   hasGlobMeta → filepath.Glob        │   *, ?, []; WalkDir for directories
│   IsDir → expandDir (recursive)      │   (.yaml/.yml/.json only, dot-files
│   else → single file                 │   skipped, symlinks not followed).
└──────────────────────────────────────┘   Sorted + deduplicated.
       │
       ▼  []SpecRef
┌──────────────────────────────────────┐
│ batch.PlanFor (per ref)              │   Single-spec mode: Opts pass through
│   slug = filename stem [a-z0-9]      │   verbatim. Batch mode derives:
│   PackageName  = slug + "mcp"        │     - OutDir per spec
│   OutDir       = out/<slug>mcp       │     - ClientImport (companion mode)
│   ClientImport = base/<slug>         │     - ModulePath   (proxy mode)
│   ModulePath   = base/<slug>         │   batch.DetectCollisions aborts
└──────────────────────────────────────┘   before any write if slugs collide.
       │
       ▼  []SpecPlan
┌──────────────────────────────────────┐
│ For each plan:                       │   Per-plan errors are captured and
│   loader.Load(plan.Ref.Path)         │   reported at end; in batch mode
│        │                             │   the loop keeps going so CI sees
│        ▼  *openapi3.T (validated)    │   every failing spec in one run.
│   renderWithOps:                     │   Exit code rolls up to
│     CollectOperations  ─┐            │   exitGenerate (3) when any plan
│     parseSecuritySchemes│ proxy only │   failed.
│     execute template    │            │
│        │                │            │
│        ▼ ([]byte, []Op) │            │
│   os.WriteFile(.mcp.go) │            │
│   ┌── mode == proxy? ───┘            │
│   │  WriteScaffold:                  │   Proxy mode also emits the
│   │    main.go    (chosen SDK)       │   wrapper module so the user gets
│   │    go.mod     (runtime + SDK)    │   `go build && ./binary` out of
│   │    README.md  (env-var table)    │   the box. Companion mode stops
│   └──────────────────────────────────┘   at the .mcp.go write.
```

`loader.Load` reads each individual file or URL exactly as before — detects Swagger 2.0 via top-level `swagger: 2.0` and converts via `kin-openapi/openapi2conv` when needed. `generator.CollectOperations` walks paths × methods in sorted order, building a per-operation `SchemaConverter` (the shared `nameByPtr` lookup map is rebuilt per plan because each spec has its own component pool — there is no cross-spec sharing in v1). The internal `renderWithOps` worker returns the rendered source *and* the `[]Operation` slice so the proxy-mode scaffold step can deduplicate the schemes referenced by the operations without re-walking the spec. The final byte stream is gofmt-clean.

## Runtime architecture (generated code → MCP server)

The MCP transport, adapter, and `runtime.MCPServer` interface are identical across both emission modes. What changes is the body of the generated handler closure:

```
                          ┌────────────────────┐
LLM client ──tools/call──▶│  MCP transport     │
                          │  (stdio, SSE, ...) │
                          └────────┬───────────┘
                                   │
                                   ▼
                          ┌────────────────────┐
                          │  go-sdk *Server  ─┐ │   thin adapter
                          │  OR              │ │   (pkg/runtime/<lib>/)
                          │  mark3labs Server│ │
                          └────────┬─────────┘─┘
                                   │ implements
                                   ▼
                          ┌────────────────────┐
                          │ runtime.MCPServer  │   AddTool(Tool, ToolHandler)
                          └────────┬───────────┘
                                   │
              ┌────────────────────┴────────────────────┐
              │                                         │
   ── companion mode ──                       ── proxy mode (-mode=proxy) ──
              │                                         │
              ▼                                         ▼
┌────────────────────────────┐         ┌─────────────────────────────────┐
│ generated handler closure  │         │ generated handler closure       │
│  decode args via runtime.* │         │  decode args via                │
│    DecodePathParam,        │         │    runtime.DecodeProxyParam     │
│    DecodeBody,             │         │    (stringifies path/query/     │
│    DecodeParamsCombined    │         │     header/cookie for the wire) │
│                            │         │                                 │
│  call typed oapi-codegen   │         │  build *http.Request directly   │
│    client method:          │         │    runtime.BuildProxyURL        │
│    c.<Op>WithResponse(...) │         │    runtime.EncodeJSON|FormBody  │
│                            │         │    runtime.BuildMultipartBody   │
│                            │         │                                 │
│  user wires auth via       │         │  apply auth from env vars:      │
│    request editor /        │         │    applyAuth<Scheme>(req) →     │
│    WithExtraProperties     │         │    runtime.ApplyAPIKey/Bearer/  │
│                            │         │    Basic + MissingCredential-   │
│                            │         │    Error on missing env var     │
│                            │         │                                 │
│                            │         │  send via cfg.HTTPClient.Do     │
│                            │         │                                 │
│  wrap response:            │         │  wrap response:                 │
│    NewToolResultFromHTTP(  │         │    NewToolResultFromHTTP(       │
│      resp.StatusCode(),    │         │      resp.StatusCode,           │
│      headerOf(...),        │         │      resp.Header,               │
│      resp.Body, ct)        │         │      ReadResponseBody, ct)      │
└────────────────────────────┘         └─────────────────────────────────┘
```

Both modes register tools via `runtime.MCPServer.AddTool`, so generated code is library-agnostic. Swapping `gosdk.NewServer(...)` for `mark3labs.NewServer(...)` is the only change required to switch backends — and in proxy mode the `-sdk` flag picks which one the emitted `main.go` imports.

## Schema conversion (`pkg/generator/schema.go`)

The converter turns kin-openapi `*openapi3.SchemaRef` values into JSON Schema (draft-07-compatible). Notable rules:

| OpenAPI input | JSON Schema output |
|---|---|
| `nullable: true` (3.0) | `type: ["<orig>", "null"]` |
| missing `type` with `properties` set | inferred as `type: object` |
| missing `type` with `items` set | inferred as `type: array` |
| `exclusiveMinimum: true` + `minimum: N` (3.0) | `exclusiveMinimum: N` |
| `exclusiveMinimum: N` (3.1) | `exclusiveMinimum: N` (passes through) |
| `example` | `examples: [<example>]` |
| Component `$ref` reached either via `Ref` string or by pointer | hoisted into per-tool `$defs`, recursion-safe |
| `xml`, `externalDocs`, `discriminator` | dropped (OpenAPI-only) |

Each MCP tool gets its own converter so the emitted `$defs` map is self-contained per tool. The `nameByPtr` lookup (component-name resolution from inlined pointers) is shared across converters within one `CollectOperations` call to avoid O(P · S) rebuild work.

### OpenAI compatibility mode (`-openai-compat`)

OpenAI's tool-call API does not support `$ref`, `oneOf`/`anyOf`/`allOf`, or open-ended objects. With `-openai-compat`:

- `Convert` always inlines — `$defs` stay empty, references are dereferenced bottom-up.
- `oneOf`/`anyOf` collapse to the first branch; `allOf` entries are shallow-merged.
- Every object schema gets `additionalProperties: false`, including the envelope objects (`root`, `path`/`query`/`header` groups) built outside the converter.

## Loader behaviour

- **Validation**: every loaded spec passes through `openapi3.Validate(ctx)`. Invalid specs fail fast.
- **External refs**: enabled. The loader uses `LoadFromDataWithPath` so relative `$ref`s in the spec resolve against the spec file's directory.
- **Swagger 2.0 conversion**: handled by `kin-openapi/openapi2conv.ToV3`. Body parameters become `requestBody`, response schemas become `responses.content`.
- **`WriteV3YAMLJSONOnly`**: emits the in-memory v3 representation as YAML for downstream tools that only accept v3 (notably `oapi-codegen`). Non-JSON content types are pruned on a deep clone — the original document is not mutated.

## Generated handler shape

### Companion mode

For `POST /pets` with a JSON body:

```go
s.AddTool(
    runtime.ApplyConfig(runtime.Tool{
        Name:           "addPet",
        Description:    "Creates a new pet",
        RawInputSchema: json.RawMessage(input_addPet),
    }, cfg),
    func(ctx context.Context, req *runtime.CallToolRequest) (*runtime.CallToolResult, error) {
        var body pet.AddPetJSONRequestBody
        if err := runtime.DecodeBody(req.Arguments, &body); err != nil {
            return runtime.HandleError(err)
        }
        resp, err := c.AddPetWithResponse(ctx, body)
        if err != nil {
            return runtime.HandleError(err)
        }
        if resp == nil {
            return runtime.NewToolResultError("empty response"), nil
        }
        return runtime.NewToolResultFromHTTP(
            resp.StatusCode(), headerOf(resp.HTTPResponse), resp.Body, "application/json",
        ), nil
    },
)
```

The call signature follows oapi-codegen's deterministic argument order: `ctx`, positional path params, `*<Op>Params` (if query/header parameters exist), typed body (if a request body exists), `reqEditors...`.

### Proxy mode

The same operation in proxy mode (no oapi-codegen client; the closure builds the request inline):

```go
s.AddTool(
    runtime.ApplyConfig(runtime.Tool{
        Name:           "addPet",
        Description:    "Creates a new pet",
        RawInputSchema: json.RawMessage(input_addPet),
    }, cfg),
    func(ctx context.Context, req *runtime.CallToolRequest) (*runtime.CallToolResult, error) {
        pathStr := "/pets"
        q := url.Values{}
        u, err := runtime.BuildProxyURL(baseURL, pathStr, q)
        if err != nil {
            return runtime.HandleError(err)
        }

        var reqBody io.Reader
        var bodyCT string
        if raw, ok := req.Arguments["body"]; ok && raw != nil {
            reqBody, bodyCT, err = runtime.EncodeJSONBody(raw)
            if err != nil {
                return runtime.HandleError(err)
            }
        }

        httpReq, err := http.NewRequestWithContext(ctx, "POST", u, reqBody)
        if err != nil {
            return runtime.HandleError(err)
        }
        if bodyCT != "" {
            httpReq.Header.Set("Content-Type", bodyCT)
        }
        if err := applyAuthBearerAuth(httpReq); err != nil {  // generated per scheme
            return runtime.HandleError(err)
        }

        httpResp, err := httpClient.Do(httpReq)
        if err != nil {
            return runtime.HandleError(err)
        }
        respBody, err := runtime.ReadResponseBody(httpResp)
        if err != nil {
            return runtime.HandleError(err)
        }
        return runtime.NewToolResultFromHTTP(
            httpResp.StatusCode, httpResp.Header, respBody, "application/json",
        ), nil
    },
)
```

One `applyAuth<Scheme>` helper is emitted per scheme parsed from `components.securitySchemes`. Anonymous operations (`security: [{}]` or no security declared anywhere) omit the auth call entirely.

## Extension points

| Want to add… | Where to hook in |
|---|---|
| A new MCP backend | New subdirectory under `pkg/runtime/<libname>/` exporting `NewServer` and `Wrap`. Implement `AddTool`. No generator change. In proxy mode also extend `pkg/generator/scaffold.go::mcpSDKDeps` so the scaffold's `go.mod` and `main.go` template know about it. |
| A new schema-mode flag | New field on `generator.Options`. Translate it inside `NewSchemaConverter` and `buildInputSchema`. |
| A new spec-input format | New branch inside `loader.Load`. Output must be `*openapi3.T`. |
| A new emission mode | Extend `generator.Mode` with a new constant; add a parallel template alongside `fileTemplate` / `fileTemplateProxy`; branch in `renderWithOps`. Per-mode CLI flag validation lives in `cmd/openapi-go-mcp/main.go`. |
| A new auth scheme | Add a `SecurityKind` constant in `pkg/generator/security.go`; extend `classifySecurityScheme` to recognise it; add a branch in `fileTemplateProxy`'s `applyAuth<Scheme>` block; add a matching `Apply<X>` helper in `pkg/runtime/auth.go`. |
| A new request body kind | Add a `BodyKind` constant in `pkg/generator/operation.go`; teach `pickRequestContent` how to detect it; add a branch in both `fileTemplate` (companion) and `fileTemplateProxy` (proxy); add a `Build<X>Body` helper in `pkg/runtime/`. |

## Request body kinds

The generator dispatches on the operation's request content type. The MCP `body` argument shape is identical in both emission modes; what changes is how the handler reaches the wire.

| Content type | `BodyKind` | Companion call site | Proxy build site | MCP `body` argument |
|---|---|---|---|---|
| `application/json` (and `*+json`) | `BodyJSON` | `<Op>WithResponse(ctx, …, body, …)` | `runtime.EncodeJSONBody(args["body"])` | object matching the schema |
| `application/x-www-form-urlencoded` | `BodyForm` | `<Op>WithFormdataBodyWithResponse(ctx, …, body, …)` | `runtime.EncodeFormBody(req.Arguments)` | object matching the schema |
| `multipart/form-data` | `BodyMultipart` | `<Op>WithBodyWithResponse(ctx, …, contentType, body, …)` | `runtime.BuildMultipartBody(req.Arguments, fileFields)` | object; `format:binary` fields are base64 strings |
| `application/octet-stream` | `BodyOctet` | `<Op>WithBodyWithResponse(ctx, …, "application/octet-stream", body, …)` | `runtime.BuildBase64BytesBody(req.Arguments)` | base64-encoded string |
| `text/*` | `BodyText` | `<Op>WithBodyWithResponse(ctx, …, "<spec ct>", body, …)` | `runtime.BuildStringBody(req.Arguments)` | plain string |
| anything else (e.g. `application/xml`) | `BodyRaw` | `<Op>WithBodyWithResponse(ctx, …, "<spec ct>", body, …)` | `runtime.BuildStringBody(req.Arguments)` | plain string |

When an operation declares more than one content type, the generator picks deterministically in the order above (override with `-prefer-content-type`). Multipart binary fields are rewritten in the input schema from `{type:"string", format:"binary"}` to `{type:"string", contentEncoding:"base64"}`; the runtime helper `BuildMultipartBody` base64-decodes them into file parts. Raw kinds use `BuildBase64BytesBody` / `BuildStringBody`. Proxy mode sets the `Content-Type` header on the outgoing `*http.Request` from the encoder's returned value; companion mode lets oapi-codegen do the same.

## Known limitations

- Response decoding by content type: `application/json` uses `NewToolResultJSON` (structured); `text/*` uses `NewToolResultText`; `application/octet-stream`, `application/xml`, and other binary/raw types use `NewToolResultBinary` (base64-encoded into `Text`, surfaced as `{"contentType","base64"}` in `StructuredContent`). Operations with no response body keep the JSON wrapper (empty body in, empty result out).
- Multipart binary fields are detected for top-level and nested-object properties. Arrays of binary items are not unpacked into per-element parts (binary leaves under `items` schemas are ignored in v1).
- A spec header parameter named `Content-Type` alongside a non-JSON request body emits a generator-time warning. In companion mode the header is silently overridden by oapi-codegen's `<Op>WithBodyWithResponse`; in proxy mode the generated handler sets `Content-Type` from the body encoder *after* applying header params, so a spec-declared `Content-Type` is overwritten there too.
- Streaming responses (SSE, chunked transfer-encoding) surface as raw bytes — no first-class streaming support yet.
- No dynamic (runtime, no-codegen) registration path. Tracked in [TODO](TODO.md).
- The schema-converter surfaces `discriminator` as a human-readable hint in the schema's `description` (property name + mapping keys). It does not invent JSON-Schema keywords (`if`/`then`/`else`) for branch selection — clients must read the description to drive the choice.
- **Proxy mode auth scope**: only env-var-driven credentials (apiKey, http+bearer, http+basic, oauth2-as-bearer). OAuth2 token-exchange flows, mTLS, AWS SigV4, and OIDC discovery are out of scope. Users needing those plug in a custom `http.Client` via `runtime.WithHTTPClient` from companion mode.
- **Proxy mode parameter serialisation**: query/header/path/cookie values are stringified via the simple OpenAPI defaults (`form` style with `explode=false` for query; comma-join for arrays; `fmt.Sprint` for scalars). The `style` / `explode` / `allowReserved` keywords on parameters are not honoured. Specs needing matrix or pipeDelimited styles should use companion mode.

## References

- [Model Context Protocol specification](https://modelcontextprotocol.io)
- [OpenAPI 3.1 specification](https://spec.openapis.org/oas/v3.1.0)
- [oapi-codegen documentation](https://github.com/oapi-codegen/oapi-codegen#readme)
- [protoc-gen-go-mcp](https://github.com/redpanda-data/protoc-gen-go-mcp) — the protobuf counterpart this project mirrors
