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
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dipjyotimetia/openapi-go-mcp/examples/edge-cases/gen/edge"
	"github.com/dipjyotimetia/openapi-go-mcp/examples/edge-cases/gen/edgemcp"
	"github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime/gosdk"
)

// The edge-cases example exists mainly so `make regen-examples` compiles
// companion code against real oapi-codegen output for path-param shapes that
// are not plain builtins (issue #32). These tests additionally prove each
// value reaches the upstream intact.

type edgeUpstream struct {
	mu   sync.Mutex
	last upstreamCall
}

func newEdgeSession(t *testing.T) (*mcp.ClientSession, *edgeUpstream) {
	t.Helper()
	u := &edgeUpstream{}
	srv := serveTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := captureRequest(r)
		call.Path = r.URL.EscapedPath()
		u.mu.Lock()
		u.last = call
		u.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))

	client, err := edge.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatalf("edge client: %v", err)
	}
	raw, s := gosdk.NewServer("edge-mcp-e2e", "test")
	edgemcp.RegisterEdgeCasesAPIClient(s, client)

	serverT, clientT := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = raw.Run(ctx, serverT) }()

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "edge-e2e", Version: "test"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, u
}

func (u *edgeUpstream) lastCall() upstreamCall {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.last
}

func callEdgeTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return res
}

func TestE2E_EdgeCases_PathParamsReachUpstream(t *testing.T) {
	cs, up := newEdgeSession(t)

	cases := []struct {
		tool       string
		args       map[string]any
		wantMethod string
		wantPath   string
		wantQuery  string
	}{
		{
			tool:       "setTicketStatus",
			args:       map[string]any{"path": map[string]any{"status": "Banana"}},
			wantMethod: http.MethodPost,
			wantPath:   "/status/Banana",
		},
		{
			tool: "findPets",
			args: map[string]any{
				"path":  map[string]any{"petStatus": "sold", "level": 2, "ticket-kind": "bug", "kind": "k2"},
				"query": map[string]any{"sort": "desc"},
			},
			wantMethod: http.MethodGet,
			wantPath:   "/pets/sold/levels/2/kinds/bug/k2",
			wantQuery:  "sort=desc",
		},
		{
			tool:       "getBatch",
			args:       map[string]any{"path": map[string]any{"ids": []any{1, 2}, "tags": []any{"red", "blue"}, "states": []any{"available"}}},
			wantMethod: http.MethodGet,
			wantPath:   "/batch/1,2/red,blue/available",
		},
		{
			tool:       "getFormats",
			args:       map[string]any{"path": map[string]any{"small": -5, "unsigned": 4000000000, "raw": "aGk=", "named": "y"}},
			wantMethod: http.MethodGet,
			wantPath:   "/formats/-5/4000000000/aGk=/y",
		},
		{
			tool: "shadowNames",
			args: map[string]any{"path": map[string]any{
				"type": "t", "resp": "r", "body": "b", "json": "j", "cfg": "c", "edge": "e",
			}},
			wantMethod: http.MethodGet,
			wantPath:   "/shadow/t/r/b/j/c/e",
		},
		{
			tool:       "GET_tenants_tenant_things_thing",
			args:       map[string]any{"path": map[string]any{"tenant": "acme", "thing": "rocket"}},
			wantMethod: http.MethodGet,
			wantPath:   "/tenants/acme/things/rocket",
		},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			res := callEdgeTool(t, cs, tc.tool, tc.args)
			if res.IsError {
				t.Fatalf("tool error: %s", textOf(res))
			}
			got := up.lastCall()
			if got.Method != tc.wantMethod || got.Path != tc.wantPath || got.Query != tc.wantQuery {
				t.Errorf("upstream got %s %s?%s, want %s %s?%s",
					got.Method, got.Path, got.Query, tc.wantMethod, tc.wantPath, tc.wantQuery)
			}
		})
	}
}

func TestE2E_EdgeCases_EnumValueOutsideSpecIsRejected(t *testing.T) {
	cs, _ := newEdgeSession(t)
	res := callEdgeTool(t, cs, "setTicketStatus", map[string]any{"path": map[string]any{"status": "Cherry"}})
	if !res.IsError {
		t.Fatalf("expected input validation error for enum value outside the spec, got %s", textOf(res))
	}
}

func TestE2E_EdgeCases_VendorJSONBodies(t *testing.T) {
	cs, up := newEdgeSession(t)

	cases := []struct {
		tool            string
		args            map[string]any
		wantContentType string
	}{
		{
			tool: "patchResource",
			args: map[string]any{
				"path": map[string]any{"id": 7},
				"body": map[string]any{"name": "n", "status": "sold"},
			},
			wantContentType: "application/merge-patch+json",
		},
		{
			// application/json is preferred over application/hal+json.
			tool:            "createDocument",
			args:            map[string]any{"body": map[string]any{"name": "doc"}},
			wantContentType: "application/json",
		},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			res := callEdgeTool(t, cs, tc.tool, tc.args)
			if res.IsError {
				t.Fatalf("tool error: %s", textOf(res))
			}
			got := up.lastCall()
			if ct := got.Header.Get("Content-Type"); ct != tc.wantContentType {
				t.Errorf("Content-Type = %q, want %q", ct, tc.wantContentType)
			}
			var sent, want map[string]any
			if err := json.Unmarshal(got.Body, &sent); err != nil {
				t.Fatalf("upstream body %q: %v", got.Body, err)
			}
			want = tc.args["body"].(map[string]any)
			for k, v := range want {
				if sent[k] != v {
					t.Errorf("body[%q] = %v, want %v (body %s)", k, sent[k], v, got.Body)
				}
			}
		})
	}
}
