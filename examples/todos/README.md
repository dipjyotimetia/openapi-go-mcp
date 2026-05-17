# `examples/todos` — end-to-end MCP example

A realistic end-to-end demo for `openapi-go-mcp`. Two separate binaries:

- **`todos-server`** — a standalone HTTP service that implements [`todos.yaml`](./todos.yaml) on top of an in-memory store. Listens on `:8080` by default, logs every request, exposes `/healthz`, and shuts down gracefully on `SIGINT` / `SIGTERM`.
- **`todos-mcp`** — an MCP proxy that speaks JSON-RPC over **stdio** with an MCP host (Claude Desktop, Cursor, …) and forwards every tool call to `todos-server` over HTTP via an [`oapi-codegen`](https://github.com/oapi-codegen/oapi-codegen) typed client.

```
                                  ┌─────────────────────────────┐
                                  │       todos-server          │
                                  │  (cmd: server/, listens     │
   ┌──── HTTP ─────────────────▶  │   on :8080, in-memory store)│
   │                              │  GET /todos                 │
   │                              │  POST /todos                │
   │                              │  GET/PUT/DELETE /todos/{id} │
   │                              │  GET /healthz               │
   │                              └─────────────────────────────┘
   │
   │   oapi-codegen typed client (gen/todos)
   │
   │
┌──┴──────────────────────────────┐
│           todos-mcp             │           ┌──────────────────┐
│   (cmd: mcp/, MCP proxy)        │  stdio    │   MCP host       │
│                                 │ ◀──────▶  │ (Claude Desktop, │
│   generated MCP layer           │ JSON-RPC  │  Cursor, …)      │
│   (gen/todosmcp)                │           └──────────────────┘
└─────────────────────────────────┘
```

## Run it

Two terminals (the proxy launches as a child of your MCP host in real use; running it manually is just for smoke-testing).

**Terminal 1 — start the backend:**

```bash
go run ./examples/todos/server
# 2026/05/16 21:54:33 todos-server listening on :8080
```

Or build and run:

```bash
go build -o ./bin/todos-server ./examples/todos/server
./bin/todos-server                  # default :8080
./bin/todos-server -addr :9090      # custom port
```

**Terminal 2 — start the MCP proxy** (defaults to `TODOS_BASE_URL=http://localhost:8080`):

```bash
go run ./examples/todos/mcp
# todos-mcp: upstream http://localhost:8080 reachable
# todos-mcp serving over stdio (upstream: http://localhost:8080)
```

The proxy hits `/healthz` once at startup with a 2 s timeout and logs whether the upstream is reachable — a non-fatal warning, so a transient outage doesn't tear down the MCP host.

## Files

| Path | Purpose |
|---|---|
| [`todos.yaml`](./todos.yaml) | OpenAPI 3.0 spec — 5 operations covering path params, query params, JSON request/response bodies. |
| [`server/main.go`](./server/main.go) | Standalone HTTP server: flag parsing, graceful shutdown, request log middleware. |
| [`server/store.go`](./server/store.go) | In-memory store + HTTP handlers. |
| [`mcp/main.go`](./mcp/main.go) | MCP proxy: builds the typed client, probes `/healthz`, registers all tools, serves stdio. |
| [`gen/todos/oapi.yaml`](./gen/todos/oapi.yaml) | `oapi-codegen` config for the typed HTTP client. |
| [`gen/todos/todos.gen.go`](./gen/todos/todos.gen.go) | Generated typed client. |
| [`gen/todosmcp/todosmcp.mcp.go`](./gen/todosmcp/todosmcp.mcp.go) | Generated MCP layer (`RegisterTodosAPIClient`). |

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

Build both binaries:

```bash
go build -o ./bin/todos-server ./examples/todos/server
go build -o ./bin/todos-mcp    ./examples/todos/mcp
```

You must start `todos-server` before (or alongside) the MCP host launching `todos-mcp`. Common approaches:

- Run `./bin/todos-server` in a long-lived terminal / `tmux` / systemd unit.
- Run it under a process supervisor.
- For a one-machine demo, launch it from your shell's startup file.

Replace `/ABS/PATH/TO/REPO` below with the absolute path to your checkout.

### Claude Desktop

`~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows):

```json
{
  "mcpServers": {
    "todos": {
      "command": "/ABS/PATH/TO/REPO/bin/todos-mcp",
      "env": { "TODOS_BASE_URL": "http://localhost:8080" }
    }
  }
}
```

Restart Claude Desktop; you should see five `todos` tools in the picker.

### Claude Code (CLI)

```bash
claude mcp add todos /ABS/PATH/TO/REPO/bin/todos-mcp \
  -e TODOS_BASE_URL=http://localhost:8080
```

Or in `.claude/settings.json` / `~/.claude.json`:

```json
{
  "mcpServers": {
    "todos": {
      "command": "/ABS/PATH/TO/REPO/bin/todos-mcp",
      "type": "stdio",
      "env": { "TODOS_BASE_URL": "http://localhost:8080" }
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
      "command": "/ABS/PATH/TO/REPO/bin/todos-mcp",
      "env": { "TODOS_BASE_URL": "http://localhost:8080" }
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
      "transport": "stdio",
      "env": { "TODOS_BASE_URL": "http://localhost:8080" }
    }
  }
}
```

### Running through `go run` (no build step)

Useful while iterating. Point the host at `go` itself:

```json
{
  "mcpServers": {
    "todos": {
      "command": "go",
      "args": ["run", "./examples/todos/mcp"],
      "cwd": "/ABS/PATH/TO/REPO",
      "env": { "TODOS_BASE_URL": "http://localhost:8080" }
    }
  }
}
```

Slower to start (Go compiles on first launch) but always picks up local edits.

### MCP Inspector

For interactive debugging without wiring up a host (with `todos-server` already running):

```bash
TODOS_BASE_URL=http://localhost:8080 npx @modelcontextprotocol/inspector ./bin/todos-mcp
```

## Point at a different host or port

Override `TODOS_BASE_URL`:

```bash
# Server on a custom port
go run ./examples/todos/server -addr :9090

# Proxy talks to it
TODOS_BASE_URL=http://localhost:9090 go run ./examples/todos/mcp
```

Anything that conforms to [`todos.yaml`](./todos.yaml) will work — point the proxy at a remote server, a staging environment, etc.

## Talk to it without a client

Drive the proxy directly from a shell — useful for smoke tests and CI. Start the server in the background first:

```bash
go build -o ./bin/todos-server ./examples/todos/server
go build -o ./bin/todos-mcp    ./examples/todos/mcp

./bin/todos-server -addr :18080 > /tmp/todos-server.log 2>&1 &
SERVER_PID=$!
sleep 0.5

{ printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}';
  sleep 0.5;
  printf '%s\n' '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}';
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"listTodos","arguments":{}}}';
  sleep 0.5;
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"createTodo","arguments":{"body":{"title":"buy milk"}}}}';
  sleep 0.5; } | TODOS_BASE_URL=http://localhost:18080 ./bin/todos-mcp

