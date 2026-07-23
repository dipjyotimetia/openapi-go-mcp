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
// Stringification rules are deliberately kept for backwards compatibility
// with callers that do not have OpenAPI parameter metadata. Generated proxy
// handlers use SerializeProxyParam instead.
//
// Stringification rules:
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

// ProxyParamSpec is the wire-relevant subset of an OpenAPI Parameter Object.
// Style and Explode must already contain their OpenAPI defaults; the
// generator resolves those defaults while collecting operations so the
// runtime is also usable by dynamic registration without needing the source
// document.
type ProxyParamSpec struct {
	Name          string
	In            string
	Style         string
	Explode       bool
	AllowReserved bool
}

// ProxyQuery is a deterministic collection of query pairs. It is separate
// from url.Values because url.Values always percent-encodes reserved
// characters, while OpenAPI's allowReserved permits them to remain literal.
type ProxyQuery []ProxyQueryValue

// ProxyQueryValue is one query pair emitted by SerializeProxyParam.
type ProxyQueryValue struct {
	Key           string
	Value         string
	AllowReserved bool
}

// Encode returns the query string without a leading question mark. Entries
// retain their supplied order; generated operations and object members are
// sorted before creating the collection, making the result deterministic.
func (q ProxyQuery) Encode() string {
	if len(q) == 0 {
		return ""
	}
	parts := make([]string, 0, len(q))
	for _, pair := range q {
		parts = append(parts, encodeQueryComponent(pair.Key, false)+"="+encodeQueryComponent(pair.Value, pair.AllowReserved))
	}
	return strings.Join(parts, "&")
}

// ProxyParam is a serialized parameter. Value is used by path, header, and
// cookie parameters; Query holds every query pair, including repeated names
// from exploded arrays and the independent keys of exploded objects.
type ProxyParam struct {
	Value   string
	Query   ProxyQuery
	Cookies []ProxyCookie
}

// ProxyCookie is one cookie pair emitted by an OpenAPI form-style cookie
// parameter. Exploded arrays may repeat a cookie name; exploded objects use
// their property names as cookie names.
type ProxyCookie struct{ Name, Value string }

// SerializeProxyParam extracts and serializes one OpenAPI parameter using
// the OpenAPI 3 style/explode rules supported at the parameter's location.
// It returns a ToolError for missing or malformed tool input, so generated
// handlers can pass errors directly to HandleError.
func SerializeProxyParam(args map[string]any, spec ProxyParamSpec, required bool) (ProxyParam, bool, error) {
	v, present, err := proxyParamValue(args, spec.In, spec.Name, required)
	if err != nil || !present {
		return ProxyParam{}, present, err
	}
	param, err := serializeProxyParam(v, spec)
	if err != nil {
		return ProxyParam{}, false, &ToolError{
			Status:  400,
			Code:    "invalid_" + spec.In + "_param",
			Message: fmt.Sprintf("encode %s %q: %v", spec.In, spec.Name, err),
			Cause:   err,
		}
	}
	return param, true, nil
}

func proxyParamValue(args map[string]any, group, name string, required bool) (any, bool, error) {
	g, _ := args[group].(map[string]any)
	if g == nil {
		if required {
			return nil, false, missingProxyParamError(group, name)
		}
		return nil, false, nil
	}
	v, ok := g[name]
	if !ok || v == nil {
		if required {
			return nil, false, missingProxyParamError(group, name)
		}
		return nil, false, nil
	}
	return v, true, nil
}

func missingProxyParamError(group, name string) error {
	return &ToolError{
		Status:  400,
		Code:    "missing_" + group + "_param",
		Message: fmt.Sprintf("missing %s parameter %q", group, name),
	}
}

func serializeProxyParam(v any, spec ProxyParamSpec) (ProxyParam, error) {
	if err := validateProxyParamStyle(spec); err != nil {
		return ProxyParam{}, err
	}
	if spec.In == "path" {
		return ProxyParam{Value: serializePathParam(v, spec)}, nil
	}
	switch spec.Style {
	case "simple":
		return ProxyParam{Value: serializeSimple(v, spec.Explode)}, nil
	case "label":
		return ProxyParam{Value: serializeLabel(v, spec.Explode)}, nil
	case "matrix":
		return ProxyParam{Value: serializeMatrix(spec.Name, v, spec.Explode)}, nil
	case "form":
		return serializeForm(spec, v)
	case "spaceDelimited":
		return serializeDelimited(spec, v, " ")
	case "pipeDelimited":
		return serializeDelimited(spec, v, "|")
	case "deepObject":
		return serializeDeepObject(spec, v)
	default:
		return ProxyParam{}, fmt.Errorf("unsupported style %q", spec.Style)
	}
}

