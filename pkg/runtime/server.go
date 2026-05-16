// Copyright 2026 Dipjyoti Metia.
// Portions copyright 2025 Redpanda Data, Inc. (Tool/MCPServer/CallToolResult
// shape adapted from redpanda-data/protoc-gen-go-mcp, Apache-2.0).
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

// Package runtime contains the MCP-library-agnostic abstractions that
// generated code programs against. Concrete MCP libraries (e.g.
// modelcontextprotocol/go-sdk) are wired in via adapter packages under
// pkg/runtime/<adapter>/.
package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"mime"
	"net/http"
	"slices"
	"strings"
)

// MCPServer is the abstraction adapter packages implement so that generated
// code does not depend on any specific MCP library.
type MCPServer interface {
	AddTool(tool Tool, handler ToolHandler)
}

// Tool describes an MCP tool independent of any MCP library.
type Tool struct {
	Name            string
	Description     string
	RawInputSchema  json.RawMessage
	RawOutputSchema json.RawMessage
}

// ToolHandler is the callback invoked when an MCP client calls a tool.
// Returning a non-nil error is a protocol error; tool-level failures (HTTP
// errors, validation failures, ...) should be returned as a *CallToolResult
// with IsError set to true.
type ToolHandler func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error)

// CallToolRequest carries the decoded arguments from an MCP tool call.
type CallToolRequest struct {
	Arguments map[string]any
}

// CallToolResult is the response from a tool handler.
//
// StatusCode and Headers carry HTTP metadata from the upstream API call so MCP
// clients can branch on status (e.g. 201 vs 200, 304 cache hits) and read
// caller-relevant headers (Location, ETag, Retry-After). Both fields are zero
// when the result was not produced by an HTTP round-trip, preserving backwards
// compatibility with handlers that build CallToolResult directly.
type CallToolResult struct {
	Text              string
	StructuredContent any
	IsError           bool
	StatusCode        int
	Headers           map[string]string
}

// NewToolResultText creates a successful text result.
func NewToolResultText(text string) *CallToolResult {
	return &CallToolResult{Text: text}
}

// NewToolResultJSON creates a successful result that carries the same JSON
// payload as both unstructured text content (for backward compatibility) and
// structured content (matching the tool's output schema).
//
// Ownership: the caller transfers ownership of jsonBytes — the result holds
// the buffer directly (no defensive copy) so generated handlers don't pay a
// memcpy on every API call. Generated code passes resp.Body which oapi-codegen
// allocates fresh per request, so this is safe in practice.
func NewToolResultJSON(jsonBytes []byte) *CallToolResult {
	return &CallToolResult{
		Text:              string(jsonBytes),
		StructuredContent: json.RawMessage(jsonBytes),
	}
}

// NewToolResultBinary creates a successful result that carries raw bytes from
// a non-JSON response. The bytes are base64-encoded into Text (so log-style
// clients see something legible) and surface as a structured object
// {"contentType": ..., "base64": ...} for clients that consume
// StructuredContent. Useful for application/octet-stream, application/xml,
// or any other response content type that doesn't naturally decode to JSON.
func NewToolResultBinary(raw []byte, contentType string) *CallToolResult {
	encoded := base64.StdEncoding.EncodeToString(raw)
	return &CallToolResult{
		Text: encoded,
		StructuredContent: map[string]any{
			"contentType": contentType,
			"base64":      encoded,
		},
	}
}

// NewToolResultError creates an error text result.
func NewToolResultError(text string) *CallToolResult {
	return &CallToolResult{Text: text, IsError: true}
}

