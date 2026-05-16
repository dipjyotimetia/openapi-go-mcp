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
// rendering, formats the result with gofmt, and writes it to disk.
func generate(doc *openapi3.T, opts Options) error {
	src, err := Render(doc, opts)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", opts.OutDir, err)
	}
	outPath := filepath.Join(opts.OutDir, opts.PackageName+".mcp.go")
	if err := os.WriteFile(outPath, src, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	return nil
}

// Render builds the *.mcp.go source for the given spec/options without
// writing to disk. Exported so tests can golden-file the output directly.
func Render(doc *openapi3.T, opts Options) ([]byte, error) {
	if opts.ClientImport == "" {
		return nil, fmt.Errorf("ClientImport is required")
	}
	if opts.ClientType == "" {
		opts.ClientType = "ClientWithResponsesInterface"
	}
	if opts.PackageName == "" {
		opts.PackageName = derivePackageName(doc)
	}

	ops, err := CollectOperations(doc, opts)
	if err != nil {
		return nil, err
	}

	view := templateView{
		PackageName:  opts.PackageName,
		ClientImport: opts.ClientImport,
		ClientAlias:  path.Base(opts.ClientImport),
		ClientType:   opts.ClientType,
		RegisterFunc: registerFuncName(doc),
		StaticPrefix: opts.NamePrefix,
		SpecSource:   describeSource(doc),
		Ops:          ops,
		ExtraImports: collectExtraImports(ops),
	}

	tmpl, err := template.New("mcp").Funcs(templateFuncs()).Parse(fileTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, view); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		// Return the unformatted source so the caller can inspect the failure.
		return buf.Bytes(), fmt.Errorf("gofmt: %w", err)
	}
	return formatted, nil
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
