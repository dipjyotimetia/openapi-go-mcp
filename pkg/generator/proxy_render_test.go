// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package generator

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dipjyotimetia/openapi-go-mcp/pkg/loader"
)

// TestRender_ProxyMode_PetstoreCompiles parses the proxy-mode output of the
// petstore spec into the Go AST. A successful parse is a strong signal that
// the generated source is syntactically valid Go; the e2e suite later
// verifies it also builds and runs.
func TestRender_ProxyMode_PetstoreCompiles(t *testing.T) {
	specPath := filepath.Join("..", "..", "testdata", "petstore-v3.yaml")
	doc, err := loader.Load(context.Background(), specPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := Render(doc, Options{
		Mode:        ModeProxy,
		PackageName: "petstoremcp",
		ModulePath:  "github.com/example/petstore-mcp",
	})
	if err != nil {
		t.Fatalf("render proxy: %v\n--- output ---\n%s", err, prefix(got, 1200))
	}
	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "petstoremcp.go", got, parser.AllErrors); err != nil {
		t.Fatalf("generated proxy source does not parse: %v\n--- output ---\n%s", err, prefix(got, 2000))
	}
	// Spot-check the shape: must import the runtime package, must not
	// import any oapi-codegen client, must expose a register function.
	src := string(got)
	for _, want := range []string{
		`"github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime"`,
		"package petstoremcp",
		"http.NewRequestWithContext",
		"httpClient.Do",
		"runtime.NewToolResultFromHTTP",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("expected proxy output to contain %q", want)
		}
	}
	for _, forbidden := range []string{
		"WithResponse(", // oapi-codegen typed-response call
	} {
		if strings.Contains(src, forbidden) {
			t.Errorf("proxy output must not contain %q (companion-mode leakage)", forbidden)
		}
	}
}

