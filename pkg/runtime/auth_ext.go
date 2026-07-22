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
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// RequestAuthProvider applies credentials or a request signature immediately
// before an upstream request is sent. It is the extension point for security
// schemes that cannot be configured from static environment values, such as
// OIDC workload identity and AWS SigV4. Providers must return an error rather
// than allowing a request to be sent with partial credentials.
type RequestAuthProvider interface {
	Apply(ctx context.Context, req *http.Request) error
}

// RequestAuthProviderFunc adapts a function to RequestAuthProvider. It is
// useful for applications that need to supply an OIDC or SigV4 signer without
// coupling generated proxy code to a cloud SDK.
type RequestAuthProviderFunc func(context.Context, *http.Request) error

// Apply implements RequestAuthProvider.
func (f RequestAuthProviderFunc) Apply(ctx context.Context, req *http.Request) error {
	if f == nil {
		return fmt.Errorf("request auth provider is nil")
	}
	return f(ctx, req)
}

// ApplyRequestAuth invokes provider after rejecting invalid inputs. Generated
// and dynamic proxy paths can use this helper to keep custom credentials
// fail-closed at the same boundary as built-in authentication.
func ApplyRequestAuth(ctx context.Context, req *http.Request, provider RequestAuthProvider) error {
	if req == nil {
		return fmt.Errorf("request auth requires a non-nil request")
	}
	if provider == nil {
		return fmt.Errorf("request auth provider is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return provider.Apply(ctx, req)
}

// ClientCredentialsConfig configures OAuth 2.0 client-credentials token
// acquisition. The client authenticates at the token endpoint using HTTP Basic
// authentication, as defined by RFC 6749 section 2.3.1. AdditionalParameters
// permits provider-specific token fields without coupling the runtime to a
// vendor SDK.
type ClientCredentialsConfig struct {
	TokenURL             string
	ClientID             string
	ClientSecret         string
	Scopes               []string
	AdditionalParameters url.Values
	HTTPClient           *http.Client
	// AllowInsecureHTTP permits a cleartext token endpoint. It is intended
	// only for local test environments; production callers must use HTTPS.
	AllowInsecureHTTP bool
	// TokenTimeout bounds token acquisition when the supplied HTTP client has
	// no timeout. Zero defaults to 15 seconds.
	TokenTimeout time.Duration
	// RefreshSkew causes a cached token to be refreshed before expiry. Zero
	// defaults to 30 seconds. It must not be negative.
	RefreshSkew time.Duration
	// Now is primarily useful for deterministic tests. Production callers
	// should leave it nil.
	Now func() time.Time
}

// ClientCredentialsProvider obtains and caches bearer tokens for an OAuth 2.0
// client-credentials grant. A provider serializes refreshes, so simultaneous
// incoming tool calls cannot stampede the token endpoint.
type ClientCredentialsProvider struct {
	config ClientCredentialsConfig
	client *http.Client
	now    func() time.Time

	mu          sync.Mutex
	token       string
	expiry      time.Time
	refreshing  bool
	refreshDone chan struct{}
}

// NewClientCredentialsProvider validates cfg and returns a reusable provider.
func NewClientCredentialsProvider(cfg ClientCredentialsConfig) (*ClientCredentialsProvider, error) {
	if strings.TrimSpace(cfg.TokenURL) == "" {
		return nil, fmt.Errorf("OAuth client credentials token URL is required")
	}
	u, err := url.Parse(cfg.TokenURL)
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("OAuth client credentials token URL must be an absolute http(s) URL")
	}
	if u.Scheme != "https" && !cfg.AllowInsecureHTTP {
		return nil, fmt.Errorf("OAuth client credentials token URL must use HTTPS (set AllowInsecureHTTP only for local testing)")
	}
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("OAuth client credentials client ID is required")
	}
	if cfg.ClientSecret == "" {
		return nil, fmt.Errorf("OAuth client credentials client secret is required")
	}
	if cfg.RefreshSkew < 0 {
		return nil, fmt.Errorf("OAuth client credentials refresh skew must not be negative")
	}
	if cfg.RefreshSkew == 0 {
		cfg.RefreshSkew = 30 * time.Second
	}
	if cfg.TokenTimeout < 0 {
		return nil, fmt.Errorf("OAuth client credentials token timeout must not be negative")
	}
	if cfg.TokenTimeout == 0 {
		cfg.TokenTimeout = 15 * time.Second
	}
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	client = cloneTokenHTTPClient(client, cfg.TokenTimeout)
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &ClientCredentialsProvider{config: cfg, client: client, now: now}, nil
}

