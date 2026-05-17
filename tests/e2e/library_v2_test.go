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

	"github.com/dipjyotimetia/openapi-go-mcp/examples/library/gen/library"
	"github.com/dipjyotimetia/openapi-go-mcp/examples/library/gen/librarymcp"
	"github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime/gosdk"
)

// libraryUpstream is the mock HTTP server for the Swagger-2.0-derived
// library API. Its existence is itself a test artefact: it only works if
// the v2 -> v3 conversion preserved every operation's path, method, and
// schema shape.
type libraryUpstream struct {
	calls []upstreamCall
}

func (u *libraryUpstream) record(r *http.Request) {
	u.calls = append(u.calls, captureRequest(r))
}

func newLibraryUpstream(t *testing.T) (string, *libraryUpstream) {
	t.Helper()
	u := &libraryUpstream{}
	mux := http.NewServeMux()

	mux.HandleFunc("/books", func(w http.ResponseWriter, r *http.Request) {
		u.record(r)
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`[{"id":1,"title":"Go Programming","author":{"id":1,"name":"Alice"}}]`))
		case http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":99,"title":"Created","author":{"id":1,"name":"Alice"}}`))
		default:
			http.Error(w, "not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/books/", func(w http.ResponseWriter, r *http.Request) {
		u.record(r)
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			id := strings.TrimPrefix(r.URL.Path, "/books/")
			_, _ = w.Write([]byte(`{"id":` + id + `,"title":"Looked up"}`))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/authors/", func(w http.ResponseWriter, r *http.Request) {
		u.record(r)
		w.Header().Set("Content-Type", "application/json")
		id := strings.TrimPrefix(r.URL.Path, "/authors/")
		_, _ = w.Write([]byte(`{"id":` + id + `,"name":"Author ` + id + `"}`))
	})

	srv := serveTest(t, mux)
	return srv.URL, u
}

func connectLibraryClient(t *testing.T, upstreamURL string) *mcp.ClientSession {
	t.Helper()

	client, err := library.NewClientWithResponses(upstreamURL)
	if err != nil {
		t.Fatalf("library client: %v", err)
	}

	raw, s := gosdk.NewServer("library-mcp-e2e", "test")
	librarymcp.RegisterLibraryAPIClient(s, client)

	serverT, clientT := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = raw.Run(ctx, serverT) }()

	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "library-e2e", Version: "test"}, nil)
	cs, err := mcpClient.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestE2E_Library_ListTools_V2Converted(t *testing.T) {
	url, _ := newLibraryUpstream(t)
	cs := connectLibraryClient(t, url)

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
	for _, want := range []string{"listBooks", "createBook", "getBook", "deleteBook", "getAuthor"} {
		if !got[want] {
			t.Errorf("missing tool %q in v2-derived spec; got %v", want, keys(got))
		}
	}
}

func TestE2E_Library_ListBooks_HeaderForwarded(t *testing.T) {
	url, upstream := newLibraryUpstream(t)
	cs := connectLibraryClient(t, url)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "listBooks",
		Arguments: map[string]any{
			"query":  map[string]any{"limit": 50},
			"header": map[string]any{"X-API-Key": "secret"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(upstream.calls) != 1 {
		t.Fatalf("upstream calls = %d, want 1", len(upstream.calls))
	}
	c := upstream.calls[0]
	if c.Method != "GET" || c.Path != "/books" {
		t.Errorf("upstream = %s %s, want GET /books", c.Method, c.Path)
	}
	if !strings.Contains(c.Query, "limit=50") {
		t.Errorf("query missing limit=50: %q", c.Query)
	}
	if c.Header.Get("X-Api-Key") != "secret" {
		t.Errorf("X-API-Key header = %q, want secret (case-insensitive)", c.Header.Get("X-Api-Key"))
	}
}

func TestE2E_Library_CreateBook_BodyForwardedFromV2(t *testing.T) {
	url, upstream := newLibraryUpstream(t)
	cs := connectLibraryClient(t, url)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "createBook",
		Arguments: map[string]any{
			"header": map[string]any{"X-API-Key": "secret"},
			"body":   map[string]any{"title": "New Book", "authorId": 1},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %s", textOf(res))
	}
	if !strings.Contains(textOf(res), `"id":99`) {
		t.Errorf("expected created id in result, got %q", textOf(res))
	}

	if len(upstream.calls) != 1 {
		t.Fatalf("upstream calls = %d, want 1", len(upstream.calls))
	}
	c := upstream.calls[0]
	if c.Method != "POST" || c.Path != "/books" {
		t.Errorf("upstream = %s %s, want POST /books", c.Method, c.Path)
	}
	var sent map[string]any
	if err := json.Unmarshal(c.Body, &sent); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if sent["title"] != "New Book" {
		t.Errorf("body title = %v, want New Book", sent["title"])
	}
	if sent["authorId"] != float64(1) {
		t.Errorf("body authorId = %v, want 1", sent["authorId"])
	}
}

func TestE2E_Library_GetBook_PathInt64(t *testing.T) {
	url, upstream := newLibraryUpstream(t)
	cs := connectLibraryClient(t, url)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "getBook",
		Arguments: map[string]any{"path": map[string]any{"bookId": 42}},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if upstream.calls[0].Path != "/books/42" {
		t.Errorf("upstream path = %s, want /books/42", upstream.calls[0].Path)
	}
}

func TestE2E_Library_DeleteBook(t *testing.T) {
	url, upstream := newLibraryUpstream(t)
	cs := connectLibraryClient(t, url)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "deleteBook",
		Arguments: map[string]any{"path": map[string]any{"bookId": 7}},
	}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if upstream.calls[0].Method != "DELETE" || upstream.calls[0].Path != "/books/7" {
		t.Errorf("upstream = %+v, want DELETE /books/7", upstream.calls[0])
	}
}

func TestE2E_Library_GetAuthor(t *testing.T) {
	url, upstream := newLibraryUpstream(t)
	cs := connectLibraryClient(t, url)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "getAuthor",
		Arguments: map[string]any{"path": map[string]any{"authorId": 3}},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if upstream.calls[0].Path != "/authors/3" {
		t.Errorf("upstream path = %s, want /authors/3", upstream.calls[0].Path)
	}
}
