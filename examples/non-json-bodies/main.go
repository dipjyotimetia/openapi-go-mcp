// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

// Non-JSON-bodies stdio MCP server: exposes operations that consume
// application/x-www-form-urlencoded, multipart/form-data, application/octet-stream,
// text/plain, and application/xml request bodies. Forwards tool calls to an
// HTTP backend through an oapi-codegen client.
//
// Usage:
//
//	# Point NONJSON_BASE_URL at any conforming server.
//	NONJSON_BASE_URL=http://localhost:8080 go run ./examples/non-json-bodies
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dipjyotimetia/openapi-go-mcp/examples/non-json-bodies/gen/nonjson"
	"github.com/dipjyotimetia/openapi-go-mcp/examples/non-json-bodies/gen/nonjsonmcp"
	"github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime/gosdk"
)

func main() {
	baseURL := os.Getenv("NONJSON_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}

	client, err := nonjson.NewClientWithResponses(baseURL)
	if err != nil {
		log.Fatalf("create nonjson client: %v", err)
	}

	raw, s := gosdk.NewServer("nonjson-bodies-mcp", "0.1.0")
	nonjsonmcp.RegisterNonJSONBodiesClient(s, client)

	fmt.Fprintf(os.Stderr, "nonjson-bodies-mcp serving over stdio (upstream: %s)\n", baseURL)
	if err := raw.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server: %v", err)
	}
}
