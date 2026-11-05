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
	"net/http"
	"testing"
)

func TestNewToolResultFromHTTP_StructuredContentOnlyForObjects(t *testing.T) {
	h := http.Header{"Content-Type": []string{"application/json"}}
	cases := map[string]bool{
		`{"a":1}`:       true,
		"  \n{\"a\":1}": true,
		`["a","b"]`:     false,
		`"hello"`:       false,
		`42`:            false,
		`null`:          false,
		`true`:          false,
	}
	for body, wantStructured := range cases {
		res := NewToolResultFromHTTP(200, h, []byte(body), "")
		if res.Text != body {
			t.Errorf("%s: Text = %q", body, res.Text)
		}
		if got := res.StructuredContent != nil; got != wantStructured {
			t.Errorf("%s: structured = %v, want %v", body, got, wantStructured)
		}
		if got := NewToolResultJSON([]byte(body)).StructuredContent != nil; got != wantStructured {
			t.Errorf("NewToolResultJSON(%s): structured = %v, want %v", body, got, wantStructured)
		}
	}
}

func TestCookieParamsRequestEditor_ReplacesSameNameOnly(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://example.test", nil)
	req.AddCookie(&http.Cookie{Name: "client", Value: "keep"})
	req.AddCookie(&http.Cookie{Name: "session", Value: ""}) // oapi-codegen zero-valued required field
	editor := CookieParamsRequestEditor(CookiePairs{{Name: "session", Value: "abc"}, {Name: "tag", Value: "a"}, {Name: "tag", Value: "b"}})
	if err := editor(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Cookie"); got != "client=keep; session=abc; tag=a; tag=b" {
		t.Errorf("Cookie = %q", got)
	}

	// No values: request untouched.
	req2, _ := http.NewRequest(http.MethodGet, "http://example.test", nil)
	req2.AddCookie(&http.Cookie{Name: "session", Value: "x"})
	_ = CookieParamsRequestEditor(nil)(context.Background(), req2)
	if got := req2.Header.Get("Cookie"); got != "session=x" {
		t.Errorf("Cookie = %q", got)
	}
}

func TestSerializeProxyParam_ContentParams(t *testing.T) {
	v := map[string]any{"kind": "a b", "n": 1}
	cases := []struct {
		in         string
		wantValue  string
		wantQuery  string
		wantCookie string
	}{
		{in: "path", wantValue: "%7B%22kind%22:%22a%20b%22%2C%22n%22:1%7D"},
		{in: "header", wantValue: `{"kind":"a b","n":1}`},
		{in: "query", wantValue: `{"kind":"a b","n":1}`, wantQuery: `{"kind":"a b","n":1}`},
		{in: "cookie", wantValue: "%7B%22kind%22%3A%22a+b%22%2C%22n%22%3A1%7D", wantCookie: "%7B%22kind%22%3A%22a+b%22%2C%22n%22%3A1%7D"},
	}
	for _, tc := range cases {
		args := map[string]any{tc.in: map[string]any{"p": v}}
		// Style is deliberately invalid for some locations: content params
		// ignore style/explode entirely.
		got, present, err := SerializeProxyParam(args, ProxyParamSpec{Name: "p", In: tc.in, Style: "form", ContentType: "application/json"}, true)
		if err != nil || !present {
			t.Fatalf("%s: err=%v present=%v", tc.in, err, present)
		}
		if got.Value != tc.wantValue {
			t.Errorf("%s: Value = %q, want %q", tc.in, got.Value, tc.wantValue)
		}
		if tc.wantQuery != "" && (len(got.Query) != 1 || got.Query[0].Value != tc.wantQuery) {
			t.Errorf("%s: Query = %+v", tc.in, got.Query)
		}
		if tc.wantCookie != "" && (len(got.Cookies) != 1 || got.Cookies[0].Value != tc.wantCookie) {
			t.Errorf("%s: Cookies = %+v", tc.in, got.Cookies)
		}
	}

	// Non-JSON content types pass strings through verbatim.
	got, _, err := SerializeProxyParam(map[string]any{"header": map[string]any{"p": "raw text"}},
		ProxyParamSpec{Name: "p", In: "header", Style: "simple", ContentType: "text/plain"}, true)
	if err != nil || got.Value != "raw text" {
		t.Errorf("text/plain header = %q, %v", got.Value, err)
	}
}
