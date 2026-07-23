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
		UpstreamHTTPClient: upstream.Client(),
		NamePrefix:         "widgets",
		RequestTimeout:     time.Second,
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
	if err := dynamic.Register(context.Background(), server, specPath, dynamic.Config{UpstreamHTTPClient: upstream.Client()}); err != nil {
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

func TestRegister_AppliesCustomAuthProviderForOpenIDConnect(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Workload-Identity"); got != "signed" {
			t.Errorf("custom signer header = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	specPath := filepath.Join(t.TempDir(), "oidc.yaml")
	spec := fmt.Sprintf(`openapi: 3.1.0
info: { title: OIDC, version: 1.0.0 }
servers: [ { url: %s } ]
components:
  securitySchemes:
    oidc:
      type: openIdConnect
      openIdConnectUrl: https://issuer.example/.well-known/openid-configuration
paths:
  /identity:
    get:
      operationId: readIdentity
      security: [ { oidc: [] } ]
      responses: { "204": { description: ok } }
`, upstream.URL)
	if err := os.WriteFile(specPath, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}

	server := &fakeServer{}
	provider := runtime.RequestAuthProviderFunc(func(_ context.Context, req *http.Request) error {
		req.Header.Set("X-Workload-Identity", "signed")
		return nil
	})
	if err := dynamic.Register(context.Background(), server, specPath, dynamic.Config{UpstreamHTTPClient: upstream.Client(), RequestAuthProvider: provider}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	result, err := server.handlers["readIdentity"](context.Background(), &runtime.CallToolRequest{Arguments: map[string]any{}})
	if err != nil || result.IsError || result.StatusCode != http.StatusNoContent {
		t.Fatalf("handler result = %#v, %v", result, err)
	}
}

func TestRegister_BoundsUpstreamResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "12345")
	}))
	defer upstream.Close()
	specPath := filepath.Join(t.TempDir(), "large.yaml")
	spec := fmt.Sprintf(`openapi: 3.0.3
info: { title: Large, version: 1.0.0 }
servers: [ { url: %s } ]
paths:
  /large:
    get:
      operationId: getLarge
      responses:
        "200":
          description: ok
          content:
            text/plain:
              schema: { type: string }
`, upstream.URL)
	if err := os.WriteFile(specPath, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	server := &fakeServer{}
	if err := dynamic.Register(context.Background(), server, specPath, dynamic.Config{UpstreamHTTPClient: upstream.Client(), MaxResponseBytes: 4}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	result, err := server.handlers["getLarge"](context.Background(), &runtime.CallToolRequest{Arguments: map[string]any{}})
	if err != nil || !result.IsError || !strings.Contains(result.Text, `"code":"response_too_large"`) {
		t.Fatalf("handler result = %#v, %v", result, err)
	}
}

func TestRegister_UsesOpenAPIParameterSerialization(t *testing.T) {
	var gotPath, gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.EscapedPath(), r.URL.RawQuery
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	specPath := filepath.Join(t.TempDir(), "styles.yaml")
	spec := fmt.Sprintf(`openapi: 3.0.3
info: { title: Styles, version: 1.0.0 }
servers: [ { url: %s } ]
paths:
  /things/{id}:
    get:
      operationId: getStyledThing
      parameters:
        - { name: id, in: path, required: true, style: matrix, explode: true, schema: { type: object } }
        - { name: filter, in: query, style: deepObject, explode: true, schema: { type: object } }
      responses: { "204": { description: ok } }
`, upstream.URL)
	if err := os.WriteFile(specPath, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	server := &fakeServer{}
	if err := dynamic.Register(context.Background(), server, specPath, dynamic.Config{UpstreamHTTPClient: upstream.Client()}); err != nil {
		t.Fatal(err)
	}
	result, err := server.handlers["getStyledThing"](context.Background(), &runtime.CallToolRequest{Arguments: map[string]any{
		"path":  map[string]any{"id": map[string]any{"R": "100", "G": "200"}},
		"query": map[string]any{"filter": map[string]any{"status": "active"}},
	}})
	if err != nil || result.IsError {
		t.Fatalf("call result=%+v err=%v", result, err)
	}
	if gotPath != "/things/;G=200;R=100" {
		t.Errorf("path = %q", gotPath)
	}
	if gotQuery != "filter%5Bstatus%5D=active" {
		t.Errorf("query = %q", gotQuery)
	}
}

func TestRegister_RemoteSourceRequiresExplicitBaseURL(t *testing.T) {
	t.Parallel()

	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `openapi: 3.0.3
info: { title: Remote, version: 1.0.0 }
servers: [ { url: http://169.254.169.254/latest } ]
paths:
  /widgets:
    get:
      operationId: listWidgets
      responses: { "200": { description: ok } }
`)
	}))
	defer source.Close()

	err := dynamic.Register(context.Background(), &fakeServer{}, source.URL, dynamic.Config{SourceHTTPClient: source.Client()})
	if err == nil || !strings.Contains(err.Error(), "requires dynamic.Config.BaseURL") {
		t.Fatalf("Register() error = %v, want explicit BaseURL requirement", err)
	}
}

