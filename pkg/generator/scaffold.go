// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package generator

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"text/template"

	"github.com/getkin/kin-openapi/openapi3"
)

// runtimeModulePath is the import path of this generator's runtime
// package. Used in the emitted go.mod so the scaffold compiles against
// the same code that produced it.
const runtimeModulePath = "github.com/dipjyotimetia/openapi-go-mcp"

// mcpSDKDeps maps each supported -sdk value to the module + version pair
// that must appear in the scaffold's go.mod. The versions track this
// generator's own go.mod so a freshly-scaffolded module compiles against
// the same SDK we tested against.
var mcpSDKDeps = map[string]struct {
	Module  string
	Version string
}{
	"gosdk":     {Module: "github.com/modelcontextprotocol/go-sdk", Version: "v1.6.0"},
	"mark3labs": {Module: "github.com/mark3labs/mcp-go", Version: "v0.54.0"},
}

// ScaffoldOverrides lets callers (mainly tests) tweak fields the generator
// would otherwise derive from build-time metadata. None of these fields
// are exposed via the CLI — the CLI uses the defaults.
type ScaffoldOverrides struct {
	// RuntimeVersion overrides the version this scaffold's go.mod will
	// pin its dependency on the openapi-go-mcp runtime to. Empty =
	// auto-detect via debug.ReadBuildInfo (falls back to v0.0.0).
	RuntimeVersion string
	// RuntimeReplace, when non-empty, emits a `replace` directive in the
	// scaffold's go.mod pointing the runtime module at the given local
	// path. E2E tests use this to compile against the in-tree source
	// rather than the published module. Empty = no replace directive.
	RuntimeReplace string
}

// WriteScaffold emits the proxy-mode companion files alongside the
// already-rendered *.mcp.go. It is a no-op in companion mode — callers
// gate on opts.Mode before invoking. Files written:
//
//   - main.go    — entrypoint that wires the chosen MCP SDK adapter and
//     invokes the generated Register*Client function.
//   - go.mod     — module declaration pinning the runtime and SDK deps.
//   - README.md  — env-var table generated from schemes plus a
//     "how to run" block.
//
// The -force semantics that apply to the *.mcp.go file extend here too:
// existing files in opts.OutDir abort with an error unless opts.Force is
// true. A pre-existing directory (rather than file) at any target path
// is always a hard error — removing a directory would be more destructive
// than overwriting a file.
//
// The function is robust against an empty schemes slice (an anonymous
// API): the README simply notes "no authentication required".
func WriteScaffold(opts Options, doc *openapi3.T, schemes []SecurityScheme) error {
	return WriteScaffoldWithOverrides(opts, doc, schemes, ScaffoldOverrides{})
}

// WriteScaffoldWithOverrides is WriteScaffold with the override knobs
// exposed. Used by tests that need to compile the scaffold against the
// in-tree runtime via a `replace` directive.
func WriteScaffoldWithOverrides(opts Options, doc *openapi3.T, schemes []SecurityScheme, ov ScaffoldOverrides) error {
	if opts.Mode != ModeProxy {
		return nil
	}
	if opts.ModulePath == "" {
		return fmt.Errorf("scaffold: ModulePath is required in proxy mode")
	}
	sdk := opts.SDK
	if sdk == "" {
		sdk = "gosdk"
	}
	dep, ok := mcpSDKDeps[sdk]
	if !ok {
		return fmt.Errorf("scaffold: unsupported SDK %q (expected gosdk or mark3labs)", sdk)
	}

	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return fmt.Errorf("scaffold mkdir %s: %w", opts.OutDir, err)
	}

	files := []struct {
		name    string
		content []byte
		gofmt   bool
	}{
		{name: "main.go", content: renderScaffoldMain(opts, doc, sdk), gofmt: true},
		{name: "go.mod", content: []byte(renderScaffoldGoMod(opts, dep, resolveRuntimeVersion(ov), ov.RuntimeReplace))},
		{name: "README.md", content: []byte(renderScaffoldReadme(opts, doc, schemes, sdk))},
	}

	for _, f := range files {
		target := filepath.Join(opts.OutDir, f.name)
		if info, err := os.Stat(target); err == nil {
			if info.IsDir() {
				return fmt.Errorf("scaffold: %s exists and is a directory", target)
			}
			if !opts.Force {
				return fmt.Errorf("scaffold: %s already exists; pass -force to overwrite", target)
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("scaffold stat %s: %w", target, err)
		}
		body := f.content
		if f.gofmt {
			formatted, ferr := format.Source(body)
			if ferr != nil {
				return fmt.Errorf("scaffold gofmt %s: %w\n--- source ---\n%s", f.name, ferr, body)
			}
			body = formatted
		}
		if err := os.WriteFile(target, body, 0o644); err != nil {
			return fmt.Errorf("scaffold write %s: %w", target, err)
		}
	}
	return nil
}

