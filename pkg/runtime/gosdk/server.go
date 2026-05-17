// Copyright 2026 Dipjyoti Metia.
// Portions copyright 2025 Redpanda Data, Inc. (adapter shape adapted from
// redpanda-data/protoc-gen-go-mcp, Apache-2.0).
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

// Package gosdk adapts the official modelcontextprotocol/go-sdk to the
// MCP-library-agnostic runtime.MCPServer interface.
package gosdk

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime"
)

type adapter struct {
	s *mcp.Server
}

// Wrap exposes an existing *mcp.Server as a runtime.MCPServer.
func Wrap(s *mcp.Server) runtime.MCPServer { return &adapter{s: s} }

// NewServer creates a new go-sdk Server and returns it alongside the
// runtime.MCPServer adapter. Use the raw *mcp.Server for transport setup
// (e.g. s.Run with mcp.StdioTransport) and the adapter for tool registration.
func NewServer(name, version string) (*mcp.Server, runtime.MCPServer) {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    name,
		Version: version,
	}, nil)
	return s, Wrap(s)
}

func (a *adapter) AddTool(tool runtime.Tool, handler runtime.ToolHandler) {
	mt := &mcp.Tool{
		Name:        tool.Name,
		Description: tool.Description,
		InputSchema: tool.RawInputSchema,
	}
	if len(tool.RawOutputSchema) > 0 {
		mt.OutputSchema = tool.RawOutputSchema
	}

	a.s.AddTool(mt, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := runtime.DecodeArguments(req.Params.Arguments)
		if err != nil {
			// Mirror the mark3labs path: malformed input becomes an
			// IsError tool result so the LLM can self-correct instead of
			// the transport surfacing a protocol error the client may not
			// surface back.
			toolResult, _ := runtime.HandleError(err)
			return toMCPResult(toolResult), nil
		}

		result, err := handler(ctx, &runtime.CallToolRequest{Arguments: args})
		if err != nil {
			return nil, err
		}
		if result == nil {
			return nil, nil
		}
		return toMCPResult(result), nil
	})
}

func toMCPResult(result *runtime.CallToolResult) *mcp.CallToolResult {
	if result == nil {
		return nil
	}
	out := &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: result.Text}},
		StructuredContent: result.StructuredContent,
		IsError:           result.IsError,
	}
	if meta := runtime.BuildHTTPMeta(result); meta != nil {
		out.Meta = mcp.Meta{
			runtime.HTTPMetaKey: meta,
		}
	}
	return out
}
