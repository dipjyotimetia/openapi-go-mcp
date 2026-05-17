# Architecture

This document describes the structure of `openapi-gen-go-mcp` — its packages, the data flow from OpenAPI spec to generated Go source, and the extension points where new MCP backends or schema dialects can be added.

## Goals

1. **Companion-codegen ergonomics**: a single `go run` produces a `*.mcp.go` file that lives alongside the user's `oapi-codegen` output, in the same module — no foreign generator binaries, no Buf-style plugin protocol.
2. **Library-agnostic runtime**: generated code targets a small interface (`runtime.MCPServer`) so the choice of MCP server library is a build-time import swap, not a regeneration.
3. **Spec-input flexibility**: OpenAPI 3.0, 3.1, and Swagger 2.0 specs are accepted via a single loader. Swagger 2.0 is converted in-process; users never write a separate conversion step.
4. **Deterministic, reviewable output**: generated source is gofmt-clean, operation order is sorted, and a golden test guards format/regressions.

## Package layout

```
cmd/openapi-gen-go-mcp/   # CLI entry point + batch orchestration loop
pkg/loader/               # OpenAPI 3.x + Swagger 2.0 ingestion; ExpandSpecArg
pkg/batch/                # Per-spec option derivation, slug rules, collision detection
pkg/generator/            # Operation collection, schema conversion, Go source emission
pkg/runtime/              # MCP-library-agnostic types (MCPServer, Tool, helpers)
pkg/runtime/gosdk/        # Adapter for modelcontextprotocol/go-sdk
pkg/runtime/mark3labs/    # Adapter for mark3labs/mcp-go
examples/                 # End-to-end demos (one per MCP backend, one for Swagger 2.0)
testdata/                 # Spec fixtures + golden generator output
```

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
┌────────────────────────────────────┐
│ loader.ExpandSpecArg               │   Resolves -spec into []SpecRef:
│   isHTTPURL → URL passthrough      │   URL passthrough, filepath.Glob for
│   hasGlobMeta → filepath.Glob      │   *, ?, []; WalkDir for directories
│   IsDir → expandDir (recursive)    │   (.yaml/.yml/.json only, dot-files
│   else → single file               │   skipped, symlinks not followed).
└────────────────────────────────────┘   Sorted + deduplicated.
       │
       ▼  []SpecRef
┌────────────────────────────────────┐
│ batch.PlanFor (per ref)            │   Single-spec mode: Opts pass through
│   slug = filename stem [a-z0-9]    │   verbatim so the existing -package /
│   batch mode derives:              │   flat-output UX is intact. Batch mode
│     PackageName = slug + "mcp"     │   derives PackageName, OutDir, and
│     OutDir      = out/<slug>mcp    │   ClientImport from the slug.
│     ClientImport= base/<slug>      │   batch.DetectCollisions then aborts
│                                    │   before any write if two specs collide.
└────────────────────────────────────┘
       │
       ▼  []SpecPlan
┌────────────────────────────────────┐
│ For each plan (CLI orchestration): │   Per-plan errors are captured and
│                                    │   reported at end; in batch mode the
│   loader.Load(plan.Ref.Path)       │   loop keeps going so CI sees every
│       │                            │   failing spec in one run. Exit code
│       ▼  *openapi3.T (validated)   │   rolls up to exitGenerate (3) when
│   generator.CollectOperations      │   any plan failed.
│   generator.Render                 │
│   os.WriteFile(plan.Opts.OutDir/   │
│                plan.Opts.Pkg+.mcp.go)
└────────────────────────────────────┘
```

`loader.Load` reads each individual file or URL exactly as before — detects
Swagger 2.0 via top-level `swagger: 2.0` and converts via
`kin-openapi/openapi2conv` when needed. `generator.CollectOperations` walks
paths × methods in sorted order, building a per-operation
`SchemaConverter` (the shared `nameByPtr` lookup map is rebuilt per plan
because each spec has its own component pool — there is no cross-spec
sharing in v1). `generator.Render` emits `Register*Client` and one
`const input_<tool>` per operation; the final byte stream is gofmt-clean.

## Runtime architecture (generated code → MCP server)

```
                          ┌────────────────────┐
LLM client ──tools/call──▶│  MCP transport     │
                          │  (stdio, SSE, ...) │
                          └────────┬───────────┘
                                   │
                                   ▼
                          ┌────────────────────┐
                          │  go-sdk *Server  ─┐ │
                          │  OR              │ │  thin adapter
                          │  mark3labs Server│ │  (pkg/runtime/<lib>/)
                          └────────┬─────────┘─┘
                                   │ implements
                                   ▼
                          ┌────────────────────┐
                          │ runtime.MCPServer  │  AddTool(Tool, ToolHandler)
                          └────────┬───────────┘
                                   │
                                   ▼
                          ┌────────────────────┐    decode args (path/query/header/body)
                          │ generated handler  │ ─▶ runtime.DecodePathParam, DecodeBody, …
                          │ closure            │
                          │                    │    call oapi-codegen typed client
                          │                    │ ─▶ c.<Op>WithResponse(ctx, ...)
                          │                    │
                          │                    │    return JSON body as MCP result
                          │                    │ ─▶ runtime.NewToolResultJSON(resp.Body)
                          └────────────────────┘
