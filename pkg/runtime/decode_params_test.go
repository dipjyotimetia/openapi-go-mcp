// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package runtime

import (
	"errors"
	"testing"
)

// The Decode*Params functions are called from every generated handler, so
// they need direct unit tests in addition to the e2e coverage from
// tests/e2e/*. e2e only catches happy-path regressions; these tests pin
// the error semantics (status, code, ToolError shape) the generator
// contract depends on.

type paramsStruct struct {
	Limit  int    `json:"limit"`
	Filter string `json:"filter"`
}

func TestDecodeQueryParams_Populates(t *testing.T) {
	args := map[string]any{
		"query": map[string]any{"limit": 5, "filter": "active"},
	}
	var got paramsStruct
	if err := DecodeQueryParams(args, &got); err != nil {
		t.Fatalf("DecodeQueryParams: %v", err)
	}
	if got.Limit != 5 || got.Filter != "active" {
		t.Errorf("unexpected struct: %+v", got)
	}
}

func TestDecodeQueryParams_MissingGroupNoError(t *testing.T) {
	// Absent "query" key is fine — the operation may have no query params,
	// or the caller may have only sent body.
	var got paramsStruct
	if err := DecodeQueryParams(map[string]any{}, &got); err != nil {
		t.Errorf("missing group should not error; got %v", err)
	}
}

func TestDecodeQueryParams_TypeMismatchSurfacesToolError(t *testing.T) {
	// The MCP boundary should give a structured, user-facing error rather than
	// a raw JSON-decode message — verifies the ToolError envelope is applied.
	args := map[string]any{"query": map[string]any{"limit": "not-an-int"}}
	var got paramsStruct
	err := DecodeQueryParams(args, &got)
	if err == nil {
		t.Fatal("expected ToolError for type mismatch")
	}
	var te *ToolError
	if !errors.As(err, &te) {
		t.Fatalf("expected *ToolError, got %T: %v", err, err)
	}
	if te.Status != 400 || te.Code != "invalid_argument" {
		t.Errorf("unexpected ToolError envelope: status=%d code=%q", te.Status, te.Code)
	}
}

func TestDecodeHeaderParams_Populates(t *testing.T) {
	args := map[string]any{"header": map[string]any{"limit": 7}}
	var got paramsStruct
	if err := DecodeHeaderParams(args, &got); err != nil {
		t.Fatalf("DecodeHeaderParams: %v", err)
	}
	if got.Limit != 7 {
		t.Errorf("expected Limit=7, got %+v", got)
	}
}

func TestDecodeParamsCombined_MergesQueryAndHeader(t *testing.T) {
	// The oapi-codegen <Op>Params struct unifies query and header fields;
	// the combined decoder must populate both from one MCP envelope.
	args := map[string]any{
		"query":  map[string]any{"limit": 3},
		"header": map[string]any{"filter": "foo"},
	}
	var got paramsStruct
	if err := DecodeParamsCombined(args, &got); err != nil {
		t.Fatalf("DecodeParamsCombined: %v", err)
	}
	if got.Limit != 3 || got.Filter != "foo" {
		t.Errorf("merge incorrect: %+v", got)
	}
}

func TestDecodeParamsCombined_HeaderOverridesQueryOnCollision(t *testing.T) {
	// maps.Copy in DecodeParamsCombined merges query first, then header —
	// so colliding keys take the header value. This is a contract worth
	// pinning so refactors don't silently flip the precedence.
	args := map[string]any{
		"query":  map[string]any{"filter": "from-query"},
		"header": map[string]any{"filter": "from-header"},
	}
	var got paramsStruct
	if err := DecodeParamsCombined(args, &got); err != nil {
		t.Fatalf("DecodeParamsCombined: %v", err)
	}
	if got.Filter != "from-header" {
		t.Errorf("header should win on key collision; got %q", got.Filter)
	}
}

func TestDecodeParamsCombined_EmptyArgsNoError(t *testing.T) {
	var got paramsStruct
	if err := DecodeParamsCombined(map[string]any{}, &got); err != nil {
		t.Errorf("empty args should not error; got %v", err)
	}
}

func TestDecodeCookieParam_Populates(t *testing.T) {
	args := map[string]any{
		"cookie": map[string]any{"session": "abc123"},
	}
	var got string
	if err := DecodeCookieParam(args, "session", &got); err != nil {
		t.Fatalf("DecodeCookieParam: %v", err)
	}
	if got != "abc123" {
		t.Errorf("expected abc123, got %q", got)
	}
}

func TestDecodeCookieParam_MissingGroupNoError(t *testing.T) {
	// Cookie params are optional unless the spec marks them required (and
	// the input schema enforces that at the MCP boundary). The decoder
	// itself must not fail on absence.
	var got string
	if err := DecodeCookieParam(map[string]any{}, "session", &got); err != nil {
		t.Errorf("missing cookie group should not error; got %v", err)
	}
	if got != "" {
		t.Errorf("out param should stay at zero value; got %q", got)
	}
}

func TestDecodeCookieParam_MissingNameNoError(t *testing.T) {
	args := map[string]any{"cookie": map[string]any{"other": "x"}}
	var got string
	if err := DecodeCookieParam(args, "session", &got); err != nil {
		t.Errorf("missing cookie name should not error; got %v", err)
	}
}

func TestDecodeCookieParam_NilValueNoError(t *testing.T) {
	// JSON null for a cookie is treated identically to absence — the spec's
	// required-ness gate (if any) lives in the input schema.
	args := map[string]any{"cookie": map[string]any{"session": nil}}
	var got string
	if err := DecodeCookieParam(args, "session", &got); err != nil {
		t.Errorf("nil cookie value should not error; got %v", err)
	}
}

func TestDecodeCookieParam_TypeMismatchSurfacesToolError(t *testing.T) {
	args := map[string]any{"cookie": map[string]any{"count": "not-a-number"}}
	var got int
	err := DecodeCookieParam(args, "count", &got)
	if err == nil {
		t.Fatal("expected ToolError for type mismatch")
	}
	var te *ToolError
	if !errors.As(err, &te) {
		t.Fatalf("expected *ToolError, got %T", err)
	}
	if te.Status != 400 || te.Code != "invalid_cookie_param" {
		t.Errorf("unexpected envelope: status=%d code=%q", te.Status, te.Code)
	}
}
