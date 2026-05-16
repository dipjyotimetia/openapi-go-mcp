// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package runtime

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"sort"
	"strings"
)

// DecodeField JSON-roundtrips args[key] into out. out must be a non-nil
// pointer to a struct or primitive. Returns a *ToolError on failure so
// HandleError can render a useful message.
func DecodeField(args map[string]any, key string, out any) error {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil
	}
	buf, err := json.Marshal(raw)
	if err != nil {
		return &ToolError{
			Status:  400,
			Code:    "invalid_argument",
			Message: fmt.Sprintf("marshal %q: %v", key, err),
		}
	}
	if err := json.Unmarshal(buf, out); err != nil {
		return &ToolError{
			Status:  400,
			Code:    "invalid_argument",
			Message: fmt.Sprintf("decode %q: %v", key, err),
		}
	}
	return nil
}

// DecodeBody is a convenience wrapper for the conventional "body" key.
func DecodeBody(args map[string]any, out any) error {
	return DecodeField(args, "body", out)
}

// DecodePathParam JSON-decodes args["path"][name] into out.
func DecodePathParam(args map[string]any, name string, out any) error {
	path, _ := args["path"].(map[string]any)
	if path == nil {
		if out != nil {
			return &ToolError{
				Status:  400,
				Code:    "missing_path_param",
				Message: fmt.Sprintf("missing path parameter %q", name),
			}
		}
		return nil
	}
	v, ok := path[name]
	if !ok {
		return &ToolError{
			Status:  400,
			Code:    "missing_path_param",
			Message: fmt.Sprintf("missing path parameter %q", name),
		}
	}
	buf, err := json.Marshal(v)
	if err != nil {
		return &ToolError{Status: 400, Code: "invalid_path_param", Message: err.Error()}
	}
	if err := json.Unmarshal(buf, out); err != nil {
		return &ToolError{Status: 400, Code: "invalid_path_param", Message: fmt.Sprintf("decode path %q: %v", name, err)}
	}
	return nil
}

// DecodeQueryParams JSON-decodes args["query"] into out (typically a pointer
// to the oapi-codegen-generated <Op>Params struct).
func DecodeQueryParams(args map[string]any, out any) error {
	return DecodeField(args, "query", out)
}

// DecodeHeaderParams JSON-decodes args["header"] into out.
func DecodeHeaderParams(args map[string]any, out any) error {
	return DecodeField(args, "header", out)
}

// DecodeParamsCombined JSON-decodes the union of args["query"] and
// args["header"] into out. oapi-codegen emits a single <Op>Params struct
// covering both groups, so generated handlers can use this helper to populate
// it in one call.
func DecodeParamsCombined(args map[string]any, out any) error {
	merged := map[string]any{}
	if q, ok := args["query"].(map[string]any); ok {
		for k, v := range q {
			merged[k] = v
		}
	}
	if h, ok := args["header"].(map[string]any); ok {
		for k, v := range h {
			merged[k] = v
		}
	}
	if len(merged) == 0 {
		return nil
	}
	buf, err := json.Marshal(merged)
	if err != nil {
		return &ToolError{Status: 400, Code: "invalid_argument", Message: err.Error()}
	}
	if err := json.Unmarshal(buf, out); err != nil {
		return &ToolError{Status: 400, Code: "invalid_argument", Message: "decode params: " + err.Error()}
	}
	return nil
}

// multipartFilePartContentType is the default per-part Content-Type written
// for a multipart file field when the OpenAPI `encoding[field].contentType`
// did not supply one. The generator passes a RequestFilePart per file so
// per-field overrides win when present.
const multipartFilePartContentType = "application/octet-stream"

// RequestFilePart describes one binary field of a multipart body. The
// generator emits a slice literal of these from each operation's request
// body so the runtime knows which JSON-pointer paths to base64-decode and
// which Content-Disposition / Content-Type to write per part.
type RequestFilePart struct {
	// Path is the JSON-pointer into the body object that locates the
	// base64-encoded string (e.g. "/attachment", "/user/avatar").
	Path string
	// FieldName overrides the multipart form field name. When empty, the
	// runtime derives it from the last segment of Path.
	FieldName string
	// ContentType overrides the part's Content-Type. When empty, the runtime
	// uses multipartFilePartContentType.
	ContentType string
}

