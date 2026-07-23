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
	"net/http"
	"net/http/httptest"
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

func TestReadResponseBodyLimitRejectsDeclaredAndStreamedOversize(t *testing.T) {
	for _, response := range []*http.Response{
		{Body: io.NopCloser(strings.NewReader("12345")), ContentLength: 5},
		{Body: io.NopCloser(strings.NewReader("12345")), ContentLength: -1},
	} {
		_, err := ReadResponseBodyLimit(response, 4)
		var toolErr *ToolError
		if !errors.As(err, &toolErr) || toolErr.Code != "response_too_large" || toolErr.Status != http.StatusBadGateway {
			t.Fatalf("oversize result = %v", err)
		}
	}
}

func TestReadResponseBodyLimitAcceptsBoundedResponse(t *testing.T) {
	body, err := ReadResponseBodyLimit(&http.Response{Body: io.NopCloser(strings.NewReader("1234")), ContentLength: 4}, 4)
	if err != nil || string(body) != "1234" {
		t.Fatalf("body = %q, err = %v", body, err)
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

func TestSerializeProxyParam_OpenAPIStyles(t *testing.T) {
	args := map[string]any{
		"path": map[string]any{"id": []any{"red", "blue"}},
		"query": map[string]any{
			"tags":   []any{"red", "blue"},
			"filter": map[string]any{"status": "available", "limit": float64(10)},
		},
	}

	path, present, err := SerializeProxyParam(args, ProxyParamSpec{
		Name: "id", In: "path", Style: "label", Explode: true,
	}, true)
	if err != nil || !present || path.Value != ".red.blue" {
		t.Fatalf("label path = (%+v, %v, %v), want (.red.blue, true, nil)", path, present, err)
	}

	tags, present, err := SerializeProxyParam(args, ProxyParamSpec{
		Name: "tags", In: "query", Style: "form", Explode: true,
	}, false)
	if err != nil || !present || tags.Query.Encode() != "tags=red&tags=blue" {
		t.Fatalf("exploded form query = (%+v, %v, %v)", tags, present, err)
	}

	filter, present, err := SerializeProxyParam(args, ProxyParamSpec{
		Name: "filter", In: "query", Style: "deepObject", Explode: true,
	}, false)
	if err != nil || !present || filter.Query.Encode() != "filter%5Blimit%5D=10&filter%5Bstatus%5D=available" {
		t.Fatalf("deep object query = (%+v, %v, %v)", filter, present, err)
	}
}

func TestSerializeProxyParam_AllowReservedPreservesQueryReservedCharacters(t *testing.T) {
	args := map[string]any{"query": map[string]any{"redirect": "/a/b?x=1&y=2"}}
	param, present, err := SerializeProxyParam(args, ProxyParamSpec{
		Name: "redirect", In: "query", Style: "form", Explode: true, AllowReserved: true,
	}, false)
	if err != nil || !present {
		t.Fatalf("serialize: present=%v err=%v", present, err)
	}
	got, err := BuildProxyURL("https://api.example.com", "/pets", param.Query)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://api.example.com/pets?redirect=/a/b?x=1&y=2" {
		t.Errorf("allowReserved URL = %q", got)
	}
}

func TestSerializeProxyParam_LabelAndDelimitedObjects(t *testing.T) {
	args := map[string]any{
		"path":  map[string]any{"label": []any{"blue", "black"}, "object": map[string]any{"R": "100", "G": "200"}},
		"query": map[string]any{"space": map[string]any{"R": "100", "G": "200"}, "pipe": map[string]any{"R": "100", "G": "200"}},
	}
	cases := []struct {
		name string
		spec ProxyParamSpec
		want string
	}{
		{"label array", ProxyParamSpec{Name: "label", In: "path", Style: "label"}, ".blue.black"},
		{"label object", ProxyParamSpec{Name: "object", In: "path", Style: "label"}, ".G.200.R.100"},
		{"space object", ProxyParamSpec{Name: "space", In: "query", Style: "spaceDelimited"}, "space=G%20200%20R%20100"},
		{"pipe object", ProxyParamSpec{Name: "pipe", In: "query", Style: "pipeDelimited"}, "pipe=G%7C200%7CR%7C100"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, present, err := SerializeProxyParam(args, tt.spec, true)
			if err != nil || !present {
				t.Fatalf("serialize: %+v present=%v err=%v", got, present, err)
			}
			actual := got.Value
			if len(got.Query) > 0 {
				actual = got.Query.Encode()
			}
			if actual != tt.want {
				t.Errorf("serialized = %q, want %q", actual, tt.want)
			}
		})
	}
}

func TestBuildProxyURL_PreservesSerializedPathAndFragmentValue(t *testing.T) {
	pathArgs := map[string]any{"path": map[string]any{"id": "a/b"}}
	pathParam, _, err := SerializeProxyParam(pathArgs, ProxyParamSpec{Name: "id", In: "path", Style: "simple"}, true)
	if err != nil {
		t.Fatal(err)
	}
	queryArgs := map[string]any{"query": map[string]any{"redirect": "/a#section?x=1"}}
	queryParam, _, err := SerializeProxyParam(queryArgs, ProxyParamSpec{Name: "redirect", In: "query", Style: "form", Explode: true, AllowReserved: true}, true)
	if err != nil {
		t.Fatal(err)
	}

	var gotPath, gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.EscapedPath(), r.URL.RawQuery
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	target, err := BuildProxyURL(upstream.URL, "/things/"+pathParam.Value, queryParam.Query)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if gotPath != "/things/a%2Fb" {
		t.Errorf("escaped path = %q, want /things/a%%2Fb", gotPath)
	}
	if gotQuery != "redirect=/a%23section?x=1" {
		t.Errorf("raw query = %q, want hash preserved as escaped data", gotQuery)
	}
}

func TestSerializeProxyParam_CookieExplodeAndEmptyArray(t *testing.T) {
	args := map[string]any{"cookie": map[string]any{"colors": []any{}, "prefs": map[string]any{"theme": "dark"}}}
	colors, _, err := SerializeProxyParam(args, ProxyParamSpec{Name: "colors", In: "cookie", Style: "form", Explode: true}, true)
	if err != nil || len(colors.Cookies) != 1 || colors.Cookies[0] != (ProxyCookie{Name: "colors"}) {
		t.Fatalf("empty exploded cookie = %+v, err=%v", colors.Cookies, err)
	}
	prefs, _, err := SerializeProxyParam(args, ProxyParamSpec{Name: "prefs", In: "cookie", Style: "form", Explode: true}, true)
	if err != nil || len(prefs.Cookies) != 1 || prefs.Cookies[0] != (ProxyCookie{Name: "theme", Value: "dark"}) {
		t.Fatalf("exploded object cookie = %+v, err=%v", prefs.Cookies, err)
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
