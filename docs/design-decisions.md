# Design decisions

The non-obvious choices made by `openapi-go-mcp`, why they exist, and what they cost. Each entry is paired with the alternative we considered and rejected.

## 1. Companion code generation, not runtime introspection

**Decision.** Generate a `*.mcp.go` file at build time that the user compiles into their binary alongside the `oapi-codegen` client.

**Alternative considered.** A runtime library that parses an OpenAPI spec on startup and registers MCP tools dynamically — no codegen step.

**Why we chose codegen.**
- **Compile-time safety.** The generated handlers call typed `<Op>WithResponse(ctx, ...)` methods. A spec change that drops a parameter or renames a field breaks the build, not production.
- **Symmetry with the existing toolchain.** Users running `oapi-codegen` already accept a codegen step. Adding `openapi-go-mcp` is one more `go generate` line, not a new architectural primitive.
- **Reviewable diffs.** The output is gofmt-clean Go source, version-controlled. PR reviewers see exactly what the MCP server will expose — no runtime surprises.
- **No reflection / no schema-walking at request time.** Cold-start cost is zero; tool registration is straight-line code.

**Cost.** Spec changes require a regeneration step. A runtime registration path is on the roadmap (`TODO.md`) but is not the default for the reasons above.

## 2. `runtime.MCPServer` interface, not vendor lock-in

**Decision.** Generated code targets a 3-method interface (`AddTool`). Concrete MCP libraries (`modelcontextprotocol/go-sdk`, `mark3labs/mcp-go`) live behind thin adapter packages.

**Alternative considered.** Generate against the official `go-sdk` directly. Simpler, no abstraction layer.