// NewToolResultFromHTTP wraps an upstream HTTP response into a CallToolResult,
// preserving status code and a curated subset of headers so MCP clients can
// distinguish 2xx success / 201 with Location / 204 No Content / non-2xx
// failures.
//
// Decoding strategy:
//   - 2xx with a JSON Content-Type (or "+json" suffix): body is wrapped as
//     structured JSON via the same shape NewToolResultJSON produces.
//   - 2xx with any other Content-Type: body is surfaced as base64-encoded
//     structured content matching NewToolResultBinary, except Headers/StatusCode
//     are populated.
//   - 2xx with an empty body (typical of 204 No Content): success result with
//     no StructuredContent.
//   - Non-2xx: IsError=true and StructuredContent is the envelope
//     {"status":N,"headers":{...},"body":<best-effort decoded>}. Text mirrors
//     the body as a string for log-style clients.
//
// fallbackContentType is consulted only when the upstream Header has no
// Content-Type — useful when the generator knows the spec-declared content
// type but the upstream server omitted the header.
func NewToolResultFromHTTP(status int, header http.Header, body []byte, fallbackContentType string) *CallToolResult {
	ct := ""
	if header != nil {
		ct = header.Get("Content-Type")
	}
	if ct == "" {
		ct = fallbackContentType
	}
	headers := selectResponseHeaders(header)

	class := classifyContentType(ct)

	if status >= 200 && status < 300 {
		if len(body) == 0 {
			return &CallToolResult{StatusCode: status, Headers: headers}
		}
		switch class {
		case ctJSON:
			return &CallToolResult{
				Text:              string(body),
				StructuredContent: json.RawMessage(body),
				StatusCode:        status,
				Headers:           headers,
			}
		case ctText:
			// text/* surfaces verbatim — base64 would obscure the content for
			// the LLM consumer.
			return &CallToolResult{
				Text:              string(body),
				StructuredContent: map[string]any{"contentType": ct, "text": string(body)},
				StatusCode:        status,
				Headers:           headers,
			}
		}
		encoded := base64.StdEncoding.EncodeToString(body)
		return &CallToolResult{
			Text: encoded,
			StructuredContent: map[string]any{
				"contentType": ct,
				"base64":      encoded,
			},
			StatusCode: status,
			Headers:    headers,
		}
	}

	// Non-2xx: render as an error envelope so the LLM can branch on it.
	envelope := map[string]any{
		"status":  status,
		"headers": headers,
	}
	if len(body) > 0 {
		if class == ctJSON {
			var decoded any
			if err := json.Unmarshal(body, &decoded); err == nil {
				envelope["body"] = decoded
			} else {
				envelope["body"] = string(body)
			}
		} else {
			envelope["body"] = string(body)
		}
	}
	return &CallToolResult{
		Text:              string(body),
		StructuredContent: envelope,
		IsError:           true,
		StatusCode:        status,
		Headers:           headers,
	}
}

// allowedResponseHeaders is the static set of upstream response headers that
// NewToolResultFromHTTP propagates verbatim. Headers matching X-* (case-
// insensitive) are also propagated up to a size cap so common idioms like
// X-RateLimit-Remaining and X-Request-ID survive the round-trip.
//
// The list is intentionally short: every header surfaced becomes a stable
// part of the tool result shape, so growing it is a compatibility commitment.
var allowedResponseHeaders = map[string]struct{}{
	"location":            {},
	"etag":                {},
	"last-modified":       {},
	"cache-control":       {},
	"content-type":        {},
	"content-disposition": {},
	"content-language":    {},
	"retry-after":         {},
	"www-authenticate":    {},
	"link":                {},
}

// maxExtraXHeaders caps the number of X-* headers surfaced from a single
// response. Past this we silently drop the remainder rather than letting an
// adversarial / chatty upstream balloon the MCP result.
const maxExtraXHeaders = 32

// maxHeaderValueLen truncates a single header value before it lands on the
// MCP result. Caps a hostile upstream from amplifying a multi-MB header into
// every tool result for the call.
const maxHeaderValueLen = 4096

func selectResponseHeaders(h http.Header) map[string]string {
	if len(h) == 0 {
		return nil
	}
	// Iterate in sorted order so X-* truncation is deterministic and tests
	// don't flake on Go's randomised map order.
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	var out map[string]string
	xCount := 0
	for _, k := range keys {
		v := h[k]
		if len(v) == 0 {
			continue
		}
		lower := strings.ToLower(k)
		_, allowed := allowedResponseHeaders[lower]
		if !allowed {
			if !strings.HasPrefix(lower, "x-") || xCount >= maxExtraXHeaders {
				continue
			}
			xCount++
		}
		val := v[0]
		if len(val) > maxHeaderValueLen {
			val = val[:maxHeaderValueLen]
		}
		if out == nil {
			out = map[string]string{}
		}
		out[k] = val
	}
	return out
}

// contentClass labels a response body's content-type for NewToolResultFromHTTP's
// branching. Classification runs once per response via classifyContentType.
type contentClass int

const (
	ctOther contentClass = iota
	ctJSON
	ctText
)

// classifyContentType parses ct via mime.ParseMediaType and bucketises it.
// JSON covers the canonical "application/json" plus "+json" suffix variants
// (e.g. application/problem+json). Text covers any text/* media type, which
// the result helper surfaces verbatim instead of base64-encoding. Everything
// else falls through to base64 binary. Invalid / empty content-types fall
// into ctOther.
func classifyContentType(ct string) contentClass {
	if ct == "" {
		return ctOther
	}
	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil {
		// mime.ParseMediaType rejects whitespace-only or malformed values.
		// Best-effort: lowercase the whole string and hope the caller's
		// upstream is just careless about parameters.
		mediaType = strings.ToLower(strings.TrimSpace(ct))
	}
	switch {
	case mediaType == "application/json", strings.HasSuffix(mediaType, "+json"):
		return ctJSON
	case strings.HasPrefix(mediaType, "text/"):
		return ctText
	}
	return ctOther
}