func validateProxyParamStyle(spec ProxyParamSpec) error {
	valid := false
	switch spec.In {
	case "path":
		valid = spec.Style == "simple" || spec.Style == "label" || spec.Style == "matrix"
	case "query":
		valid = spec.Style == "form" || spec.Style == "spaceDelimited" || spec.Style == "pipeDelimited" || spec.Style == "deepObject"
	case "header":
		valid = spec.Style == "simple"
	case "cookie":
		valid = spec.Style == "form"
	}
	if !valid {
		return fmt.Errorf("style %q is not valid for %s parameters", spec.Style, spec.In)
	}
	if spec.Style == "deepObject" && !spec.Explode {
		return fmt.Errorf("deepObject requires explode=true")
	}
	return nil
}

func serializeSimple(v any, explode bool) string {
	if items, ok := proxyArray(v); ok {
		return strings.Join(items, ",")
	}
	if fields, ok := proxyObject(v); ok {
		if explode {
			return strings.Join(objectParts(fields, "="), ",")
		}
		return strings.Join(objectParts(fields, ","), ",")
	}
	return proxyScalar(v)
}

func serializeLabel(v any, explode bool) string {
	if items, ok := proxyArray(v); ok {
		return "." + strings.Join(items, ".")
	}
	if fields, ok := proxyObject(v); ok {
		if explode {
			return "." + strings.Join(objectParts(fields, "="), ".")
		}
		return "." + strings.Join(objectParts(fields, "."), ".")
	}
	return "." + proxyScalar(v)
}

func serializeMatrix(name string, v any, explode bool) string {
	if items, ok := proxyArray(v); ok {
		if len(items) == 0 {
			return ";" + name
		}
		if explode {
			parts := make([]string, 0, len(items))
			for _, item := range items {
				parts = append(parts, ";"+name+"="+item)
			}
			return strings.Join(parts, "")
		}
		return ";" + name + "=" + strings.Join(items, ",")
	}
	if fields, ok := proxyObject(v); ok {
		if explode {
			parts := make([]string, 0, len(fields))
			for _, field := range fields {
				parts = append(parts, ";"+field.Key+"="+field.Value)
			}
			return strings.Join(parts, "")
		}
		return ";" + name + "=" + strings.Join(objectParts(fields, ","), ",")
	}
	return ";" + name + "=" + proxyScalar(v)
}

func serializeForm(spec ProxyParamSpec, v any) (ProxyParam, error) {
	if items, ok := proxyArray(v); ok {
		if spec.Explode && spec.In == "query" {
			query := make(ProxyQuery, 0, len(items))
			for _, item := range items {
				query = append(query, ProxyQueryValue{Key: spec.Name, Value: item, AllowReserved: spec.AllowReserved})
			}
			if len(query) == 0 {
				query = append(query, ProxyQueryValue{Key: spec.Name, AllowReserved: spec.AllowReserved})
			}
			return ProxyParam{Value: strings.Join(items, ","), Query: query}, nil
		}
		if spec.Explode && spec.In == "cookie" {
			cookies := make([]ProxyCookie, 0, len(items))
			for _, item := range items {
				cookies = append(cookies, ProxyCookie{Name: spec.Name, Value: item})
			}
			if len(cookies) == 0 {
				cookies = append(cookies, ProxyCookie{Name: spec.Name})
			}
			return ProxyParam{Value: strings.Join(items, ","), Cookies: cookies}, nil
		}
		value := strings.Join(items, ",")
		return formScalarParam(spec, value), nil
	}
	if fields, ok := proxyObject(v); ok {
		if spec.In == "query" && spec.Explode {
			query := make(ProxyQuery, 0, len(fields))
			for _, field := range fields {
				query = append(query, ProxyQueryValue{Key: field.Key, Value: field.Value, AllowReserved: spec.AllowReserved})
			}
			return ProxyParam{Value: strings.Join(objectParts(fields, "="), "&"), Query: query}, nil
		}
		if spec.In == "cookie" && spec.Explode {
			cookies := make([]ProxyCookie, 0, len(fields))
			for _, field := range fields {
				cookies = append(cookies, ProxyCookie{Name: field.Key, Value: field.Value})
			}
			return ProxyParam{Value: strings.Join(objectParts(fields, "="), ";"), Cookies: cookies}, nil
		}
		value := strings.Join(objectParts(fields, ","), ",")
		return formScalarParam(spec, value), nil
	}
	return formScalarParam(spec, proxyScalar(v)), nil
}

