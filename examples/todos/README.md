# `examples/todos` — end-to-end MCP example

This is the canonical end-to-end demo for `openapi-gen-go-mcp`. A single binary:

1. Starts an in-memory HTTP backend that implements [`todos.yaml`](./todos.yaml).
2. Builds an [`oapi-codegen`](https://github.com/oapi-codegen/oapi-codegen) typed client pointing at that backend.
3. Registers every operation as an MCP tool and serves them over **stdio**.

No external service, no manual generation, no environment variables — `go run ./examples/todos` is the whole thing.

```
                       todos-mcp (one process)
   ┌────────────────────────────────────────────────────────────┐
   │                                                            │
MCP host ─stdio──▶ go-sdk MCP server ─▶ generated MCP layer     │
(Claude,         (gosdk.NewServer)    (todosmcp.Register…)      │
 Cursor, …)                                  │                  │
                                             ▼                  │
                            oapi-codegen typed client            │
                                             │                  │
                                             ▼ HTTP             │
                            in-process httptest.Server (backend.go)
   └────────────────────────────────────────────────────────────┘
```

## Run it

```bash
# from the repo root
go run ./examples/todos
```

You should see (on stderr):

```
todos-mcp: started embedded backend at http://127.0.0.1:54321
todos-mcp serving over stdio (upstream: http://127.0.0.1:54321)
```

…and the process is now waiting for JSON-RPC messages on stdin. Hook it up to an MCP host (see [MCP client config](#mcp-client-config)) or talk to it directly (see [Talk to it without a client](#talk-to-it-without-a-client)).

## Files

| Path | Purpose |
|---|---|
| [`todos.yaml`](./todos.yaml) | OpenAPI 3.0 spec — 5 operations covering path params, query params, JSON request/response bodies. |
| [`gen/todos/oapi.yaml`](./gen/todos/oapi.yaml) | `oapi-codegen` config for the typed HTTP client. |
| [`gen/todos/todos.gen.go`](./gen/todos/todos.gen.go) | Generated typed client. |
| [`gen/todosmcp/todosmcp.mcp.go`](./gen/todosmcp/todosmcp.mcp.go) | Generated MCP layer (`RegisterTodosAPIClient`). |
| [`backend.go`](./backend.go) | Tiny in-memory implementation of the spec. |
| [`main.go`](./main.go) | Wires the backend, the client, and the MCP server together. |

## Tools exposed

After `initialize`, `tools/list` returns:

| Tool | Description | Args |
|---|---|---|
| `listTodos` | List todos, optionally filtered. | `query.completed?`, `query.limit?` |
| `createTodo` | Create a todo. | `body: { title, completed? }` |
| `getTodo` | Get a todo by ID. | `path.id` |
| `updateTodo` | Partial update. | `path.id`, `body: { title?, completed? }` |
| `deleteTodo` | Delete a todo. | `path.id` |

The exact input schema for each tool is emitted in `gen/todosmcp/todosmcp.mcp.go` and reflected back to MCP clients via the standard `tools/list` call.

## MCP client config

Build a binary first so the host can launch it directly:

```bash
go build -o ./bin/todos-mcp ./examples/todos
# resulting binary: $(pwd)/bin/todos-mcp
```

Replace `/ABS/PATH/TO/REPO` below with the absolute path to your checkout.

### Claude Desktop

`~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows):

```json
{
  "mcpServers": {
    "todos": {
      "command": "/ABS/PATH/TO/REPO/bin/todos-mcp"
    }
  }
}
```

Restart Claude Desktop; you should see five `todos` tools in the picker.

### Claude Code (CLI)

```bash
claude mcp add todos /ABS/PATH/TO/REPO/bin/todos-mcp
```

Or in `.claude/settings.json` / `~/.claude.json`:

```json
{
  "mcpServers": {
    "todos": {
      "command": "/ABS/PATH/TO/REPO/bin/todos-mcp",
      "type": "stdio"
    }
  }
}
```

### Cursor

`~/.cursor/mcp.json` (or `<workspace>/.cursor/mcp.json`):

```json
{
  "mcpServers": {
    "todos": {
      "command": "/ABS/PATH/TO/REPO/bin/todos-mcp"
    }
  }
}
```

### VS Code (Continue / Cline / generic MCP clients)

```json
{
  "mcpServers": {
    "todos": {
      "command": "/ABS/PATH/TO/REPO/bin/todos-mcp",
      "transport": "stdio"
    }
  }
}
```

### Running through `go run` (no build step)

Useful while iterating on the example. Point the host at `go` itself:

```json
{
  "mcpServers": {
    "todos": {
      "command": "go",
      "args": ["run", "./examples/todos"],
      "cwd": "/ABS/PATH/TO/REPO"
    }
  }
}
```

Slower to start (Go compiles on first launch) but always picks up local edits.

### MCP Inspector

For interactive debugging without wiring up a host:

```bash
npx @modelcontextprotocol/inspector ./bin/todos-mcp
```

## Point at an external backend

The embedded backend is convenient but ephemeral. To target a real Todos API instead:

```bash
TODOS_BASE_URL=https://my-todos.example.com go run ./examples/todos
```

When `TODOS_BASE_URL` is set, the `httptest.Server` is skipped and the typed client is configured to hit that URL directly. Anything that conforms to [`todos.yaml`](./todos.yaml) will work.

In MCP-client config, pass it via `env`:

```json
{
  "mcpServers": {
    "todos": {
      "command": "/ABS/PATH/TO/REPO/bin/todos-mcp",
      "env": { "TODOS_BASE_URL": "https://my-todos.example.com" }
    }
  }
}
```

## Talk to it without a client

You can drive the server straight from a shell — useful for smoke tests and CI:

```bash
( printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'; \
  sleep 1; \
  printf '%s\n' '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}'; \
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'; \
  sleep 1; \
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"listTodos","arguments":{}}}'; \
  sleep 1; \
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"createTodo","arguments":{"body":{"title":"buy milk","completed":false}}}}'; \
  sleep 1 ) | go run ./examples/todos
