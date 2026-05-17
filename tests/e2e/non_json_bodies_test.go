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
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dipjyotimetia/openapi-go-mcp/examples/non-json-bodies/gen/nonjson"
	"github.com/dipjyotimetia/openapi-go-mcp/examples/non-json-bodies/gen/nonjsonmcp"
	"github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime/gosdk"
)

// newNonJSONUpstream returns a mock HTTP server covering every non-JSON body
// route in examples/non-json-bodies. Each handler echoes a fixed JSON response
// while the captured upstreamCall lets tests assert on the wire shape — body
// bytes, content-type, multipart parts, form fields, etc.
func newNonJSONUpstream(t *testing.T) (*httptest.Server, *[]upstreamCall, *sync.Mutex) {
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
	mux.HandleFunc("/forms/login", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"session_token":"abc123"}`))
	})
	mux.HandleFunc("/files/upload", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"file-1"}`))
	})
	mux.HandleFunc("/blobs", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.WriteHeader(http.StatusOK)
	})
	// GET /blobs/{id} returns raw bytes — exercises the non-JSON response path.
	mux.HandleFunc("/blobs/", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte{0xCA, 0xFE, 0xBA, 0xBE})
	})
	mux.HandleFunc("/reports/latest", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("latest report contents"))
	})
	mux.HandleFunc("/notes", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/xml-import", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &calls, &mu
}

// connectNonJSONClient wires the non-json-bodies example MCP wrapper around an
// oapi-codegen client pointing at upstreamURL and returns an opened in-memory
// MCP session.
func connectNonJSONClient(t *testing.T, upstreamURL string) *mcp.ClientSession {
	t.Helper()

	client, err := nonjson.NewClientWithResponses(upstreamURL)
	if err != nil {
		t.Fatalf("nonjson client: %v", err)
	}

	raw, s := gosdk.NewServer("nonjson-mcp-e2e", "test")
	nonjsonmcp.RegisterNonJSONBodiesClient(s, client)

	serverT, clientT := mcp.NewInMemoryTransports()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() {
		_ = raw.Run(ctx, serverT)
	}()

	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "nonjson-e2e", Version: "test"}, nil)
	cs, err := mcpClient.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestE2E_NonJSON_ListTools(t *testing.T) {
	upstream, _, _ := newNonJSONUpstream(t)
	cs := connectNonJSONClient(t, upstream.URL)
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
	for _, want := range []string{"submitLogin", "uploadFile", "uploadBlob", "postNote", "importXML"} {
		if !got[want] {
			t.Errorf("missing tool %q in tools/list (got %v)", want, keys(got))
		}
	}
}

func TestE2E_NonJSON_Form_UrlencodedBody(t *testing.T) {
	upstream, calls, mu := newNonJSONUpstream(t)
	cs := connectNonJSONClient(t, upstream.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "submitLogin",
		Arguments: map[string]any{
			"body": map[string]any{
				"username":    "alice",
				"password":    "s3cret",
				"remember_me": true,
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
	if c.Method != "POST" || c.Path != "/forms/login" {
		t.Errorf("upstream = %s %s, want POST /forms/login", c.Method, c.Path)
	}
	if ct := c.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
		t.Errorf("content-type = %q, want application/x-www-form-urlencoded", ct)
	}
	body := string(c.Body)
	for _, want := range []string{"username=alice", "password=s3cret", "remember_me=true"} {
		if !strings.Contains(body, want) {
			t.Errorf("form body missing %q: %s", want, body)
		}
	}
}

func TestE2E_NonJSON_Multipart_FileAndForm(t *testing.T) {
	upstream, calls, mu := newNonJSONUpstream(t)
	cs := connectNonJSONClient(t, upstream.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	fileBytes := []byte{0x01, 0x02, 0x03, 0x04}
	_, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "uploadFile",
		Arguments: map[string]any{
			"body": map[string]any{
				"caption":    "test upload",
				"attachment": base64.StdEncoding.EncodeToString(fileBytes),
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
	if c.Method != "POST" || c.Path != "/files/upload" {
		t.Errorf("upstream = %s %s, want POST /files/upload", c.Method, c.Path)
	}
	mediaType, params, err := mime.ParseMediaType(c.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		t.Fatalf("content-type = %q, want multipart/form-data with boundary", c.Header.Get("Content-Type"))
	}
	mr := multipart.NewReader(strings.NewReader(string(c.Body)), params["boundary"])

	parts := map[string][]byte{}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		buf, _ := io.ReadAll(part)
		parts[part.FormName()] = buf
	}
	if got := string(parts["caption"]); got != "test upload" {
		t.Errorf("caption part = %q, want %q", got, "test upload")
	}
	if got := parts["attachment"]; string(got) != string(fileBytes) {
		t.Errorf("attachment part bytes = % x, want % x", got, fileBytes)
	}
}

func TestE2E_NonJSON_Octet_Base64RoundTrip(t *testing.T) {
	upstream, calls, mu := newNonJSONUpstream(t)
	cs := connectNonJSONClient(t, upstream.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	raw := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	_, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "uploadBlob",
		Arguments: map[string]any{
			"body": base64.StdEncoding.EncodeToString(raw),
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
	if c.Header.Get("Content-Type") != "application/octet-stream" {
		t.Errorf("content-type = %q, want application/octet-stream", c.Header.Get("Content-Type"))
	}
	if string(c.Body) != string(raw) {
		t.Errorf("body bytes = % x, want % x", c.Body, raw)
	}
}

func TestE2E_NonJSON_Text_PlainBody(t *testing.T) {
	upstream, calls, mu := newNonJSONUpstream(t)
	cs := connectNonJSONClient(t, upstream.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "postNote",
		Arguments: map[string]any{"body": "hello world"},
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
	if c.Header.Get("Content-Type") != "text/plain" {
		t.Errorf("content-type = %q, want text/plain", c.Header.Get("Content-Type"))
	}
	if string(c.Body) != "hello world" {
		t.Errorf("body = %q, want %q", c.Body, "hello world")
	}
}

func TestE2E_NonJSON_Response_Binary_Base64Wrapped(t *testing.T) {
	upstream, _, _ := newNonJSONUpstream(t)
	cs := connectNonJSONClient(t, upstream.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "downloadBlob",
		Arguments: map[string]any{
			"path": map[string]any{"id": "abc123"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %v", textOf(res))
	}
	// The non-JSON response wrapper base64-encodes the bytes into Text so
	// MCP clients that only look at content/text get something legible.
	want := base64.StdEncoding.EncodeToString([]byte{0xCA, 0xFE, 0xBA, 0xBE})
	if got := textOf(res); got != want {
		t.Errorf("text content = %q, want %q", got, want)
	}
}

func TestE2E_NonJSON_Response_Text_PlainBody(t *testing.T) {
	upstream, _, _ := newNonJSONUpstream(t)
	cs := connectNonJSONClient(t, upstream.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "getLatestReport"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if got := textOf(res); got != "latest report contents" {
		t.Errorf("text content = %q, want %q", got, "latest report contents")
	}
}

func TestE2E_NonJSON_XML_RawStringBody(t *testing.T) {
	upstream, calls, mu := newNonJSONUpstream(t)
	cs := connectNonJSONClient(t, upstream.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	xml := `<note><body>hi</body></note>`
	_, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "importXML",
		Arguments: map[string]any{"body": xml},
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
	if c.Header.Get("Content-Type") != "application/xml" {
		t.Errorf("content-type = %q, want application/xml", c.Header.Get("Content-Type"))
	}
	if string(c.Body) != xml {
		t.Errorf("body = %q, want %q", c.Body, xml)
	}
}