func formScalarParam(spec ProxyParamSpec, value string) ProxyParam {
	param := ProxyParam{Value: value}
	if spec.In == "query" {
		param.Query = ProxyQuery{{Key: spec.Name, Value: value, AllowReserved: spec.AllowReserved}}
	}
	if spec.In == "cookie" {
		param.Cookies = []ProxyCookie{{Name: spec.Name, Value: value}}
	}
	return param
}

func serializeDelimited(spec ProxyParamSpec, v any, delimiter string) (ProxyParam, error) {
	value := proxyScalar(v)
	if items, ok := proxyArray(v); ok {
		value = strings.Join(items, delimiter)
	}
	if fields, ok := proxyObject(v); ok {
		value = strings.Join(objectParts(fields, delimiter), delimiter)
	}
	return ProxyParam{Value: value, Query: ProxyQuery{{Key: spec.Name, Value: value, AllowReserved: spec.AllowReserved}}}, nil
}

func serializeDeepObject(spec ProxyParamSpec, v any) (ProxyParam, error) {
	fields, ok := proxyObject(v)
	if !ok {
		return ProxyParam{}, fmt.Errorf("deepObject requires an object value")
	}
	query := make(ProxyQuery, 0, len(fields))
	for _, field := range fields {
		query = append(query, ProxyQueryValue{Key: spec.Name + "[" + field.Key + "]", Value: field.Value, AllowReserved: spec.AllowReserved})
	}
	return ProxyParam{Query: query}, nil
}

type proxyObjectField struct{ Key, Value string }

func proxyArray(v any) ([]string, bool) {
	items, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, proxyScalar(item))
	}
	return out, true
}

func proxyObject(v any) ([]proxyObjectField, bool) {
	object, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	out := make([]proxyObjectField, 0, len(keys))
	for _, key := range keys {
		out = append(out, proxyObjectField{Key: key, Value: proxyScalar(object[key])})
	}
	return out, true
}

func objectParts(fields []proxyObjectField, separator string) []string {
	parts := make([]string, 0, len(fields)*2)
	for _, field := range fields {
		parts = append(parts, field.Key+separator+field.Value)
	}
	return parts
}

func proxyScalar(v any) string {
	s, err := stringifyParam(v)
	if err != nil {
		return ""
	}
	return s
}

func encodeQueryComponent(value string, allowReserved bool) string {
	if !allowReserved {
		// url.QueryEscape uses '+' for spaces, which is valid form encoding but
		// not the percent-encoded delimiter form defined by OpenAPI's parameter
		// serialization tables (notably spaceDelimited).
		return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
	}
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if isUnreserved(c) || (isOpenAPIReserved(c) && c != '#') {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

// A literal # cannot be safely retained in a query component: Go (and URI
// parsers generally) treats it as the fragment delimiter. It is therefore
// percent-encoded even when allowReserved is true, preserving the value sent
// to the upstream service instead of silently losing everything after it.

func serializePathParam(v any, spec ProxyParamSpec) string {
	name := url.PathEscape(spec.Name)
	escape := func(value any) string { return url.PathEscape(proxyScalar(value)) }
	if items, ok := proxyArrayRaw(v, escape); ok {
		switch spec.Style {
		case "simple":
			return strings.Join(items, ",")
		case "label":
			return "." + strings.Join(items, ".")
		case "matrix":
			if len(items) == 0 {
				return ";" + name
			}
			if spec.Explode {
				parts := make([]string, 0, len(items))
				for _, item := range items {
					parts = append(parts, ";"+name+"="+item)
				}
				return strings.Join(parts, "")
			}
			return ";" + name + "=" + strings.Join(items, ",")
		}
	}
	if fields, ok := proxyObjectEscaped(v, escape); ok {
		switch spec.Style {
		case "simple":
			if spec.Explode {
				return strings.Join(objectParts(fields, "="), ",")
			}
			return strings.Join(objectParts(fields, ","), ",")
		case "label":
			if spec.Explode {
				return "." + strings.Join(objectParts(fields, "="), ".")
			}
			return "." + strings.Join(objectParts(fields, "."), ".")
		case "matrix":
			if spec.Explode {
				parts := make([]string, 0, len(fields))
				for _, field := range fields {
					parts = append(parts, ";"+field.Key+"="+field.Value)
				}
				return strings.Join(parts, "")
			}
			return ";" + name + "=" + strings.Join(objectParts(fields, ","), ",")
		}
	}
	value := escape(v)
	switch spec.Style {
	case "simple":
		return value
	case "label":
		return "." + value
	case "matrix":
		return ";" + name + "=" + value
	default:
		return value
	}
}

func proxyArrayRaw(v any, transform func(any) string) ([]string, bool) {
	items, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, transform(item))
	}
	return out, true
}

