// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package e2e

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/dipjyotimetia/openapi-go-mcp/examples/petstore/gen/pet"
	"github.com/dipjyotimetia/openapi-go-mcp/examples/petstore/gen/petmcp"
	"github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime/mark3labs"
)

// mark3labsHarness wires the same petstore generated code as the gosdk tests,
// but routes MCP messages through mark3labs's MCPServer.HandleMessage. The
// generated code is identical — only the adapter changes — so passing tests
// prove the runtime split is real.
type mark3labsHarness struct {
	server *mcpserver.MCPServer
	nextID atomic.Int64
}

func newMark3labsHarness(t *testing.T, upstreamURL string) *mark3labsHarness {
	t.Helper()
	client, err := pet.NewClientWithResponses(upstreamURL)
	if err != nil {
		t.Fatalf("petstore client: %v", err)
	}
	raw, s := mark3labs.NewServer("petstore-mcp-e2e", "test")
	petmcp.RegisterSwaggerPetstoreClient(s, client)
	return &mark3labsHarness{server: raw}
}

func (h *mark3labsHarness) call(ctx context.Context, method string, params any) (map[string]any, error) {
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      h.nextID.Add(1),
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp := h.server.HandleMessage(ctx, raw)
	if resp == nil {
		return nil, nil
	}
	// JSONRPCMessage is an interface that marshals to the wire form; round-trip
	// to a generic map for inspection.
	buf, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(buf, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func TestE2E_Mark3labs_ListTools(t *testing.T) {
	upstream, _, _ := newPetstoreUpstream(t)
	h := newMark3labsHarness(t, upstream.URL)

	resp, err := h.call(context.Background(), "tools/list", map[string]any{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in tools/list response: %+v", resp)
	}
	tools, _ := result["tools"].([]any)
	if len(tools) == 0 {
		t.Fatalf("no tools returned")
	}

	got := make(map[string]bool, len(tools))
	for _, raw := range tools {
		m, _ := raw.(map[string]any)
		if n, _ := m["name"].(string); n != "" {
			got[n] = true
		}
	}
	for _, want := range []string{"findPets", "findPetByID", "addPet", "deletePet"} {
		if !got[want] {
			t.Errorf("missing tool %q (got %v)", want, keys(got))
		}
	}
}

func TestE2E_Mark3labs_FindPetByID_ForwardsPath(t *testing.T) {
	upstream, calls, mu := newPetstoreUpstream(t)
	h := newMark3labsHarness(t, upstream.URL)

	resp, err := h.call(context.Background(), "tools/call", map[string]any{
		"name":      "findPetByID",
		"arguments": map[string]any{"path": map[string]any{"id": 7}},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	text := mark3labsToolText(t, resp)
	if !strings.Contains(text, `"id":7`) {
		t.Errorf("tool result text missing pet id 7: %q", text)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*calls) != 1 || (*calls)[0].Method != "GET" || (*calls)[0].Path != "/pets/7" {
		t.Errorf("expected GET /pets/7, got %+v", *calls)
	}
}

func TestE2E_Mark3labs_AddPet_ForwardsBody(t *testing.T) {
	upstream, calls, mu := newPetstoreUpstream(t)
	h := newMark3labsHarness(t, upstream.URL)

	if _, err := h.call(context.Background(), "tools/call", map[string]any{
		"name": "addPet",
		"arguments": map[string]any{
			"body": map[string]any{"name": "Rex", "tag": "dog"},
		},
	}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*calls) != 1 {
		t.Fatalf("expected 1 upstream call, got %d", len(*calls))
	}
	c := (*calls)[0]
	if c.Method != "POST" || c.Path != "/pets" {
		t.Errorf("upstream = %s %s, want POST /pets", c.Method, c.Path)
	}
	var sent map[string]any
	if err := json.Unmarshal(c.Body, &sent); err != nil {
		t.Fatalf("upstream body not JSON: %v\n%s", err, c.Body)
	}
	if sent["name"] != "Rex" {
		t.Errorf("upstream body name = %v, want Rex", sent["name"])
	}
}

func TestE2E_Mark3labs_MissingPathParam_ReturnsToolError(t *testing.T) {
	upstream, calls, mu := newPetstoreUpstream(t)
	h := newMark3labsHarness(t, upstream.URL)

	resp, err := h.call(context.Background(), "tools/call", map[string]any{
		"name":      "findPetByID",
		"arguments": map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	result, _ := resp["result"].(map[string]any)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Errorf("expected isError=true, got %+v", result)
	}
	text := mark3labsToolText(t, resp)
	if !strings.Contains(text, "missing_path_param") {
		t.Errorf("expected missing_path_param in error text, got %q", text)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*calls) != 0 {
		t.Errorf("upstream should not have been called, got %d calls", len(*calls))
	}
}

func mark3labsToolText(t *testing.T, resp map[string]any) string {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("response has no result: %+v", resp)
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		return ""
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	return text
}
