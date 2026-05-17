# Usage patterns

`openapi-go-mcp` doesn't ship a server — it generates one. The generated `*.mcp.go` registers every OpenAPI operation as an MCP tool that forwards to an `oapi-codegen` typed HTTP client. Each pattern below is a different way of assembling those pieces into a deployable binary.

> All examples assume you've already run the two-step codegen for your spec:
> ```bash
> oapi-codegen -generate types,client -package pet -o gen/pet/pet.gen.go petstore.yaml
> openapi-go-mcp -spec petstore.yaml -out gen/petmcp -package petmcp \
>     -client-import github.com/me/myrepo/gen/pet
> ```

## Pattern 1 — Local stdio MCP server (Claude Desktop, IDEs)

The default. One binary per upstream API, launched by the MCP host over stdio.

```go
// main.go
package main

import (
    "context"
    "log"
    "os"

    "github.com/modelcontextprotocol/go-sdk/mcp"

    "github.com/me/myrepo/gen/pet"
    "github.com/me/myrepo/gen/petmcp"
    "github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime/gosdk"
)

func main() {
    client, err := pet.NewClientWithResponses(os.Getenv("PETSTORE_BASE_URL"))
    if err != nil {
        log.Fatal(err)
    }

    raw, s := gosdk.NewServer("petstore-mcp", "1.0.0")
    petmcp.RegisterSwaggerPetstoreClient(s, client)

    if err := raw.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
        log.Fatal(err)
    }
}
```

