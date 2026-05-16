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
	"net/http"
	"reflect"
	"testing"
)

func TestDecodeArguments_Map(t *testing.T) {
	in := map[string]any{"a": float64(1)}
	got, err := DecodeArguments(in)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("map should round-trip identically, got %v", got)
	}
}

func TestDecodeArguments_RawJSON(t *testing.T) {
	raw := json.RawMessage(`{"a":1,"b":"x"}`)
	got, err := DecodeArguments(raw)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got["a"].(float64) != 1 || got["b"].(string) != "x" {
		t.Fatalf("decoded: %v", got)
	}
}

func TestDecodeArguments_Bytes(t *testing.T) {
	got, err := DecodeArguments([]byte(`{"k":"v"}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got["k"] != "v" {
		t.Fatalf("got %v", got)
	}
}

func TestDecodeArguments_String(t *testing.T) {
	got, err := DecodeArguments(`{"k":"v"}`)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got["k"] != "v" {
		t.Fatalf("got %v", got)
	}
}

func TestDecodeArguments_NilAndEmpty(t *testing.T) {
	for _, in := range []any{nil, json.RawMessage{}, []byte{}, ""} {
		got, err := DecodeArguments(in)
		if err != nil {
			t.Fatalf("err for %T: %v", in, err)
		}
		if len(got) != 0 {
			t.Fatalf("expected empty map for %T, got %v", in, got)
		}
	}
}

func TestDecodeArguments_MalformedJSON(t *testing.T) {
	_, err := DecodeArguments(json.RawMessage(`{"a":}`))
	if err == nil {
		t.Fatal("expected error")
	}
	var te *ToolError
	if !errors.As(err, &te) || te.Code != "invalid_arguments" {
		t.Fatalf("expected invalid_arguments ToolError, got %#v", err)
	}
}

func TestBuildHTTPMeta(t *testing.T) {
	if m := BuildHTTPMeta(nil); m != nil {
		t.Errorf("nil result should produce nil meta, got %v", m)
	}
	if m := BuildHTTPMeta(&CallToolResult{}); m != nil {
		t.Errorf("zero result should produce nil meta, got %v", m)
	}
	res := &CallToolResult{
		StatusCode: 201,
		Headers:    map[string]string{"Location": "/x"},
	}
	m := BuildHTTPMeta(res)
	if m == nil {
		t.Fatal("expected non-nil meta")
	}
	if m.StatusCode != 201 || m.Headers["Location"] != "/x" {
		t.Errorf("meta: %+v", m)
	}
}

func TestNewToolResultFromHTTP_ProducesHTTPMeta(t *testing.T) {
	header := http.Header{}
	header.Set("Location", "/abc")
	res := NewToolResultFromHTTP(201, header, []byte(`{}`), "application/json")
	m := BuildHTTPMeta(res)
	if m == nil || m.StatusCode != 201 || m.Headers["Location"] != "/abc" {
		t.Fatalf("expected HTTPMeta carrying 201+Location, got %+v", m)
	}
}