// Apply obtains a usable access token and attaches it as a Bearer credential.
func (p *ClientCredentialsProvider) Apply(ctx context.Context, req *http.Request) error {
	if p == nil {
		return fmt.Errorf("OAuth client credentials provider is nil")
	}
	if req == nil {
		return fmt.Errorf("OAuth client credentials requires a non-nil request")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	token, err := p.tokenFor(ctx)
	if err != nil {
		return err
	}
	return ApplyBearer(req, token)
}

func (p *ClientCredentialsProvider) tokenFor(ctx context.Context) (string, error) {
	for {
		p.mu.Lock()
		if p.token != "" && !p.expiry.IsZero() && p.now().Before(p.expiry.Add(-p.config.RefreshSkew)) {
			token := p.token
			p.mu.Unlock()
			return token, nil
		}
		if p.refreshing {
			done := p.refreshDone
			p.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		p.refreshing = true
		p.refreshDone = make(chan struct{})
		p.mu.Unlock()

		token, expiry, err := p.fetchToken(ctx)
		p.mu.Lock()
		if err == nil {
			p.token = token
			p.expiry = expiry
		}
		p.refreshing = false
		close(p.refreshDone)
		p.mu.Unlock()
		if err != nil {
			return "", err
		}
		return token, nil
	}
}

func (p *ClientCredentialsProvider) fetchToken(ctx context.Context) (string, time.Time, error) {
	form := make(url.Values, len(p.config.AdditionalParameters)+2)
	for key, values := range p.config.AdditionalParameters {
		form[key] = append([]string(nil), values...)
	}
	form.Set("grant_type", "client_credentials")
	if len(p.config.Scopes) > 0 {
		form.Set("scope", strings.Join(p.config.Scopes, " "))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.config.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("create OAuth token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(p.config.ClientID, p.config.ClientSecret)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("request OAuth token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		// Do not return the response body: identity providers can include
		// diagnostics or echoed details that should not reach MCP clients.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return "", time.Time{}, fmt.Errorf("OAuth token endpoint returned HTTP %d", resp.StatusCode)
	}

	var payload struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return "", time.Time{}, fmt.Errorf("decode OAuth token response: %w", err)
	}
	if payload.AccessToken == "" {
		return "", time.Time{}, fmt.Errorf("OAuth token response did not contain an access_token")
	}
	if payload.TokenType != "" && !strings.EqualFold(payload.TokenType, "bearer") {
		return "", time.Time{}, fmt.Errorf("OAuth token response has unsupported token_type %q", payload.TokenType)
	}
	expiry := time.Time{}
	if payload.ExpiresIn > 0 {
		const maxDurationSeconds = int64(^uint64(0)>>1) / int64(time.Second)
		if payload.ExpiresIn > maxDurationSeconds {
			return "", time.Time{}, fmt.Errorf("OAuth token response expires_in is too large")
		}
		expiry = p.now().Add(time.Duration(payload.ExpiresIn) * time.Second)
	}
	// A missing expiry cannot safely be cached: obtain a fresh token on the
	// next call rather than risk using a revoked credential.
	return payload.AccessToken, expiry, nil
}

func cloneTokenHTTPClient(base *http.Client, timeout time.Duration) *http.Client {
	clone := *base
	if clone.Timeout == 0 {
		clone.Timeout = timeout
	}
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &clone
}

// MTLSConfig configures client-certificate authentication for an upstream
// HTTPS server. CertificateFile and KeyFile must be supplied together; CAFile
// is optional and, when supplied, replaces the system root pool for the
// returned TLS configuration.
type MTLSConfig struct {
	CertificateFile string
	KeyFile         string
	CAFile          string
	ServerName      string
	// MinVersion defaults to TLS 1.2. Lower versions are rejected.
	MinVersion uint16
}

// TLSConfigFromFiles loads mTLS material from explicit paths without changing
// global TLS state. It never enables InsecureSkipVerify.
func TLSConfigFromFiles(cfg MTLSConfig) (*tls.Config, error) {
	if (cfg.CertificateFile == "") != (cfg.KeyFile == "") {
		return nil, fmt.Errorf("mTLS certificate and key files must be supplied together")
	}
	if cfg.CertificateFile == "" {
		return nil, fmt.Errorf("mTLS certificate and key files are required")
	}
	minVersion := cfg.MinVersion
	if minVersion == 0 {
		minVersion = tls.VersionTLS12
	}
	if minVersion < tls.VersionTLS12 {
		return nil, fmt.Errorf("mTLS minimum TLS version must be TLS 1.2 or later")
	}
	certificate, err := tls.LoadX509KeyPair(cfg.CertificateFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load mTLS client certificate: %w", err)
	}
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   minVersion,
		ServerName:   cfg.ServerName,
	}
	if cfg.CAFile == "" {
		return tlsConfig, nil
	}
	caPEM, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		return nil, fmt.Errorf("read mTLS CA file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("mTLS CA file does not contain a valid certificate")
	}
	tlsConfig.RootCAs = pool
	return tlsConfig, nil
}

// HTTPClientWithMTLS returns a cloned HTTP client whose cloned transport uses
// cfg. The base client and transport are never modified, which permits a
// process to route different APIs through independently configured mTLS
// connections.
func HTTPClientWithMTLS(base *http.Client, cfg MTLSConfig) (*http.Client, error) {
	tlsConfig, err := TLSConfigFromFiles(cfg)
	if err != nil {
		return nil, err
	}
	if base == nil {
		base = &http.Client{}
	}
	// http.Client has no Clone method. A shallow copy is sufficient here:
	// its fields are values or immutable interface pointers, and we replace
	// Transport below with a separately cloned *http.Transport.
	clientValue := *base
	client := &clientValue
	var transport *http.Transport
	switch configured := base.Transport.(type) {
	case nil:
		transport = http.DefaultTransport.(*http.Transport).Clone()
	case *http.Transport:
		if configured == nil {
			transport = http.DefaultTransport.(*http.Transport).Clone()
		} else {
			transport = configured.Clone()
		}
	default:
		return nil, fmt.Errorf("mTLS requires an *http.Transport, got %T", base.Transport)
	}
	transport.TLSClientConfig = tlsConfig
	client.Transport = transport
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client, nil
}
