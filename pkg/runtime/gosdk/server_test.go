// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package gosdk

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime"
)

func TestCallTool_RejectsInvalidSchemaArgumentsBeforeHandler(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"name": {"$ref": "#/$defs/name"}},
		"required": ["name"],
		"additionalProperties": false,
		"$defs": {"name": {"type": "string"}}
	}`)
	validator := runtime.CompileInputValidator(schema)
	var calls atomic.Int32
	handler := func(context.Context, *runtime.CallToolRequest) (*runtime.CallToolResult, error) {
		calls.Add(1)
		return runtime.NewToolResultText("handler should not run"), nil
	}

	for _, args := range []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"name": 7}`),
		json.RawMessage(`{"name": "ok", "extra": true}`),
	} {
		result, err := callTool(context.Background(), validator, handler, args)
		if err != nil {
			t.Fatalf("callTool(%s): %v", args, err)
		}
		if !result.IsError {
			t.Errorf("callTool(%s) IsError = false, want true", args)
		}
		text, ok := result.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatalf("callTool(%s) content = %T, want *mcp.TextContent", args, result.Content[0])
		}
		if text.Text == "" {
			t.Errorf("callTool(%s) returned an empty validation error", args)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(text.Text), &payload); err != nil {
			t.Fatalf("callTool(%s) error is not JSON: %v", args, err)
		}
		if payload["code"] != "invalid_arguments" {
			t.Errorf("callTool(%s) error code = %v, want invalid_arguments", args, payload["code"])
		}
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("handler calls = %d, want 0", got)
	}
}

func TestToMCPResult_ImageMedia(t *testing.T) {
	raw := []byte{0x89, 'P', 'N', 'G'}
	res := toMCPResult(runtime.NewToolResultImage(raw, "image/png"))

	if len(res.Content) != 1 {
		t.Fatalf("content blocks: got %d, want 1", len(res.Content))
	}
	img, ok := res.Content[0].(*mcp.ImageContent)
	if !ok {
		t.Fatalf("content block: got %T, want *mcp.ImageContent", res.Content[0])
	}
	// gosdk takes raw bytes and base64-encodes on the wire itself.
	if string(img.Data) != string(raw) {
		t.Errorf("Data: got %v, want raw bytes %v", img.Data, raw)
	}
	if img.MIMEType != "image/png" {
		t.Errorf("MIMEType: got %q", img.MIMEType)
	}
	if res.IsError {
		t.Errorf("media result should not be an error")
	}
}

func TestToMCPResult_AudioMedia(t *testing.T) {
	raw := []byte{0xFF, 0xFB}
	res := toMCPResult(runtime.NewToolResultAudio(raw, "audio/mpeg"))

	aud, ok := res.Content[0].(*mcp.AudioContent)
	if !ok {
		t.Fatalf("content block: got %T, want *mcp.AudioContent", res.Content[0])
	}
	if string(aud.Data) != string(raw) || aud.MIMEType != "audio/mpeg" {
		t.Errorf("unexpected audio block: %+v", aud)
	}
}

func TestToMCPResult_EmbeddedResource(t *testing.T) {
	raw := []byte("%PDF")
	res := toMCPResult(runtime.NewToolResultResource(raw, "application/pdf"))
	resource, ok := res.Content[0].(*mcp.EmbeddedResource)
	if !ok || resource.Resource == nil {
		t.Fatalf("content block = %#v", res.Content[0])
	}
	if string(resource.Resource.Blob) != string(raw) || resource.Resource.MIMEType != "application/pdf" || resource.Resource.URI == "" {
		t.Errorf("resource = %#v", resource.Resource)
	}
}

func TestToMCPResult_TextUnchanged(t *testing.T) {
	res := toMCPResult(runtime.NewToolResultText("hi"))
	txt, ok := res.Content[0].(*mcp.TextContent)
	if !ok || txt.Text != "hi" {
		t.Errorf("non-media results must stay TextContent, got %T", res.Content[0])
	}
}

func TestToMCPResult_MediaKeepsHTTPMeta(t *testing.T) {
	in := runtime.NewToolResultImage([]byte{1}, "image/png")
	in.StatusCode = 200
	in.Headers = map[string]string{"ETag": `"x"`}
	res := toMCPResult(in)
	if res.Meta[runtime.HTTPMetaKey] == nil {
		t.Errorf("HTTP meta must survive media projection")
	}
}