func TestRegister_RemoteSourceUsesConfiguredUpstreamClientAndBaseURL(t *testing.T) {
	t.Parallel()

	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		if r.URL.Path != "/widgets" {
			t.Errorf("path = %q, want /widgets", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer upstream.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `openapi: 3.0.3
info: { title: Remote, version: 1.0.0 }
servers: [ { url: http://169.254.169.254/latest } ]
paths:
  /widgets:
    get:
      operationId: listWidgets
      responses: { "200": { description: ok } }
`)
	}))
	defer source.Close()

	server := &fakeServer{}
	err := dynamic.Register(context.Background(), server, source.URL, dynamic.Config{
		SourceHTTPClient:   source.Client(),
		UpstreamHTTPClient: upstream.Client(),
		BaseURL:            upstream.URL,
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	result, err := server.handlers["listWidgets"](context.Background(), &runtime.CallToolRequest{Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if result.IsError || !upstreamCalled {
		t.Fatalf("result = %#v, upstreamCalled = %v", result, upstreamCalled)
	}
}

func TestRegister_RejectsRemoteSourceRedirectsAndUnsafeSourceURLs(t *testing.T) {
	t.Parallel()

	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/spec.yaml", http.StatusFound)
			return
		}
		_, _ = fmt.Fprint(w, `openapi: 3.0.3
info: { title: Remote, version: 1.0.0 }
paths: {}
`)
	}))
	defer source.Close()

	err := dynamic.Register(context.Background(), &fakeServer{}, source.URL+"/redirect", dynamic.Config{SourceHTTPClient: source.Client(), BaseURL: "https://api.example.test"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("redirect error = %v, want un-followed HTTP 302", err)
	}

	for _, sourceURL := range []string{
		"https://user:password@example.test/openapi.yaml",
		"https://example.test/openapi.yaml#fragment",
		"http://example.test/openapi.yaml",
	} {
		err = dynamic.Register(context.Background(), &fakeServer{}, sourceURL, dynamic.Config{})
		if err == nil {
			t.Errorf("Register(%q) succeeded, want source validation failure", sourceURL)
		}
	}
}

func TestRegister_RejectsExternalReferencesInRemoteSource(t *testing.T) {
	t.Parallel()

	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `openapi: 3.0.3
info: { title: Remote, version: 1.0.0 }
paths:
  /widgets:
    get:
      operationId: listWidgets
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: { $ref: 'https://169.254.169.254/schema.yaml#/Widget' }
`)
	}))
	defer source.Close()

	err := dynamic.Register(context.Background(), &fakeServer{}, source.URL, dynamic.Config{
		SourceHTTPClient: source.Client(),
		BaseURL:          "https://api.example.test",
	})
	if err == nil || !strings.Contains(err.Error(), "contains external $ref") {
		t.Fatalf("Register() error = %v, want external reference rejection", err)
	}
}

func TestRegister_ProviderSelectsOpenAISchema(t *testing.T) {
	t.Parallel()

	specPath := filepath.Join(t.TempDir(), "provider.yaml")
	spec := `openapi: 3.0.3
info: { title: Provider, version: 1.0.0 }
servers: [ { url: https://api.example.test } ]
paths:
  /widgets:
    post:
      operationId: createWidget
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: '#/components/schemas/Widget' }
      responses: { "201": { description: created } }
components:
  schemas:
    Widget:
      type: object
      required: [name]
      properties:
        name: { type: string }
        labels:
          type: object
          additionalProperties: { type: string }
`
	if err := os.WriteFile(specPath, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}

	standard := &fakeServer{}
	if err := dynamic.Register(context.Background(), standard, specPath, dynamic.Config{}); err != nil {
		t.Fatalf("standard Register() error = %v", err)
	}
	openAI := &fakeServer{}
	if err := dynamic.Register(context.Background(), openAI, specPath, dynamic.Config{Provider: runtime.LLMProviderOpenAI}); err != nil {
		t.Fatalf("OpenAI Register() error = %v", err)
	}
	standardSchema := string(standard.tools["createWidget"].RawInputSchema)
	openAISchema := string(openAI.tools["createWidget"].RawInputSchema)
	if !strings.Contains(standardSchema, `"$defs"`) {
		t.Errorf("standard schema does not retain definitions: %s", standardSchema)
	}
	if strings.Contains(openAISchema, `"$defs"`) || strings.Contains(openAISchema, `"$ref"`) {
		t.Errorf("OpenAI schema must be flattened: %s", openAISchema)
	}
	if !strings.Contains(openAISchema, `"additionalProperties": false`) {
		t.Errorf("OpenAI schema must be strict: %s", openAISchema)
	}
}

func TestRegister_RejectsUnsafeConfiguredBaseURL(t *testing.T) {
	t.Parallel()

	specPath := filepath.Join(t.TempDir(), "base-url.yaml")
	if err := os.WriteFile(specPath, []byte(`openapi: 3.0.3
info: { title: Base, version: 1.0.0 }
paths: {}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := dynamic.Register(context.Background(), &fakeServer{}, specPath, dynamic.Config{BaseURL: "https://user:password@example.test"})
	if err == nil || !strings.Contains(err.Error(), "must not contain credentials") {
		t.Fatalf("Register() error = %v, want unsafe BaseURL rejection", err)
	}
}

func mapsKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}
