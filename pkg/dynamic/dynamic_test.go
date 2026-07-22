// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dynamic_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dipjyotimetia/openapi-go-mcp/pkg/dynamic"
	"github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime"
)

type fakeServer struct {
	tools    map[string]runtime.Tool
	handlers map[string]runtime.ToolHandler
}

func (s *fakeServer) AddTool(tool runtime.Tool, handler runtime.ToolHandler) {
	if s.tools == nil {
		s.tools = map[string]runtime.Tool{}
		s.handlers = map[string]runtime.ToolHandler{}
	}
	s.tools[tool.Name] = tool
	s.handlers[tool.Name] = handler
}

func TestRegister_RegistersAndInvokesProxyTool(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/widgets/widget-1" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("dryRun"); got != "true" {
			t.Errorf("dryRun = %q, want true", got)
		}
		if got := r.Header.Get("X-Trace-ID"); got != "trace-1" {
			t.Errorf("X-Trace-ID = %q", got)
		}
		cookie, err := r.Cookie("session")
		if err != nil || cookie.Value != "session-1" {
			t.Errorf("session cookie = %v, %v", cookie, err)
		}
		defer func() { _ = r.Body.Close() }()
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if got := body["name"]; got != "updated" {
			t.Errorf("body name = %#v", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"widget-1","name":"updated"}`)
	}))
	defer upstream.Close()

	specPath := filepath.Join(t.TempDir(), "widgets.yaml")
	serverHost := strings.TrimPrefix(upstream.URL, "http://")
	spec := fmt.Sprintf(`openapi: 3.0.3
info:
  title: Widgets
  version: 1.0.0
servers:
  - url: http://{host}
    variables:
      host:
        default: %s
paths:
  /widgets/{id}:
    post:
      operationId: updateWidget
      parameters:
        - name: id
          in: path
          required: true
          schema: { type: string }
        - name: dryRun
          in: query
          schema: { type: boolean }
        - name: X-Trace-ID
          in: header
          schema: { type: string }
        - name: session
          in: cookie
          schema: { type: string }
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [name]
              properties:
                name: { type: string }
      responses:
        "200":
          description: updated
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: { type: string }
                  name: { type: string }
  /hidden:
    get:
      x-mcp: false
      operationId: hidden
      responses:
        "204": { description: hidden }
`, serverHost)
	if err := os.WriteFile(specPath, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}

	server := &fakeServer{}
	if err := dynamic.Register(context.Background(), server, specPath, dynamic.Config{
		HTTPClient:     upstream.Client(),
		NamePrefix:     "widgets",
		RequestTimeout: time.Second,
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	handler, ok := server.handlers["widgets_updateWidget"]
	if !ok {
		t.Fatalf("registered tools = %v, want widgets_updateWidget", mapsKeys(server.handlers))
	}
	if _, ok := server.handlers["widgets_hidden"]; ok {
		t.Fatal("x-mcp:false operation was registered")
	}
	result, err := handler(context.Background(), &runtime.CallToolRequest{Arguments: map[string]any{
		"path":   map[string]any{"id": "widget-1"},
		"query":  map[string]any{"dryRun": true},
		"header": map[string]any{"X-Trace-ID": "trace-1"},
		"cookie": map[string]any{"session": "session-1"},
		"body":   map[string]any{"name": "updated"},
	}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if result.IsError || result.StatusCode != http.StatusOK {
		t.Fatalf("result = %#v", result)
	}
	if result.Text != `{"id":"widget-1","name":"updated"}` {
		t.Errorf("result text = %q", result.Text)
	}
}

func TestRegister_FailsClosedWhenDeclaredSecurityHasNoCredential(t *testing.T) {
	called := false
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer upstream.Close()
	t.Setenv("API_KEY_TEST_DYNAMIC_REQUIRED_KEY", "")

	specPath := filepath.Join(t.TempDir(), "secure.yaml")
	spec := fmt.Sprintf(`openapi: 3.0.3
info: { title: Secure, version: 1.0.0 }
servers: [ { url: %s } ]
components:
  securitySchemes:
    testDynamicRequiredKey:
      type: apiKey
      in: header
      name: X-API-Key
paths:
  /secure:
    get:
      operationId: readSecure
      security: [ { testDynamicRequiredKey: [] } ]
      responses:
        "200": { description: ok }
`, upstream.URL)
	if err := os.WriteFile(specPath, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}

	server := &fakeServer{}
	if err := dynamic.Register(context.Background(), server, specPath, dynamic.Config{HTTPClient: upstream.Client()}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	result, err := server.handlers["readSecure"](context.Background(), &runtime.CallToolRequest{Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if !result.IsError || !strings.Contains(result.Text, "no configured credential satisfies declared security") {
		t.Fatalf("result = %#v", result)
	}
	if called {
		t.Fatal("upstream received a request without the required credential")
	}
}

func mapsKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}
