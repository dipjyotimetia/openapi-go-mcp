// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package runtime

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestNewToolResultFromHTTP_200JSON(t *testing.T) {
	body := []byte(`{"id":42,"name":"Fido"}`)
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("ETag", `"abc123"`)
	header.Set("X-Request-Id", "req-1")

	res := NewToolResultFromHTTP(200, header, body, "")

	if res.IsError {
		t.Fatalf("2xx should not be IsError")
	}
	if res.StatusCode != 200 {
		t.Errorf("StatusCode: got %d, want 200", res.StatusCode)
	}
	if res.Headers["Etag"] != `"abc123"` && res.Headers["ETag"] != `"abc123"` {
		t.Errorf("ETag header lost: got %v", res.Headers)
	}
	if res.Headers["X-Request-Id"] != "req-1" {
		t.Errorf("X-Request-Id should propagate via X-* allowlist: got %v", res.Headers)
	}
	if res.Text != string(body) {
		t.Errorf("Text: got %q want %q", res.Text, body)
	}
	if _, ok := res.StructuredContent.(json.RawMessage); !ok {
		t.Errorf("StructuredContent should be json.RawMessage for JSON body, got %T", res.StructuredContent)
	}
}

func TestNewToolResultFromHTTP_201WithLocation(t *testing.T) {
	body := []byte(`{"id":7}`)
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("Location", "/things/7")

	res := NewToolResultFromHTTP(201, header, body, "")

	if res.IsError {
		t.Fatalf("201 should be success")
	}
	if res.StatusCode != 201 {
		t.Errorf("StatusCode: got %d", res.StatusCode)
	}
	if res.Headers["Location"] != "/things/7" {
		t.Errorf("Location header should propagate: got %v", res.Headers)
	}
}

func TestNewToolResultFromHTTP_204Empty(t *testing.T) {
	res := NewToolResultFromHTTP(204, nil, nil, "")
	if res.IsError {
		t.Fatalf("204 should be success")
	}
	if res.StatusCode != 204 {
		t.Errorf("StatusCode: got %d", res.StatusCode)
	}
	if res.StructuredContent != nil {
		t.Errorf("204 should have nil StructuredContent, got %v", res.StructuredContent)
	}
	if res.Text != "" {
		t.Errorf("204 should have empty Text, got %q", res.Text)
	}
}

func TestNewToolResultFromHTTP_404ErrorBody(t *testing.T) {
	body := []byte(`{"error":"not found"}`)
	header := http.Header{}
	header.Set("Content-Type", "application/json")

	res := NewToolResultFromHTTP(404, header, body, "")

	if !res.IsError {
		t.Fatalf("404 must be IsError=true")
	}
	if res.StatusCode != 404 {
		t.Errorf("StatusCode: got %d", res.StatusCode)
	}
	env, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent should be map envelope, got %T", res.StructuredContent)
	}
	if status, _ := env["status"].(int); status != 404 {
		t.Errorf("envelope.status: got %v want 404", env["status"])
	}
	decoded, _ := env["body"].(map[string]any)
	if decoded["error"] != "not found" {
		t.Errorf("envelope.body.error: got %v", decoded)
	}
}

func TestNewToolResultFromHTTP_500PlainText(t *testing.T) {
	body := []byte("upstream went boom")
	header := http.Header{}
	header.Set("Content-Type", "text/plain")

	res := NewToolResultFromHTTP(500, header, body, "")
	if !res.IsError {
		t.Fatalf("500 must be IsError")
	}
	env := res.StructuredContent.(map[string]any)
	if env["body"] != "upstream went boom" {
		t.Errorf("plain-text error body should stay a string, got %v (%T)", env["body"], env["body"])
	}
}

func TestNewToolResultFromHTTP_2xxText_VerbatimNotBase64(t *testing.T) {
	body := []byte("hello world")
	header := http.Header{}
	header.Set("Content-Type", "text/plain")

	res := NewToolResultFromHTTP(200, header, body, "")
	if res.IsError {
		t.Fatalf("2xx text should be success")
	}
	if res.Text != "hello world" {
		t.Errorf("text/* should pass through verbatim, got %q", res.Text)
	}
	sc, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("text/* should produce {contentType,text} envelope, got %T", res.StructuredContent)
	}
	if sc["text"] != "hello world" {
		t.Errorf("envelope.text: got %v", sc["text"])
	}
}

