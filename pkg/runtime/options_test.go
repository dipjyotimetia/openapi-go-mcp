// Copyright 2026 Dipjyoti Metia.
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
	"net/http"
	"testing"
	"time"
)

func TestApplyConfig_NumberAndBooleanExtraProps(t *testing.T) {
	tool := Tool{
		Name:           "x",
		RawInputSchema: []byte(`{"type":"object","properties":{}}`),
	}
	cfg := NewConfig()
	WithExtraProperties(
		ExtraProperty{Name: "limit", Type: "integer", Description: "max rows"},
		ExtraProperty{Name: "verbose", Type: "boolean"},
	)(cfg)
	got := ApplyConfig(tool, cfg)

	var schema map[string]any
	if err := json.Unmarshal(got.RawInputSchema, &schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	props := schema["properties"].(map[string]any)
	if t1 := props["limit"].(map[string]any)["type"]; t1 != "integer" {
		t.Errorf("limit type: got %v want integer", t1)
	}
	if t2 := props["verbose"].(map[string]any)["type"]; t2 != "boolean" {
		t.Errorf("verbose type: got %v want boolean", t2)
	}
}

func TestApplyConfig_UnknownTypeFallsBackToString(t *testing.T) {
	tool := Tool{Name: "x", RawInputSchema: []byte(`{"type":"object","properties":{}}`)}
	cfg := NewConfig()
	WithExtraProperties(ExtraProperty{Name: "k", Type: "weird-type"})(cfg)
	got := ApplyConfig(tool, cfg)

	var schema map[string]any
	_ = json.Unmarshal(got.RawInputSchema, &schema)
	props := schema["properties"].(map[string]any)
	if props["k"].(map[string]any)["type"] != "string" {
		t.Errorf("unknown type should fall back to string")
	}
}

func TestApplyConfig_MalformedRawSchemaReturnsToolUnchanged(t *testing.T) {
	tool := Tool{Name: "x", RawInputSchema: []byte(`not-json`)}
	cfg := NewConfig()
	WithExtraProperties(ExtraProperty{Name: "k"})(cfg)
	got := ApplyConfig(tool, cfg)
	if string(got.RawInputSchema) != "not-json" {
		t.Errorf("malformed schema must be returned untouched; got %s", got.RawInputSchema)
	}
}

func TestApplyConfig_WrongPropertiesTypePreservesSchema(t *testing.T) {
	// "properties" is a string — schema is invalid but well-formed JSON.
	// We refuse to silently overwrite it.
	tool := Tool{Name: "x", RawInputSchema: []byte(`{"properties":"oops"}`)}
	cfg := NewConfig()
	WithExtraProperties(ExtraProperty{Name: "k"})(cfg)
	got := ApplyConfig(tool, cfg)
	if string(got.RawInputSchema) != `{"properties":"oops"}` {
		t.Errorf("wrong properties type should keep schema untouched; got %s", got.RawInputSchema)
	}
}

func TestApplyConfig_WrongRequiredTypePreservesSchema(t *testing.T) {
	tool := Tool{Name: "x", RawInputSchema: []byte(`{"properties":{},"required":"oops"}`)}
	cfg := NewConfig()
	WithExtraProperties(ExtraProperty{Name: "k", Required: true})(cfg)
	got := ApplyConfig(tool, cfg)
	// schema preserved verbatim
	if string(got.RawInputSchema) != `{"properties":{},"required":"oops"}` {
		t.Errorf("wrong required type should preserve schema; got %s", got.RawInputSchema)
	}
}

func TestApplyConfig_RequiredPropagation(t *testing.T) {
	tool := Tool{Name: "x", RawInputSchema: []byte(`{"properties":{}}`)}
	cfg := NewConfig()
	WithExtraProperties(ExtraProperty{Name: "tok", Required: true})(cfg)
	got := ApplyConfig(tool, cfg)
	var schema map[string]any
	_ = json.Unmarshal(got.RawInputSchema, &schema)
	req, ok := schema["required"].([]any)
	if !ok || len(req) != 1 || req[0] != "tok" {
		t.Errorf("required: got %v", schema["required"])
	}
}

func TestWithHTTPClient_AndTimeout_AndServerVars(t *testing.T) {
	cfg := NewConfig()
	client := &http.Client{Timeout: 5 * time.Second}
	WithHTTPClient(client)(cfg)
	WithRequestTimeout(2 * time.Second)(cfg)
	WithServerVariables(map[string]string{"host": "api.example.com"})(cfg)
	WithServerVariables(map[string]string{"version": "v2"})(cfg)

	if cfg.HTTPClient != client {
		t.Errorf("HTTPClient not stored")
	}
	if cfg.RequestTimeout != 2*time.Second {
		t.Errorf("RequestTimeout: got %v", cfg.RequestTimeout)
	}
	if cfg.ServerVariables["host"] != "api.example.com" || cfg.ServerVariables["version"] != "v2" {
		t.Errorf("ServerVariables merged incorrectly: %v", cfg.ServerVariables)
	}
}

func TestApplyExtraPropertiesToContext_RemovesArgsAndStoresValues(t *testing.T) {
	type ctxKey string
	tenantKey := ctxKey("tenant")
	limitKey := ctxKey("limit")
	args := map[string]any{
		"tenant": "acme",
		"limit":  float64(10),
	}

	ctx := ApplyExtraPropertiesToContext(context.Background(), args, []ExtraProperty{
		{Name: "tenant", ContextKey: tenantKey},
		{Name: "limit", ContextKey: limitKey},
		{Name: "missing", ContextKey: ctxKey("missing")},
	})

	if got := ctx.Value(tenantKey); got != "acme" {
		t.Errorf("tenant context value = %#v, want acme", got)
	}
	if got := ctx.Value(limitKey); got != float64(10) {
		t.Errorf("limit context value = %#v, want 10", got)
	}
	if _, ok := args["tenant"]; ok {
		t.Errorf("tenant arg was not removed: %+v", args)
	}
	if _, ok := args["limit"]; ok {
		t.Errorf("limit arg was not removed: %+v", args)
	}
}