func proxyObjectEscaped(v any, transform func(any) string) ([]proxyObjectField, bool) {
	object, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	out := make([]proxyObjectField, 0, len(keys))
	for _, key := range keys {
		out = append(out, proxyObjectField{Key: transform(key), Value: transform(object[key])})
	}
	return out, true
}

func isUnreserved(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~'
}

func isOpenAPIReserved(c byte) bool {
	return strings.ContainsRune(":/?#[]@!$&'()*+,;=", rune(c))
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
// query accepts url.Values for backwards compatibility and ProxyQuery for
// OpenAPI-aware serialization. New generated proxy handlers use ProxyQuery.
func BuildProxyURL(baseURL, opPath string, query any) (string, error) {
	if baseURL == "" {
		return "", fmt.Errorf("base URL is empty (set API_BASE_URL or configure servers[] in the spec)")
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse base URL %q: %w", baseURL, err)
	}
	basePath := strings.TrimRight(u.EscapedPath(), "/")
	op := opPath
	if op != "" && !strings.HasPrefix(op, "/") {
		op = "/" + op
	}
	rawPath := basePath + op
	decodedPath, err := url.PathUnescape(rawPath)
	if err != nil {
		return "", fmt.Errorf("decode proxy path %q: %w", rawPath, err)
	}
	u.Path = decodedPath
	u.RawPath = rawPath
	extraQuery, err := encodeProxyQuery(query)
	if err != nil {
		return "", err
	}
	if extraQuery != "" {
		if values, ok := query.(url.Values); ok {
			// Preserve the established url.Values behaviour, including its
			// canonical key sort when a base URL already carries a query.
			merged := u.Query()
			for key, entries := range values {
				for _, entry := range entries {
					merged.Add(key, entry)
				}
			}
			u.RawQuery = merged.Encode()
		} else if u.RawQuery == "" {
			u.RawQuery = extraQuery
		} else {
			u.RawQuery += "&" + extraQuery
		}
	}
	return u.String(), nil
}

func encodeProxyQuery(query any) (string, error) {
	switch q := query.(type) {
	case nil:
		return "", nil
	case url.Values:
		return q.Encode(), nil
	case ProxyQuery:
		return q.Encode(), nil
	default:
		return "", fmt.Errorf("unsupported proxy query type %T", query)
	}
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

// DefaultMaxResponseBytes is the fail-safe response limit applied by proxy
// handlers when no explicit maximum is configured. It limits the memory and
// base64 amplification an untrusted or unexpectedly chatty upstream can cause.
const DefaultMaxResponseBytes int64 = 16 << 20

// ReadResponseBody drains an HTTP response up to DefaultMaxResponseBytes.
// New proxy code should use ReadResponseBodyLimit so deployments can choose a
// smaller or larger bound through runtime.WithMaxResponseBytes.
func ReadResponseBody(resp *http.Response) ([]byte, error) {
	return ReadResponseBodyLimit(resp, DefaultMaxResponseBytes)
}

// ReadResponseBodyLimit drains and closes resp.Body while enforcing maxBytes.
// A non-positive maxBytes uses DefaultMaxResponseBytes. Over-limit responses
// become a structured tool error before their payload is exposed to an MCP
// client or stored fully in memory.
func ReadResponseBodyLimit(resp *http.Response, maxBytes int64) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, nil
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxResponseBytes
	}
	// Drained body; close errors after io.ReadAll are not actionable.
	defer func() { _ = resp.Body.Close() }()
	if resp.ContentLength > maxBytes {
		return nil, &ToolError{Status: http.StatusBadGateway, Code: "response_too_large", Message: fmt.Sprintf("upstream response exceeds configured limit of %d bytes", maxBytes)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, &ToolError{Status: http.StatusBadGateway, Code: "response_too_large", Message: fmt.Sprintf("upstream response exceeds configured limit of %d bytes", maxBytes)}
	}
	return body, nil
}
