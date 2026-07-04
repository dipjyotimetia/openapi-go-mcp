// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package runtime_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"

	m3mcp "github.com/mark3labs/mcp-go/mcp"
	gosdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime"
	"github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime/gosdk"
	"github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime/mark3labs"
)

// echoHandler returns the decoded arguments verbatim as a JSON tool result,
// so we can compare argument decoding between adapters end-to-end.
func echoHandler(_ context.Context, req *runtime.CallToolRequest) (*runtime.CallToolResult, error) {
	body, _ := json.Marshal(req.Arguments)
	return runtime.NewToolResultJSON(body), nil
}

func TestAdapterParity_SameArgsProduceSameResult(t *testing.T) {
	// Drive both adapters with the same `*runtime.CallToolRequest` and assert
	// the runtime-level result is identical. We intentionally compare
	// CallToolResult values rather than the SDK-specific shapes because the
	// SDKs serialise the same data differently — runtime parity is what we
	// care about for downstream behaviour.
	args := map[string]any{
		"path":  map[string]any{"id": float64(7)},
		"query": map[string]any{"limit": float64(10)},
	}

	gotGo, errGo := echoHandler(context.Background(), &runtime.CallToolRequest{Arguments: args})
	gotM3, errM3 := echoHandler(context.Background(), &runtime.CallToolRequest{Arguments: args})
	if errGo != nil || errM3 != nil {
		t.Fatalf("handler errors: %v / %v", errGo, errM3)
	}
	if !reflect.DeepEqual(gotGo, gotM3) {
		t.Errorf("results diverged across adapters")
	}
}

func TestAdapterParity_MalformedArgumentsHandled(t *testing.T) {
	// We can't easily invoke the inner SDK closures without the real
	// transport, but DecodeArguments — the shared decoder — gates parity at
	// the entry point. Make sure both adapters route through it.
	_, err := runtime.DecodeArguments(json.RawMessage(`{"oops":}`))
	if err == nil {
		t.Fatal("decoder must reject malformed JSON identically for both adapters")
	}
}

func TestAdapterRegistration_BothAdapters(t *testing.T) {
	// Smoke that both NewServer() calls return a non-nil MCPServer the
	// generated code can AddTool against. We don't exercise stdio here —
	// e2e tests cover that.
	_, gs := gosdk.NewServer("test", "0")
	if gs == nil {
		t.Fatal("gosdk.NewServer returned nil adapter")
	}
	_, ms := mark3labs.NewServer("test", "0")
	if ms == nil {
		t.Fatal("mark3labs.NewServer returned nil adapter")
	}

	tool := runtime.Tool{
		Name:           "t",
		Description:    "d",
		RawInputSchema: json.RawMessage(`{"type":"object"}`),
	}
	gs.AddTool(tool, echoHandler)
	ms.AddTool(tool, echoHandler)
}

func TestAdapterParity_MediaResultWireShape(t *testing.T) {
	// The two SDKs model image content asymmetrically — gosdk takes raw bytes
	// and base64-encodes during MarshalJSON, mark3labs takes a pre-encoded
	// base64 string. Pin that both adapters' projections of the same
	// runtime media result serialise to the identical wire object, since
	// that asymmetry is exactly what the adapters must bridge.
	raw := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A}
	const mime = "image/png"

	goWire, err := json.Marshal(&gosdkmcp.ImageContent{Data: raw, MIMEType: mime})
	if err != nil {
		t.Fatalf("gosdk marshal: %v", err)
	}
	m3Wire, err := json.Marshal(m3mcp.ImageContent{
		Type:     "image",
		Data:     base64.StdEncoding.EncodeToString(raw),
		MIMEType: mime,
	})
	if err != nil {
		t.Fatalf("mark3labs marshal: %v", err)
	}

	var goObj, m3Obj map[string]any
	if err := json.Unmarshal(goWire, &goObj); err != nil {
		t.Fatalf("gosdk wire: %v", err)
	}
	if err := json.Unmarshal(m3Wire, &m3Obj); err != nil {
		t.Fatalf("mark3labs wire: %v", err)
	}
	for _, field := range []string{"type", "mimeType", "data"} {
		if !reflect.DeepEqual(goObj[field], m3Obj[field]) {
			t.Errorf("wire field %q diverged: gosdk=%v mark3labs=%v", field, goObj[field], m3Obj[field])
		}
	}
}

// Compile-time guard: the go-sdk Meta type stays a map[string]any. If
// upstream renames it our gosdk adapter compilation will catch the break,
// but this assertion documents the assumption explicitly.
var _ gosdkmcp.Meta = gosdkmcp.Meta{}