kill -TERM $SERVER_PID
cat /tmp/todos-server.log
```

The server log shows every request the proxy forwarded:

```
todos-server listening on :18080
GET    /healthz             200 132µs
GET    /todos               200 96µs
POST   /todos               201 101µs
todos-server shutting down
todos-server stopped
```

## Regenerate from the spec

The two generated directories (`gen/todos`, `gen/todosmcp`) are committed so the example builds without extra tooling, but they're produced from `todos.yaml`. To regenerate after changing the spec:

```bash
# from the repo root
make regen-examples
```

Or just the todos pieces:

```bash
oapi-codegen -config examples/todos/gen/todos/oapi.yaml examples/todos/todos.yaml

go run ./cmd/openapi-go-mcp \
    -spec examples/todos/todos.yaml \
    -out examples/todos/gen/todosmcp \
    -package todosmcp \
    -client-import github.com/dipjyotimetia/openapi-go-mcp/examples/todos/gen/todos
```

The MCP register-function name (`RegisterTodosAPIClient`) is derived from the spec's `info.title` (`Todos API` → `TodosAPI`). Change the title and `mcp/main.go` must follow.

## What to copy from this example

If you're applying this pattern to your own API:

- **The `mcp/main.go` shell is the whole job.** Build the typed client → `NewServer` → `Register…Client` → run transport. About 30 lines once you strip the upstream probe.
- **You will not have a `server/`.** It only exists here so the demo is runnable without a real backend. Delete the directory and point `TODOS_BASE_URL` at your actual API.
- **Health-probe is optional.** [`probeUpstream`](./mcp/main.go) is non-fatal and just gives a clearer stderr line at startup. Drop it if you don't want the dependency on a `/healthz` route.
- **Pick a transport.** This example uses `mcp.StdioTransport{}`. For a network-reachable proxy, see Pattern 2 in [`docs/usage-patterns.md`](../../docs/usage-patterns.md).
- **Pick a backend.** Swap `pkg/runtime/gosdk` for `pkg/runtime/mark3labs` with no regeneration — see Pattern 3 in the same doc.
