// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

// Petstore stdio MCP server: exposes the OpenAPI petstore as MCP tools by
// forwarding tool calls to a remote HTTP API through an oapi-codegen client.
//
// Usage:
//
//	# Regenerate the oapi-codegen client and our MCP layer (see ../../README.md)
//	go run ./examples/petstore
//
// Set PETSTORE_BASE_URL to point at any conforming server; defaults to the
// public Swagger demo so the example works out of the box.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dipjyotimetia/openapi-go-mcp/examples/petstore/gen/pet"
	"github.com/dipjyotimetia/openapi-go-mcp/examples/petstore/gen/petmcp"
	"github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime/gosdk"
)

func main() {
	baseURL := os.Getenv("PETSTORE_BASE_URL")
	if baseURL == "" {
		baseURL = "https://petstore.swagger.io/v2"
	}

	client, err := pet.NewClientWithResponses(baseURL)
	if err != nil {
		log.Fatalf("create petstore client: %v", err)
	}

	raw, s := gosdk.NewServer("petstore-mcp", "0.1.0")
	petmcp.RegisterSwaggerPetstoreClient(s, client)

	fmt.Fprintf(os.Stderr, "petstore-mcp serving over stdio (upstream: %s)\n", baseURL)
	if err := raw.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server: %v", err)
	}
}