Claude Desktop config (`~/Library/Application Support/Claude/claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "petstore": {
      "command": "/usr/local/bin/petstore-mcp",
      "env": { "PETSTORE_BASE_URL": "https://api.example.com" }
    }
  }
}
```

See [`examples/petstore`](../examples/petstore) for the working version.

## Pattern 2 — Remote MCP server (HTTP / SSE)

Same generated code, different transport. Useful when the upstream API can't be reached from the user's laptop, or when the MCP server should be deployed once and shared.

```go
raw, s := gosdk.NewServer("petstore-mcp", "1.0.0")
petmcp.RegisterSwaggerPetstoreClient(s, client)

// Replace StdioTransport with the go-sdk's HTTP/SSE transport:
handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return raw }, nil)
log.Fatal(http.ListenAndServe(":8080", handler))
```

Generated code is transport-agnostic — it only calls `runtime.MCPServer.AddTool`. The transport lives entirely in `main`.

## Pattern 3 — Backend swap (go-sdk ↔ mark3labs)

Change one import and one constructor line:

```go
// Was:
//   "github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime/gosdk"
//   raw, s := gosdk.NewServer("petstore-mcp", "1.0.0")

import "github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime/mark3labs"
import mcpserver "github.com/mark3labs/mcp-go/server"

raw, s := mark3labs.NewServer("petstore-mcp", "1.0.0")
petmcp.RegisterSwaggerPetstoreClient(s, client)   // unchanged
mcpserver.ServeStdio(raw)
```

No regeneration needed. See [`examples/petstore-mark3labs/main.go`](../examples/petstore-mark3labs/main.go).

## Pattern 4 — Multi-tenant / multi-environment namespacing

Run two instances of the same API (e.g., staging vs prod) inside one MCP server, distinguished by tool-name prefix:

```go
prodClient, _ := pet.NewClientWithResponses("https://api.example.com")
stagingClient, _ := pet.NewClientWithResponses("https://staging.api.example.com")

raw, s := gosdk.NewServer("petstore-mcp", "1.0.0")

petmcp.RegisterSwaggerPetstoreClient(s, prodClient,
    runtime.WithNamePrefix("prod"))     // tools: prod_addPet, prod_findPetById, ...
petmcp.RegisterSwaggerPetstoreClient(s, stagingClient,
    runtime.WithNamePrefix("staging"))  // tools: staging_addPet, staging_findPetById, ...
```

The prefix can also be baked in at generation time with `-name-prefix` if it never changes per-deployment.

## Pattern 5 — Per-call auth injection (multi-tenant API tokens)

Use `WithExtraProperties` to add a schema field that the LLM must supply on every tool call. The value is removed from the args and placed on the request context:

```go
type ctxKey string
const tokenKey ctxKey = "tenant-token"

petmcp.RegisterSwaggerPetstoreClient(s, client,
    runtime.WithExtraProperties(runtime.ExtraProperty{
        Name:        "tenant_token",
        Description: "Bearer token for the calling tenant",
        Required:    true,
        ContextKey:  tokenKey,
    }),
)
```

Then read the token in an `oapi-codegen` request editor (configured on the client) and add it to the outbound `Authorization` header. The token never lives in process memory between calls.

## Pattern 6 — Static API key (single-tenant)

For single-tenant cases, use `oapi-codegen`'s standard request editor instead of an extra property — the LLM never sees the key:

```go
client, err := pet.NewClientWithResponses("https://api.example.com",
    pet.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
        req.Header.Set("Authorization", "Bearer "+os.Getenv("API_KEY"))
        return nil
    }),
)
```

This is the right pattern when the deployment itself owns the credential.

## Pattern 7 — Strict-mode schema for OpenAI tool calls

OpenAI's tool-call validator rejects `$ref`, `oneOf`/`anyOf`/`allOf`, and open-ended objects. Generate with `-openai-compat`:

```bash
openapi-go-mcp \
    -spec petstore.yaml -out gen/petmcp -package petmcp \
    -client-import github.com/me/myrepo/gen/pet \
    -openai-compat
```

The generator inlines `$defs`, collapses `oneOf`/`anyOf` to the first branch, shallow-merges `allOf`, and adds `additionalProperties: false` to every object. See [`architecture.md`](architecture.md#openai-compatibility-mode--openai-compat) for the full set of transforms.

> Lossy by design — composition keywords lose alternatives. Use only when targeting strict-schema validators.

## Pattern 8 — Sidecar to an internal service

Deploy the generated MCP server in the same pod as the upstream service and point it at `http://localhost:<port>`. The MCP server is what the LLM sees; the service itself stays internal.

```go
client, _ := internal.NewClientWithResponses("http://127.0.0.1:8080")
raw, s := gosdk.NewServer("internal-api-mcp", "1.0.0")
internalmcp.RegisterClient(s, client)
raw.Run(ctx, &mcp.StdioTransport{}) // or HTTP transport on a different port
```

This avoids exposing the upstream service to the network while still letting an LLM reach it.

## Pattern 9 — Swagger 2.0 input

`oapi-codegen` rejects Swagger 2.0. Convert in-process first, then run both codegens against the converted v3:

```bash
openapi-go-mcp -spec petstore-v2.json -emit-v3 petstore-v3.yaml
oapi-codegen -generate types,client -package pet -o gen/pet/pet.gen.go petstore-v3.yaml
openapi-go-mcp -spec petstore-v3.yaml -out gen/petmcp -package petmcp \
    -client-import github.com/me/myrepo/gen/pet
```

`-emit-v3` also prunes non-JSON content types on a deep clone — works around an `oapi-codegen` v2.7.0 bug with responses exposed under multiple content types. See [`examples/swagger2-petstore`](../examples/swagger2-petstore).

## Pattern 10 — Aggregating multiple APIs in one MCP server

Register more than one client against the same server. Use prefixes to keep tool names unambiguous:

```go
raw, s := gosdk.NewServer("aggregated-mcp", "1.0.0")

petmcp.RegisterSwaggerPetstoreClient(s, petClient,
    runtime.WithNamePrefix("pet"))
ordersmcp.RegisterOrdersClient(s, orderClient,
    runtime.WithNamePrefix("orders"))
billingmcp.RegisterBillingClient(s, billingClient,
    runtime.WithNamePrefix("billing"))

raw.Run(ctx, &mcp.StdioTransport{})
```

One binary, many upstream APIs, one MCP endpoint for the LLM.

## Pattern 11 — Curating which operations become MCP tools

Not every operation in a spec should be reachable from an LLM. Mark
operations, path-items, or the whole document with an `x-mcp` extension to
opt them in or out. Operation-level annotations beat path-level annotations
beat document-level annotations beat the generator's CLI default.

```yaml
openapi: 3.0.0
info: { title: BillingAPI, version: "1.0" }

# Document-wide default for this spec: nothing becomes an MCP tool unless
# explicitly opted in below. (Equivalent to passing -exclude-by-default at
# the CLI, but kept in the spec so every consumer of the file behaves the
# same way without remembering the flag.)
x-mcp: false

paths:
  /invoices:
    get:                       # opt-in this one operation
      operationId: listInvoices
      x-mcp: true
      responses: { "200": { description: ok } }
    post:                      # … but not this one
      operationId: createInvoice
      responses: { "200": { description: ok } }

  /admin/refund:               # whole path-item opted out
    x-mcp: false
    post:
      operationId: refund
      responses: { "200": { description: ok } }
```

Run the generator with `-list` first to see which operations survive
filtering; excluded operations show up as `excluded-by-x-mcp` info
diagnostics on stderr, and unrecognised `x-mcp` values (typos like
`x-mcp: yes`) become `invalid-x-mcp-value` warnings so they don't slip
past review:

```bash
openapi-go-mcp -spec billing.yaml -list
openapi-go-mcp -force -spec billing.yaml \
    -out gen/billingmcp -package billingmcp \
    -client-import github.com/acme/billing/gen/billing
```

`-force` is required to overwrite an existing `*.mcp.go`; the safety check
catches accidental regenerations that would clobber hand-edited or already
committed output.

## Pattern 12 — Batch generation across many specs

A monorepo with one spec per service (or a fan-out integration that touches
every API at a partner) is the usual driver for this pattern. Point `-spec`
at a directory, a glob, or a comma-separated list, and the CLI runs the
single-spec pipeline once per matched file.

```bash
# Recursive directory: every .yaml/.yml/.json under apis/ becomes a tool set
openapi-go-mcp \
    -spec apis/ \
    -out gen \
    -client-import github.com/acme/apis/gen \
    -force

# Glob with stdlib filepath.Glob syntax (no ** in v1; use a directory for recursion)
openapi-go-mcp \
    -spec 'apis/*.yaml' \
    -out gen \
    -client-import github.com/acme/apis/gen

# Multiple folders / mixed inputs, comma-separated. Each entry is expanded
# independently then concatenated, sorted, and deduplicated.
openapi-go-mcp \
    -spec 'core-apis/,partner-apis/,extras/audit.yaml' \
    -out gen \
    -client-import github.com/acme/apis/gen
```

For every matched spec the generator derives a slug from the filename stem
(lowercase, alphanumeric only — e.g. `billing-api.yaml → billingapi`) and
writes:

| Derived field | Value |
|---|---|
| `PackageName` | `<slug>mcp` |
| `OutDir`      | `<out>/<slug>mcp/` |
| `ClientImport`| `<base>/<slug>` (Go forward-slash join) |

So `-out gen -client-import github.com/acme/apis/gen` on `apis/billing.yaml`
emits `gen/billingmcp/billingmcp.mcp.go` whose `import` line reads
`"github.com/acme/apis/gen/billing"`. Pair each `<slug>mcp` directory with an
`oapi-codegen` invocation that writes its typed client under the matching
import path.

**Behaviour notes:**

- **Failures don't stop the run.** If one spec is malformed, the rest still
  generate; every error is reported at end and the process exits with code
  `3` so CI catches it.
- **Slug collisions abort upfront.** Two specs that derive the same slug
  (e.g. `v1/api.yaml` and `v2/api.yaml`) are reported by path before any
  file is written — rename one to disambiguate.
- **`-package` and `-emit-v3` are rejected** (`exitUsage`) in batch mode:
  `-package` would force every output to share a name, and `-emit-v3`
  writes one file.
- **`-list` is supported.** With multiple matched specs, the output is
  grouped under `=== <path> ===` headers per spec — handy for grepping a
  monorepo before committing the regenerate.
- **URLs stay single-spec.** A glob over `https://…` would be ambiguous;
  passing one URL in `-spec` works exactly as today.
- **Symlinks not followed.** `filepath.WalkDir` reports symlinks as files
  (not directories), so a stray symlink can't escape the user's intended
  tree.

## Pattern 13 — Standalone proxy server (zero-boilerplate)

You have an OpenAPI/Swagger spec and credentials in env vars. You want a
binary that exposes the API as MCP tools, with no Go code to write and no
`oapi-codegen` step to wire up.

```bash
openapi-go-mcp \
    -mode=proxy \
    -spec petstore.yaml \
    -out gen/petstore-mcp \
    -module github.com/me/petstore-mcp

cd gen/petstore-mcp
go mod tidy
go build

# Credentials come from env vars derived from the spec's securitySchemes.
# The generated README lists the env var per scheme.
BEARER_TOKEN_BEARERAUTH=sk-xxx ./petstore-mcp
```

The scaffold writes four files into `-out`:

```
gen/petstore-mcp/
├── main.go               # entrypoint — stdio MCP transport, env-var auth.
├── go.mod                # pins the generator runtime + chosen MCP SDK.
├── README.md             # env-var table generated from securitySchemes.
└── petstoremcp/
    └── petstoremcp.mcp.go  # the generated MCP tool registration.
```

Switch SDK with `-sdk=mark3labs`; the generated `main.go` and `go.mod`
adjust. Upstream base URL defaults to `servers[0].url` from the spec;
override at runtime with `API_BASE_URL`.

Auth shapes the generator wires automatically:

| Spec | Env var(s) |
|---|---|
| `type: apiKey, in: header/query/cookie` | `API_KEY_<UPPER_SCHEME_NAME>` |
| `type: http, scheme: bearer` | `BEARER_TOKEN_<UPPER_SCHEME_NAME>` |
| `type: http, scheme: basic` | `BASIC_AUTH_USERNAME_<UPPER_SCHEME_NAME>` + `BASIC_AUTH_PASSWORD_<UPPER_SCHEME_NAME>` |
| `type: oauth2` | `OAUTH2_ACCESS_TOKEN_<UPPER_SCHEME_NAME>` (used as Bearer; no token-exchange flow) |
| `type: openIdConnect`, `type: http, scheme: digest` | unsupported — surfaced as `unsupported-security-scheme` warning, dropped from wiring |

A missing required credential surfaces as an MCP tool-result error
naming the env var the user should set — never a silent upstream 401.

## Pattern 14 — Batch proxy for a monorepo of specs

```bash
openapi-go-mcp \
    -mode=proxy \
    -spec apis/ \
    -out gen \
    -module github.com/acme/apis-mcp \
    -force
```

Every spec under `apis/` becomes its own independent Go module under
`gen/<slug>mcp/`, each with its own `go.mod`, its own `main.go`, and an
import path of `github.com/acme/apis-mcp/<slug>`. Each module builds
independently. Combine with `x-mcp: false` (Pattern 11) to curate which
operations from each spec become tools.

## Choosing a pattern

| If you want… | Use |
|---|---|
| LLM in Claude Desktop calls a third-party API | Pattern 1 + Pattern 6 |
| Remote MCP server shared by a team | Pattern 2 |
| Same API at staging + prod in one binary | Pattern 4 |
| LLM acts on behalf of different end-users | Pattern 5 |
| Target OpenAI's tool-call validator | Pattern 7 |
| Expose an internal service to an LLM | Pattern 8 |
| Run a Swagger 2.0 spec | Pattern 9 |
| Aggregate several APIs behind one MCP endpoint | Pattern 10 |
| Publish only a curated subset of a large spec | Pattern 11 |
| Regenerate many specs in one CLI invocation | Pattern 12 |
| Zero-boilerplate runnable MCP server from a spec | Pattern 13 |
| Same, but for many specs at once | Pattern 14 |

For the reasoning behind the architectural choices these patterns rely on, see [`design-decisions.md`](design-decisions.md).
