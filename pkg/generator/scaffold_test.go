// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package generator

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func newScaffoldDoc(title string) *openapi3.T {
	return &openapi3.T{
		Info: &openapi3.Info{Title: title, Version: "1.2.3"},
	}
}

func TestWriteScaffold_GoSDK_AllFilesPresent(t *testing.T) {
	out := t.TempDir()
	opts := Options{
		Mode:        ModeProxy,
		OutDir:      out,
		PackageName: "petmcp",
		ModulePath:  "example.com/petmcp",
		SDK:         "gosdk",
	}
	doc := newScaffoldDoc("Petstore")
	if err := WriteScaffold(opts, doc, nil); err != nil {
		t.Fatalf("WriteScaffold: %v", err)
	}
	for _, name := range []string{"main.go", "go.mod", "README.md"} {
		p := filepath.Join(out, name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s; %v", p, err)
		}
	}
}

func TestWriteScaffold_GoSDK_MainCompilesAsAST(t *testing.T) {
	out := t.TempDir()
	doc := newScaffoldDoc("Petstore")
	err := WriteScaffold(Options{
		Mode:        ModeProxy,
		OutDir:      out,
		PackageName: "petmcp",
		ModulePath:  "example.com/petmcp",
		SDK:         "gosdk",
	}, doc, nil)
	if err != nil {
		t.Fatalf("WriteScaffold: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(out, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "main.go", body, parser.AllErrors); err != nil {
		t.Fatalf("main.go does not parse: %v\n---\n%s", err, string(body))
	}
	src := string(body)
	for _, want := range []string{
		`"example.com/petmcp/petmcp"`,
		`"github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime/gosdk"`,
		`"github.com/modelcontextprotocol/go-sdk/mcp"`,
		"gosdk.NewServer",
		`mcp.StdioTransport{}`,
		"petmcp.RegisterPetstoreClient(s)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("main.go missing %q", want)
		}
	}
}

