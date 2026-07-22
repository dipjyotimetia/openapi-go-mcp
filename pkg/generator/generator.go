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

// Mode selects which output shape the generator emits.
type Mode string

const (
	// ModeCompanion is the default mode: emit a single *.mcp.go that
	// delegates to a user-supplied oapi-codegen client. Companion files
	// drop into an existing module; the user writes main(). Backwards-
	// compatible with every previous release; the golden test pins it
	// byte-for-byte.
	ModeCompanion Mode = ""
	// ModeProxy emits a runnable Go module: <pkg>.mcp.go plus main.go,
	// go.mod, and README.md. The generated handlers build *http.Request
	// objects directly and dispatch through cfg.HTTPClient, with
	// authentication wired from the spec's securitySchemes via env vars.
	// No oapi-codegen step is needed.
	ModeProxy Mode = "proxy"
)

// Options configures Generate.
type Options struct {
	// Mode selects the output shape. Zero value = ModeCompanion (today's
	// behaviour). ModeProxy emits a runnable module with built-in auth.
	Mode Mode
	// ModulePath is the Go module path used in the generated go.mod when
	// Mode == ModeProxy. Required in proxy mode; rejected in companion.
	ModulePath string
	// SDK picks which MCP SDK adapter the generated main.go imports.
	// Valid: "gosdk" (default) or "mark3labs". Only consulted when
	// Mode == ModeProxy; companion mode is SDK-agnostic.
	SDK string
	// RuntimeVersion pins the generated proxy scaffold to this version of the
	// openapi-go-mcp runtime. It is normally populated by the CLI from its
	// release metadata. Development builds must supply it explicitly when the
	// build information does not contain a resolvable module version.
	RuntimeVersion string
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
	// ExcludeByDefault inverts the x-mcp fallback. When false (the zero value
	// and the recommended default), every operation becomes an MCP tool
	// unless explicitly opted out with `x-mcp: false` at the operation,
	// path-item, or document level. When true, only operations explicitly
	// opted in with `x-mcp: true` (at any level) are generated — useful for
	// large specs where the spec author wants to publish a small curated
	// subset as MCP tools without rewriting the rest of the document.
	ExcludeByDefault bool
	// Force, when true, lets Generate overwrite an existing output file
	// without complaint. When false (the default), Generate returns an
	// error if <OutDir>/<PackageName>.mcp.go already exists, so re-running
	// the tool against a directory that contains hand-edited output is a
	// loud failure rather than a silent overwrite.
	Force bool
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
// error when a required field is missing. Required fields differ by
// Mode: companion mode needs ClientImport (the oapi-codegen package);
// proxy mode needs ModulePath instead (the module path for the emitted
// go.mod) and ignores ClientImport. Centralising defaulting prevents
// Render and Generate from drifting.
func (opts *Options) normalize(doc *openapi3.T) error {
	switch opts.Mode {
	case ModeCompanion:
		if opts.ClientImport == "" {
			return fmt.Errorf("ClientImport is required in companion mode")
		}
	case ModeProxy:
		if opts.ModulePath == "" {
			return fmt.Errorf("ModulePath is required in proxy mode (the import path written into the generated go.mod)")
		}
		if opts.SDK == "" {
			opts.SDK = "gosdk"
		}
		if opts.SDK != "gosdk" && opts.SDK != "mark3labs" {
			return fmt.Errorf("SDK must be \"gosdk\" or \"mark3labs\"; got %q", opts.SDK)
		}
	default:
		return fmt.Errorf("unknown Mode %q", opts.Mode)
	}
	if opts.ClientType == "" {
		opts.ClientType = "ClientWithResponsesInterface"
	}
	if opts.PackageName == "" {
		opts.PackageName = derivePackageName(doc)
	}
	return nil
}

// MCPPackageSuffix is the conventional suffix appended to every generated
// Go package name (e.g. spec title "Petstore" → package "petstoremcp").
// Exported so pkg/batch can derive its per-spec package names without
// duplicating the convention.
const MCPPackageSuffix = "mcp"

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
	b.WriteString(MCPPackageSuffix)
	return b.String()
}
