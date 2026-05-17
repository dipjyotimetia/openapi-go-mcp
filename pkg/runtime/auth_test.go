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
	"errors"
	"net/http"
	"strings"
	"testing"
)

func newReq(t *testing.T, target string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	return r
}

func TestApplyAPIKey_Header(t *testing.T) {
	r := newReq(t, "https://api.example.com/things")
	if err := ApplyAPIKey(r, AuthInHeader, "X-API-Key", "secret"); err != nil {
		t.Fatalf("ApplyAPIKey: %v", err)
	}
	if got := r.Header.Get("X-API-Key"); got != "secret" {
		t.Errorf("X-API-Key header: got %q, want %q", got, "secret")
	}
}

func TestApplyAPIKey_Query_PreservesExisting(t *testing.T) {
	// An operation may already have query params set on the URL when the
	// auth helper runs; ApplyAPIKey must not clobber them.
	r := newReq(t, "https://api.example.com/things?keep=me&also=present")
	if err := ApplyAPIKey(r, AuthInQuery, "api_key", "tok"); err != nil {
		t.Fatalf("ApplyAPIKey: %v", err)
	}
	q := r.URL.Query()
	if q.Get("api_key") != "tok" {
		t.Errorf("api_key not set: %q", r.URL.RawQuery)
	}
	if q.Get("keep") != "me" || q.Get("also") != "present" {
		t.Errorf("existing query params clobbered: %q", r.URL.RawQuery)
	}
}

func TestApplyAPIKey_Cookie(t *testing.T) {
	r := newReq(t, "https://api.example.com/")
	if err := ApplyAPIKey(r, AuthInCookie, "session", "abc"); err != nil {
		t.Fatalf("ApplyAPIKey: %v", err)
	}
	cookies := r.Cookies()
	if len(cookies) != 1 || cookies[0].Name != "session" || cookies[0].Value != "abc" {
		t.Errorf("cookie not attached correctly: %+v", cookies)
	}
}

func TestApplyAPIKey_EmptyValueErrors(t *testing.T) {
	r := newReq(t, "https://api.example.com/")
	if err := ApplyAPIKey(r, AuthInHeader, "X-API-Key", ""); err == nil {
		t.Error("expected error for empty apiKey value")
	}
}

func TestApplyAPIKey_UnknownLocationErrors(t *testing.T) {
	r := newReq(t, "https://api.example.com/")
	err := ApplyAPIKey(r, AuthLocation("body"), "X-API-Key", "tok")
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("expected unsupported-location error, got %v", err)
	}
}

func TestApplyBearer(t *testing.T) {
	r := newReq(t, "https://api.example.com/")
	if err := ApplyBearer(r, "deadbeef"); err != nil {
		t.Fatalf("ApplyBearer: %v", err)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer deadbeef" {
		t.Errorf("Authorization: got %q, want %q", got, "Bearer deadbeef")
	}
}

func TestApplyBearer_EmptyErrors(t *testing.T) {
	r := newReq(t, "https://api.example.com/")
	if err := ApplyBearer(r, ""); err == nil {
		t.Error("expected error for empty bearer token")
	}
}

func TestApplyBasic(t *testing.T) {
	r := newReq(t, "https://api.example.com/")
	if err := ApplyBasic(r, "user", "p@ss"); err != nil {
		t.Fatalf("ApplyBasic: %v", err)
	}
	got := r.Header.Get("Authorization")
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:p@ss"))
	if got != want {
		t.Errorf("Authorization: got %q, want %q", got, want)
	}
}

func TestApplyBasic_MissingHalfErrors(t *testing.T) {
	r := newReq(t, "https://api.example.com/")
	if err := ApplyBasic(r, "user", ""); err == nil {
		t.Error("expected error when password missing")
	}
	if err := ApplyBasic(r, "", "secret"); err == nil {
		t.Error("expected error when username missing")
	}
}

func TestMissingCredentialError_Format(t *testing.T) {
	e := &MissingCredentialError{SchemeName: "githubAuth", EnvVar: "BEARER_TOKEN_GITHUBAUTH"}
	msg := e.Error()
	for _, want := range []string{"githubAuth", "BEARER_TOKEN_GITHUBAUTH"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q: %s", want, msg)
		}
	}
	// Must satisfy `errors.As` so callers can branch on the type.
	var got *MissingCredentialError
	if !errors.As(e, &got) {
		t.Errorf("errors.As must recognise *MissingCredentialError")
	}
}

func TestAppendQuery_Encodes(t *testing.T) {
	r := newReq(t, "https://api.example.com/?keep=1")
	AppendQuery(r, "foo", "bar baz")
	q := r.URL.Query()
	if q.Get("foo") != "bar baz" {
		t.Errorf("foo query: %q", r.URL.RawQuery)
	}
	if q.Get("keep") != "1" {
		t.Errorf("existing query lost: %q", r.URL.RawQuery)
	}
	// RawQuery must round-trip through net/url's canonical encoding so
	// the upstream receives the value we intended.
	if !strings.Contains(r.URL.RawQuery, "foo=bar+baz") {
		t.Errorf("expected URL-encoded form for spaces; got %q", r.URL.RawQuery)
	}
}

func TestQueryEscape_ReExport(t *testing.T) {
	// This re-export exists so generated code doesn't need net/url import.
	if got := QueryEscape("a b/c"); got != "a+b%2Fc" {
		t.Errorf("QueryEscape: got %q", got)
	}
}
