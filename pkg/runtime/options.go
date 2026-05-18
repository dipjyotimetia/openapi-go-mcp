// Copyright 2026 Dipjyoti Metia.
// Portions copyright 2025 Redpanda Data, Inc. (Option/ExtraProperty pattern
// adapted from redpanda-data/protoc-gen-go-mcp, Apache-2.0).
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package runtime

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"time"
)

// Option configures tool registration.
type Option func(*Config)

// Config is the resolved set of registration options. Generated code creates
// one with NewConfig, applies user Options to it, then passes it to
// ApplyConfig for each tool.
type Config struct {
	ExtraProperties []ExtraProperty
	NamePrefix      string
	// HTTPClient is an optional shared *http.Client. When non-nil, generated
	// handlers may use it to honour user-supplied timeouts, transports, and
	// connection pools. Wiring is the caller's responsibility — the runtime
	// only stores the value so user code can read it.
	HTTPClient *http.Client
	// RequestTimeout, when non-zero, is the per-request deadline generated
	// handlers should apply via context.WithTimeout before delegating to the
	// upstream oapi-codegen client. Zero means "no per-request timeout".
	RequestTimeout time.Duration
	// ServerVariables holds substitutions for OpenAPI `servers[*].variables`
	// templated URLs (e.g. {scheme}, {host}). Generated code may read this
	// when constructing the upstream client base URL. Empty == use whatever
	// default the spec declared.
	ServerVariables map[string]string
}

// ExtraProperty defines an additional schema property to add to every tool's
// input schema. The decoded value is placed on the request context via
// ContextKey so handlers can read it.
//
// Type controls the JSON Schema "type" emitted for the property. Supported
// values: "string" (default), "number", "integer", "boolean". An empty Type
// is treated as "string" for backwards compatibility.
type ExtraProperty struct {
	Name        string
	Description string
	Required    bool
	ContextKey  any
	Type        string
}

// NewConfig returns a Config with default values.
func NewConfig() *Config { return &Config{} }

// WithNamePrefix prepends "<prefix>_" to every tool name at registration time.
// Useful when the same service is registered multiple times under different
// names (e.g. two instances of the same API behind different base URLs).
func WithNamePrefix(prefix string) Option {
	return func(c *Config) {
		c.NamePrefix = prefix
	}
}

// WithExtraProperties adds extra properties to every tool schema. At call
// time the values are extracted from request arguments and placed on the
// handler's context. Each property may declare its JSON Schema type via
// ExtraProperty.Type; an empty Type means "string".
func WithExtraProperties(properties ...ExtraProperty) Option {
	return func(c *Config) {
		c.ExtraProperties = append(c.ExtraProperties, properties...)
	}
}

// WithHTTPClient stores a shared *http.Client on the Config. Generated code
// may consult cfg.HTTPClient when constructing the upstream oapi-codegen
// client so user-supplied transports, timeouts, and connection pools apply.
func WithHTTPClient(c *http.Client) Option {
	return func(cfg *Config) {
		cfg.HTTPClient = c
	}
}

// WithRequestTimeout sets a per-tool-call request deadline. Generated
// handlers may wrap the inbound context with context.WithTimeout(d) before
// delegating to the upstream client.
func WithRequestTimeout(d time.Duration) Option {
	return func(cfg *Config) {
		cfg.RequestTimeout = d
	}
}

// WithServerVariables records substitutions for OpenAPI server URL templates.
// Generated code reads cfg.ServerVariables when computing the upstream base
// URL — e.g. for `https://{host}/{basePath}` the caller might pass
// {"host":"api.example.com","basePath":"v1"}.
func WithServerVariables(vars map[string]string) Option {
	return func(cfg *Config) {
		if cfg.ServerVariables == nil {
			cfg.ServerVariables = map[string]string{}
		}
		maps.Copy(cfg.ServerVariables, vars)
	}
}

// ApplyConfig applies prefix and extra-property transformations to a tool and
// returns the modified copy.
func ApplyConfig(tool Tool, cfg *Config) Tool {
	if cfg == nil {
		return tool
	}
	if cfg.NamePrefix != "" {
		tool.Name = cfg.NamePrefix + "_" + tool.Name
	}
	if len(cfg.ExtraProperties) > 0 {
		tool = AddExtraPropertiesToTool(tool, cfg.ExtraProperties)
	}
	return tool
}

// AddExtraPropertiesToTool modifies a tool's schema to include the given
// extra properties. Each extra property is added under "properties" with the
// type declared on ExtraProperty.Type (defaulting to "string").
//
// Schemas that are malformed (not valid JSON, root not an object, or where
// "properties" / "required" carry unexpected types) are returned unchanged.
// This keeps tool registration resilient: a bad extra-property declaration
// disables the augmentation rather than panicking at server start.
func AddExtraPropertiesToTool(tool Tool, properties []ExtraProperty) Tool {
	if len(properties) == 0 {
		return tool
	}

	var schema map[string]any
	if err := json.Unmarshal(tool.RawInputSchema, &schema); err != nil {
		return tool
	}
	if schema == nil {
		return tool
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		// If "properties" exists but isn't an object we refuse to overwrite
		// it — that would silently discard whatever schema the user authored.
		if _, exists := schema["properties"]; exists {
			return tool
		}
		props = make(map[string]any)
		schema["properties"] = props
	}

	var required []any
	switch r := schema["required"].(type) {
	case nil:
	case []any:
		required = r
	default:
		return tool
	}

	for _, p := range properties {
		t := p.Type
		switch t {
		case "string", "number", "integer", "boolean":
		default:
			t = "string"
		}
		entry := map[string]any{"type": t}
		if p.Description != "" {
			entry["description"] = p.Description
		}
		props[p.Name] = entry
		if p.Required {
			required = append(required, p.Name)
		}
	}
	if len(required) > 0 {
		schema["required"] = required
	}

	modified, err := json.Marshal(schema)
	if err != nil {
		return tool
	}
	out := tool
	out.RawInputSchema = modified
	return out
}

// ExtractExtraProperty pulls the value of an extra property out of args
// (removing it so generated body/path decoders don't see it). Returns the
// string value and a bool indicating whether the property was present.
func ExtractExtraProperty(args map[string]any, name string) (string, bool) {
	v, ok := ExtractExtraPropertyValue(args, name)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return s, true
}

// ExtractExtraPropertyValue pulls the raw value of an extra property out of
// args, removing it so generated body/path decoders don't see it.
func ExtractExtraPropertyValue(args map[string]any, name string) (any, bool) {
	v, ok := args[name]
	if !ok {
		return nil, false
	}
	delete(args, name)
	return v, true
}

// ApplyExtraPropertiesToContext removes configured extra properties from args
// and stores present values on ctx under their ContextKey. Properties without
// a ContextKey are still removed so they don't leak into generated decoders.
func ApplyExtraPropertiesToContext(ctx context.Context, args map[string]any, properties []ExtraProperty) context.Context {
	for _, p := range properties {
		v, ok := ExtractExtraPropertyValue(args, p.Name)
		if !ok || p.ContextKey == nil {
			continue
		}
		ctx = context.WithValue(ctx, p.ContextKey, v)
	}
	return ctx
}
