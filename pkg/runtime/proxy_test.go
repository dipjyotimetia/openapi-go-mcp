// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package runtime

import (
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"testing"
)

func TestDecodeProxyParam_StringPassthrough(t *testing.T) {
	args := map[string]any{"query": map[string]any{"status": "available"}}
	got, present, err := DecodeProxyParam(args, "query", "status", false)
	if err != nil || !present || got != "available" {
		t.Errorf("got (%q, %v, %v); want (\"available\", true, nil)", got, present, err)
	}
}

func TestDecodeProxyParam_NumberAndBoolStringified(t *testing.T) {
	args := map[string]any{"query": map[string]any{
		"limit":  float64(42),
		"active": true,
	}}
	limit, _, err := DecodeProxyParam(args, "query", "limit", false)
	if err != nil || limit != "42" {
		t.Errorf("limit: got %q (%v), want \"42\"", limit, err)
	}
	active, _, err := DecodeProxyParam(args, "query", "active", false)
	if err != nil || active != "true" {
		t.Errorf("active: got %q (%v)", active, err)
	}
}

func TestDecodeProxyParam_ArrayJoinedWithComma(t *testing.T) {
	args := map[string]any{"query": map[string]any{
		"tags": []any{"red", "blue", "green"},
	}}
	got, _, err := DecodeProxyParam(args, "query", "tags", false)
	if err != nil || got != "red,blue,green" {
		t.Errorf("tags: got %q (%v)", got, err)
	}
}

func TestDecodeProxyParam_ObjectJSONEncoded(t *testing.T) {
	args := map[string]any{"query": map[string]any{
		"filter": map[string]any{"k": "v"},
	}}
	got, _, err := DecodeProxyParam(args, "query", "filter", false)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	// JSON-encoded representation; ordering may vary but the value must
	// parse back into the same map.
	var back map[string]any
	if err := json.Unmarshal([]byte(got), &back); err != nil {
		t.Fatalf("re-decode %q: %v", got, err)
	}
	if back["k"] != "v" {
		t.Errorf("round-trip lost data: %+v", back)
	}
}

func TestDecodeProxyParam_MissingRequiredErrors(t *testing.T) {
	args := map[string]any{"query": map[string]any{}}
	_, _, err := DecodeProxyParam(args, "query", "petId", true)
	if err == nil {
		t.Error("expected ToolError for missing required param")
	}
	var te *ToolError
	if !errors.As(err, &te) {
		t.Errorf("error should be *ToolError; got %T", err)
	}
}

func TestDecodeProxyParam_MissingOptionalReturnsAbsent(t *testing.T) {
	args := map[string]any{"query": map[string]any{}}
	got, present, err := DecodeProxyParam(args, "query", "tag", false)
	if err != nil || present || got != "" {
		t.Errorf("optional missing: got (%q, %v, %v); want (\"\", false, nil)", got, present, err)
	}
}

func TestDecodeProxyParam_NilGroupHandled(t *testing.T) {
	args := map[string]any{} // no "query" key at all
	_, present, err := DecodeProxyParam(args, "query", "x", false)
	if present || err != nil {
		t.Errorf("missing group should not error for optional param; got (%v, %v)", present, err)
	}
	_, _, err = DecodeProxyParam(args, "query", "x", true)
	if err == nil {
		t.Error("missing group with required=true must error")
	}
}

func TestBuildProxyURL_BasicJoin(t *testing.T) {
	got, err := BuildProxyURL("https://api.example.com", "/pets/123", nil)
	if err != nil || got != "https://api.example.com/pets/123" {
		t.Errorf("got %q (%v)", got, err)
	}
}

func TestBuildProxyURL_TrailingAndLeadingSlashes(t *testing.T) {
	got, err := BuildProxyURL("https://api.example.com/", "pets/123", nil)
	if err != nil || got != "https://api.example.com/pets/123" {
		t.Errorf("got %q (%v)", got, err)
	}
}

func TestBuildProxyURL_AppendsQuery(t *testing.T) {
	q := url.Values{}
	q.Add("status", "available")
	q.Add("tag", "red")
	got, err := BuildProxyURL("https://api.example.com", "/pets", q)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "status=available") || !strings.Contains(got, "tag=red") {
		t.Errorf("expected query params in URL; got %q", got)
	}
}

func TestBuildProxyURL_EmptyBaseErrors(t *testing.T) {
	_, err := BuildProxyURL("", "/pets", nil)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected empty-base error, got %v", err)
	}
}

func TestBuildProxyURL_MergesIntoExistingQueryOnBase(t *testing.T) {
	q := url.Values{}
	q.Set("status", "available")
	got, err := BuildProxyURL("https://api.example.com/v2?tenant=acme", "/pets", q)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://api.example.com/v2/pets?status=available&tenant=acme"
	if got != want {
		t.Errorf("BuildProxyURL = %q, want %q", got, want)
	}
}

func TestPathEscape_UsesPathSegmentEscaping(t *testing.T) {
	got := PathEscape("a b/c")
	want := "a%20b%2Fc"
	if got != want {
		t.Errorf("PathEscape = %q, want %q", got, want)
	}
}

func TestEncodeJSONBody(t *testing.T) {
	r, ct, err := EncodeJSONBody(map[string]any{"name": "Fido"})
	if err != nil {
		t.Fatal(err)
	}
	if ct != "application/json" {
		t.Errorf("content type: got %q", ct)
	}
	b, _ := io.ReadAll(r)
	var back map[string]string
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("body: %v", err)
	}
	if back["name"] != "Fido" {
		t.Errorf("round-trip: %+v", back)
	}
}

func TestEncodeFormBody_FlattensAndSortsKeys(t *testing.T) {
	args := map[string]any{"body": map[string]any{
		"zeta":  "z",
		"alpha": "a",
		"num":   float64(7),
	}}
	r, ct, err := EncodeFormBody(args)
	if err != nil {
		t.Fatal(err)
	}
	if ct != "application/x-www-form-urlencoded" {
		t.Errorf("content type: %q", ct)
	}
	b, _ := io.ReadAll(r)
	got := string(b)
	// Sorted order: alpha, num, zeta.
	if !strings.HasPrefix(got, "alpha=a&num=7&zeta=z") {
		t.Errorf("form body not sorted as expected: %q", got)
	}
}

func TestEncodeFormBody_EmptyBodyProducesEmptyForm(t *testing.T) {
	r, _, err := EncodeFormBody(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r)
	if string(b) != "" {
		t.Errorf("expected empty form body, got %q", string(b))
	}
}
