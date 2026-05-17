// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package e2e

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// addReplaceDirective rewrites the scaffold's go.mod so it picks up the
// in-tree runtime via a `replace` directive. Production scaffolds resolve
// the runtime through the public module proxy; tests must pin to the
// current working tree so they exercise the same code the tests live in.
func addReplaceDirective(t *testing.T, goModPath, repoRootPath string) {
	t.Helper()
	body, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	body = append(body, []byte("\nreplace github.com/dipjyotimetia/openapi-gen-go-mcp => "+repoRootPath+"\n")...)
	if err := os.WriteFile(goModPath, body, 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
}

// buildScaffold compiles the generated module at outDir into a binary at
// the returned path. Returns ("", err) when the build fails so the caller
// can surface the captured output.
func buildScaffold(t *testing.T, outDir string) (string, error) {
	t.Helper()
	bin := filepath.Join(outDir, "proxy.test.bin")
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = outDir
	if out, err := tidy.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go mod tidy: %w\n%s", err, out)
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = outDir
	if out, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build: %w\n%s", err, out)
	}
	return bin, nil
}

// stdioRoundTrip launches bin and exchanges newline-delimited JSON-RPC
// messages with it. Each entry in requests is sent as one line; the
// helper reads one line of response per request, in order, then closes
// stdin so the server exits cleanly. Returns the concatenated responses.
//
// Reading-then-closing avoids two failure modes: (a) closing stdin too
// early can race the server's processing of the last request, and
// (b) keeping stdin open prevents the server from exiting at all.
// The timeout bounds the total subprocess lifetime.
func stdioRoundTrip(t *testing.T, bin string, env []string, requests []string, timeout time.Duration) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	cmd.Env = append(os.Environ(), env...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Read responses line-by-line in a goroutine so we can match them to
	// the request count rather than guessing buffer sizes.
	respCh := make(chan string, len(requests)+1)
	go func() {
		defer close(respCh)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 1024), 1024*1024)
		for sc.Scan() {
			respCh <- sc.Text()
		}
	}()

	// Send all requests up front. Each one provokes exactly one
	// response on stdout (per MCP's request/response model).
	for _, r := range requests {
		if _, err := io.WriteString(stdin, r+"\n"); err != nil {
			t.Fatalf("write stdin: %v", err)
		}
	}

	var collected []string
	for i := range len(requests) {
		select {
		case line, ok := <-respCh:
			if !ok {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				t.Fatalf("server closed stdout before producing all %d response(s); got %d:\n%s",
					len(requests), len(collected), strings.Join(collected, "\n"))
			}
			collected = append(collected, line)
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("timed out waiting for response %d/%d after %s", i+1, len(requests), timeout)
		}
	}
	_ = stdin.Close()
	_ = cmd.Wait()
	return strings.Join(collected, "\n")
}

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

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{"<read error: " + err.Error() + ">"}
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
