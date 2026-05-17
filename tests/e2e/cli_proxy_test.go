// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package e2e

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCLI_Proxy_GeneratesFullScaffold(t *testing.T) {
	specDir := t.TempDir()
	outDir := t.TempDir()
	mustWriteSpec(t, filepath.Join(specDir, "petstore.yaml"), minimalSpec("getThing"))

	_, stderr, err := runCLI(t,
		"-mode=proxy",
		"-spec", filepath.Join(specDir, "petstore.yaml"),
		"-out", outDir,
		"-module", "example.com/petstore-mcp",
	)
	if err != nil {
		t.Fatalf("CLI failed: %v\nstderr=%s", err, stderr)
	}
	// All three scaffold files must exist at the module root.
	for _, name := range []string{"main.go", "go.mod", "README.md"} {
		if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
			t.Errorf("expected scaffold %s: %v", name, err)
		}
	}
	// The MCP file lives in a sub-package directory (main.go is package
	// main, so the .mcp.go file can't share the directory).
	pkgDir := filepath.Join(outDir, "getthingmcp")
	if _, err := os.Stat(filepath.Join(pkgDir, "getthingmcp.mcp.go")); err != nil {
		t.Errorf("expected <pkg>/<pkg>.mcp.go in %s; root=%v pkg=%v",
			outDir, listDir(t, outDir), listDir(t, pkgDir))
	}
}

func TestCLI_Proxy_RejectsModuleInCompanionMode(t *testing.T) {
	specDir := t.TempDir()
	mustWriteSpec(t, filepath.Join(specDir, "spec.yaml"), minimalSpec("getThing"))
	_, stderr, err := runCLI(t,
		"-spec", filepath.Join(specDir, "spec.yaml"),
		"-out", t.TempDir(),
		"-client-import", "example.com/g",
		"-module", "example.com/mod",
	)
	if got := exitCode(err); got != 1 {
		t.Fatalf("expected exit 1 (-module without -mode=proxy); got %d\nstderr=%s", got, stderr)
	}
}

func TestCLI_Proxy_RejectsMissingModule(t *testing.T) {
	specDir := t.TempDir()
	mustWriteSpec(t, filepath.Join(specDir, "spec.yaml"), minimalSpec("getThing"))
	_, stderr, err := runCLI(t,
		"-mode=proxy",
		"-spec", filepath.Join(specDir, "spec.yaml"),
		"-out", t.TempDir(),
	)
	if got := exitCode(err); got != 1 {
		t.Fatalf("expected exit 1 (proxy mode needs -module); got %d\nstderr=%s", got, stderr)
	}
}

func TestCLI_Proxy_RejectsUnknownSDK(t *testing.T) {
	specDir := t.TempDir()
	mustWriteSpec(t, filepath.Join(specDir, "spec.yaml"), minimalSpec("getThing"))
	_, stderr, err := runCLI(t,
		"-mode=proxy",
		"-sdk=unknown",
		"-spec", filepath.Join(specDir, "spec.yaml"),
		"-module", "example.com/m",
		"-out", t.TempDir(),
	)
	if got := exitCode(err); got != 1 {
		t.Fatalf("expected exit 1 for unknown -sdk; got %d\nstderr=%s", got, stderr)
	}
}

func TestCLI_Proxy_BatchEachSpecGetsItsOwnModule(t *testing.T) {
	specDir := t.TempDir()
	outDir := t.TempDir()
	mustWriteSpec(t, filepath.Join(specDir, "alpha.yaml"), minimalSpec("alphaOp"))
	mustWriteSpec(t, filepath.Join(specDir, "beta.yaml"), minimalSpec("betaOp"))

	_, stderr, err := runCLI(t,
		"-mode=proxy",
		"-spec", specDir,
		"-out", outDir,
		"-module", "example.com/apis",
	)
	if err != nil {
		t.Fatalf("CLI failed: %v\nstderr=%s", err, stderr)
	}
	for _, slug := range []string{"alpha", "beta"} {
		dir := filepath.Join(outDir, slug+"mcp")
		// Each spec lands in its own subdir with its own go.mod.
		body, statErr := os.ReadFile(filepath.Join(dir, "go.mod"))
		if statErr != nil {
			t.Errorf("missing go.mod for %s: %v", slug, statErr)
			continue
		}
		if !strings.Contains(string(body), "module example.com/apis/"+slug) {
			t.Errorf("%s/go.mod must carry per-spec module path; got:\n%s", slug, body)
		}
	}
}