// TestRender_ProxyMode_AuthWiring confirms that a spec with a bearer
// scheme produces an apply<Scheme>Auth helper that reads the right env
// var, and that the per-operation block invokes it.
func TestRender_ProxyMode_AuthWiring(t *testing.T) {
	// Reuse the petstore fixture and graft a securitySchemes section +
	// global security requirement onto it via an inline document. Avoids
	// a new testdata file for a one-line spec extension.
	spec := []byte(`openapi: 3.0.0
info: { title: AuthTest, version: "1.0" }
servers: [ { url: https://api.test/v1 } ]
security:
  - bearerAuth: []
components:
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
paths:
  /thing:
    get:
      operationId: getThing
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
`)
	tmp := filepath.Join(t.TempDir(), "spec.yaml")
	if err := os.WriteFile(tmp, spec, 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	doc, err := loader.Load(context.Background(), tmp)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := Render(doc, Options{
		Mode:        ModeProxy,
		PackageName: "authtestmcp",
		ModulePath:  "example.com/authtest",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	src := string(got)
	// One helper per scheme. Naming: applyAuth<PascalCaseSchemeName>.
	if !strings.Contains(src, "func applyAuthBearerAuth(req *http.Request) error") {
		t.Errorf("expected applyAuthBearerAuth helper; got %q", prefix(got, 1500))
	}
	// Helper reads the env var the generator derived.
	if !strings.Contains(src, `os.Getenv("BEARER_TOKEN_BEARERAUTH")`) {
		t.Errorf("auth helper should read BEARER_TOKEN_BEARERAUTH env var")
	}
	if !strings.Contains(src, "runtime.ApplyBearer(req, v)") {
		t.Errorf("auth helper should call runtime.ApplyBearer")
	}
	// The operation block invokes the helper.
	if !strings.Contains(src, "applyAuthBearerAuth(httpReq)") {
		t.Errorf("operation should invoke applyAuthBearerAuth")
	}
}

// TestRender_ProxyMode_AnonymousSkipsAuthHelpers verifies that an
// operation with no security requirement does NOT invoke any auth helper.
func TestRender_ProxyMode_AnonymousSkipsAuthHelpers(t *testing.T) {
	spec := []byte(`openapi: 3.0.0
info: { title: AnonTest, version: "1.0" }
servers: [ { url: https://api.test } ]
paths:
  /open:
    get:
      operationId: getOpen
      responses:
        "200": { description: ok }
`)
	tmp := filepath.Join(t.TempDir(), "spec.yaml")
	if err := os.WriteFile(tmp, spec, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	doc, err := loader.Load(context.Background(), tmp)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := Render(doc, Options{
		Mode:        ModeProxy,
		PackageName: "anonmcp",
		ModulePath:  "example.com/anon",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(string(got), "func applyAuth") || strings.Contains(string(got), "MissingCredentialError") {
		t.Errorf("anonymous spec must not generate auth code; got\n%s", prefix(got, 1200))
	}
}

// TestRender_CompanionMode_DoesNotPopulateSecurity confirms the existing
// companion-mode behaviour: Operation.Security stays nil even when the
// spec has security requirements, so the legacy golden output is
// unaffected by the new code path.
func TestRender_CompanionMode_DoesNotPopulateSecurity(t *testing.T) {
	spec := []byte(`openapi: 3.0.0
info: { title: CompanionTest, version: "1.0" }
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
	tmp := filepath.Join(t.TempDir(), "spec.yaml")
	if err := os.WriteFile(tmp, spec, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	doc, err := loader.Load(context.Background(), tmp)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ops, _, err := CollectOperations(doc, Options{
		Mode:         ModeCompanion,
		PackageName:  "companionmcp",
		ClientImport: "github.com/x/y",
		ClientType:   "ClientWithResponsesInterface",
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(ops) == 0 || ops[0].Security != nil || ops[0].Anonymous {
		t.Errorf("companion mode must leave Security/Anonymous unset; got %+v", ops[0])
	}
}

// TestRender_ProxyMode_NoServersFallback documents the runtime contract
// for specs without a `servers:` block: the generated handler falls back
// to API_BASE_URL with an empty default, so a user MUST set the env var
// at runtime. A regression here (e.g. defaulting to "http://localhost"
// or panicking) would silently send requests to the wrong place.
func TestRender_ProxyMode_NoServersFallback(t *testing.T) {
	spec := []byte(`openapi: 3.0.0
info: { title: NoServers, version: "1.0" }
paths:
  /thing:
    get:
      operationId: getThing
      responses:
        "200": { description: ok }
`)
	tmp := filepath.Join(t.TempDir(), "spec.yaml")
	if err := os.WriteFile(tmp, spec, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	doc, err := loader.Load(context.Background(), tmp)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := Render(doc, Options{
		Mode:        ModeProxy,
		PackageName: "noservermcp",
		ModulePath:  "example.com/noserver",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	src := string(got)
	// The generated handler must read API_BASE_URL.
	if !strings.Contains(src, `os.Getenv("API_BASE_URL")`) {
		t.Errorf("expected API_BASE_URL fallback in generated source\n--src--\n%s", prefix(got, 1500))
	}
	// And the spec-side default must be the empty string (no servers[]).
	// We assert by checking the literal `baseURL = ""` assignment the
	// template emits after the env-var lookup.
	if !strings.Contains(src, `baseURL = ""`) {
		t.Errorf("expected empty-string spec default for no-servers spec\n--src--\n%s", prefix(got, 1500))
	}
}

func TestRender_ProxyMode_AppliesRuntimeOptions(t *testing.T) {
	spec := []byte(`openapi: 3.0.0
info: { title: RuntimeOpts, version: "1.0" }
servers:
  - url: https://{host}/v1
    variables:
      host:
        default: api.example.com
paths:
  /things/{thingId}:
    get:
      operationId: getThing
      parameters:
        - in: path
          name: thingId
          required: true
          schema: { type: string }
      responses:
        "200": { description: ok }
`)
	tmp := filepath.Join(t.TempDir(), "spec.yaml")
	if err := os.WriteFile(tmp, spec, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	doc, err := loader.Load(context.Background(), tmp)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := Render(doc, Options{
		Mode:        ModeProxy,
		PackageName: "runtimeoptsmcp",
		ModulePath:  "example.com/runtimeopts",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	src := string(got)
	for _, want := range []string{
		"runtime.ApplyExtraPropertiesToContext(ctx, req.Arguments, cfg.ExtraProperties)",
		"context.WithTimeout(ctx, cfg.RequestTimeout)",
		"runtime.SubstituteServerVariables(baseURL, cfg.ServerVariables)",
		"runtime.PathEscape(v)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("proxy output missing %q\n--src--\n%s", want, prefix(got, 2400))
		}
	}
	if strings.Contains(src, "runtime.QueryEscape(v)") {
		t.Errorf("proxy path params must not use query escaping")
	}
}
