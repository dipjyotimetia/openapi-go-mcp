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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// DecodeProxyParam extracts args[group][name] and renders it as a string
// suitable for placing on the wire (URL path segment, query value, header
// value, cookie value). It's the proxy-mode counterpart to the typed
// DecodeXxxParam helpers used by companion mode: rather than populating
// an oapi-codegen struct, it produces a string the generated handler can
// concatenate directly into the outgoing *http.Request.
//
// `group` must be one of "path", "query", "header", "cookie". `required`
// controls behaviour for a missing value: when true, missing → *ToolError;
// when false, missing → ("", false, nil) so the caller skips the field.
//
// Stringification rules (kept simple — proxy mode v1 supports the common
// case, not every OpenAPI parameter-serialisation style):
//   - string → as-is.
//   - bool / number → fmt.Sprint (e.g. true → "true", 3.14 → "3.14").
//   - []any → comma-joined ("a,b,c"). Matches OpenAPI's default `form`
//     style with explode=false for query, `simple` for path/header.
//   - object → JSON-encoded. Covers the rare deepObject case and lets
//     the upstream decode it back if it expects JSON-as-string.
//
// Spec authors who need different serialisations (matrix, pipeDelimited,
// explode=true) should currently use companion mode; proxy-mode support
// is documented in design-decisions §14.
func DecodeProxyParam(args map[string]any, group, name string, required bool) (string, bool, error) {
	g, _ := args[group].(map[string]any)
	if g == nil {
		if required {
			return "", false, &ToolError{
				Status:  400,
				Code:    "missing_" + group + "_param",
				Message: fmt.Sprintf("missing %s parameter %q", group, name),
			}
		}
		return "", false, nil
	}
	v, ok := g[name]
	if !ok || v == nil {
		if required {
			return "", false, &ToolError{
				Status:  400,
				Code:    "missing_" + group + "_param",
				Message: fmt.Sprintf("missing %s parameter %q", group, name),
			}
		}
		return "", false, nil
	}
	s, err := stringifyParam(v)
	if err != nil {
		return "", false, &ToolError{
			Status:  400,
			Code:    "invalid_" + group + "_param",
			Message: fmt.Sprintf("encode %s %q: %v", group, name, err),
			Cause:   err,
		}
	}
	return s, true, nil
}

// stringifyParam renders one decoded JSON value as the wire-side string
// described in DecodeProxyParam's contract. Splitting the rendering out
// keeps DecodeProxyParam focused on the missing-value branch and lets
// tests exercise the rendering rules directly.
func stringifyParam(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case bool:
		if x {
			return "true", nil
		}
		return "false", nil
	case float64, float32, int, int64, int32, uint, uint64, uint32:
		return fmt.Sprint(x), nil
	case json.Number:
		return x.String(), nil
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			s, err := stringifyParam(item)
			if err != nil {
				return "", err
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, ","), nil
	case map[string]any:
		buf, err := json.Marshal(x)
		if err != nil {
			return "", err
		}
		return string(buf), nil
	default:
		// Fall back to JSON encoding — robust for nil and any future
		// numeric type the decoder hands us. nil is uncommon (the caller
		// has already filtered absent keys) but covered defensively.
		buf, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(buf), nil
	}
}

// BuildProxyURL constructs the outgoing request URL by concatenating the
// resolved base URL, the operation path (with {placeholders} already
// substituted by the generated handler), and the optional query values.
// Returns an absolute URL string ready for http.NewRequestWithContext.
//
// The trailing slash on baseURL and the leading slash on opPath are
// normalised: exactly one slash joins them. Spec authors who name their
// operation path "" (legal but rare) get baseURL back unchanged.
func BuildProxyURL(baseURL, opPath string, query url.Values) (string, error) {
	if baseURL == "" {
		return "", fmt.Errorf("base URL is empty (set API_BASE_URL or configure servers[] in the spec)")
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse base URL %q: %w", baseURL, err)
	}
	basePath := strings.TrimRight(u.Path, "/")
	op := opPath
	if op != "" && !strings.HasPrefix(op, "/") {
		op = "/" + op
	}
	u.Path = basePath + op
	if len(query) > 0 {
		q := u.Query()
		for key, values := range query {
			for _, value := range values {
				q.Add(key, value)
			}
		}
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// EncodeJSONBody marshals body into a bytes.Buffer suitable for use as an
// io.Reader on http.NewRequestWithContext. Returns the buffer and the
// content type ("application/json") so the proxy handler can set both in
// one call. Mirrors the BuildXxxBody helpers' signature shape.
func EncodeJSONBody(body any) (io.Reader, string, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, "", &ToolError{
			Status:  400,
			Code:    "invalid_body",
			Message: "encode request body to JSON: " + err.Error(),
			Cause:   err,
		}
	}
	return bytes.NewReader(buf), "application/json", nil
}

// EncodeFormBody url-encodes args["body"] (expected to be a flat
// map[string]any) as application/x-www-form-urlencoded. Non-scalar values
// are stringified via stringifyParam so callers can pass arrays without a
// separate encoding step.
func EncodeFormBody(args map[string]any) (io.Reader, string, error) {
	raw, ok := args["body"].(map[string]any)
	if !ok || raw == nil {
		return strings.NewReader(""), "application/x-www-form-urlencoded", nil
	}
	form := url.Values{}
	// Sort keys for deterministic output — useful when tests inspect the
	// raw request body and for upstream cache keys.
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		s, err := stringifyParam(raw[k])
		if err != nil {
			return nil, "", &ToolError{
				Status:  400,
				Code:    "invalid_body",
				Message: fmt.Sprintf("encode form field %q: %v", k, err),
				Cause:   err,
			}
		}
		form.Set(k, s)
	}
	return strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", nil
}

// ReadResponseBody is a small wrapper that drains resp.Body in one
// allocation, returning ([]byte, error). Exists so the generated handler
// has a single helper call rather than inlined io.ReadAll boilerplate
// that varies across operations.
func ReadResponseBody(resp *http.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, nil
	}
	// Drained body; close errors after io.ReadAll are not actionable.
	defer func() { _ = resp.Body.Close() }()
	return io.ReadAll(resp.Body)
}
