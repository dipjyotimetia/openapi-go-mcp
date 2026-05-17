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
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// upstreamCall records an HTTP request observed by a test's mock upstream.
// The fields capture enough to assert the generated MCP handler forwarded
// method/path/query/headers/body faithfully.
type upstreamCall struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   []byte
}

// captureRequest snapshots r into an upstreamCall, consuming the body.
func captureRequest(r *http.Request) upstreamCall {
	body, _ := io.ReadAll(r.Body)
	return upstreamCall{
		Method: r.Method,
		Path:   r.URL.Path,
		Query:  r.URL.RawQuery,
		Header: r.Header.Clone(),
		Body:   body,
	}
}

// serveTest spins up an httptest server with the given handler and registers
// its shutdown with t.Cleanup. Returns the live server for URL access.
func serveTest(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// textOf returns the first TextContent block in res, or "" if none. Used by
// every e2e test to assert tool result bodies.
func textOf(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// keys returns a stable-iteration view of a string-keyed bool set, for
// human-readable test failure messages.
func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
