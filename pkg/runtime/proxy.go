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
	Value string
	Query ProxyQuery
}

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
		sep := ","
		if explode {
			sep = "."
		}
		return "." + strings.Join(items, sep)
	}
	if fields, ok := proxyObject(v); ok {
		if explode {
			return "." + strings.Join(objectParts(fields, "="), ".")
		}
		return "." + strings.Join(objectParts(fields, ","), ",")
	}
	return "." + proxyScalar(v)
}

func serializeMatrix(name string, v any, explode bool) string {
	if items, ok := proxyArray(v); ok {
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
		if spec.In == "query" && spec.Explode {
			query := make(ProxyQuery, 0, len(items))
			for _, item := range items {
				query = append(query, ProxyQueryValue{Key: spec.Name, Value: item, AllowReserved: spec.AllowReserved})
			}
			return ProxyParam{Value: strings.Join(items, ","), Query: query}, nil
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
	return param
}

func serializeDelimited(spec ProxyParamSpec, v any, delimiter string) (ProxyParam, error) {
	if _, ok := proxyObject(v); ok {
		return ProxyParam{}, fmt.Errorf("%s does not support object values", spec.Style)
	}
	value := proxyScalar(v)
	if items, ok := proxyArray(v); ok {
		value = strings.Join(items, delimiter)
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
		return url.QueryEscape(value)
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
	basePath := strings.TrimRight(u.Path, "/")
	op := opPath
	if op != "" && !strings.HasPrefix(op, "/") {
		op = "/" + op
	}
	u.Path = basePath + op
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
