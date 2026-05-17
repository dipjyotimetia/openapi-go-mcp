// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

// Same petstore example as ../petstore, but wired through the mark3labs/mcp-go
// adapter instead of the official go-sdk — demonstrating that the generated
// *.mcp.go is MCP-library-agnostic.
package main

import (
	"fmt"
	"log"
	"os"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/dipjyotimetia/openapi-go-mcp/examples/petstore/gen/pet"
	"github.com/dipjyotimetia/openapi-go-mcp/examples/petstore/gen/petmcp"
	"github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime/mark3labs"
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

	raw, s := mark3labs.NewServer("petstore-mcp", "0.1.0")
	petmcp.RegisterSwaggerPetstoreClient(s, client)

	fmt.Fprintf(os.Stderr, "petstore-mcp (mark3labs) serving over stdio (upstream: %s)\n", baseURL)
	if err := mcpserver.ServeStdio(raw); err != nil {
		log.Fatalf("server: %v", err)
	}
}
