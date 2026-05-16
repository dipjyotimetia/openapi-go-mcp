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
	src, diags, err := RenderWithDiagnostics(doc, opts)
	if err != nil {
		return diags, err
	}
	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return diags, fmt.Errorf("mkdir %s: %w", opts.OutDir, err)
	}
	outPath := filepath.Join(opts.OutDir, opts.PackageName+".mcp.go")
	if err := os.WriteFile(outPath, src, 0o644); err != nil {
		return diags, fmt.Errorf("write %s: %w", outPath, err)
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
	if err := opts.normalize(doc); err != nil {
		return nil, nil, err
	}

	ops, diags, err := CollectOperations(doc, opts)
	if err != nil {
		return nil, diags, err
	}

	clientAlias := path.Base(opts.ClientImport)
	if err := validateClientAlias(clientAlias); err != nil {
		return nil, diags, err
	}
	if err := validateNoSchemaConstCollisions(ops); err != nil {
		return nil, diags, err
	}

	view := templateView{
		PackageName:  opts.PackageName,
		ClientImport: opts.ClientImport,
		ClientAlias:  clientAlias,
		ClientType:   opts.ClientType,
		RegisterFunc: registerFuncName(doc),
		StaticPrefix: opts.NamePrefix,
		SpecSource:   describeSource(doc),
		Ops:          ops,
		ExtraImports: collectExtraImports(ops),
	}

	tmpl, err := template.New("mcp").Funcs(templateFuncs()).Parse(fileTemplate)
	if err != nil {
		return nil, diags, fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, view); err != nil {
		return nil, diags, fmt.Errorf("execute template: %w", err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		// Return the unformatted source so the caller can inspect the failure.
		return buf.Bytes(), diags, fmt.Errorf("gofmt: %w", err)
	}
	return formatted, diags, nil
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