**Why we chose the interface.**
- **The MCP ecosystem is young and unsettled.** Pinning generated code to one library would force every user to rev when that library breaks API. An interface shifts that cost into one ~50-line adapter file.
- **Backend swap is a one-line change in `main`.** Users can A/B between libraries without regeneration (see [Pattern 3](usage-patterns.md#pattern-3--backend-swap-go-sdk--mark3labs)).
- **Test isolation.** The generator can be tested against a stub `MCPServer` without booting a real MCP transport.

**Cost.** A tiny indirection layer (`pkg/runtime/`). Generated code can't use library-specific features without breaking the abstraction — a deliberate constraint.

## 3. Grouped input schema (`path` / `query` / `header` / `body`), not flat

**Decision.** Every tool's input schema is an object with up to four sub-objects, one per OpenAPI parameter location:

```json
{
  "type": "object",
  "properties": {
    "path":   { "type": "object", "properties": { "petId": {...} } },
    "query":  { "type": "object", "properties": { "limit": {...} } },
    "header": { "type": "object", "properties": { "X-Trace-Id": {...} } },
    "body":   { "$ref": "#/$defs/NewPet" }
  }
}
```

**Alternative considered.** Flatten everything into one top-level object — easier for the LLM to fill in.

**Why we chose grouping.**
- **Name collisions are real.** A path param `id` and a body field `id` would collide under a flat schema.
- **Round-trip clarity for LLMs.** The grouping mirrors the HTTP request shape, so the model's tool call reads almost like an HTTP wire request. In practice this improves tool-use accuracy on complex APIs.
- **Decoder simplicity.** `runtime.DecodePathParam` / `DecodeBody` / `DecodeQueryParams` map 1:1 to a group; no merge logic except `DecodeParamsCombined`, which exists only because `oapi-codegen` emits one `*<Op>Params` struct for query+header combined.

**Cost.** One extra level of nesting in tool calls. Empty groups are omitted from the schema so the model isn't asked to fill them.

## 4. Per-operation `$defs`, not a single shared component pool

**Decision.** Each tool's schema carries its own `$defs` containing only the components reachable from that operation.

**Alternative considered.** One global `$defs` per generated file, shared across every tool's schema.

**Why we chose per-operation.**
- **MCP tool schemas are independent units.** A tool's schema must be self-contained — the LLM sees it in isolation when picking a tool.
- **Schema slimming.** Tools that touch only a few components don't ship the entire spec's schema graph.
- **Per-tool dialect.** `-openai-compat` can inline `$defs` for one tool without affecting others. With a global pool, the whole file would have to be in one dialect.

**Cost.** Repetition when many tools share the same component. A shared `nameByPtr` lookup map is built once per `CollectOperations` call to avoid O(P · S) rebuild cost, so duplication is in output bytes, not in generator work.

## 5. JSON Schema with `$ref` by default, OpenAI-compat as opt-in

**Decision.** Default output is draft-07-compatible JSON Schema with `$ref`. The lossy OpenAI-strict dialect (`-openai-compat`) is a flag.

**Alternative considered.** OpenAI-compat as the default, since OpenAI is the largest consumer of tool calls today.

**Why we chose the richer default.**
- **Loss is one-way.** `-openai-compat` collapses `oneOf`/`anyOf` to the first branch and shallow-merges `allOf`. Making this the default would silently drop spec semantics for every user.
- **Many MCP clients accept `$ref`.** Claude, the official MCP go-sdk, and most modern validators all support draft-07. OpenAI-strict is the outlier.
- **Explicit opt-in matches the contract.** If you ask for a lossy dialect, you get one. If you don't, you don't.

**Cost.** OpenAI users need to know the flag exists. Documented in [`usage-patterns.md`](usage-patterns.md#pattern-7--strict-mode-schema-for-openai-tool-calls) and the README CLI section.

## 6. Request bodies: JSON + form + multipart + raw fallback; responses: JSON only

**Decision.** Request bodies support `application/json`, `application/x-www-form-urlencoded`, `multipart/form-data`, `application/octet-stream`, `text/*`, and any other content type via a raw-string fallback (`application/xml` and friends). When an operation declares more than one, the generator picks deterministically in that priority order. Response bodies still must be JSON-decodable; non-JSON responses are surfaced as raw bytes through `runtime.NewToolResultJSON`.

**Alternative considered.** Keep the original JSON-only stance (the v0.1.0 release shipped this way) and recommend a wrapper service for multipart APIs.

**Why we expanded the surface.**
- **Real specs need this.** Form posts, file uploads, plain-text webhooks, and XML imports are common in enterprise and legacy APIs — rejecting them at codegen forced users to hand-write handlers.
- **`oapi-codegen` already emits every shape we need.** Every typed client carries `<Op>WithBodyWithResponse(ctx, contentType, io.Reader, …)` as a universal raw fallback, plus typed `WithFormdataBody` for form data — no special-case generation on our side.
- **MCP arguments are still JSON.** Binary fields are accepted as base64-encoded strings; the runtime helper `BuildMultipartBody` decodes them and writes the parts. The complexity is contained in the runtime, not pushed to the LLM.
- **Fixed priority is deterministic.** No new CLI flag is needed; the priority order is documented in the architecture doc and locked by the unit tests.

**Why responses stay JSON-only.** Response negotiation (Accept headers, content-sniffing, decoding to typed Go) is a separate, larger surface area. `NewToolResultJSON` already passes through raw bytes for non-JSON responses; first-class non-JSON response support is tracked separately.

**Cost.** Multipart bodies with nested binary fields, OpenAPI `encoding[field]` per-part metadata, and a header parameter named `Content-Type` are all known gaps documented in [`architecture.md`](architecture.md#known-limitations). Workaround for nested binary fields: flatten the body schema, or use a wrapper service.

## 7. Determinism is a hard requirement

**Decision.** Generator output is sorted (paths, methods, fields), gofmt-clean, and guarded by a golden test (`pkg/generator/golden_test.go`).

**Alternative considered.** Map-order iteration; let `gofmt` handle final cleanup.

**Why we chose determinism.**
- **PR reviewability.** Generated code lives in users' repos and shows up in their diffs. Non-deterministic output produces noise diffs that mask real changes.
- **Cacheability.** Identical input → identical output is a precondition for build caches, content-addressed artifacts, and reproducible builds.
- **Regression catch.** The golden test catches accidental output changes; legitimate changes require `UPDATE_GOLDEN=1` and a reviewed diff.

**Cost.** Generator code has explicit sorts where Go would otherwise iterate maps. Cheap.

## 8. Validation happens inside `loader.Load`

**Decision.** Every spec passes `openapi3.Validate(ctx)` before any generator code runs.

**Alternative considered.** Best-effort generation, surface errors as the generator hits them.

**Why we chose upfront validation.**
- **Fail-fast on bad specs.** A typo in the spec produces one clear error from kin-openapi, not a cascade of confusing template errors.
- **Generator code can assume valid input.** No defensive nil-checks for spec invariants that the validator already enforces.

**Cost.** Some legitimately weird specs that kin-openapi rejects can't be processed without first being fixed. In practice this is the right tradeoff.

## 9. `-emit-v3` exists because of one downstream bug

**Decision.** A dedicated flag converts Swagger 2.0 → OpenAPI 3 YAML, pruning non-JSON content types on a deep clone.

**Why this exists.** `oapi-codegen` v2.7.0 has a known issue where responses exposed under multiple content types confuse the client generator. `-emit-v3` produces a v3 YAML that has only JSON responses, suitable for `oapi-codegen` consumption. The original document in memory is never mutated — only the emitted file.

**Cost.** A flag that exists to work around a downstream bug is debt. It will likely outlive the upstream fix because users will have wired it into their build pipelines. Documented as a Swagger 2.0 helper to limit its blast radius.

## 10. `WithExtraProperties` for per-call context, not headers-as-args

**Decision.** Extra context (tenant tokens, base-URL overrides) is exposed as schema properties at registration time, decoded into request context at call time.

**Alternative considered.** Let the LLM populate arbitrary HTTP headers via a generic `headers` arg.

**Why we chose extra-properties.**
- **Constrained surface.** Only the properties the deployer registers are visible to the LLM. No path for the LLM to set arbitrary headers (e.g., spoof `Authorization`).
- **Schema-typed.** The properties show up in the tool's input schema with descriptions, so the model has guidance on what to fill in.
- **Context, not args.** The decoded value lives on `context.Context`, so the `oapi-codegen` request editor can read it without changing the typed call signature.

**Cost.** Adding a new per-call context property requires editing the registration call, not a generic header arg. Acceptable — the alternative is a security hole.

## 11. Auto-derived per-spec config in batch mode, not a config file

**Decision.** When `-spec` matches more than one spec, the generator derives `PackageName`, `OutDir`, and `ClientImport` from each spec's filename stem (the *slug*: `billing-api.yaml → billingapi`). `PackageName=<slug>mcp`, `OutDir=<out>/<slug>mcp`, `ClientImport=<base>/<slug>`. No config file. No CLI map. The user supplies a single base `-client-import` and an `-out` directory; everything else is mechanical.

**Alternative considered.** A YAML config file mapping each spec path to its package name and import path (the approach the TypeScript reference generator was originally going to take).

**Why filename-derived.**
- **Convention beats configuration.** A monorepo with one spec per service already encodes "what this thing is" in the filename. Re-encoding it in a config is duplicate state that drifts.
- **Trivially scriptable.** `openapi-go-mcp -spec apis/ -out gen -client-import …` is one command. Adding a config file means another file the user has to maintain and another path the build has to source.
- **Collisions surface loudly.** Two specs that derive the same slug (`v1/api.yaml`, `v2/api.yaml`) are caught up front by `batch.DetectCollisions` and reported with all source paths. The user fixes the filename, not a config table.

**Cost.** Specs with non-alphanumeric filenames need to be renamed to a `[a-z0-9]` form. `Slug` errors fast when the stem sanitises to empty — no silent degradation.

## 12. Batch-mode failures continue rather than fail-fast

**Decision.** In multi-spec mode, a per-spec load or generate failure is captured and the loop keeps going. All errors are printed at the end; exit code rolls up to `exitGenerate` (`3`).

**Alternative considered.** Fail fast on the first failure, matching today's single-spec behaviour.

**Why continue.**
- **CI gets a complete picture in one run.** Twenty specs and three of them broken: one run produces three error reports, not three pull requests.
- **Matches the structured-diagnostic philosophy.** The generator already accumulates per-operation diagnostics across a single spec; doing the same across multiple specs is the natural extension.

**Cost.** A failed spec leaves earlier specs' output on disk. Acceptable — matches today's "write as you go" semantics. The error report names every failing spec by path so the user can rerun selectively.

## 13. Dual-mode (companion + proxy) rather than replacing one with the other

**Decision.** Proxy mode (`-mode=proxy`) ships alongside companion mode. Companion mode stays the default; its output is byte-for-byte unchanged (golden test guards it).

**Alternative considered.** Make proxy mode the default and demote companion to opt-in; or replace companion entirely.

**Why both.**
- **Enterprises embed companion mode.** Teams that wire the companion file into their own service binary (custom transport, mTLS, internal tracing, custom retry policies via their `oapi-codegen` client) would have to rewrite that integration if companion mode disappeared. The cost-to-value is bad.
- **New users get the turnkey path.** Proxy mode is the harsha-iiiv-equivalent zero-config flow: one command, one binary, env-var auth from the spec. New adopters don't need to learn `oapi-codegen` to ship.
- **Shared core, ~20% divergent surface.** Both modes share schema conversion, parameter decoding, response wrapping, MCP-library adapters, batch orchestration, and `x-mcp` filtering. Only request construction and the auth helpers differ. Maintaining two templates is cheap given the test coverage.

**Cost.** Two codegen templates (`fileTemplate` + `fileTemplateProxy`), two assertions in the renderer, and a small `Options.Mode` branch. The CLI gets three new flags (`-mode`, `-module`, `-sdk`) but they're inert in the default path.

## 14. Env-var-only auth, with conventions matched to harsha-iiiv

**Decision.** Proxy mode authenticates from environment variables. No OAuth2 token-exchange flow, no `--auth-config` YAML, no JSON-mounted credentials. Variable names mirror the TypeScript prior art so users moving between ecosystems aren't surprised: `API_KEY_<NAME>`, `BEARER_TOKEN_<NAME>`, `BASIC_AUTH_USERNAME_<NAME>` + `BASIC_AUTH_PASSWORD_<NAME>`, `OAUTH2_ACCESS_TOKEN_<NAME>`.

**Alternative considered.** Full OAuth2 client_credentials / authorization_code flows with a token-acquisition step at startup; or a config file mapping schemes to specific env vars.

**Why env-only.**
- **Single line of integration with secret stores.** Vault, AWS Secrets Manager, K8s Secrets, Doppler — all surface as env vars. Building a token-fetch loop inside the generated binary would duplicate machinery the deployment already has.
- **Conventions over configuration.** A config file is one more thing to keep in sync with the spec. The env-var name is derived from the scheme key once; if you rename the scheme, you rename the var.
- **OAuth2 → Bearer-from-env is honest.** Most OAuth2 deployments fetch a token via a sidecar (or the platform). The generator's job is to forward it, not re-implement RFC 6749.
- **Matches the dominant TypeScript prior art.** Users moving from `harsha-iiiv/openapi-mcp-generator` see the same env-var shape and don't need to relearn anything.

**When multiple security requirements apply.** OpenAPI's `security: [ { A }, { B } ]` means "A *or* B is sufficient". Proxy mode picks the first requirement whose schemes are all parseable; everything else is anonymous fallback. Spec authors who need true multi-scheme routing should compose schemes within one requirement (`{ A, B }` = both required), which proxy mode applies in alphabetical order. This trade-off is documented in `pkg/generator/security.go::ResolveOperationSecurity`.

**Cost.** No support for token rotation without restart; no support for OIDC discovery, mTLS, AWS SigV4, or other transport-level auth. Users who need those plug in a custom `http.Client` via `runtime.WithHTTPClient` from a companion-mode integration — the escape hatch is intact.

## Non-goals

These are deliberately out of scope:

- **Generating MCP server stubs that the user implements.** The point of the project is to wrap an existing HTTP API, not to scaffold a new one.
- **Schema dialects beyond default + OpenAI-compat.** New dialects must clear a high bar: a real consumer with a documented schema constraint.
- **Streaming responses (SSE, chunked).** Surfaced as raw bytes in the tool result. First-class streaming is plausible future work; not v0.1.
- **`discriminator` mapping propagation.** OpenAPI-specific semantics that JSON Schema lacks.

See [`architecture.md`](architecture.md#known-limitations) for the full limitations list and [`usage-patterns.md`](usage-patterns.md) for the deployment recipes these decisions enable.
