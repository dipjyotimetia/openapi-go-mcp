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
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/getkin/kin-openapi/openapi3"
)

// generate is the worker called by the exported Generate; it does all the
// rendering, formats the result with gofmt, and writes it to disk. It
// returns the diagnostics collected during rendering so the CLI can surface
// them after a successful write.
func generate(doc *openapi3.T, opts Options) ([]Diagnostic, error) {
	src, ops, diags, err := renderWithOps(doc, opts)
	if err != nil {
		return diags, err
	}
	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return diags, fmt.Errorf("mkdir %s: %w", opts.OutDir, err)
	}
	// In proxy mode the output is a runnable Go module: main.go (package
	// main) lives at OutDir, and the generated MCP file lives in a
	// subdirectory matching its package name. Companion mode keeps the
	// flat layout (one file, no main) for backwards compatibility.
	pkgDir := opts.OutDir
	if opts.Mode == ModeProxy {
		pkgDir = filepath.Join(opts.OutDir, opts.PackageName)
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			return diags, fmt.Errorf("mkdir %s: %w", pkgDir, err)
		}
	}
	outPath := filepath.Join(pkgDir, opts.PackageName+".mcp.go")
	info, statErr := os.Stat(outPath)
	switch {
	case statErr == nil && info.IsDir():
		// A directory at the output path is rejected even with -force.
		// Removing it would be far more destructive than overwriting a
		// file, and the user almost certainly did not intend it.
		return diags, fmt.Errorf("output path %s exists and is a directory", outPath)
	case statErr == nil && !opts.Force:
		return diags, fmt.Errorf("output file %s already exists; pass -force to overwrite", outPath)
	case statErr != nil && !os.IsNotExist(statErr):
		return diags, fmt.Errorf("stat %s: %w", outPath, statErr)
	}
	if err := os.WriteFile(outPath, src, 0o644); err != nil {
		return diags, fmt.Errorf("write %s: %w", outPath, err)
	}

	// Proxy mode bundles a runnable main.go + go.mod + README alongside the
	// .mcp.go so the output is a complete Go module the user can `go build`.
	// Companion mode skips this; WriteScaffold is itself a no-op there. Ops
	// are reused from the render step so we don't re-walk the spec.
	if opts.Mode == ModeProxy {
		if err := WriteScaffold(opts, doc, collectUsedSchemes(ops)); err != nil {
			return diags, fmt.Errorf("scaffold: %w", err)
		}
	}
	return diags, nil
}

// Render builds the *.mcp.go source for the given spec/options without
// writing to disk. Exported so tests can golden-file the output directly.
// Diagnostics are written to opts.Warnings (or os.Stderr) but the slice is
// not returned — call RenderWithDiagnostics when you need structured access.
func Render(doc *openapi3.T, opts Options) ([]byte, error) {
	src, _, err := RenderWithDiagnostics(doc, opts)
	return src, err
}

// RenderWithDiagnostics is Render plus the structured Diagnostic slice. The
// slice is also non-nil when err != nil so callers can inspect what was
// collected before the failure point.
func RenderWithDiagnostics(doc *openapi3.T, opts Options) ([]byte, []Diagnostic, error) {
	src, _, diags, err := renderWithOps(doc, opts)
	return src, diags, err
}

