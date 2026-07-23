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
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	Name           string
	Description    string
	RawInputSchema json.RawMessage
	// StrictInputSchema marks an OpenAI strict-schema registration. Runtime
	// schema augmentations must retain strict invariants for this surface.
	StrictInputSchema bool
	RawOutputSchema   json.RawMessage
	// Annotations carries the MCP tool-annotation hints. Nil means "no
	// annotations" — the underlying SDK omits the block entirely.
	Annotations *ToolAnnotations
}

// ToolAnnotations mirrors the MCP specification's tool annotation hints
// independent of any MCP library. Generated code derives them from the
// operation's HTTP method (GET/HEAD → read-only + idempotent, DELETE →
// destructive + idempotent, PUT → idempotent).
//
// ReadOnlyHint and IdempotentHint are plain bools because the protocol
// default for both is false — leaving them unset and setting them to false
// are equivalent. DestructiveHint and OpenWorldHint default to TRUE in the
// protocol, so they are pointers: nil means "unset, use the protocol
// default", a non-nil value is an explicit override (see BoolPtr).
type ToolAnnotations struct {
	// Title is a human-readable display name for the tool.
	Title string
	// ReadOnlyHint, if true, signals the tool does not modify its environment.
	ReadOnlyHint bool
	// IdempotentHint, if true, signals repeated calls with the same arguments
	// have no additional effect. Meaningful only when ReadOnlyHint is false.
	IdempotentHint bool
	// DestructiveHint, if true, signals the tool may perform destructive
	// updates. Meaningful only when ReadOnlyHint is false.
	DestructiveHint *bool
	// OpenWorldHint, if true, signals the tool interacts with an open world of
	// external entities.
	OpenWorldHint *bool
}

// BoolPtr returns a pointer to v. Helper for the *bool annotation hints whose
// protocol default is true and therefore need explicit pointers to override.
func BoolPtr(v bool) *bool { return &v }

// ToolHandler is the callback invoked when an MCP client calls a tool.
// Returning a non-nil error is a protocol error; tool-level failures (HTTP
// errors, validation failures, ...) should be returned as a *CallToolResult
// with IsError set to true.
type ToolHandler func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error)

// CallToolRequest carries the decoded arguments from an MCP tool call.
type CallToolRequest struct {
	Arguments map[string]any
}

// MediaKind labels the native MCP content block a CallToolResult's Binary
// payload should surface as. The empty value means "no media" — the result is
// projected as text/structured content exactly as before the field existed.
type MediaKind string

const (
	// MediaNone marks a result with no native media payload.
	MediaNone MediaKind = ""
	// MediaImage marks Binary as image bytes to surface as MCP ImageContent.
	MediaImage MediaKind = "image"
	// MediaAudio marks Binary as audio bytes to surface as MCP AudioContent.
	MediaAudio MediaKind = "audio"
	// MediaResource marks Binary as an embedded MCP blob resource. It is used
	// for binary types, such as PDFs and video, that MCP has no dedicated block.
	MediaResource MediaKind = "resource"
)

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
	// Binary carries raw (not base64) media bytes when MediaKind is set.
	// Adapters project it into the MCP library's native ImageContent /
	// AudioContent block instead of a TextContent block.
	Binary []byte
	// MediaKind selects which native content block Binary becomes. MediaNone
	// (the zero value) means Binary is ignored and Text is projected as usual.
	MediaKind MediaKind
	// MIMEType is the media type of Binary (e.g. "image/png"), without
	// parameters. Set if and only if MediaKind is set.
	MIMEType string
	// ResourceURI identifies an embedded resource payload. It is set only when
	// MediaKind is MediaResource and is content-addressed to avoid inventing a
	// fetchable upstream URL or leaking upstream credentials to MCP clients.
	ResourceURI string
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

// NewToolResultBinary creates a backwards-compatible base64 text envelope for
// callers that explicitly need one. Generated handlers use
// NewToolResultResource for generic binary HTTP responses instead, so modern
// MCP clients receive an embedded blob resource without a duplicate payload.
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

// NewToolResultImage creates a successful result carrying raw image bytes
// that adapters surface as a native MCP ImageContent block, letting MCP
// clients render the image directly instead of receiving base64 text.
//
// Text and StructuredContent are deliberately left empty: duplicating the
// payload as base64 would double a multi-MB image on the wire, and the data
// already lives in the native content block.
//
// Ownership: the caller transfers ownership of raw — the result holds the
// buffer directly (no defensive copy), matching NewToolResultJSON.
func NewToolResultImage(raw []byte, mimeType string) *CallToolResult {
	return &CallToolResult{Binary: raw, MediaKind: MediaImage, MIMEType: mimeType}
}

// NewToolResultAudio creates a successful result carrying raw audio bytes
// that adapters surface as a native MCP AudioContent block. See
// NewToolResultImage for the ownership and payload-duplication notes.
func NewToolResultAudio(raw []byte, mimeType string) *CallToolResult {
	return &CallToolResult{Binary: raw, MediaKind: MediaAudio, MIMEType: mimeType}
}

// NewToolResultResource creates an embedded blob resource result for a binary
// response that has no dedicated MCP content type. The URI is an opaque,
// content-addressed URN; clients receive the bytes in the same result and do
// not need (or get) a network location for the upstream response.
func NewToolResultResource(raw []byte, mimeType string) *CallToolResult {
	digest := sha256.Sum256(raw)
	return &CallToolResult{
		Binary:      raw,
		MediaKind:   MediaResource,
		MIMEType:    mimeType,
		ResourceURI: "urn:openapi-go-mcp:response:sha256:" + fmt.Sprintf("%x", digest[:]),
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
//   - 2xx with an image/* or audio/* Content-Type: body is surfaced as a
//     native MCP ImageContent / AudioContent block (the shape
//     NewToolResultImage / NewToolResultAudio produce) so clients can render
//     it directly. No base64 text duplicate is emitted.
//   - 2xx with any other Content-Type: body is surfaced as an embedded blob
//     resource with an opaque content-addressed URN; Headers/StatusCode are
//     still populated.
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

	class, mediaType := classifyContentType(ct)

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
		case ctImage, ctAudio:
			kind := MediaImage
			if class == ctAudio {
				kind = MediaAudio
			}
			return &CallToolResult{
				Binary:     body,
				MediaKind:  kind,
				MIMEType:   mediaType,
				StatusCode: status,
				Headers:    headers,
			}
		}
		resource := NewToolResultResource(body, mediaType)
		resource.StatusCode = status
		resource.Headers = headers
		return resource
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
	ctImage
	ctAudio
)

// classifyContentType parses ct via mime.ParseMediaType and bucketises it,
// returning the class alongside the parsed parameter-free media type. JSON
// covers the canonical "application/json" plus "+json" suffix variants (e.g.
// application/problem+json) — the suffix check runs before the image/audio
// prefixes, so a hypothetical image/foo+json stays JSON. Text covers any
// text/* media type, which the result helper surfaces verbatim instead of
// base64-encoding. Image and audio cover image/* (including image/svg+xml)
// and audio/*, which surface as native MCP content blocks. Everything else
// falls through to base64 binary. Invalid / empty content-types fall into
// ctOther.
func classifyContentType(ct string) (contentClass, string) {
	if ct == "" {
		return ctOther, ""
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
		return ctJSON, mediaType
	case strings.HasPrefix(mediaType, "text/"):
		return ctText, mediaType
	case strings.HasPrefix(mediaType, "image/"):
		return ctImage, mediaType
	case strings.HasPrefix(mediaType, "audio/"):
		return ctAudio, mediaType
	}
	return ctOther, mediaType
}