```

Each `id` correlates a response to its request. The first response is the server's `initialize` result; the second is the full tool catalog; the rest are live tool calls hitting the embedded backend.

## Regenerate from the spec

The two generated directories (`gen/todos`, `gen/todosmcp`) are committed so the example builds without extra tooling, but they're produced from `todos.yaml`. To regenerate after changing the spec:

```bash
# from the repo root
make regen-examples
```

Or just the todos pieces:

```bash
oapi-codegen -config examples/todos/gen/todos/oapi.yaml examples/todos/todos.yaml

go run ./cmd/openapi-gen-go-mcp \
    -spec examples/todos/todos.yaml \
    -out examples/todos/gen/todosmcp \
    -package todosmcp \
    -client-import github.com/dipjyotimetia/openapi-gen-go-mcp/examples/todos/gen/todos
```

The MCP register-function name (`RegisterTodosAPIClient`) is derived from the spec's `info.title` (`Todos API` → `TodosAPI`). Change the title in the spec and `main.go` must follow.

## What to copy from this example

If you're applying this pattern to your own API:

- **Keep `main.go` thin.** The whole point is that the generated code does the work. Aim for: build client → `NewServer` → `Register…Client` → run transport.
- **Drop the embedded backend.** [`backend.go`](./backend.go) exists only to make the demo self-contained. In a real deployment you delete it and set the base URL to your actual API.
- **Pick a transport.** This example uses `mcp.StdioTransport{}`. For a network-reachable server, see Pattern 2 in [`docs/usage-patterns.md`](../../docs/usage-patterns.md).
- **Pick a backend.** Swap `pkg/runtime/gosdk` for `pkg/runtime/mark3labs` with no regeneration — see Pattern 3 in the same doc.
