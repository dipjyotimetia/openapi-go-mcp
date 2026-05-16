// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

// todos-mcp is the MCP proxy half of the todos example. It speaks JSON-RPC
// over stdio with an MCP host (Claude Desktop, Cursor, …) and forwards every
// tool call to the standalone todos-server over HTTP via an oapi-codegen
// typed client.
//
// Start the server in another terminal first:
//
//	go run ./examples/todos/server
//
// Then run the proxy (typically launched by an MCP host):
//
//	go run ./examples/todos/mcp
//	TODOS_BASE_URL=http://localhost:9090 go run ./examples/todos/mcp
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dipjyotimetia/openapi-gen-go-mcp/examples/todos/gen/todos"
	"github.com/dipjyotimetia/openapi-gen-go-mcp/examples/todos/gen/todosmcp"
	"github.com/dipjyotimetia/openapi-gen-go-mcp/pkg/runtime/gosdk"
)

func main() {
	baseURL := os.Getenv("TODOS_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}

	client, err := todos.NewClientWithResponses(baseURL)
	if err != nil {
		log.Fatalf("create todos client: %v", err)
	}

	probeUpstream(baseURL)

	raw, s := gosdk.NewServer("todos-mcp", "0.1.0")
	todosmcp.RegisterTodosAPIClient(s, client)

	fmt.Fprintf(os.Stderr, "todos-mcp serving over stdio (upstream: %s)\n", baseURL)
	if err := raw.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// probeUpstream hits /healthz once with a short timeout. A failure is logged
// but does not abort startup: the MCP host has already launched us and a
// transient upstream outage shouldn't take the proxy down.
func probeUpstream(baseURL string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// baseURL is operator-configured via TODOS_BASE_URL; this is a self-test
	// probe, not user-driven SSRF surface.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil) // #nosec G107,G704
	if err != nil {
		fmt.Fprintf(os.Stderr, "todos-mcp: warning: bad upstream URL %q: %v\n", baseURL, err)
		return
	}
	resp, err := http.DefaultClient.Do(req) // #nosec G107,G704
	if err != nil {
		fmt.Fprintf(os.Stderr, "todos-mcp: warning: upstream %s unreachable: %v\n", baseURL, err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "todos-mcp: warning: upstream /healthz returned %d\n", resp.StatusCode)
		return
	}
	fmt.Fprintf(os.Stderr, "todos-mcp: upstream %s reachable\n", baseURL)
}
