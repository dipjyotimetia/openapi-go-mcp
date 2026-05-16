// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

// Todos: a fully self-contained end-to-end example for openapi-gen-go-mcp.
//
// One process does three things:
//
//  1. Starts an in-memory HTTP backend that implements examples/todos/todos.yaml.
//  2. Builds an oapi-codegen typed client that points at that backend.
//  3. Registers every operation as an MCP tool and serves them over stdio.
//
// To run:
//
//	go run ./examples/todos
//
// To point at an external backend instead of the embedded one:
//
//	TODOS_BASE_URL=https://my-todos.example.com go run ./examples/todos
package main

import (
	"context"
	"fmt"
	"log"
	"net/http/httptest"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dipjyotimetia/openapi-gen-go-mcp/examples/todos/gen/todos"
	"github.com/dipjyotimetia/openapi-gen-go-mcp/examples/todos/gen/todosmcp"
	"github.com/dipjyotimetia/openapi-gen-go-mcp/pkg/runtime/gosdk"
)

func main() {
	baseURL := resolveBackend()

	client, err := todos.NewClientWithResponses(baseURL)
	if err != nil {
		log.Fatalf("create todos client: %v", err)
	}

	raw, s := gosdk.NewServer("todos-mcp", "0.1.0")
	todosmcp.RegisterTodosAPIClient(s, client)

	fmt.Fprintf(os.Stderr, "todos-mcp serving over stdio (upstream: %s)\n", baseURL)
	if err := raw.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// resolveBackend returns TODOS_BASE_URL when set; otherwise it starts a
// process-scoped httptest.Server with the bundled in-memory store.
func resolveBackend() string {
	if u := os.Getenv("TODOS_BASE_URL"); u != "" {
		return u
	}
	srv := httptest.NewServer(newTodoStore().handler())
	fmt.Fprintf(os.Stderr, "todos-mcp: started embedded backend at %s\n", srv.URL)
	return srv.URL
}