// resolveRuntimeVersion picks the version string the scaffold's go.mod
// uses to require this generator's runtime package. Override > build-info
// > "v0.0.0" fallback. The fallback gets the user to `go mod tidy` cleanly
// since v0.0.0 isn't a real release the proxy serves.
func resolveRuntimeVersion(ov ScaffoldOverrides) string {
	if ov.RuntimeVersion != "" {
		return ov.RuntimeVersion
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "v0.0.0"
}

// renderScaffoldGoMod emits the go.mod declaration. The require block is
// sorted for determinism. A `replace` directive is appended only when
// requested (e2e test path); production scaffolds don't include one.
func renderScaffoldGoMod(opts Options, sdk struct{ Module, Version string }, runtimeVer, replacePath string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "module %s\n\n", opts.ModulePath)
	// go.mod's `go` directive: pin to a baseline that the generated
	// source is known to compile against. Matching the generator's own
	// minor version is too aggressive; pin to the stable baseline.
	b.WriteString("go 1.23\n\n")
	requires := []struct{ Module, Version string }{
		{Module: runtimeModulePath, Version: runtimeVer},
		sdk,
	}
	sort.Slice(requires, func(i, j int) bool { return requires[i].Module < requires[j].Module })
	b.WriteString("require (\n")
	for _, r := range requires {
		fmt.Fprintf(&b, "\t%s %s\n", r.Module, r.Version)
	}
	b.WriteString(")\n")
	if replacePath != "" {
		fmt.Fprintf(&b, "\nreplace %s => %s\n", runtimeModulePath, replacePath)
	}
	return b.String()
}

// scaffoldMainTemplate is the main.go template. Branches on Adapter ("gosdk"
// vs "mark3labs") to emit the matching transport setup. Inputs are
// pre-validated; the template never sees an unsupported SDK.
const scaffoldMainTemplate = `// Code generated by openapi-go-mcp. DO NOT EDIT.
//
// Entrypoint for the {{.ServerName}} MCP server. Reads upstream credentials
// from environment variables (see README.md) and serves MCP tools on stdio.

package main

import (
{{- if eq .Adapter "gosdk"}}
	"context"
{{- end}}
	"fmt"
	"log"
	"os"

{{- if eq .Adapter "gosdk"}}
	"github.com/modelcontextprotocol/go-sdk/mcp"
{{- else}}
	mcpserver "github.com/mark3labs/mcp-go/server"
{{- end}}

	"{{.PkgImport}}"
	"github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime/{{.Adapter}}"
)

func main() {
	raw, s := {{.Adapter}}.NewServer({{quote .ServerName}}, {{quote .ServerVersion}})
	{{.PkgName}}.{{.RegisterFunc}}(s)

	fmt.Fprintf(os.Stderr, "%s serving over stdio\n", {{quote .ServerName}})
{{- if eq .Adapter "gosdk"}}
	if err := raw.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server: %v", err)
	}
{{- else}}
	if err := mcpserver.ServeStdio(raw); err != nil {
		log.Fatalf("server: %v", err)
	}
{{- end}}
}
`

