// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

// Package generator turns a kin-openapi document into Go source that
// registers each OpenAPI operation as an MCP tool.
package generator

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// Options configures Generate.
type Options struct {
	// OutDir is the directory where the generated *.mcp.go file is written.
	OutDir string
	// PackageName is the Go package name for the generated file. If empty,
	// it is derived from the spec title with the suffix "mcp".
	PackageName string
	// ClientImport is the Go import path of the user's oapi-codegen output.
	ClientImport string
	// ClientType is the unqualified name of the typed-response client
	// interface exposed by ClientImport. Defaults to
	// "ClientWithResponsesInterface".
	ClientType string
	// NamePrefix prepends "<prefix>_" to every tool name at generation time.
	// Runtime prefixing via runtime.WithNamePrefix is still available.
	NamePrefix string
	// OpenAICompat narrows generated JSON Schema to the subset accepted by
	// OpenAI tool calls (no $ref, no oneOf/anyOf/allOf, additionalProperties:false).
	OpenAICompat bool
	// PreferContentType, when non-empty, makes the generator pick this content
	// type for the request body whenever an operation declares it — overriding
	// the default JSON → form → multipart → octet → text/* → xml priority.
	// Operations that don't declare the preferred type fall back to the
	// default priority.
	PreferContentType string
	// Warnings receives non-fatal generator messages (e.g. spec/handler
	// conflicts). When nil, warnings are written to os.Stderr.
	Warnings io.Writer
}

// ListOperations writes a human-readable summary of the operations in doc to w.
// Useful for debugging spec input before generating code.
func ListOperations(w io.Writer, doc *openapi3.T) {
	type row struct{ method, path, opID string }
	var rows []row

	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			rows = append(rows, row{method, path, op.OperationID})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].path != rows[j].path {
			return rows[i].path < rows[j].path
		}
		return rows[i].method < rows[j].method
	})

	_, _ = fmt.Fprintf(w, "%-7s  %-40s  %s\n", "METHOD", "PATH", "OPERATION ID")
	for _, r := range rows {
		opID := r.opID
		if opID == "" {
			opID = "(missing)"
		}
		_, _ = fmt.Fprintf(w, "%-7s  %-40s  %s\n", r.method, r.path, opID)
	}
}

// Generate is the entry point used by the CLI. It writes the generated file
// and returns the diagnostics collected during the walk so the CLI can
// surface them in addition to legacy stderr output.
func Generate(doc *openapi3.T, opts Options) ([]Diagnostic, error) {
	if err := opts.normalize(doc); err != nil {
		return nil, err
	}
	if opts.OutDir == "" {
		opts.OutDir = "./mcp"
	}
	return generate(doc, opts)
}

// normalize fills in optional fields with their defaults and returns an
// error when a required field (currently just ClientImport) is missing.
// Centralising defaulting prevents Render and Generate from drifting.
func (opts *Options) normalize(doc *openapi3.T) error {
	if opts.ClientImport == "" {
		return fmt.Errorf("ClientImport is required")
	}
	if opts.ClientType == "" {
		opts.ClientType = "ClientWithResponsesInterface"
	}
	if opts.PackageName == "" {
		opts.PackageName = derivePackageName(doc)
	}
	return nil
}

func derivePackageName(doc *openapi3.T) string {
	title := "api"
	if doc.Info != nil && doc.Info.Title != "" {
		title = doc.Info.Title
	}
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		b.WriteString("api")
	}
	b.WriteString("mcp")
	return b.String()
}
