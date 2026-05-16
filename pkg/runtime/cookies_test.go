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

func TestCookieRequestEditor_AddsNonEmptyCookies(t *testing.T) {
	editor := CookieRequestEditor(CookieValues{
		"session": "abc",
		"csrf":    "xyz",
	})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com", nil)
	if err := editor(context.Background(), req); err != nil {
		t.Fatalf("editor: %v", err)
	}
	got := map[string]string{}
	for _, c := range req.Cookies() {
		got[c.Name] = c.Value
	}
	if got["session"] != "abc" || got["csrf"] != "xyz" {
		t.Errorf("cookies = %v", got)
	}
}

func TestCookieRequestEditor_SkipsEmpty(t *testing.T) {
	editor := CookieRequestEditor(CookieValues{
		"session": "abc",
		"csrf":    "",
	})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com", nil)
	_ = editor(context.Background(), req)
	for _, c := range req.Cookies() {
		if c.Name == "csrf" {
			t.Errorf("empty cookie value should be skipped, got %+v", c)
		}
	}
}

func TestCookieRequestEditor_DeterministicOrder(t *testing.T) {
	// Two runs over the same map must emit cookies in identical order; the
	// editor sorts internally so map-iteration randomness can't leak out.
	values := CookieValues{"b": "2", "a": "1", "c": "3"}
	first := cookieHeaderFromEditor(t, values)
	for i := 0; i < 5; i++ {
		if got := cookieHeaderFromEditor(t, values); got != first {
			t.Fatalf("cookie order diverged across runs: %q vs %q", first, got)
		}
	}
	// And the order must actually be sorted (a then b then c).
	if first != "a=1; b=2; c=3" {
		t.Errorf("expected sorted Cookie header, got %q", first)
	}
}

func cookieHeaderFromEditor(t *testing.T, values CookieValues) string {
	t.Helper()
	editor := CookieRequestEditor(values)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com", nil)
	if err := editor(context.Background(), req); err != nil {
		t.Fatalf("editor: %v", err)
	}
	return req.Header.Get("Cookie")
}

func TestCookieRequestEditor_EmptyValuesMap(t *testing.T) {
	editor := CookieRequestEditor(nil)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com", nil)
	if err := editor(context.Background(), req); err != nil {
		t.Fatalf("editor on nil map: %v", err)
	}
	if got := req.Header.Get("Cookie"); got != "" {
		t.Errorf("nil map should add no cookies, got %q", got)
	}
}
