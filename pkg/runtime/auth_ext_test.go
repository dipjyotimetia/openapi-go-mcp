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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRequestAuthProviderFunc_AppliesRequestAuth(t *testing.T) {
	t.Parallel()
	req := newReq(t, "https://api.example.com/things")
	provider := RequestAuthProviderFunc(func(_ context.Context, got *http.Request) error {
		got.Header.Set("X-Signed", "yes")
		return nil
	})

	if err := ApplyRequestAuth(context.Background(), req, provider); err != nil {
		t.Fatalf("ApplyRequestAuth: %v", err)
	}
	if got := req.Header.Get("X-Signed"); got != "yes" {
		t.Errorf("X-Signed = %q, want yes", got)
	}
}

func TestApplyRequestAuth_RejectsNilProvider(t *testing.T) {
	t.Parallel()
	err := ApplyRequestAuth(context.Background(), newReq(t, "https://api.example.com"), nil)
	if err == nil {
		t.Fatal("ApplyRequestAuth succeeded with nil provider")
	}
}

func TestClientCredentialsProvider_CachesTokenUntilRefreshWindow(t *testing.T) {
	var requests atomic.Int32
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got, want := r.Method, http.MethodPost; got != want {
			t.Errorf("method = %s, want %s", got, want)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "client-id" || pass != "client-secret" {
			t.Errorf("basic auth = %q/%q/%v", user, pass, ok)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "client_credentials" {
			t.Errorf("grant_type = %q", got)
		}
		if got := r.Form.Get("scope"); got != "read write" {
			t.Errorf("scope = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "issued-token", "token_type": "Bearer", "expires_in": 120})
	}))
	defer tokenServer.Close()

	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	provider, err := NewClientCredentialsProvider(ClientCredentialsConfig{
		TokenURL:     tokenServer.URL,
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		Scopes:       []string{"read", "write"},
		RefreshSkew:  30 * time.Second,
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewClientCredentialsProvider: %v", err)
	}

	for range 2 {
		req := newReq(t, "https://api.example.com/things")
		if err := provider.Apply(context.Background(), req); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer issued-token" {
			t.Errorf("Authorization = %q", got)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("token requests = %d, want 1", got)
	}

	now = now.Add(91 * time.Second) // 29 seconds remain: inside the refresh window.
	if err := provider.Apply(context.Background(), newReq(t, "https://api.example.com/things")); err != nil {
		t.Fatalf("Apply after refresh window: %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Errorf("token requests after refresh = %d, want 2", got)
	}
}

func TestClientCredentialsProvider_RejectsOAuthErrorWithoutLeakingBody(t *testing.T) {
	t.Parallel()
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"error":"invalid_client","error_description":"secret diagnostic"}`)
	}))
	defer tokenServer.Close()

	provider, err := NewClientCredentialsProvider(ClientCredentialsConfig{
		TokenURL: tokenServer.URL, ClientID: "id", ClientSecret: "secret",
	})
	if err != nil {
		t.Fatalf("NewClientCredentialsProvider: %v", err)
	}
	err = provider.Apply(context.Background(), newReq(t, "https://api.example.com/things"))
	if err == nil {
		t.Fatal("Apply succeeded despite token endpoint error")
	}
	if got := err.Error(); got == "" || strings.Contains(got, "secret diagnostic") {
		t.Errorf("error should identify failure without response body: %q", got)
	}
}

func TestTLSConfigFromFiles_LoadsClientCertificateAndCA(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	certPath, keyPath, caPath := writeTLSFixture(t, dir)

	cfg, err := TLSConfigFromFiles(MTLSConfig{
		CertificateFile: certPath,
		KeyFile:         keyPath,
		CAFile:          caPath,
		ServerName:      "upstream.internal",
	})
	if err != nil {
		t.Fatalf("TLSConfigFromFiles: %v", err)
	}
	if got := len(cfg.Certificates); got != 1 {
		t.Errorf("client certificates = %d, want 1", got)
	}
	if cfg.RootCAs == nil || cfg.ServerName != "upstream.internal" || cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("unexpected TLS config: %#v", cfg)
	}
}

func TestHTTPClientWithMTLS_ClonesClientAndTransport(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	certPath, keyPath, _ := writeTLSFixture(t, dir)
	baseTransport := http.DefaultTransport.(*http.Transport).Clone()
	base := &http.Client{Transport: baseTransport, Timeout: time.Second}

	client, err := HTTPClientWithMTLS(base, MTLSConfig{CertificateFile: certPath, KeyFile: keyPath})
	if err != nil {
		t.Fatalf("HTTPClientWithMTLS: %v", err)
	}
	if client == base || client.Transport == base.Transport || client.Timeout != base.Timeout {
		t.Errorf("base client or transport was not safely cloned")
	}
	if transport := client.Transport.(*http.Transport); transport.TLSClientConfig == nil || len(transport.TLSClientConfig.Certificates) != 1 {
		t.Errorf("mTLS transport missing client certificate: %#v", transport.TLSClientConfig)
	}
}

func TestTLSConfigFromFiles_RequiresCertificateAndKeyTogether(t *testing.T) {
	t.Parallel()
	_, err := TLSConfigFromFiles(MTLSConfig{CertificateFile: "only-cert.pem"})
	if err == nil {
		t.Fatal("TLSConfigFromFiles succeeded without key")
	}
}

func writeTLSFixture(t *testing.T, dir string) (certPath, keyPath, caPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	caPath = filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath, caPath
}
