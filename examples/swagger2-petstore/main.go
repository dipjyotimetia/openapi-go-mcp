// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

// Swagger 2.0 petstore stdio MCP server.
//
// Workflow:
//
//  1. petstore.json is a Swagger 2.0 spec (testdata/petstore-v2.json).
//  2. openapi-go-mcp -emit-v3 converts it to OpenAPI 3 and prunes
//     non-JSON content types, since the rest of the pipeline only handles
//     JSON.
//  3. oapi-codegen generates a typed HTTP client from the converted spec.
//  4. openapi-go-mcp generates the MCP layer (./gen/petmcp).
//  5. This main.go wires the client + MCP layer into a stdio server via the
//     official go-sdk adapter.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dipjyotimetia/openapi-go-mcp/examples/swagger2-petstore/gen/pet"
	"github.com/dipjyotimetia/openapi-go-mcp/examples/swagger2-petstore/gen/petmcp"
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

	raw, s := gosdk.NewServer("swagger2-petstore-mcp", "0.1.0")
	petmcp.RegisterSwaggerPetstoreClient(s, client)

	fmt.Fprintf(os.Stderr, "swagger2-petstore-mcp serving over stdio (upstream: %s)\n", baseURL)
	if err := raw.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server: %v", err)
	}
}
