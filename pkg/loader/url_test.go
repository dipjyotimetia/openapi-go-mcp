// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package loader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const minimalSpec = `openapi: 3.0.3
info:
  title: T
  version: "1"
paths:
  /x:
    get:
      operationId: getX
      responses:
        '200':
          description: OK
`

func TestLoadFromURL_OpenAPI3(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write([]byte(minimalSpec))
	}))
	defer srv.Close()

	doc, err := LoadFromURL(context.Background(), srv.URL+"/spec.yaml")
	if err != nil {
		t.Fatalf("LoadFromURL: %v", err)
	}
	if doc.Info.Title != "T" {
		t.Errorf("expected title T, got %q", doc.Info.Title)
	}
}

func TestLoadFromURL_LoadDispatchesURLs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(minimalSpec))
	}))
	defer srv.Close()
	if _, err := Load(context.Background(), srv.URL+"/spec.yaml"); err != nil {
		t.Fatalf("Load(URL): %v", err)
	}
}

func TestLoadFromURL_RejectsBadScheme(t *testing.T) {
	_, err := LoadFromURL(context.Background(), "ftp://example.com/spec")
	if err == nil || !strings.Contains(err.Error(), "unsupported scheme") {
		t.Errorf("expected unsupported-scheme error, got %v", err)
	}
}

func TestLoadFromURL_RejectsNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	_, err := LoadFromURL(context.Background(), srv.URL+"/missing")
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("expected HTTP 404 surfaced, got %v", err)
	}
}

func TestLoadFromURL_RejectsOversized(t *testing.T) {
	huge := strings.Repeat("a", 1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(huge))
	}))
	defer srv.Close()
	_, err := LoadFromURL(context.Background(), srv.URL+"/big", WithMaxBodySize(100))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("expected size-cap error, got %v", err)
	}
}

func TestLoadFromURL_CustomClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test-Token") != "shibboleth" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(minimalSpec))
	}))
	defer srv.Close()

	// A custom transport injects the auth header on every request.
	rt := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		req.Header.Set("X-Test-Token", "shibboleth")
		return http.DefaultTransport.RoundTrip(req)
	})
	client := &http.Client{Transport: rt}

	if _, err := LoadFromURL(context.Background(), srv.URL+"/spec", WithHTTPClient(client)); err != nil {
		t.Fatalf("LoadFromURL with custom client: %v", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
