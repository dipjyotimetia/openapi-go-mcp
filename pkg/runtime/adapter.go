// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package runtime

import (
	"encoding/json"
	"fmt"
)

// DecodeArguments normalises an SDK-supplied arguments value into the
// map[string]any shape the generated handlers consume. It accepts:
//
//   - nil / empty                       → empty map
//   - map[string]any (mark3labs path)   → returned verbatim
//   - json.RawMessage / []byte (gosdk)  → JSON-unmarshalled
//   - string                            → JSON-unmarshalled (defensive)
//
// Any other shape, or a JSON parse failure, returns a *ToolError so adapters
// can surface a consistent IsError tool result regardless of which SDK
// delivered the request. Centralising this guarantees parity between
// pkg/runtime/gosdk and pkg/runtime/mark3labs (and any future adapter).
func DecodeArguments(raw any) (map[string]any, error) {
	switch v := raw.(type) {
	case nil:
		return map[string]any{}, nil
	case map[string]any:
		return v, nil
	case json.RawMessage:
		if len(v) == 0 {
			return map[string]any{}, nil
		}
		return unmarshalArgs(v)
	case []byte:
		if len(v) == 0 {
			return map[string]any{}, nil
		}
		return unmarshalArgs(v)
	case string:
		if v == "" {
			return map[string]any{}, nil
		}
		return unmarshalArgs([]byte(v))
	default:
		// Last-ditch: JSON-marshal then unmarshal. Catches exotic shapes
		// (e.g. map[string]json.RawMessage) without failing loudly.
		buf, err := json.Marshal(v)
		if err != nil {
			return nil, &ToolError{
				Status:  400,
				Code:    "invalid_arguments",
				Message: fmt.Sprintf("unsupported arguments type %T", raw),
				Cause:   err,
			}
		}
		return unmarshalArgs(buf)
	}
}

func unmarshalArgs(buf []byte) (map[string]any, error) {
	out := map[string]any{}
	if err := json.Unmarshal(buf, &out); err != nil {
		return nil, &ToolError{
			Status:  400,
			Code:    "invalid_arguments",
			Message: "decode arguments: " + err.Error(),
			Cause:   err,
		}
	}
	return out, nil
}

// HTTPMetaKey is the stable JSON-pointer-style key adapters use under the
// underlying SDK's `_meta` channel to carry HTTP status + selected response
// headers. Clients that consume `_meta` can read both without depending on
// the runtime package.
const HTTPMetaKey = "openapi-go-mcp/http"

// HTTPMeta is the shape written under HTTPMetaKey. Adapter packages convert
// this into the SDK's native metadata representation.
type HTTPMeta struct {
	StatusCode int               `json:"status,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
}

// BuildHTTPMeta produces an HTTPMeta value from a CallToolResult, or nil if
// the result carries no HTTP metadata (zero status and no headers). Adapter
// packages call this when projecting a runtime result onto the SDK type.
func BuildHTTPMeta(result *CallToolResult) *HTTPMeta {
	if result == nil {
		return nil
	}
	if result.StatusCode == 0 && len(result.Headers) == 0 {
		return nil
	}
	return &HTTPMeta{
		StatusCode: result.StatusCode,
		Headers:    result.Headers,
	}
}
