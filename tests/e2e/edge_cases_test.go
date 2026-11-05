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
	"net/url"
	"strings"
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
		writeEdgeResponse(w, r.URL.Path)
	}))

	// A client-level editor sets an unrelated cookie; generated cookie
	// handling must preserve it.
	client, err := edge.NewClientWithResponses(srv.URL, edge.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
		r.AddCookie(&http.Cookie{Name: "client", Value: "keep"})
		return nil
	}))
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

func writeEdgeResponse(w http.ResponseWriter, path string) {
	switch path {
	case "/responses/array":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`["a","b"]`))
	case "/responses/scalar":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`42`))
	case "/responses/created":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"name":"new"}`))
	case "/responses/vendor":
		w.Header().Set("Content-Type", "application/vnd.api+json")
		_, _ = w.Write([]byte(`{"name":"vnd"}`))
	case "/responses/text":
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(`hello`))
	default:
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}
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

func TestE2E_EdgeCases_HeadersAndCookies(t *testing.T) {
	cs, up := newEdgeSession(t)

	res := callEdgeTool(t, cs, "getSession", map[string]any{
		"header": map[string]any{
			"X-Request-Id": "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
			"X-Count":      3,
			"X-Mode":       "slow",
			"X-Tags":       []any{"a", "b"},
			"X-Flag":       true,
		},
		"cookie": map[string]any{"session": "abc", "theme": "dark"},
	})
	if res.IsError {
		t.Fatalf("tool error: %s", textOf(res))
	}
	got := up.lastCall()
	for name, want := range map[string]string{
		"X-Request-Id": "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
		"X-Count":      "3",
		"X-Mode":       "slow",
		"X-Tags":       "a,b",
		"X-Flag":       "true",
	} {
		if v := got.Header.Get(name); v != want {
			t.Errorf("header %s = %q, want %q", name, v, want)
		}
	}
	// Exactly one session cookie: oapi-codegen's zero-valued required field
	// must not be sent alongside the tool argument.
	if c := got.Header.Values("Cookie"); len(c) != 1 || c[0] != "client=keep; session=abc; theme=dark" {
		t.Errorf("Cookie = %q, want [\"client=keep; session=abc; theme=dark\"]", c)
	}

	res = callEdgeTool(t, cs, "cookieOnly", map[string]any{"cookie": map[string]any{"session": "only"}})
	if res.IsError {
		t.Fatalf("cookieOnly tool error: %s", textOf(res))
	}
	if c := up.lastCall().Header.Values("Cookie"); len(c) != 1 || c[0] != "client=keep; session=only" {
		t.Errorf("cookieOnly Cookie = %q", c)
	}

	res = callEdgeTool(t, cs, "getSession", map[string]any{
		"header": map[string]any{"X-Request-Id": "not-a-uuid"},
		"cookie": map[string]any{"session": "abc"},
	})
	if !res.IsError {
		t.Errorf("expected validation error for malformed uuid header, got %s", textOf(res))
	}
}

func TestE2E_EdgeCases_ContentParamsAreJSON(t *testing.T) {
	cs, up := newEdgeSession(t)

	sel := map[string]any{"kind": "a b", "limit": 2}
	res := callEdgeTool(t, cs, "search", map[string]any{
		"path":   map[string]any{"selector": sel},
		"query":  map[string]any{"filter": sel},
		"header": map[string]any{"X-Selector": sel},
		"cookie": map[string]any{"pageCursor": sel},
	})
	if res.IsError {
		t.Fatalf("tool error: %s", textOf(res))
	}
	got := up.lastCall()
	assertJSONEqual := func(where, raw string) {
		t.Helper()
		var v map[string]any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Errorf("%s = %q is not JSON: %v", where, raw, err)
			return
		}
		if v["kind"] != "a b" || v["limit"] != float64(2) {
			t.Errorf("%s = %s, want %v", where, raw, sel)
		}
	}
	path, _ := url.PathUnescape(strings.TrimPrefix(got.Path, "/search/"))
	assertJSONEqual("path", path)
	query, _ := url.ParseQuery(got.Query)
	assertJSONEqual("query", query.Get("filter"))
	assertJSONEqual("header", got.Header.Get("X-Selector"))
	req := &http.Request{Header: got.Header}
	cookie, err := req.Cookie("pageCursor")
	if err != nil {
		t.Fatalf("pageCursor cookie: %v", err)
	}
	cv, _ := url.QueryUnescape(cookie.Value)
	assertJSONEqual("cookie", cv)
}

func TestE2E_EdgeCases_ResponseShapes(t *testing.T) {
	cs, _ := newEdgeSession(t)

	cases := []struct {
		tool           string
		wantText       string
		wantStructured bool
	}{
		// MCP structuredContent must be an object: arrays and scalars are text only.
		{tool: "listNames", wantText: `["a","b"]`},
		{tool: "getCount", wantText: `42`},
		{tool: "createOrFetch", wantText: `{"name":"new"}`, wantStructured: true},
		{tool: "getVendor", wantText: `{"name":"vnd"}`, wantStructured: true},
		{tool: "getText", wantText: `hello`, wantStructured: true},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			res := callEdgeTool(t, cs, tc.tool, map[string]any{})
			if res.IsError {
				t.Fatalf("tool error: %s", textOf(res))
			}
			if got := textOf(res); got != tc.wantText {
				t.Errorf("text = %q, want %q", got, tc.wantText)
			}
			if has := res.StructuredContent != nil; has != tc.wantStructured {
				t.Errorf("structuredContent present = %v, want %v (%v)", has, tc.wantStructured, res.StructuredContent)
			}
			if res.StructuredContent != nil {
				if _, ok := res.StructuredContent.(map[string]any); !ok {
					t.Errorf("structuredContent must be an object, got %T", res.StructuredContent)
				}
			}
		})
	}
}