```

The generated `Register…Client(s runtime.MCPServer, c <Client>, opts...)` function does not know which MCP library is at the other end of the `MCPServer` interface. Swapping `gosdk.NewServer(...)` for `mark3labs.NewServer(...)` is the only change required to switch backends.

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
        return runtime.NewToolResultJSON(resp.Body), nil
    },
)
```

The call signature follows oapi-codegen's deterministic argument order: `ctx`, positional path params, `*<Op>Params` (if query/header parameters exist), typed body (if a request body exists), `reqEditors...`.

## Extension points

| Want to add… | Where to hook in |
|---|---|
| A new MCP backend | New subdirectory under `pkg/runtime/<libname>/` exporting `NewServer` and `Wrap`. Implement `AddTool`. No generator change. |
| A new schema-mode flag | New field on `generator.Options`. Translate it inside `NewSchemaConverter` and `buildInputSchema`. |
| A new spec-input format | New branch inside `loader.Load`. Output must be `*openapi3.T`. |
| Server-handler mode (generate a stub interface the user implements) | New template alongside `fileTemplate`; new `Generate` mode toggled by an `Options` flag. Out of scope for v0.1. |
| Authentication scheme wiring | Add an `Operation` field for required security schemes; expand the handler template to read tokens from `runtime.ExtraProperty` context. |

## Request body kinds

The generator dispatches on the operation's request content type:

| Content type | Body kind | Generated call site | MCP `body` argument |
|---|---|---|---|
| `application/json` (and `*+json`) | `BodyJSON` | `<Op>WithResponse(ctx, …, body, …)` | object matching the schema |
| `application/x-www-form-urlencoded` | `BodyForm` | `<Op>WithFormdataBodyWithResponse(ctx, …, body, …)` | object matching the schema |
| `multipart/form-data` | `BodyMultipart` | `<Op>WithBodyWithResponse(ctx, …, contentType, body, …)` | object; `format:binary` fields are base64 strings |
| `application/octet-stream` | `BodyOctet` | `<Op>WithBodyWithResponse(ctx, …, "application/octet-stream", body, …)` | base64-encoded string |
| `text/*` | `BodyText` | `<Op>WithBodyWithResponse(ctx, …, "<spec ct>", body, …)` | plain string |
| anything else (e.g. `application/xml`) | `BodyRaw` | `<Op>WithBodyWithResponse(ctx, …, "<spec ct>", body, …)` | plain string |

When an operation declares more than one content type, the generator picks deterministically in the order above. Multipart binary fields are rewritten in the input schema from `{type:"string", format:"binary"}` to `{type:"string", contentEncoding:"base64"}`; the runtime helper `BuildMultipartBody` base64-decodes them into file parts. Raw kinds use `BuildBase64BytesBody` / `BuildStringBody`.

## Known limitations

- Response decoding by content type: `application/json` uses `NewToolResultJSON` (structured); `text/*` uses `NewToolResultText`; `application/octet-stream`, `application/xml`, and other binary/raw types use `NewToolResultBinary` (base64-encoded into `Text`, surfaced as `{"contentType","base64"}` in `StructuredContent`). Operations with no response body keep the JSON wrapper (empty body in, empty result out).
- Multipart binary fields are detected for top-level and nested-object properties. Arrays of binary items are not unpacked into per-element parts (binary leaves under `items` schemas are ignored in v1).
- A spec header parameter named `Content-Type` alongside a non-JSON request body emits a generator-time warning. The header is still silently overridden by oapi-codegen's `<Op>WithBodyWithResponse` at runtime.
- Streaming responses (SSE, chunked transfer-encoding) surface as raw bytes — no first-class streaming support yet.
- No dynamic (runtime, no-codegen) registration path. Tracked in [TODO](TODO.md).
- The schema-converter surfaces `discriminator` as a human-readable hint in the schema's `description` (property name + mapping keys). It does not invent JSON-Schema keywords (`if`/`then`/`else`) for branch selection — clients must read the description to drive the choice.

## References

- [Model Context Protocol specification](https://modelcontextprotocol.io)
- [OpenAPI 3.1 specification](https://spec.openapis.org/oas/v3.1.0)
- [oapi-codegen documentation](https://github.com/oapi-codegen/oapi-codegen#readme)
- [protoc-gen-go-mcp](https://github.com/redpanda-data/protoc-gen-go-mcp) — the protobuf counterpart this project mirrors