func TestNewToolResultFromHTTP_2xxNonJSON_Base64(t *testing.T) {
	raw := []byte{0x01, 0x02, 0x03}
	header := http.Header{}
	header.Set("Content-Type", "image/png")

	res := NewToolResultFromHTTP(200, header, raw, "")
	if res.IsError {
		t.Fatalf("2xx binary should still be success")
	}
	sc, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent should be base64 envelope, got %T", res.StructuredContent)
	}
	if sc["contentType"] != "image/png" {
		t.Errorf("contentType: got %v", sc["contentType"])
	}
	want := base64.StdEncoding.EncodeToString(raw)
	if sc["base64"] != want {
		t.Errorf("base64: got %v want %s", sc["base64"], want)
	}
}

func TestNewToolResultFromHTTP_ContentTypeFallback(t *testing.T) {
	body := []byte(`{"x":1}`)
	// Upstream omitted Content-Type — fallback must drive the JSON branch.
	res := NewToolResultFromHTTP(200, nil, body, "application/vnd.example+json")
	if _, ok := res.StructuredContent.(json.RawMessage); !ok {
		t.Errorf("+json fallback should select JSON branch, got %T", res.StructuredContent)
	}
}

func TestNewToolResultFromHTTP_XHeaderCap(t *testing.T) {
	header := http.Header{}
	for i := 0; i < maxExtraXHeaders+10; i++ {
		header.Set("X-Custom-"+itoa(i), "v")
	}
	res := NewToolResultFromHTTP(200, header, nil, "")
	if len(res.Headers) > maxExtraXHeaders {
		t.Errorf("X-* headers should be capped at %d, got %d", maxExtraXHeaders, len(res.Headers))
	}
}

func TestNewToolResultFromHTTP_TruncatesOversizedHeaderValues(t *testing.T) {
	header := http.Header{}
	huge := strings.Repeat("a", maxHeaderValueLen+1024)
	header.Set("Cache-Control", huge)
	header.Set("X-Trace", huge)

	res := NewToolResultFromHTTP(200, header, nil, "")
	if got := len(res.Headers["Cache-Control"]); got != maxHeaderValueLen {
		t.Errorf("Cache-Control truncated to %d, want %d", got, maxHeaderValueLen)
	}
	if got := len(res.Headers["X-Trace"]); got != maxHeaderValueLen {
		t.Errorf("X-Trace truncated to %d, want %d", got, maxHeaderValueLen)
	}
}

func TestNewToolResultFromHTTP_DropsArbitraryHeaders(t *testing.T) {
	header := http.Header{}
	header.Set("Server", "nginx") // not on allowlist, not X-*
	header.Set("Set-Cookie", "session=secret")
	header.Set("Location", "/keep")
	res := NewToolResultFromHTTP(200, header, nil, "")
	if _, present := res.Headers["Server"]; present {
		t.Errorf("Server header must not be propagated by default")
	}
	if _, present := res.Headers["Set-Cookie"]; present {
		t.Errorf("Set-Cookie must not be propagated by default")
	}
	if res.Headers["Location"] != "/keep" {
		t.Errorf("Location should be propagated: got %v", res.Headers)
	}
}

func TestClassifyContentType(t *testing.T) {
	cases := map[string]contentClass{
		"application/json":                ctJSON,
		"application/json; charset=UTF-8": ctJSON,
		"application/problem+json":        ctJSON,
		"Application/JSON":                ctJSON,
		"text/plain":                      ctText,
		"text/json":                       ctText, // text/* wins; LLM-readable
		"text/html; charset=utf-8":        ctText,
		"application/xml":                 ctOther,
		"application/octet-stream":        ctOther,
		"":                                ctOther,
		"   ":                             ctOther,
	}
	for ct, want := range cases {
		if got := classifyContentType(ct); got != want {
			t.Errorf("classifyContentType(%q) = %v, want %v", ct, got, want)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// Build-time sanity that the helpers package-export shape we depend on
// elsewhere.
var _ = strings.HasPrefix