// BuildMultipartBody encodes args["body"] (a JSON object) as a multipart/form-data
// payload. Properties whose JSON-pointer path matches a RequestFilePart are
// base64-decoded into a file part (with the supplied FieldName / ContentType
// overrides applied); all other properties become plain form fields
// (string values pass through, non-string values are JSON-encoded).
//
// File parts may live at top-level (Path "/avatar") or nested inside an
// object (Path "/user/avatar"). Nested file paths are extracted from the
// surrounding object before that object is written as a form field; if the
// extraction leaves the object empty it is omitted entirely. Arrays of binary
// items are not supported in v1.
//
// Properties are emitted in sorted key order so generated tests can assert on
// the part list without relying on Go map iteration. The returned content type
// carries the boundary the multipart writer chose.
//
// A missing or empty body produces a valid empty multipart payload — callers
// rely on the MCP input schema to enforce required-ness.
func BuildMultipartBody(args map[string]any, fileFields []RequestFilePart) (string, io.Reader, error) {
	byPath := make(map[string]RequestFilePart, len(fileFields))
	nestedByTopKey := make(map[string][]string)
	for _, fp := range fileFields {
		byPath[fp.Path] = fp
		if topKey, rest, ok := splitTopSegment(fp.Path); ok && rest != "" {
			nestedByTopKey[topKey] = append(nestedByTopKey[topKey], fp.Path)
		}
	}

	body, err := bodyAsObject(args)
	if err != nil {
		return "", nil, err
	}

	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, k := range keys {
		v := body[k]
		if fp, isFile := byPath["/"+k]; isFile {
			if err := writeFilePart(mw, k, v, fp); err != nil {
				return "", nil, err
			}
			continue
		}
		if nestedPaths, hasNested := nestedByTopKey[k]; hasNested {
			obj, ok := v.(map[string]any)
			if !ok {
				return "", nil, &ToolError{
					Status:  400,
					Code:    "invalid_body",
					Message: fmt.Sprintf("nested file path under %q expects an object, got %T", k, v),
				}
			}
			sort.Strings(nestedPaths)
			for _, np := range nestedPaths {
				fp := byPath[np]
				val, found := extractNestedValue(obj, np)
				if !found {
					continue
				}
				if err := writeFilePart(mw, lastPathSegment(np), val, fp); err != nil {
					return "", nil, err
				}
			}
			if len(obj) > 0 {
				if err := writeFormField(mw, k, obj); err != nil {
					return "", nil, err
				}
			}
			continue
		}
		if err := writeFormField(mw, k, v); err != nil {
			return "", nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return "", nil, &ToolError{Status: 500, Code: "multipart_close", Message: err.Error()}
	}
	return mw.FormDataContentType(), &buf, nil
}

// splitTopSegment splits a JSON-pointer path "/top/rest…" into top + rest.
// Returns ok=false when the input doesn't start with "/" or has no top
// segment. rest may be empty for a single-segment path.
func splitTopSegment(path string) (top, rest string, ok bool) {
	if len(path) == 0 || path[0] != '/' {
		return "", "", false
	}
	top, rest, _ = strings.Cut(path[1:], "/")
	return top, rest, true
}

// lastPathSegment returns the segment after the final "/" of a JSON pointer.
// Used as the default multipart form-field name for a nested file path.
func lastPathSegment(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// extractNestedValue descends into obj following the JSON-pointer path and
// removes the leaf value, also pruning intermediate maps that become empty.
// Returns (nil, false) if any segment is missing or has the wrong type.
func extractNestedValue(obj map[string]any, path string) (any, bool) {
	// Strip the leading "/" then split. We expect path to live under obj —
	// the caller has already stripped the top-level segment by walking into
	// obj, but the path we received still contains it ("/top/rest…") so we
	// drop both the leading slash AND the top segment here.
	_, rest, ok := splitTopSegment(path)
	if !ok {
		return nil, false
	}
	return descendAndPrune(obj, rest)
}

// descendAndPrune walks remaining path segments (slash-separated, no leading
// slash) through nested map[string]any nodes, deletes the leaf, and prunes
// any now-empty intermediate maps.
func descendAndPrune(node map[string]any, rest string) (any, bool) {
	if rest == "" {
		return nil, false
	}
	head, tail, hasMore := strings.Cut(rest, "/")
	v, ok := node[head]
	if !ok {
		return nil, false
	}
	if !hasMore {
		delete(node, head)
		return v, true
	}
	child, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	result, found := descendAndPrune(child, tail)
	if found && len(child) == 0 {
		delete(node, head)
	}
	return result, found
}

// BuildBase64BytesBody reads args["body"] as a base64-encoded string and
// returns its decoded bytes as an io.Reader, suitable for an
// application/octet-stream request body.
func BuildBase64BytesBody(args map[string]any) (io.Reader, error) {
	s, ok, err := bodyAsString(args)
	if err != nil {
		return nil, err
	}
	if !ok {
		return bytes.NewReader(nil), nil
	}
	decoded, decodeErr := base64.StdEncoding.DecodeString(s)
	if decodeErr != nil {
		return nil, &ToolError{
			Status:  400,
			Code:    "invalid_body",
			Message: "decode body as base64: " + decodeErr.Error(),
		}
	}
	return bytes.NewReader(decoded), nil
}

// BuildStringBody reads args["body"] as a string and returns it as an
// io.Reader, suitable for text/* and other raw-string request bodies.
func BuildStringBody(args map[string]any) (io.Reader, error) {
	s, _, err := bodyAsString(args)
	if err != nil {
		return nil, err
	}
	return strings.NewReader(s), nil
}

// bodyAsObject extracts args["body"] as a JSON object. A missing or nil body
// is reported as the empty map, not an error, matching DecodeBody semantics.
func bodyAsObject(args map[string]any) (map[string]any, error) {
	raw, ok := args["body"]
	if !ok || raw == nil {
		return map[string]any{}, nil
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, &ToolError{
			Status:  400,
			Code:    "invalid_body",
			Message: fmt.Sprintf("body must be an object, got %T", raw),
		}
	}
	return obj, nil
}

// bodyAsString extracts args["body"] as a string. The bool return distinguishes
// "absent" (false) from "present and empty" (true) so callers can pick the
// right zero-value behaviour. A non-string body is rejected.
func bodyAsString(args map[string]any) (string, bool, error) {
	raw, ok := args["body"]
	if !ok || raw == nil {
		return "", false, nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", false, &ToolError{
			Status:  400,
			Code:    "invalid_body",
			Message: fmt.Sprintf("body must be a string, got %T", raw),
		}
	}
	return s, true, nil
}

// writeFilePart writes a single multipart file part. The value must be a
// base64-encoded string; arbitrary JSON types are rejected so that schema
// drift surfaces loudly rather than encoding the JSON literal as part bytes.
// fp's FieldName and ContentType override the defaults derived from name /
// multipartFilePartContentType.
func writeFilePart(mw *multipart.Writer, name string, v any, fp RequestFilePart) error {
	s, ok := v.(string)
	if !ok {
		return &ToolError{
			Status:  400,
			Code:    "invalid_body",
			Message: fmt.Sprintf("file field %q must be a base64 string, got %T", name, v),
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return &ToolError{
			Status:  400,
			Code:    "invalid_body",
			Message: fmt.Sprintf("decode file field %q as base64: %v", name, err),
		}
	}
	fieldName := fp.FieldName
	if fieldName == "" {
		fieldName = name
	}
	contentType := fp.ContentType
	if contentType == "" {
		contentType = multipartFilePartContentType
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name=%q; filename=%q`, fieldName, fieldName))
	header.Set("Content-Type", contentType)
	part, err := mw.CreatePart(header)
	if err != nil {
		return &ToolError{Status: 500, Code: "multipart_part", Message: err.Error()}
	}
	if _, err := part.Write(decoded); err != nil {
		return &ToolError{Status: 500, Code: "multipart_write", Message: err.Error()}
	}
	return nil
}

// writeFormField writes a single multipart form field. Strings pass through;
// everything else is JSON-encoded so structured arguments survive the form
// boundary.
func writeFormField(mw *multipart.Writer, name string, v any) error {
	var serialised string
	switch x := v.(type) {
	case nil:
		serialised = ""
	case string:
		serialised = x
	default:
		buf, err := json.Marshal(x)
		if err != nil {
			return &ToolError{
				Status:  400,
				Code:    "invalid_body",
				Message: fmt.Sprintf("marshal form field %q: %v", name, err),
			}
		}
		serialised = string(buf)
	}
	if err := mw.WriteField(name, serialised); err != nil {
		return &ToolError{Status: 500, Code: "multipart_field", Message: err.Error()}
	}
	return nil
}