// renderScaffoldMain builds the main.go source. Errors from template
// execution panic — the inputs are tightly constrained (opts validated
// upstream, doc.Info nilness handled), so any failure here is a generator
// bug we want surfaced loudly in tests.
func renderScaffoldMain(opts Options, doc *openapi3.T, sdk string) []byte {
	view := struct {
		Adapter       string
		PkgImport     string
		PkgName       string
		RegisterFunc  string
		ServerName    string
		ServerVersion string
	}{
		Adapter:       sdk,
		PkgImport:     path.Join(opts.ModulePath, opts.PackageName),
		PkgName:       opts.PackageName,
		RegisterFunc:  registerFuncName(doc),
		ServerName:    derivedServerName(opts, doc),
		ServerVersion: derivedServerVersion(doc),
	}
	tmpl := template.Must(template.New("main").Funcs(template.FuncMap{"quote": goQuote}).Parse(scaffoldMainTemplate))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, view); err != nil {
		panic(fmt.Sprintf("scaffold main template: %v", err))
	}
	return buf.Bytes()
}

// derivedServerName is the "name" the generated main.go advertises to MCP
// clients. Prefers the spec title, falls back to "mcp-server".
func derivedServerName(opts Options, doc *openapi3.T) string {
	if doc != nil && doc.Info != nil && doc.Info.Title != "" {
		return doc.Info.Title
	}
	if opts.PackageName != "" {
		return opts.PackageName
	}
	return "mcp-server"
}

// derivedServerVersion is the version advertised at startup. Pulled from
// doc.Info.Version when present, otherwise "0.0.0" — MCP clients expect
// a non-empty version string.
func derivedServerVersion(doc *openapi3.T) string {
	if doc != nil && doc.Info != nil && doc.Info.Version != "" {
		return doc.Info.Version
	}
	return "0.0.0"
}

// renderScaffoldReadme emits a minimal README.md tailored to the generated
// module: how to set the base URL, the env-var table for each scheme, and
// a one-liner to build and run.
func renderScaffoldReadme(opts Options, doc *openapi3.T, schemes []SecurityScheme, sdk string) string {
	var b strings.Builder
	title := derivedServerName(opts, doc)
	fmt.Fprintf(&b, "# %s — MCP proxy\n\n", title)
	fmt.Fprintf(&b, "Generated by `openapi-go-mcp` in **proxy mode**. This module proxies MCP tool calls to the upstream HTTP API described by the OpenAPI spec.\n\n")
	fmt.Fprintf(&b, "MCP SDK adapter: **%s**\n\n", sdk)

	b.WriteString("## Configuration\n\n")
	b.WriteString("| Variable | Purpose |\n|---|---|\n")
	b.WriteString("| `API_BASE_URL` | Upstream HTTP base URL. Overrides the spec's `servers[0].url`. |\n")

	if len(schemes) == 0 {
		b.WriteString("\n_The spec declares no authentication; the proxy makes anonymous requests._\n")
	} else {
		// Sort schemes for stable README ordering.
		ss := append([]SecurityScheme(nil), schemes...)
		sort.Slice(ss, func(i, j int) bool { return ss[i].Name < ss[j].Name })
		for _, s := range ss {
			switch s.Kind {
			case SecurityHTTPBasic:
				fmt.Fprintf(&b, "| `%s` | Username for HTTP Basic auth scheme `%s`. |\n", s.UsernameEnvVar, s.Name)
				fmt.Fprintf(&b, "| `%s` | Password for HTTP Basic auth scheme `%s`. |\n", s.PasswordEnvVar, s.Name)
			default:
				fmt.Fprintf(&b, "| `%s` | Credential for security scheme `%s` (kind: %s). |\n", s.EnvVar, s.Name, s.Kind)
			}
		}
	}

	b.WriteString("\n## Run\n\n")
	b.WriteString("```bash\n")
	b.WriteString("go mod tidy\n")
	b.WriteString("go build\n")
	fmt.Fprintf(&b, "./%s\n", filepath.Base(opts.ModulePath))
	b.WriteString("```\n\n")
	b.WriteString("The server communicates over stdio — wire it into an MCP-aware client (Claude Desktop, MCP Inspector, etc.) per that client's documentation.\n")
	return b.String()
}
