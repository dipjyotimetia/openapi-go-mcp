// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

// Helpers shared across the proxy-mode e2e tests. Pure test utilities —
// nothing in this file is invoked from production code.

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

// addReplaceDirective appends a `replace` directive to a scaffold's go.mod
// so it picks up the in-tree runtime instead of the published module.
// Production scaffolds never need this — but tests must compile against
// the same code they live in, otherwise a refactor of the runtime helpers
// wouldn't be exercised end-to-end.
func addReplaceDirective(t *testing.T, goModPath, repoRootPath string) {
	t.Helper()
	f, err := os.OpenFile(goModPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open go.mod for append: %v", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := fmt.Fprintf(f, "\nreplace github.com/dipjyotimetia/openapi-go-mcp => %s\n", repoRootPath); err != nil {
		t.Fatalf("append replace directive: %v", err)
	}
}

// buildScaffold runs `go mod tidy && go build` in outDir and returns the
// path to the resulting binary. Returns ("", err) when either step fails
// so the caller can surface the captured output.
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

// listDir is a debug helper that returns the names of every entry in dir,
// used in test failure messages so a missing-file assertion can show what
// the test actually produced.
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

// initRequest returns the canonical MCP `initialize` JSON-RPC line. Every
// proxy e2e test starts with this; centralising the literal keeps the
// per-test fixtures focused on what each test is actually asserting.
const initRequest = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"e2e","version":"0"}}}`

// toolCallRequest returns a JSON-RPC `tools/call` line with the given id,
// tool name, and arguments-JSON. argsJSON must be a valid JSON object
// literal (e.g. `{}` or `{"path":{"id":1}}`); we don't validate it here.
func toolCallRequest(id int, tool, argsJSON string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, id, tool, argsJSON)
}

// proxyHarness owns the upstream stub, generates+builds the proxy
// module, runs `initialize` + `tools/call`, and exposes the captured
// upstream request. It's the shared engine behind every auth-matrix,
// body-kind, and parameter-flow e2e test.
type proxyHarness struct {
	upstreamURL string
	captured    func() *http.Request
	close       func()
}

// newProxyHarness starts an httptest.Server that records the first
// request it receives, then returns a harness with the upstream URL
// the spec should point at. The respond callback lets each test
// customise the upstream's response (status, headers, body).
func newProxyHarness(t *testing.T, respond func(w http.ResponseWriter, r *http.Request)) *proxyHarness {
	t.Helper()
	var (
		mu     sync.Mutex
		gotReq *http.Request
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotReq = cloneRequest(r)
		mu.Unlock()
		if respond != nil {
			respond(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	return &proxyHarness{
		upstreamURL: srv.URL,
		captured: func() *http.Request {
			mu.Lock()
			defer mu.Unlock()
			return gotReq
		},
		close: srv.Close,
	}
}

// runProxyToolCall is the shared harness behind every build-and-run e2e
// test. It accepts a complete OpenAPI spec (with the spec's `servers[0]`
// pointing at h.upstreamURL), generates the proxy module, builds it
// against the in-tree runtime, and exchanges the supplied JSON-RPC
// requests with it. Returns the concatenated MCP responses.
//
// The caller writes the spec themselves — this gives each test full
// control over indentation, security shape, and operation parameters
// without the helper having to template YAML.
func runProxyToolCall(t *testing.T, spec string, env []string, requests []string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping build-and-run e2e in -short mode")
	}
	specDir := t.TempDir()
	specPath := filepath.Join(specDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	outDir := t.TempDir()
	if _, stderr, err := runCLI(t,
		"-mode=proxy",
		"-spec", specPath,
		"-out", outDir,
		"-module", "example.com/proxytestmcp",
	); err != nil {
		t.Fatalf("CLI: %v\n%s", err, stderr)
	}
	addReplaceDirective(t, filepath.Join(outDir, "go.mod"), repoRoot(t))
	bin, err := buildScaffold(t, outDir)
	if err != nil {
		t.Fatalf("scaffold build: %v", err)
	}
	return stdioRoundTrip(t, bin, env, requests, 20*time.Second)
}

// cloneRequest captures the parts of an *http.Request needed for
// post-handler assertions. The original request body is already drained
// by the handler, so we read it under the same lock that publishes the
// captured request.
func cloneRequest(r *http.Request) *http.Request {
	c := r.Clone(context.Background())
	if r.Body != nil {
		body, _ := io.ReadAll(r.Body)
		c.Body = io.NopCloser(strings.NewReader(string(body)))
		// Stash the read bytes on the clone for tests that want to inspect them.
		c.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(string(body))), nil
		}
	}
	return c
}
