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

	"github.com/dipjyotimetia/openapi-go-mcp/examples/complex/gen/complex"
	"github.com/dipjyotimetia/openapi-go-mcp/examples/complex/gen/complexmcp"
	"github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime/gosdk"
)

type complexUpstream struct {
	calls []upstreamCall
}

func (u *complexUpstream) record(r *http.Request) { u.calls = append(u.calls, captureRequest(r)) }

func newComplexUpstream(t *testing.T) (string, *complexUpstream) {
	t.Helper()
	u := &complexUpstream{}
	mux := http.NewServeMux()

	mux.HandleFunc("/threads", func(w http.ResponseWriter, r *http.Request) {
		u.record(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		// Echo the request body so the test can verify round-trip semantics.
		_, _ = w.Write(u.calls[len(u.calls)-1].Body)
	})
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		u.record(r)
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("/profiles", func(w http.ResponseWriter, r *http.Request) {
		u.record(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(u.calls[len(u.calls)-1].Body)
	})
	mux.HandleFunc("/items/", func(w http.ResponseWriter, r *http.Request) {
		u.record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"00000000-0000-0000-0000-000000000001","status":"active"}`))
	})

	srv := serveTest(t, mux)
	return srv.URL, u
}

func connectComplexClient(t *testing.T, upstreamURL string) *mcp.ClientSession {
	t.Helper()

	client, err := complex.NewClientWithResponses(upstreamURL)
	if err != nil {
		t.Fatalf("complex client: %v", err)
	}

	raw, s := gosdk.NewServer("complex-mcp-e2e", "test")
	complexmcp.RegisterComplexSchemasAPIClient(s, client)

	serverT, clientT := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = raw.Run(ctx, serverT) }()

	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "complex-e2e", Version: "test"}, nil)
	cs, err := mcpClient.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// schemaFor returns the InputSchema of the named tool, parsed into a generic
// JSON tree, or fails the test if the tool isn't found.
func schemaFor(t *testing.T, cs *mcp.ClientSession, name string) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range res.Tools {
		if tool.Name != name {
			continue
		}
		buf, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("marshal tool %q schema: %v", name, err)
		}
		var out map[string]any
		if err := json.Unmarshal(buf, &out); err != nil {
			t.Fatalf("unmarshal tool %q schema: %v", name, err)
		}
		return out
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func TestE2E_Complex_RecursiveSchema_UsesDollarRef(t *testing.T) {
	url, _ := newComplexUpstream(t)
	cs := connectComplexClient(t, url)

	sch := schemaFor(t, cs, "createThread")
	defs, ok := sch["$defs"].(map[string]any)
	if !ok {
		t.Fatalf("expected $defs in createThread schema, got %v", sch)
	}
	comment, ok := defs["Comment"].(map[string]any)
	if !ok {
		t.Fatalf("expected $defs/Comment, got %v", defs)
	}
	props, _ := comment["properties"].(map[string]any)
	children, _ := props["children"].(map[string]any)
	items, _ := children["items"].(map[string]any)
	if items["$ref"] != "#/$defs/Comment" {
		t.Errorf("expected recursive $ref to #/$defs/Comment, got %v", items)
	}
}

func TestE2E_Complex_OneOf_PreservedInStandardMode(t *testing.T) {
	url, _ := newComplexUpstream(t)
	cs := connectComplexClient(t, url)

	sch := schemaFor(t, cs, "submitEvent")
	defs, _ := sch["$defs"].(map[string]any)
	event, _ := defs["Event"].(map[string]any)
	branches, ok := event["oneOf"].([]any)
	if !ok || len(branches) != 2 {
		t.Fatalf("expected 2-branch oneOf for Event, got %v", event)
	}
}

func TestE2E_Complex_AllOf_PreservedInStandardMode(t *testing.T) {
	url, _ := newComplexUpstream(t)
	cs := connectComplexClient(t, url)

	sch := schemaFor(t, cs, "createProfile")
	defs, _ := sch["$defs"].(map[string]any)
	ext, _ := defs["ExtendedProfile"].(map[string]any)
	branches, ok := ext["allOf"].([]any)
	if !ok || len(branches) == 0 {
		t.Fatalf("expected allOf branches on ExtendedProfile, got %v", ext)
	}
}

func TestE2E_Complex_GetItem_EnumAndFormats(t *testing.T) {
	url, _ := newComplexUpstream(t)
	cs := connectComplexClient(t, url)

	sch := schemaFor(t, cs, "getItem")
	props, _ := sch["properties"].(map[string]any)
	pathGroup, _ := props["path"].(map[string]any)
	pathProps, _ := pathGroup["properties"].(map[string]any)
	itemID, _ := pathProps["itemId"].(map[string]any)
	if itemID["format"] != "uuid" {
		t.Errorf("expected uuid format on itemId, got %v", itemID["format"])
	}

	queryGroup, _ := props["query"].(map[string]any)
	queryProps, _ := queryGroup["properties"].(map[string]any)
	status, _ := queryProps["status"].(map[string]any)
	enum, _ := status["enum"].([]any)
	if len(enum) != 3 {
		t.Errorf("expected 3-value enum on status, got %v", status)
	}

	since, _ := queryProps["since"].(map[string]any)
	if since["format"] != "date-time" {
		t.Errorf("expected date-time format on since, got %v", since)
	}
}

func TestE2E_Complex_CreateThread_RecursiveBodyForwarded(t *testing.T) {
	url, upstream := newComplexUpstream(t)
	cs := connectComplexClient(t, url)

	const rootID = "11111111-1111-1111-1111-111111111111"
	const childID = "22222222-2222-2222-2222-222222222222"

	tree := map[string]any{
		"id":   rootID,
		"body": "root comment",
		"children": []any{
			map[string]any{
				"id":   childID,
				"body": "reply",
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "createThread",
		Arguments: map[string]any{"body": tree},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %s", textOf(res))
	}

	if len(upstream.calls) != 1 {
		t.Fatalf("upstream calls = %d, want 1", len(upstream.calls))
	}
	var sent map[string]any
	if err := json.Unmarshal(upstream.calls[0].Body, &sent); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if sent["id"] != rootID {
		t.Errorf("upstream root id = %v, want %s", sent["id"], rootID)
	}
	children, _ := sent["children"].([]any)
	if len(children) != 1 {
		t.Fatalf("expected 1 child in upstream body, got %d", len(children))
	}
	child, _ := children[0].(map[string]any)
	if child["id"] != childID {
		t.Errorf("upstream child id = %v, want %s", child["id"], childID)
	}
}

func TestE2E_Complex_GetItem_EnumQueryForwarded(t *testing.T) {
	url, upstream := newComplexUpstream(t)
	cs := connectComplexClient(t, url)

	const itemID = "33333333-3333-3333-3333-333333333333"

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "getItem",
		Arguments: map[string]any{
			"path":  map[string]any{"itemId": itemID},
			"query": map[string]any{"status": "archived"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if upstream.calls[0].Path != "/items/"+itemID {
		t.Errorf("path = %s, want /items/%s", upstream.calls[0].Path, itemID)
	}
	if !strings.Contains(upstream.calls[0].Query, "status=archived") {
		t.Errorf("query missing status=archived: %q", upstream.calls[0].Query)
	}
}