func TestWriteScaffold_Mark3labs_MainShape(t *testing.T) {
	out := t.TempDir()
	err := WriteScaffold(Options{
		Mode:        ModeProxy,
		OutDir:      out,
		PackageName: "petmcp",
		ModulePath:  "example.com/petmcp",
		SDK:         "mark3labs",
	}, newScaffoldDoc("Petstore"), nil)
	if err != nil {
		t.Fatalf("WriteScaffold: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(out, "main.go"))
	src := string(body)
	for _, want := range []string{
		`mcpserver "github.com/mark3labs/mcp-go/server"`,
		"mark3labs.NewServer",
		"mcpserver.ServeStdio(raw)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("mark3labs main.go missing %q\n---\n%s", want, src)
		}
	}
	// Must NOT import the gosdk transport when mark3labs is selected.
	if strings.Contains(src, "go-sdk/mcp") {
		t.Errorf("mark3labs main.go must not import go-sdk; got\n%s", src)
	}
}

func TestWriteScaffold_GoMod_ShapeAndOrder(t *testing.T) {
	out := t.TempDir()
	err := WriteScaffoldWithOverrides(Options{
		Mode:        ModeProxy,
		OutDir:      out,
		PackageName: "petmcp",
		ModulePath:  "example.com/petmcp",
		SDK:         "gosdk",
	}, newScaffoldDoc("Petstore"), nil, ScaffoldOverrides{RuntimeVersion: "v1.2.3"})
	if err != nil {
		t.Fatalf("WriteScaffold: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(out, "go.mod"))
	src := string(body)
	// module + go directives present.
	for _, want := range []string{
		"module example.com/petmcp",
		"go 1.23",
		"require (",
		"github.com/dipjyotimetia/openapi-go-mcp v1.2.3",
		"github.com/modelcontextprotocol/go-sdk",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("go.mod missing %q\n---\n%s", want, src)
		}
	}
	// Requires sorted: dipjyotimetia (d) before modelcontextprotocol (m).
	d := strings.Index(src, "dipjyotimetia")
	m := strings.Index(src, "modelcontextprotocol")
	if d < 0 || m < 0 || d > m {
		t.Errorf("require block must be sorted (dipjyotimetia before modelcontextprotocol); got\n%s", src)
	}
}

func TestWriteScaffold_GoMod_ReplaceDirective(t *testing.T) {
	out := t.TempDir()
	err := WriteScaffoldWithOverrides(Options{
		Mode:        ModeProxy,
		OutDir:      out,
		PackageName: "petmcp",
		ModulePath:  "example.com/petmcp",
		SDK:         "gosdk",
	}, newScaffoldDoc("Petstore"), nil, ScaffoldOverrides{
		RuntimeVersion: "v1.2.3",
		RuntimeReplace: "../..",
	})
	if err != nil {
		t.Fatalf("WriteScaffold: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(out, "go.mod"))
	if !strings.Contains(string(body), "replace github.com/dipjyotimetia/openapi-go-mcp => ../..") {
		t.Errorf("expected replace directive in go.mod:\n%s", string(body))
	}
}

func TestWriteScaffold_README_AuthTable(t *testing.T) {
	out := t.TempDir()
	schemes := []SecurityScheme{
		{Name: "bearerAuth", Kind: SecurityHTTPBearer, EnvVar: "BEARER_TOKEN_BEARERAUTH"},
		{Name: "apiKeyAuth", Kind: SecurityAPIKey, In: "header", ParamName: "X-API-Key", EnvVar: "API_KEY_APIKEYAUTH"},
		{
			Name: "basicAuth", Kind: SecurityHTTPBasic,
			UsernameEnvVar: "BASIC_AUTH_USERNAME_BASICAUTH",
			PasswordEnvVar: "BASIC_AUTH_PASSWORD_BASICAUTH",
		},
	}
	err := WriteScaffold(Options{
		Mode:        ModeProxy,
		OutDir:      out,
		PackageName: "petmcp",
		ModulePath:  "example.com/petmcp",
		SDK:         "gosdk",
	}, newScaffoldDoc("Petstore"), schemes)
	if err != nil {
		t.Fatalf("WriteScaffold: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(out, "README.md"))
	src := string(body)
	for _, want := range []string{
		"API_BASE_URL",
		"BEARER_TOKEN_BEARERAUTH",
		"API_KEY_APIKEYAUTH",
		"BASIC_AUTH_USERNAME_BASICAUTH",
		"BASIC_AUTH_PASSWORD_BASICAUTH",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("README missing %q\n---\n%s", want, src)
		}
	}
}

func TestWriteScaffold_README_AnonymousSpec(t *testing.T) {
	out := t.TempDir()
	if err := WriteScaffold(Options{
		Mode:        ModeProxy,
		OutDir:      out,
		PackageName: "openmcp",
		ModulePath:  "example.com/openmcp",
		SDK:         "gosdk",
	}, newScaffoldDoc("OpenAPI"), nil); err != nil {
		t.Fatalf("WriteScaffold: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(out, "README.md"))
	if !strings.Contains(string(body), "no authentication") {
		t.Errorf("README should note anonymous case:\n%s", string(body))
	}
}

func TestWriteScaffold_RefusesOverwriteWithoutForce(t *testing.T) {
	out := t.TempDir()
	// Plant a pre-existing main.go.
	if err := os.WriteFile(filepath.Join(out, "main.go"), []byte("// existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := WriteScaffold(Options{
		Mode:        ModeProxy,
		OutDir:      out,
		PackageName: "petmcp",
		ModulePath:  "example.com/petmcp",
		SDK:         "gosdk",
	}, newScaffoldDoc("Petstore"), nil)
	if err == nil || !strings.Contains(err.Error(), "force") {
		t.Errorf("expected force-required error; got %v", err)
	}
}

func TestWriteScaffold_ForceOverwrites(t *testing.T) {
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, "main.go"), []byte("// existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := WriteScaffold(Options{
		Mode:        ModeProxy,
		Force:       true,
		OutDir:      out,
		PackageName: "petmcp",
		ModulePath:  "example.com/petmcp",
		SDK:         "gosdk",
	}, newScaffoldDoc("Petstore"), nil)
	if err != nil {
		t.Fatalf("WriteScaffold with Force: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(out, "main.go"))
	if strings.Contains(string(body), "// existing") {
		t.Errorf("Force should overwrite the file; got\n%s", string(body))
	}
}

func TestWriteScaffold_DirectoryAtTargetIsHardError(t *testing.T) {
	out := t.TempDir()
	if err := os.Mkdir(filepath.Join(out, "main.go"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := WriteScaffold(Options{
		Mode:        ModeProxy,
		Force:       true, // even with force, dir is rejected
		OutDir:      out,
		PackageName: "petmcp",
		ModulePath:  "example.com/petmcp",
		SDK:         "gosdk",
	}, newScaffoldDoc("Petstore"), nil)
	if err == nil || !strings.Contains(err.Error(), "directory") {
		t.Errorf("expected directory-at-target error even with Force; got %v", err)
	}
}

func TestWriteScaffold_NoOpInCompanionMode(t *testing.T) {
	// Companion mode should never touch the filesystem from this entrypoint.
	out := t.TempDir()
	err := WriteScaffold(Options{Mode: ModeCompanion, OutDir: out, ClientImport: "x/y"}, newScaffoldDoc("X"), nil)
	if err != nil {
		t.Errorf("companion-mode call should be a no-op, not an error: %v", err)
	}
	entries, _ := os.ReadDir(out)
	if len(entries) != 0 {
		t.Errorf("companion-mode WriteScaffold must not write files; got %d entries", len(entries))
	}
}

func TestWriteScaffold_RejectsUnknownSDK(t *testing.T) {
	out := t.TempDir()
	err := WriteScaffold(Options{
		Mode:        ModeProxy,
		OutDir:      out,
		PackageName: "p",
		ModulePath:  "example.com/p",
		SDK:         "unknown",
	}, newScaffoldDoc("X"), nil)
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Errorf("expected unsupported-SDK error, got %v", err)
	}
}
