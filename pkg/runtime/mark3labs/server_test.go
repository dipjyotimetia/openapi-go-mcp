// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package mark3labs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

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

	for _, args := range []any{
		map[string]any{},
		map[string]any{"name": 7},
		map[string]any{"name": "ok", "extra": true},
	} {
		result, err := callTool(context.Background(), validator, handler, args)
		if err != nil {
			t.Fatalf("callTool(%v): %v", args, err)
		}
		if !result.IsError {
			t.Errorf("callTool(%v) IsError = false, want true", args)
		}
		text, ok := result.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatalf("callTool(%v) content = %T, want mcp.TextContent", args, result.Content[0])
		}
		if text.Text == "" {
			t.Errorf("callTool(%v) returned an empty validation error", args)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(text.Text), &payload); err != nil {
			t.Fatalf("callTool(%v) error is not JSON: %v", args, err)
		}
		if payload["code"] != "invalid_arguments" {
			t.Errorf("callTool(%v) error code = %v, want invalid_arguments", args, payload["code"])
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
	img, ok := res.Content[0].(mcp.ImageContent)
	if !ok {
		t.Fatalf("content block: got %T, want mcp.ImageContent", res.Content[0])
	}
	// mark3labs carries the payload as a base64 string.
	if want := base64.StdEncoding.EncodeToString(raw); img.Data != want {
		t.Errorf("Data: got %q, want %q", img.Data, want)
	}
	if img.MIMEType != "image/png" || img.Type != "image" {
		t.Errorf("unexpected image block: %+v", img)
	}
	if res.IsError {
		t.Errorf("media result should not be an error")
	}
}

func TestToMCPResult_AudioMedia(t *testing.T) {
	raw := []byte{0xFF, 0xFB}
	res := toMCPResult(runtime.NewToolResultAudio(raw, "audio/mpeg"))

	aud, ok := res.Content[0].(mcp.AudioContent)
	if !ok {
		t.Fatalf("content block: got %T, want mcp.AudioContent", res.Content[0])
	}
	if want := base64.StdEncoding.EncodeToString(raw); aud.Data != want || aud.MIMEType != "audio/mpeg" || aud.Type != "audio" {
		t.Errorf("unexpected audio block: %+v", aud)
	}
}

func TestToMCPResult_TextUnchanged(t *testing.T) {
	res := toMCPResult(runtime.NewToolResultText("hi"))
	txt, ok := res.Content[0].(mcp.TextContent)
	if !ok || txt.Text != "hi" {
		t.Errorf("non-media results must stay TextContent, got %T", res.Content[0])
	}
}

func TestToMCPResult_MediaKeepsHTTPMeta(t *testing.T) {
	in := runtime.NewToolResultImage([]byte{1}, "image/png")
	in.StatusCode = 200
	in.Headers = map[string]string{"ETag": `"x"`}
	res := toMCPResult(in)
	if res.Meta == nil || res.Meta.AdditionalFields[runtime.HTTPMetaKey] == nil {
		t.Errorf("HTTP meta must survive media projection")
	}
}