// renderWithOps is the shared worker behind Render / RenderWithDiagnostics /
// generate. It returns the rendered source plus the resolved []Operation so
// callers that also need the operation list (e.g. the scaffold writer) don't
// have to walk the spec a second time. Exported callers receive the same
// data via the wrappers above.
func renderWithOps(doc *openapi3.T, opts Options) ([]byte, []Operation, []Diagnostic, error) {
	if err := opts.normalize(doc); err != nil {
		return nil, nil, nil, err
	}

	ops, diags, err := CollectOperations(doc, opts)
	if err != nil {
		return nil, nil, diags, err
	}

	if err := validateNoSchemaConstCollisions(ops); err != nil {
		return nil, nil, diags, err
	}

	var (
		tmplSrc      string
		clientAlias  string
		clientImport string
		extraImports []string
		baseDefault  string
		allSchemes   []SecurityScheme
	)
	switch opts.Mode {
	case ModeProxy:
		// Proxy mode doesn't import an oapi-codegen package; ClientImport/
		// ClientAlias are unused in the template. Aggregating the union of
		// security schemes referenced by any operation gives the template
		// the deduplicated list it needs to emit apply<Scheme>Auth helpers.
		tmplSrc = fileTemplateProxy
		baseDefault = defaultBaseURL(doc)
		allSchemes = collectUsedSchemes(ops)
	default:
		clientAlias = path.Base(opts.ClientImport)
		if err := validateClientAlias(clientAlias); err != nil {
			return nil, nil, diags, err
		}
		clientImport = opts.ClientImport
		extraImports = collectExtraImports(ops)
		tmplSrc = fileTemplate
	}

	view := templateView{
		PackageName:    opts.PackageName,
		ClientImport:   clientImport,
		ClientAlias:    clientAlias,
		ClientType:     opts.ClientType,
		RegisterFunc:   registerFuncName(doc),
		StaticPrefix:   opts.NamePrefix,
		SpecSource:     describeSource(doc),
		Ops:            ops,
		ExtraImports:   extraImports,
		BaseURLDefault: baseDefault,
		AllSchemes:     allSchemes,
	}

	tmpl, err := template.New("mcp").Funcs(templateFuncs()).Parse(tmplSrc)
	if err != nil {
		return nil, nil, diags, fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, view); err != nil {
		return nil, nil, diags, fmt.Errorf("execute template: %w", err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		// Return the unformatted source so the caller can inspect the failure.
		return buf.Bytes(), ops, diags, fmt.Errorf("gofmt: %w", err)
	}
	return formatted, ops, diags, nil
}

// defaultBaseURL returns the spec's first declared server URL, or "" when
// the spec has no servers[]. Proxy mode falls back to this string when
// API_BASE_URL is unset; if both are empty the generated handler returns
// a runtime error pointing at the env var. Server variables are left as
// {placeholders} for the runtime to expand via SubstituteServerVariables
// (called by the generated main.go, not here).
func defaultBaseURL(doc *openapi3.T) string {
	if doc == nil || len(doc.Servers) == 0 || doc.Servers[0] == nil {
		return ""
	}
	return doc.Servers[0].URL
}

// collectUsedSchemes returns the deduplicated union of security schemes
// referenced by any operation in ops, in stable Name order. Used by the
// proxy template to emit one apply<Scheme>Auth helper per scheme regardless
// of how many operations reference it.
func collectUsedSchemes(ops []Operation) []SecurityScheme {
	seen := make(map[string]SecurityScheme)
	for _, op := range ops {
		for _, s := range op.Security {
			seen[s.Name] = s
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]SecurityScheme, 0, len(seen))
	for _, v := range seen {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Write writes the rendered source to w. Convenience for testing.
func Write(doc *openapi3.T, opts Options, w io.Writer) error {
	src, err := Render(doc, opts)
	if err != nil {
		return err
	}
	_, err = w.Write(src)
	return err
}

// validateClientAlias rejects ClientImport paths whose base segment would
// collide with Go reserved words or with a small set of stdlib packages the
// template imports anyway (json, context, runtime…). A collision would
// produce code that fails to compile in confusing ways; failing fast in the
// generator surfaces the problem at codegen time with a clear message.
func validateClientAlias(alias string) error {
	if alias == "" {
		return fmt.Errorf("derived client alias is empty; ClientImport must end with a non-empty segment")
	}
	if token.IsKeyword(alias) {
		return fmt.Errorf("client import path's base segment %q is a Go reserved word; rename the package directory or alias it via a separate import wrapper", alias)
	}
	if _, clash := reservedClientAliases[alias]; clash {
		return fmt.Errorf("client import path's base segment %q collides with a package the generated file already imports; rename the directory or wrap it in a sub-package", alias)
	}
	return nil
}

// reservedClientAliases lists names that would shadow imports the template
// always emits (`context`, `json`, `runtime`) or predeclared identifiers
// (`string`, `int`, `error`, …) that the generated code relies on resolving
// to their builtin meaning. `token.IsKeyword` only catches Go keywords, so
// the predeclared set is enumerated here.
var reservedClientAliases = map[string]struct{}{
	"context": {},
	"json":    {},
	"runtime": {},
	// predeclared identifiers (https://go.dev/ref/spec#Predeclared_identifiers)
	"any":        {},
	"bool":       {},
	"byte":       {},
	"comparable": {},
	"complex64":  {},
	"complex128": {},
	"error":      {},
	"false":      {},
	"float32":    {},
	"float64":    {},
	"int":        {},
	"int8":       {},
	"int16":      {},
	"int32":      {},
	"int64":      {},
	"iota":       {},
	"nil":        {},
	"rune":       {},
	"string":     {},
	"true":       {},
	"uint":       {},
	"uint8":      {},
	"uint16":     {},
	"uint32":     {},
	"uint64":     {},
	"uintptr":    {},
}

// validateNoSchemaConstCollisions ensures every tool's safeIdent-mangled
// const name is unique. Two operations whose ToolName mangles to the same
// identifier (e.g. "get-pet" and "get_pet" both → "get_pet") would emit
// duplicate const declarations; reject this at codegen time.
func validateNoSchemaConstCollisions(ops []Operation) error {
	seen := make(map[string]string, len(ops))
	for _, op := range ops {
		c := "input_" + safeIdent(op.ToolName)
		if prev, dup := seen[c]; dup && prev != op.ToolName {
			return fmt.Errorf("tool names %q and %q both mangle to const %q; rename one operation or pass a -name-prefix to disambiguate", prev, op.ToolName, c)
		}
		seen[c] = op.ToolName
	}
	return nil
}

type templateView struct {
	PackageName  string
	ClientImport string
	ClientAlias  string
	ClientType   string
	RegisterFunc string
	StaticPrefix string
	SpecSource   string
	Ops          []Operation
	// ExtraImports lists additional Go imports required by typed path-param
	// declarations (e.g. openapi_types.UUID needs github.com/oapi-codegen/runtime/types).
	// Each entry is `alias "path"` or just `"path"` when no alias is needed.
	ExtraImports []string
	// BaseURLDefault is the spec's first declared server URL; only populated
	// in ModeProxy. The generated handler defaults to this string when
	// API_BASE_URL is unset. Empty when the spec has no servers[].
	BaseURLDefault string
	// AllSchemes is the deduplicated set of security schemes referenced by
	// any operation; only populated in ModeProxy. The proxy template emits
	// one apply<Scheme>Auth helper per entry.
	AllSchemes []SecurityScheme
}

// collectExtraImports walks every path parameter in ops and returns the sorted,
// deduplicated import lines required by their Go types.
func collectExtraImports(ops []Operation) []string {
	seen := map[string]string{} // import path -> alias (or "")
	for _, op := range ops {
		for _, p := range op.PathParams {
			if p.GoTypeImport == "" {
				continue
			}
			seen[p.GoTypeImport] = importAliasFor(p.GoType)
		}
	}
	if len(seen) == 0 {
		return nil
	}
	paths := make([]string, 0, len(seen))
	for p := range seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if alias := seen[p]; alias != "" {
			out = append(out, alias+" "+strconv.Quote(p))
		} else {
			out = append(out, strconv.Quote(p))
		}
	}
	return out
}

// importAliasFor returns the alias needed to make goType resolve against its
// import path. Currently only oapi-codegen's runtime/types needs aliasing —
// its package name is `types`, but we use `openapi_types` for clarity.
func importAliasFor(goType string) string {
	if strings.HasPrefix(goType, "openapi_types.") {
		return "openapi_types"
	}
	return ""
}

func registerFuncName(doc *openapi3.T) string {
	title := "API"
	if doc.Info != nil && doc.Info.Title != "" {
		title = doc.Info.Title
	}
	return "Register" + PascalCase(title) + "Client"
}

func describeSource(doc *openapi3.T) string {
	if doc.Info == nil {
		return ""
	}
	parts := []string{doc.Info.Title}
	if doc.Info.Version != "" {
		parts = append(parts, "v"+doc.Info.Version)
	}
	return strings.Join(parts, " ")
}
