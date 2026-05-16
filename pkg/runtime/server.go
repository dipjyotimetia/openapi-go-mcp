// Copyright 2026 Dipjyoti Metia.
// Portions copyright 2025 Redpanda Data, Inc. (Tool/MCPServer/CallToolResult
// shape adapted from redpanda-data/protoc-gen-go-mcp, Apache-2.0).
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

// Package runtime contains the MCP-library-agnostic abstractions that
// generated code programs against. Concrete MCP libraries (e.g.
// modelcontextprotocol/go-sdk) are wired in via adapter packages under
// pkg/runtime/<adapter>/.
package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
)

// MCPServer is the abstraction adapter packages implement so that generated
// code does not depend on any specific MCP library.
type MCPServer interface {
	AddTool(tool Tool, handler ToolHandler)
}

// Tool describes an MCP tool independent of any MCP library.
type Tool struct {
	Name            string
	Description     string
	RawInputSchema  json.RawMessage
	RawOutputSchema json.RawMessage
}

// ToolHandler is the callback invoked when an MCP client calls a tool.
// Returning a non-nil error is a protocol error; tool-level failures (HTTP
// errors, validation failures, ...) should be returned as a *CallToolResult
// with IsError set to true.
type ToolHandler func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error)

// CallToolRequest carries the decoded arguments from an MCP tool call.
type CallToolRequest struct {
	Arguments map[string]any
}

// CallToolResult is the response from a tool handler.
type CallToolResult struct {
	Text              string
	StructuredContent any
	IsError           bool
}

// NewToolResultText creates a successful text result.
func NewToolResultText(text string) *CallToolResult {
	return &CallToolResult{Text: text}
}

// NewToolResultJSON creates a successful result that carries the same JSON
// payload as both unstructured text content (for backward compatibility) and
// structured content (matching the tool's output schema). The slice is copied
// into StructuredContent so callers may safely reuse the buffer afterwards.
func NewToolResultJSON(jsonBytes []byte) *CallToolResult {
	structured := append(json.RawMessage(nil), jsonBytes...)
	return &CallToolResult{
		Text:              string(jsonBytes),
		StructuredContent: structured,
	}
}

// NewToolResultBinary creates a successful result that carries raw bytes from
// a non-JSON response. The bytes are base64-encoded into Text (so log-style
// clients see something legible) and surface as a structured object
// {"contentType": ..., "base64": ...} for clients that consume
// StructuredContent. Useful for application/octet-stream, application/xml,
// or any other response content type that doesn't naturally decode to JSON.
func NewToolResultBinary(raw []byte, contentType string) *CallToolResult {
	encoded := base64.StdEncoding.EncodeToString(raw)
	return &CallToolResult{
		Text: encoded,
		StructuredContent: map[string]any{
			"contentType": contentType,
			"base64":      encoded,
		},
	}
}

// NewToolResultError creates an error text result.
func NewToolResultError(text string) *CallToolResult {
	return &CallToolResult{Text: text, IsError: true}
}
