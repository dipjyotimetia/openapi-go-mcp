// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

// Package e2e contains end-to-end tests that exercise the generator's output:
// an OpenAPI spec → oapi-codegen client → generated *.mcp.go → MCP wire
// transport → real MCP client. These tests prove the whole pipeline works on
// each shipped adapter.
package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dipjyotimetia/openapi-go-mcp/examples/petstore/gen/pet"
	"github.com/dipjyotimetia/openapi-go-mcp/examples/petstore/gen/petmcp"
	"github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime/gosdk"
)

// newPetstoreUpstream returns an httptest server implementing the four
// petstore operations, plus a slice of captured requests. The mock is
// intentionally permissive so the test can assert exactly what was sent.
func newPetstoreUpstream(t *testing.T) (*httptest.Server, *[]upstreamCall, *sync.Mutex) {
	t.Helper()
	var (
		mu    sync.Mutex
		calls []upstreamCall
	)
	record := func(r *http.Request) {
		c := captureRequest(r)
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, c)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/pets", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":1,"name":"Rex","tag":"dog"}]`))
		case http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":42,"name":"Created","tag":"new"}`))
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/pets/", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		id := strings.TrimPrefix(r.URL.Path, "/pets/")
		if _, err := strconv.ParseInt(id, 10, 64); err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":` + id + `,"name":"Lookup","tag":"found"}`))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &calls, &mu
}

// connectGoSDKClient wires up an in-process MCP server (the petstore tools)
// and returns an opened client session. Cleanup is registered via t.Cleanup.
func connectGoSDKClient(t *testing.T, upstreamURL string) *mcp.ClientSession {
	t.Helper()

	client, err := pet.NewClientWithResponses(upstreamURL)
	if err != nil {
		t.Fatalf("petstore client: %v", err)
	}

	raw, s := gosdk.NewServer("petstore-mcp-e2e", "test")
	petmcp.RegisterSwaggerPetstoreClient(s, client)

	serverT, clientT := mcp.NewInMemoryTransports()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// The go-sdk's Server.Run blocks until the transport closes, so we run
	// it in a goroutine that cleans up alongside the test.
	go func() {
		_ = raw.Run(ctx, serverT)
	}()

	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "petstore-e2e", Version: "test"}, nil)
	cs, err := mcpClient.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
	})
	return cs
}

func TestE2E_GoSDK_ListTools(t *testing.T) {
	upstream, _, _ := newPetstoreUpstream(t)
	cs := connectGoSDKClient(t, upstream.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	res, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	gotNames := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		gotNames[tool.Name] = true
	}
	for _, want := range []string{"findPets", "findPetByID", "addPet", "deletePet"} {
		if !gotNames[want] {
			t.Errorf("missing tool %q in tools/list response (got %v)", want, keys(gotNames))
		}
	}
}

func TestE2E_GoSDK_FindPetByID_ForwardsPath(t *testing.T) {
	upstream, calls, mu := newPetstoreUpstream(t)
	cs := connectGoSDKClient(t, upstream.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "findPetByID",
		Arguments: map[string]any{"path": map[string]any{"id": 7}},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %v", textOf(res))
	}

	wantBody := `{"id":7,"name":"Lookup","tag":"found"}`
	if got := textOf(res); got != wantBody {
		t.Errorf("tool result body = %q, want %q", got, wantBody)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*calls) != 1 {
		t.Fatalf("expected 1 upstream call, got %d", len(*calls))
	}
	c := (*calls)[0]
	if c.Method != "GET" || c.Path != "/pets/7" {
		t.Errorf("upstream request = %s %s, want GET /pets/7", c.Method, c.Path)
	}
}

func TestE2E_GoSDK_FindPets_ForwardsQuery(t *testing.T) {
	upstream, calls, mu := newPetstoreUpstream(t)
	cs := connectGoSDKClient(t, upstream.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "findPets",
		Arguments: map[string]any{
			"query": map[string]any{
				"limit": 5,
				"tags":  []string{"dog", "cat"},
			},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*calls) != 1 {
		t.Fatalf("expected 1 upstream call, got %d", len(*calls))
	}
	c := (*calls)[0]
	if c.Method != "GET" || c.Path != "/pets" {
		t.Errorf("upstream method/path = %s %s, want GET /pets", c.Method, c.Path)
	}
	if !strings.Contains(c.Query, "limit=5") {
		t.Errorf("query missing limit=5: %q", c.Query)
	}
	if !strings.Contains(c.Query, "tags=") {
		t.Errorf("query missing tags=: %q", c.Query)
	}
}

func TestE2E_GoSDK_AddPet_ForwardsBody(t *testing.T) {
	upstream, calls, mu := newPetstoreUpstream(t)
	cs := connectGoSDKClient(t, upstream.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "addPet",
		Arguments: map[string]any{
			"body": map[string]any{"name": "Fido", "tag": "dog"},
		},
	})
	if err != nil {
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
		t.Fatalf("upstream body is not JSON: %v\n%s", err, c.Body)
	}
	if sent["name"] != "Fido" || sent["tag"] != "dog" {
		t.Errorf("upstream body = %v, want name=Fido tag=dog", sent)
	}
}

func TestE2E_GoSDK_DeletePet(t *testing.T) {
	upstream, calls, mu := newPetstoreUpstream(t)
	cs := connectGoSDKClient(t, upstream.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "deletePet",
		Arguments: map[string]any{"path": map[string]any{"id": 99}},
	}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*calls) != 1 || (*calls)[0].Method != "DELETE" || (*calls)[0].Path != "/pets/99" {
		t.Errorf("expected DELETE /pets/99, got %+v", *calls)
	}
}

func TestE2E_GoSDK_MissingPathParam_ReturnsToolError(t *testing.T) {
	upstream, calls, mu := newPetstoreUpstream(t)
	cs := connectGoSDKClient(t, upstream.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "findPetByID",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool returned protocol error (should have been IsError result): %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError result for missing path param, got OK: %s", textOf(res))
	}
	if !strings.Contains(textOf(res), "missing_path_param") {
		t.Errorf("expected missing_path_param in error text, got %q", textOf(res))
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*calls) != 0 {
		t.Errorf("upstream should not have been called on arg-validation failure, got %d calls", len(*calls))
	}
}
