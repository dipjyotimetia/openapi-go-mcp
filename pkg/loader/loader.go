// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

// Package loader reads OpenAPI 3.x and Swagger 2.0 specifications and
// normalises them into the kin-openapi *openapi3.T type used by the rest
// of the generator.
package loader

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

// DefaultMaxSpecSize caps how many bytes Load / LoadFromURL will read from a
// spec source. Specs in the wild are usually <1 MiB; the 32 MiB cap exists
// purely to keep a stray petabyte URL from exhausting the process.
const DefaultMaxSpecSize int64 = 32 << 20

// DefaultURLLoadTimeout bounds the HTTP fetch in LoadFromURL when the
// caller-supplied context has no deadline. It is intentionally generous —
// fetching a spec is rarely on the hot path.
const DefaultURLLoadTimeout = 30 * time.Second

// Load reads an OpenAPI spec from a file path or http(s):// URL and returns
// the kin-openapi v3 representation. Swagger 2.0 specs are detected and
// converted automatically; the returned document is validated.
//
// URLs are fetched with the default HTTP client; see LoadFromURL for
// caller-controlled transport / size limits.
func Load(ctx context.Context, path string) (*openapi3.T, error) {
	if isHTTPURL(path) {
		return LoadFromURL(ctx, path)
	}
	// path is the user-supplied OpenAPI spec — reading it is the function's
	// entire purpose.
	raw, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		// Surface absolute path + CWD so the user can resolve relative-path
		// confusion fast.
		if abs, absErr := filepath.Abs(path); absErr == nil {
			cwd, _ := os.Getwd()
			return nil, fmt.Errorf("read %s (resolved to %s; CWD %s): %w", path, abs, cwd, err)
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	if isSwagger2(raw) {
		var location *url.URL
		if base, absErr := filepath.Abs(path); absErr == nil {
			location = &url.URL{Scheme: "file", Path: base}
		}
		doc, err := convertSwagger2(ctx, raw, location)
		if err != nil {
			return nil, fmt.Errorf("convert swagger 2.0: %w", err)
		}
		if err := doc.Validate(ctx); err != nil {
			return nil, fmt.Errorf("validate converted v3: %w", err)
		}
		return doc, nil
	}

	l := openapi3.NewLoader()
	l.IsExternalRefsAllowed = true
	l.Context = ctx

	var doc *openapi3.T
	if base, err := filepath.Abs(path); err == nil {
		doc, err = l.LoadFromDataWithPath(raw, &url.URL{Scheme: "file", Path: base})
		if err != nil {
			return nil, fmt.Errorf("parse openapi: %w", err)
		}
	} else {
		doc, err = l.LoadFromData(raw)
		if err != nil {
			return nil, fmt.Errorf("parse openapi: %w", err)
		}
	}
	if err := doc.Validate(ctx); err != nil {
		return nil, fmt.Errorf("validate openapi: %w", err)
	}
	return doc, nil
}

// IsJSONContentType reports whether ct is a JSON media type — either the
// canonical "application/json" or any suffix variant such as
// "application/problem+json". Exported so other packages share a single
// predicate.
func IsJSONContentType(ct string) bool {
	return ct == "application/json" || strings.HasSuffix(ct, "+json")
}

// URLLoadOption configures LoadFromURL. Defaults are documented on each
// constructor.
type URLLoadOption func(*urlLoadConfig)

type urlLoadConfig struct {
	client      *http.Client
	maxBodySize int64
	timeout     time.Duration
}

// WithHTTPClient overrides the *http.Client used to fetch the spec.
// Useful for injecting custom transports (proxies, mTLS, auth headers).
func WithHTTPClient(c *http.Client) URLLoadOption {
	return func(cfg *urlLoadConfig) { cfg.client = c }
}

// WithMaxBodySize caps how many bytes the loader will read from the URL.
// Zero or negative restores DefaultMaxSpecSize.
func WithMaxBodySize(n int64) URLLoadOption {
	return func(cfg *urlLoadConfig) { cfg.maxBodySize = n }
}

// WithTimeout sets a fetch deadline when the caller-supplied context has
// none. Zero restores DefaultURLLoadTimeout.
func WithTimeout(d time.Duration) URLLoadOption {
	return func(cfg *urlLoadConfig) { cfg.timeout = d }
}

func isHTTPURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// LoadFromURL fetches an OpenAPI spec from an http(s):// URL, decodes it
// (auto-detecting Swagger 2.0 vs OpenAPI 3.x), and validates the result.
// The returned document carries no remembered URL — external $refs in the
// spec are resolved against the file:// origin kin-openapi defaults to when
// no base URL is set; specs that rely on URL-relative refs should be saved
// locally first.
func LoadFromURL(ctx context.Context, rawURL string, opts ...URLLoadOption) (*openapi3.T, error) {
	cfg := urlLoadConfig{
		maxBodySize: DefaultMaxSpecSize,
		timeout:     DefaultURLLoadTimeout,
	}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.maxBodySize <= 0 {
		cfg.maxBodySize = DefaultMaxSpecSize
	}
	if cfg.client == nil {
		cfg.client = http.DefaultClient
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse spec URL %q: %w", rawURL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme %q (only http(s):// is supported here)", parsed.Scheme)
	}

	if _, hasDeadline := ctx.Deadline(); !hasDeadline && cfg.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Accept", "application/json, application/yaml;q=0.9, */*;q=0.5")

	resp, err := cfg.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch %s: HTTP %d", rawURL, resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, cfg.maxBodySize+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if int64(len(raw)) > cfg.maxBodySize {
		return nil, fmt.Errorf("spec body exceeds %d bytes (set WithMaxBodySize to raise)", cfg.maxBodySize)
	}

	if isSwagger2(raw) {
		doc, err := convertSwagger2(ctx, raw, parsed)
		if err != nil {
			return nil, fmt.Errorf("convert swagger 2.0: %w", err)
		}
		if err := doc.Validate(ctx); err != nil {
			return nil, fmt.Errorf("validate converted v3: %w", err)
		}
		return doc, nil
	}

	l := openapi3.NewLoader()
	l.IsExternalRefsAllowed = true
	l.Context = ctx
	doc, err := l.LoadFromDataWithPath(raw, parsed)
	if err != nil {
		return nil, fmt.Errorf("parse openapi: %w", err)
	}
	if err := doc.Validate(ctx); err != nil {
		return nil, fmt.Errorf("validate openapi: %w", err)
	}
	return doc, nil
}

// WriteV3YAMLJSONOnly serialises doc as OpenAPI 3.x YAML, with non-JSON
// content types pruned from response bodies. Request bodies are preserved
// verbatim so downstream oapi-codegen can emit the matching Formdata /
// Multipart / WithBody helpers. The input doc is not modified — pruning
// happens on an internal clone. Useful for piping a Swagger-2.0-converted
// spec into tools (like oapi-codegen) that only accept OpenAPI 3 and can
// mis-handle responses exposed under multiple content types.
func WriteV3YAMLJSONOnly(doc *openapi3.T, path string) error {
	if doc == nil {
		return fmt.Errorf("nil document")
	}
	cloned, err := cloneDoc(doc)
	if err != nil {
		return fmt.Errorf("clone openapi: %w", err)
	}
	pruneNonJSONContent(cloned)

	yamlBytes, err := yaml.Marshal(cloned)
	if err != nil {
		return fmt.Errorf("marshal yaml: %w", err)
	}
	return os.WriteFile(path, yamlBytes, 0o644)
}

// isSwagger2 reports whether raw is a Swagger 2.0 document. It decodes only
// the top-level shape, so a "swagger: 2.0" reference inside a description
// string of an OpenAPI 3 file does not trigger a false positive.
func isSwagger2(raw []byte) bool {
	var top map[string]any
	if err := yaml.Unmarshal(raw, &top); err != nil {
		return false
	}
	v, ok := top["swagger"]
	if !ok {
		return false
	}
	switch s := v.(type) {
	case string:
		return strings.HasPrefix(s, "2.")
	case float64:
		return s >= 2 && s < 3
	}
	return false
}

func convertSwagger2(ctx context.Context, raw []byte, location *url.URL) (*openapi3.T, error) {
	jsonBytes, err := yamlOrJSONToJSON(raw)
	if err != nil {
		return nil, err
	}
	var v2 openapi2.T
	if err := v2.UnmarshalJSON(jsonBytes); err != nil {
		return nil, fmt.Errorf("unmarshal swagger 2.0: %w", err)
	}
	loader := openapi3.NewLoader()
	loader.Context = ctx
	loader.IsExternalRefsAllowed = true
	v3, err := openapi2conv.ToV3WithLoader(&v2, loader, location)
	if err != nil {
		return nil, fmt.Errorf("convert v2 → v3: %w", err)
	}
	return v3, nil
}

// yamlOrJSONToJSON returns raw as JSON, decoding YAML if needed.
func yamlOrJSONToJSON(raw []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		return raw, nil
	}
	var node any
	if err := yaml.Unmarshal(raw, &node); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	return json.Marshal(normaliseYAMLMaps(node))
}

// cloneDoc returns a deep copy of doc by round-tripping through JSON. This
// is the only stable way to clone *openapi3.T — the struct is graph-shaped
// with shared *SchemaRef pointers.
func cloneDoc(doc *openapi3.T) (*openapi3.T, error) {
	buf, err := doc.MarshalJSON()
	if err != nil {
		return nil, err
	}
	out := &openapi3.T{}
	if err := out.UnmarshalJSON(buf); err != nil {
		return nil, err
	}
	return out, nil
}

// pruneNonJSONContent removes every non-JSON content type from response
// bodies. Request bodies are left intact — the generator now lowers
// form/multipart/octet/text/raw request bodies into MCP tool arguments and
// needs the original content map to pick a content type. JSON is recognised
// by IsJSONContentType.
func pruneNonJSONContent(doc *openapi3.T) {
	if doc.Paths == nil {
		return
	}
	for _, item := range doc.Paths.Map() {
		if item == nil {
			continue
		}
		for _, op := range item.Operations() {
			if op == nil || op.Responses == nil {
				continue
			}
			for _, respRef := range op.Responses.Map() {
				if respRef == nil || respRef.Value == nil {
					continue
				}
				keepJSONOnly(respRef.Value.Content)
			}
		}
	}
}

func keepJSONOnly(c openapi3.Content) {
	for ct := range c {
		if !IsJSONContentType(ct) {
			delete(c, ct)
		}
	}
}

// normaliseYAMLMaps converts map[interface{}]interface{} (legacy YAML) into
// map[string]interface{} for JSON compatibility.
func normaliseYAMLMaps(v any) any {
	switch x := v.(type) {
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[fmt.Sprint(k)] = normaliseYAMLMaps(val)
		}
		return out
	case map[string]any:
		for k, val := range x {
			x[k] = normaliseYAMLMaps(val)
		}
		return x
	case []any:
		for i, val := range x {
			x[i] = normaliseYAMLMaps(val)
		}
		return x
	default:
		return v
	}
}
