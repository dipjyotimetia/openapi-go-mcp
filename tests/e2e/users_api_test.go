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
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dipjyotimetia/openapi-go-mcp/examples/users-api/gen/users"
	"github.com/dipjyotimetia/openapi-go-mcp/examples/users-api/gen/usersmcp"
	"github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime/gosdk"
)

// usersUpstream is a small mock HTTP server that records every request it sees
// and serves canned JSON responses for the users-api routes.
type usersUpstream struct {
	calls []upstreamCall
}

func newUsersUpstream(t *testing.T) (string, *usersUpstream) {
	t.Helper()
	u := &usersUpstream{}
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		u.record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	mux.HandleFunc("/users", func(w http.ResponseWriter, r *http.Request) {
		u.record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"00000000-0000-0000-0000-000000000001","email":"a@example.com","name":"Alice"}]`))
	})

	mux.HandleFunc("/users/", func(w http.ResponseWriter, r *http.Request) {
		u.record(r)
		// Path can be /users/{id} or /users/{id}/posts/{postId}.
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/posts/"):
			_, _ = w.Write([]byte(`{"id":42,"title":"Hello","body":"World"}`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			_, _ = w.Write([]byte(`{"id":"11111111-1111-1111-1111-111111111111","email":"b@example.com","name":"Bob","role":"admin"}`))
		}
	})

	srv := serveTest(t, mux)
	return srv.URL, u
}

func (u *usersUpstream) record(r *http.Request) {
	u.calls = append(u.calls, captureRequest(r))
}

func connectUsersClient(t *testing.T, upstreamURL string) *mcp.ClientSession {
	t.Helper()

	client, err := users.NewClientWithResponses(upstreamURL)
	if err != nil {
		t.Fatalf("users client: %v", err)
	}

	raw, s := gosdk.NewServer("users-mcp-e2e", "test")
	usersmcp.RegisterUsersAPIClient(s, client)

	serverT, clientT := mcp.NewInMemoryTransports()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = raw.Run(ctx, serverT) }()

	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "users-e2e", Version: "test"}, nil)
	cs, err := mcpClient.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestE2E_Users_ListTools(t *testing.T) {
	url, _ := newUsersUpstream(t)
	cs := connectUsersClient(t, url)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	got := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	for _, want := range []string{"getHealth", "listUsers", "getUser", "replaceUser", "patchUser", "deleteUser", "getUserPost"} {
		if !got[want] {
			t.Errorf("missing tool %q (got %v)", want, keys(got))
		}
	}
}

func TestE2E_Users_GetHealth_NoParams(t *testing.T) {
	url, upstream := newUsersUpstream(t)
	cs := connectUsersClient(t, url)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "getHealth"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %s", textOf(res))
	}
	if got := textOf(res); !strings.Contains(got, `"status":"ok"`) {
		t.Errorf("body = %q", got)
	}
	if len(upstream.calls) != 1 || upstream.calls[0].Method != "GET" || upstream.calls[0].Path != "/health" {
		t.Errorf("upstream = %+v, want GET /health", upstream.calls)
	}
}

func TestE2E_Users_GetUser_UUIDPath(t *testing.T) {
	url, upstream := newUsersUpstream(t)
	cs := connectUsersClient(t, url)

	const uid = "550e8400-e29b-41d4-a716-446655440000"

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "getUser",
		Arguments: map[string]any{"path": map[string]any{"userId": uid}},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(upstream.calls) != 1 {
		t.Fatalf("expected 1 upstream call, got %d", len(upstream.calls))
	}
	c := upstream.calls[0]
	if c.Method != "GET" {
		t.Errorf("method = %s, want GET", c.Method)
	}
	wantPath := "/users/" + uid
	if c.Path != wantPath {
		t.Errorf("path = %s, want %s", c.Path, wantPath)
	}
}

func TestE2E_Users_GetUserPost_MultiPathParams(t *testing.T) {
	url, upstream := newUsersUpstream(t)
	cs := connectUsersClient(t, url)

	const uid = "550e8400-e29b-41d4-a716-446655440000"

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "getUserPost",
		Arguments: map[string]any{
			"path": map[string]any{"userId": uid, "postId": 42},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(upstream.calls) != 1 {
		t.Fatalf("expected 1 upstream call, got %d", len(upstream.calls))
	}
	c := upstream.calls[0]
	wantPath := "/users/" + uid + "/posts/42"
	if c.Path != wantPath {
		t.Errorf("path = %s, want %s", c.Path, wantPath)
	}
}

func TestE2E_Users_ListUsers_RequiredHeader(t *testing.T) {
	url, upstream := newUsersUpstream(t)
	cs := connectUsersClient(t, url)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "listUsers",
		Arguments: map[string]any{
			"query":  map[string]any{"limit": 10},
			"header": map[string]any{"X-Tenant-Id": "acme"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(upstream.calls) != 1 {
		t.Fatalf("expected 1 upstream call, got %d", len(upstream.calls))
	}
	c := upstream.calls[0]
	if got := c.Header.Get("X-Tenant-Id"); got != "acme" {
		t.Errorf("X-Tenant-Id header = %q, want acme", got)
	}
	if !strings.Contains(c.Query, "limit=10") {
		t.Errorf("query missing limit=10: %q", c.Query)
	}
}

func TestE2E_Users_ReplaceUser_PUT_BodyAndIfMatch(t *testing.T) {
	url, upstream := newUsersUpstream(t)
	cs := connectUsersClient(t, url)

	const uid = "550e8400-e29b-41d4-a716-446655440000"

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "replaceUser",
		Arguments: map[string]any{
			"path":   map[string]any{"userId": uid},
			"header": map[string]any{"If-Match": `"etag-123"`},
			"body": map[string]any{
				"email": "new@example.com",
				"name":  "New Name",
				"role":  "admin",
			},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(upstream.calls) != 1 {
		t.Fatalf("expected 1 upstream call, got %d", len(upstream.calls))
	}
	c := upstream.calls[0]
	if c.Method != "PUT" {
		t.Errorf("method = %s, want PUT", c.Method)
	}
	if c.Header.Get("If-Match") != `"etag-123"` {
		t.Errorf("If-Match header = %q, want etag-123", c.Header.Get("If-Match"))
	}
	var sent map[string]any
	if err := json.Unmarshal(c.Body, &sent); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if sent["email"] != "new@example.com" || sent["name"] != "New Name" {
		t.Errorf("body = %v", sent)
	}
}

func TestE2E_Users_PatchUser(t *testing.T) {
	url, upstream := newUsersUpstream(t)
	cs := connectUsersClient(t, url)

	const uid = "550e8400-e29b-41d4-a716-446655440000"

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "patchUser",
		Arguments: map[string]any{
			"path": map[string]any{"userId": uid},
			"body": map[string]any{"role": "guest"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if upstream.calls[0].Method != "PATCH" {
		t.Errorf("method = %s, want PATCH", upstream.calls[0].Method)
	}
	var sent map[string]any
	if err := json.Unmarshal(upstream.calls[0].Body, &sent); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if sent["role"] != "guest" {
		t.Errorf("body = %v", sent)
	}
}

func TestE2E_Users_DeleteUser(t *testing.T) {
	url, upstream := newUsersUpstream(t)
	cs := connectUsersClient(t, url)

	const uid = "550e8400-e29b-41d4-a716-446655440000"

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "deleteUser",
		Arguments: map[string]any{"path": map[string]any{"userId": uid}},
	}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if upstream.calls[0].Method != "DELETE" || upstream.calls[0].Path != "/users/"+uid {
		t.Errorf("upstream = %+v, want DELETE /users/%s", upstream.calls[0], uid)
	}
}

func TestE2E_Users_GetUser_BadUUID_ReturnsToolError(t *testing.T) {
	url, upstream := newUsersUpstream(t)
	cs := connectUsersClient(t, url)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "getUser",
		Arguments: map[string]any{"path": map[string]any{"userId": "not-a-uuid"}},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError for bad UUID, got OK: %s", textOf(res))
	}
	if len(upstream.calls) != 0 {
		t.Errorf("upstream should not have been called on invalid UUID, got %d calls", len(upstream.calls))
	}
}