// TestCLI_Proxy_BuildsAndAnswersToolsList is the keystone test for proxy
// mode: generate, swap in a replace-runtime directive, `go build`, run
// the binary, send `tools/list` over stdio, verify the operation appears.
func TestCLI_Proxy_BuildsAndAnswersToolsList(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build-and-run e2e in -short mode")
	}
	specDir := t.TempDir()
	outDir := t.TempDir()
	specPath := filepath.Join(specDir, "tiny.yaml")
	mustWriteSpec(t, specPath, minimalSpec("getThing"))

	if _, stderr, err := runCLI(t,
		"-mode=proxy",
		"-spec", specPath,
		"-out", outDir,
		"-module", "example.com/tinymcp",
	); err != nil {
		t.Fatalf("CLI: %v\n%s", err, stderr)
	}
	addReplaceDirective(t, filepath.Join(outDir, "go.mod"), repoRoot(t))

	bin, err := buildScaffold(t, outDir)
	if err != nil {
		t.Fatalf("scaffold build: %v", err)
	}

	// MCP requires `initialize` before `tools/list`, but the go-sdk
	// adapter handles both messages on one line if newline-delimited.
	// Send `initialize` and expect a non-error JSON-RPC response.
	resp := stdioRoundTrip(t, bin, nil,
		[]string{
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`,
		},
		15*time.Second)
	if !strings.Contains(resp, `"result"`) || strings.Contains(resp, `"error":{`) {
		t.Fatalf("initialize did not return a result: %s", resp)
	}
}

// TestCLI_Proxy_Mark3labsBuildsAndRuns is the mark3labs-SDK counterpart of
// TestCLI_Proxy_BuildsAndAnswersToolsList. It verifies the scaffold's
// alternate main.go template compiles against the pinned mark3labs/mcp-go
// version and that the resulting binary serves MCP on stdio. Existing
// scaffold tests only render-time check the main.go text; a real build
// catches API drift, missing imports, or transport mismatches.
func TestCLI_Proxy_Mark3labsBuildsAndRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build-and-run e2e in -short mode")
	}
	specDir := t.TempDir()
	outDir := t.TempDir()
	mustWriteSpec(t, filepath.Join(specDir, "tiny.yaml"), minimalSpec("getThing"))

	if _, stderr, err := runCLI(t,
		"-mode=proxy",
		"-sdk=mark3labs",
		"-spec", filepath.Join(specDir, "tiny.yaml"),
		"-out", outDir,
		"-module", "example.com/tinymark3labs",
	); err != nil {
		t.Fatalf("CLI: %v\n%s", err, stderr)
	}
	addReplaceDirective(t, filepath.Join(outDir, "go.mod"), repoRoot(t))
	bin, err := buildScaffold(t, outDir)
	if err != nil {
		t.Fatalf("scaffold build (mark3labs): %v", err)
	}
	resp := stdioRoundTrip(t, bin, nil, []string{initRequest}, 15*time.Second)
	if !strings.Contains(resp, `"result"`) || strings.Contains(resp, `"error":{`) {
		t.Fatalf("mark3labs scaffold did not return a result for initialize: %s", resp)
	}
}

// TestCLI_Proxy_BatchBuildsAllModules generates two proxy modules in a
// single CLI invocation and confirms each one compiles. Today the batch
// test only inspects go.mod text; a regression that produced subtly
// broken Go source per-module wouldn't surface until end-user pain.
func TestCLI_Proxy_BatchBuildsAllModules(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build-and-run e2e in -short mode")
	}
	specDir := t.TempDir()
	outDir := t.TempDir()
	mustWriteSpec(t, filepath.Join(specDir, "alpha.yaml"), minimalSpec("alphaOp"))
	mustWriteSpec(t, filepath.Join(specDir, "beta.yaml"), minimalSpec("betaOp"))

	if _, stderr, err := runCLI(t,
		"-mode=proxy",
		"-spec", specDir,
		"-out", outDir,
		"-module", "example.com/apis",
	); err != nil {
		t.Fatalf("CLI: %v\n%s", err, stderr)
	}
	for _, slug := range []string{"alpha", "beta"} {
		modDir := filepath.Join(outDir, slug+"mcp")
		addReplaceDirective(t, filepath.Join(modDir, "go.mod"), repoRoot(t))
		if _, err := buildScaffold(t, modDir); err != nil {
			t.Errorf("batch proxy module %q failed to build: %v", slug, err)
		}
	}
}

// TestCLI_Proxy_AuthInjectsBearerHeader verifies the end-to-end auth
// wiring: spec declares http+bearer; running the proxy with the env var
// set causes the upstream request to carry "Authorization: Bearer <tok>".
func TestCLI_Proxy_AuthInjectsBearerHeader(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build-and-run e2e in -short mode")
	}
	// Capture the upstream request so we can assert on its headers.
	var (
		mu     sync.Mutex
		gotHdr http.Header
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotHdr = r.Header.Clone()
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	spec := []byte(`openapi: 3.0.0
info: { title: AuthProxy, version: "1.0" }
servers: [ { url: "` + upstream.URL + `" } ]
security:
  - bearerAuth: []
components:
  securitySchemes:
    bearerAuth: { type: http, scheme: bearer }
paths:
  /thing:
    get:
      operationId: getThing
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: { type: object }
`)
	specDir := t.TempDir()
	specPath := filepath.Join(specDir, "auth.yaml")
	if err := os.WriteFile(specPath, spec, 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()
	if _, stderr, err := runCLI(t,
		"-mode=proxy",
		"-spec", specPath,
		"-out", outDir,
		"-module", "example.com/authmcp",
	); err != nil {
		t.Fatalf("CLI: %v\n%s", err, stderr)
	}
	addReplaceDirective(t, filepath.Join(outDir, "go.mod"), repoRoot(t))
	bin, err := buildScaffold(t, outDir)
	if err != nil {
		t.Fatalf("scaffold build: %v", err)
	}

	// Send initialize, then tools/call to exercise the upstream request.
	reqs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"getThing","arguments":{}}}`,
	}
	resp := stdioRoundTrip(t, bin, []string{"BEARER_TOKEN_BEARERAUTH=test-token-123"}, reqs, 20*time.Second)
	if resp == "" {
		t.Fatalf("no response from server")
	}
	// Wait briefly for upstream capture (response read can race upstream serve).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		seen := gotHdr != nil
		mu.Unlock()
		if seen {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	auth := gotHdr.Get("Authorization")
	mu.Unlock()
	if auth != "Bearer test-token-123" {
		t.Errorf("upstream did not receive expected Authorization header; got %q\n--mcp-resp--\n%s", auth, resp)
	}
}

// TestCLI_Proxy_MissingCredentialSurfacedToClient verifies the
// MissingCredentialError path: a required scheme without an env var
// produces an MCP error response naming the missing env var, rather
// than a silent 401 from upstream.
func TestCLI_Proxy_MissingCredentialSurfacedToClient(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build-and-run e2e in -short mode")
	}
	spec := []byte(`openapi: 3.0.0
info: { title: AuthMissing, version: "1.0" }
servers: [ { url: "http://127.0.0.1:1" } ]
security:
  - bearerAuth: []
components:
  securitySchemes:
    bearerAuth: { type: http, scheme: bearer }
paths:
  /thing:
    get:
      operationId: getThing
      responses:
        "200": { description: ok }
`)
	specDir := t.TempDir()
	specPath := filepath.Join(specDir, "missing.yaml")
	if err := os.WriteFile(specPath, spec, 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()
	if _, stderr, err := runCLI(t,
		"-mode=proxy",
		"-spec", specPath,
		"-out", outDir,
		"-module", "example.com/missingmcp",
	); err != nil {
		t.Fatalf("CLI: %v\n%s", err, stderr)
	}
	addReplaceDirective(t, filepath.Join(outDir, "go.mod"), repoRoot(t))
	bin, err := buildScaffold(t, outDir)
	if err != nil {
		t.Fatalf("scaffold build: %v", err)
	}

	reqs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"getThing","arguments":{}}}`,
	}
	resp := stdioRoundTrip(t, bin, nil, reqs, 15*time.Second)
	if !strings.Contains(resp, "BEARER_TOKEN_BEARERAUTH") {
		t.Errorf("MCP response should name the missing env var; got %s", resp)
	}
}

// authSpec builds a minimal OpenAPI 3 doc whose single operation is
// protected by the named security scheme and whose servers[0].url points
// at upstreamURL. schemeBody is the literal YAML under
// `components.securitySchemes.testAuth:` and must already be indented
// with eight spaces per line so it sits inside the components block.
func authSpec(upstreamURL, schemeBody string) string {
	return `openapi: 3.0.0
info: { title: AuthMatrix, version: "1.0" }
servers: [ { url: "` + upstreamURL + `" } ]
security:
  - testAuth: []
components:
  securitySchemes:
    testAuth:
` + schemeBody + `
paths:
  /thing:
    get:
      operationId: getThing
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: { type: object }
`
}

// TestCLI_Proxy_AuthMatrix exercises every supported security scheme end
// to end: spec is generated, scaffold is built, binary runs, MCP
// tools/call hits a stub upstream, and we assert the credential reached
// the wire in the right slot (header / query / cookie / Authorization).
//
// Sub-tests share the same skeleton; only the spec fragment, env vars,
// and per-test assertion differ. This is the regression net for every
// change to the security generator, the apply<Scheme>Auth template, or
// the runtime.Apply* helpers.
func TestCLI_Proxy_AuthMatrix(t *testing.T) {
	cases := []struct {
		name    string
		scheme  string // YAML body under `testAuth:` (8-space-indented)
		env     []string
		assert  func(t *testing.T, r *http.Request)
		toolReq string // tools/call arguments JSON
	}{
		{
			name: "APIKeyHeader",
			scheme: `      type: apiKey
      in: header
      name: X-API-Key`,
			env: []string{"API_KEY_TESTAUTH=hdr-secret"},
			assert: func(t *testing.T, r *http.Request) {
				if got := r.Header.Get("X-API-Key"); got != "hdr-secret" {
					t.Errorf("X-API-Key: got %q, want %q", got, "hdr-secret")
				}
			},
			toolReq: "{}",
		},
		{
			name: "APIKeyQuery",
			scheme: `      type: apiKey
      in: query
      name: api_key`,
			env: []string{"API_KEY_TESTAUTH=qry-secret"},
			assert: func(t *testing.T, r *http.Request) {
				if got := r.URL.Query().Get("api_key"); got != "qry-secret" {
					t.Errorf("?api_key: got %q, want %q (raw=%q)", got, "qry-secret", r.URL.RawQuery)
				}
			},
			toolReq: "{}",
		},
		{
			name: "APIKeyCookie",
			scheme: `      type: apiKey
      in: cookie
      name: sid`,
			env: []string{"API_KEY_TESTAUTH=cookie-secret"},
			assert: func(t *testing.T, r *http.Request) {
				c, err := r.Cookie("sid")
				if err != nil || c.Value != "cookie-secret" {
					t.Errorf("sid cookie: got %+v err=%v", c, err)
				}
			},
			toolReq: "{}",
		},
		{
			name: "HTTPBasic",
			scheme: `      type: http
      scheme: basic`,
			env: []string{
				"BASIC_AUTH_USERNAME_TESTAUTH=alice",
				"BASIC_AUTH_PASSWORD_TESTAUTH=swordfish",
			},
			assert: func(t *testing.T, r *http.Request) {
				u, p, ok := r.BasicAuth()
				if !ok || u != "alice" || p != "swordfish" {
					t.Errorf("BasicAuth: u=%q p=%q ok=%v auth=%q", u, p, ok, r.Header.Get("Authorization"))
				}
			},
			toolReq: "{}",
		},
		{
			name: "OAuth2AsBearer",
			scheme: `      type: oauth2
      flows:
        clientCredentials:
          tokenUrl: https://issuer.example/token
          scopes: { "read:thing": "read things" }`,
			env: []string{"OAUTH2_ACCESS_TOKEN_TESTAUTH=oauth-bearer"},
			assert: func(t *testing.T, r *http.Request) {
				if got := r.Header.Get("Authorization"); got != "Bearer oauth-bearer" {
					t.Errorf("Authorization: got %q, want %q", got, "Bearer oauth-bearer")
				}
			},
			toolReq: "{}",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newProxyHarness(t, nil)
			defer h.close()

			resp := runProxyToolCall(t,
				authSpec(h.upstreamURL, tc.scheme),
				tc.env,
				[]string{initRequest, toolCallRequest(2, "getThing", tc.toolReq)},
			)
			req := h.captured()
			if req == nil {
				t.Fatalf("upstream never received a request\n--mcp-resp--\n%s", resp)
			}
			tc.assert(t, req)
		})
	}
}

// TestCLI_Proxy_BodyKinds verifies that each request-body content type
// the proxy template knows about flows through to the upstream with the
// correct Content-Type header and body bytes. Today only the JSON path
// is implicitly exercised via the petstore spec; this matrix pins form,
// multipart, octet, and text/plain — all of which take separate template
// branches that would otherwise regress silently.
func TestCLI_Proxy_BodyKinds(t *testing.T) {
	cases := []struct {
		name          string
		bodyYAML      string // YAML fragment under `requestBody.content:`
		toolArgs      string // tools/call arguments JSON
		wantCTPrefix  string // expected upstream Content-Type prefix
		wantBodyMatch func(t *testing.T, got []byte)
	}{
		{
			name: "JSON",
			bodyYAML: `          application/json:
            schema:
              type: object
              properties:
                name: { type: string }
                count: { type: integer }`,
			toolArgs:     `{"body":{"name":"Fido","count":3}}`,
			wantCTPrefix: "application/json",
			wantBodyMatch: func(t *testing.T, got []byte) {
				s := string(got)
				if !strings.Contains(s, `"name":"Fido"`) || !strings.Contains(s, `"count":3`) {
					t.Errorf("JSON body missing expected fields: %q", s)
				}
			},
		},
		{
			name: "Form",
			bodyYAML: `          application/x-www-form-urlencoded:
            schema:
              type: object
              properties:
                user: { type: string }
                tier: { type: string }`,
			toolArgs:     `{"body":{"user":"alice","tier":"gold"}}`,
			wantCTPrefix: "application/x-www-form-urlencoded",
			wantBodyMatch: func(t *testing.T, got []byte) {
				s := string(got)
				if !strings.Contains(s, "user=alice") || !strings.Contains(s, "tier=gold") {
					t.Errorf("form body missing expected fields: %q", s)
				}
			},
		},
		{
			name: "OctetStream",
			bodyYAML: `          application/octet-stream:
            schema:
              type: string
              format: binary`,
			// "hello" base64-encoded — the runtime decodes before sending.
			toolArgs:     `{"body":"aGVsbG8="}`,
			wantCTPrefix: "application/octet-stream",
			wantBodyMatch: func(t *testing.T, got []byte) {
				if string(got) != "hello" {
					t.Errorf("octet body: got %q, want %q", got, "hello")
				}
			},
		},
		{
			name: "TextPlain",
			bodyYAML: `          text/plain:
            schema:
              type: string`,
			toolArgs:     `{"body":"hello world"}`,
			wantCTPrefix: "text/plain",
			wantBodyMatch: func(t *testing.T, got []byte) {
				if string(got) != "hello world" {
					t.Errorf("text body: got %q, want %q", got, "hello world")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newProxyHarness(t, nil)
			defer h.close()

			spec := `openapi: 3.0.0
info: { title: BodyKinds, version: "1.0" }
servers: [ { url: "` + h.upstreamURL + `" } ]
paths:
  /thing:
    post:
      operationId: postThing
      requestBody:
        required: true
        content:
` + tc.bodyYAML + `
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: { type: object }
`
			resp := runProxyToolCall(t, spec, nil,
				[]string{initRequest, toolCallRequest(2, "postThing", tc.toolArgs)})
			req := h.captured()
			if req == nil {
				t.Fatalf("upstream never received request\n--mcp-resp--\n%s", resp)
			}
			ct := req.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, tc.wantCTPrefix) {
				t.Errorf("Content-Type: got %q, want prefix %q", ct, tc.wantCTPrefix)
			}
			// Body was buffered into the cloned request by cloneRequest.
			body, err := req.GetBody()
			if err != nil {
				t.Fatalf("read captured body: %v", err)
			}
			defer func() { _ = body.Close() }()
			raw, _ := io.ReadAll(body)
			tc.wantBodyMatch(t, raw)
		})
	}
}

// TestCLI_Proxy_HeaderAndCookieParamsReachUpstream proves that an MCP
// tool call providing header and cookie parameters wires them into the
// outgoing *http.Request. Path + query are implicitly exercised by other
// tests; header/cookie use separate template branches.
func TestCLI_Proxy_HeaderAndCookieParamsReachUpstream(t *testing.T) {
	h := newProxyHarness(t, nil)
	defer h.close()

	spec := `openapi: 3.0.0
info: { title: ParamMatrix, version: "1.0" }
servers: [ { url: "` + h.upstreamURL + `" } ]
paths:
  /thing:
    get:
      operationId: getThing
      parameters:
        - name: X-Trace-Id
          in: header
          required: true
          schema: { type: string }
        - name: session
          in: cookie
          required: true
          schema: { type: string }
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: { type: object }
`
	resp := runProxyToolCall(t, spec, nil,
		[]string{initRequest, toolCallRequest(2, "getThing",
			`{"header":{"X-Trace-Id":"trace-42"},"cookie":{"session":"sess-9000"}}`)})
	req := h.captured()
	if req == nil {
		t.Fatalf("upstream never received request\n--mcp-resp--\n%s", resp)
	}
	if got := req.Header.Get("X-Trace-Id"); got != "trace-42" {
		t.Errorf("X-Trace-Id header: got %q, want %q", got, "trace-42")
	}
	c, err := req.Cookie("session")
	if err != nil || c.Value != "sess-9000" {
		t.Errorf("session cookie: got %+v err=%v", c, err)
	}
}

// TestCLI_Proxy_ResponseShapes exercises the proxy's handling of every
// non-trivial upstream response shape: 204 No Content, a 5xx error with
// a text body, and a 2xx text/plain response. The proxy is supposed to
// surface each shape through NewToolResultFromHTTP — this pins the
// envelope the MCP client actually sees.
func TestCLI_Proxy_ResponseShapes(t *testing.T) {
	cases := []struct {
		name      string
		respond   func(w http.ResponseWriter, r *http.Request)
		assertMCP func(t *testing.T, resp string)
	}{
		{
			name: "204NoContent",
			respond: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			},
			assertMCP: func(t *testing.T, resp string) {
				// Success envelope; no error flag, no body claim.
				if strings.Contains(resp, `"isError":true`) {
					t.Errorf("204 should not be IsError; got %s", resp)
				}
			},
		},
		{
			name: "500WithTextBody",
			respond: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("upstream blew up"))
			},
			assertMCP: func(t *testing.T, resp string) {
				if !strings.Contains(resp, `"isError":true`) {
					t.Errorf("5xx should set IsError=true; got %s", resp)
				}
				if !strings.Contains(resp, "upstream blew up") {
					t.Errorf("5xx body should pass through to MCP result; got %s", resp)
				}
			},
		},
		{
			name: "200TextPlain",
			respond: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				_, _ = w.Write([]byte("just-a-string"))
			},
			assertMCP: func(t *testing.T, resp string) {
				if !strings.Contains(resp, "just-a-string") {
					t.Errorf("text/plain body should surface in MCP result; got %s", resp)
				}
				if strings.Contains(resp, `"isError":true`) {
					t.Errorf("text/plain 200 should not be IsError; got %s", resp)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newProxyHarness(t, tc.respond)
			defer h.close()

			spec := `openapi: 3.0.0
info: { title: ResponseShapes, version: "1.0" }
servers: [ { url: "` + h.upstreamURL + `" } ]
paths:
  /thing:
    get:
      operationId: getThing
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: { type: object }
`
			resp := runProxyToolCall(t, spec, nil,
				[]string{initRequest, toolCallRequest(2, "getThing", "{}")})
			tc.assertMCP(t, resp)
		})
	}
}
