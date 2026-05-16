// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

// Command openapi-gen-go-mcp generates MCP-server Go code from an OpenAPI
// 3.x or Swagger 2.0 specification.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/dipjyotimetia/openapi-gen-go-mcp/pkg/generator"
	"github.com/dipjyotimetia/openapi-gen-go-mcp/pkg/loader"
)

// Build metadata populated by GoReleaser ldflags (`-X main.version=...`).
// When the binary is built from source by `go install` the goreleaser values
// are empty and we fall back to `debug.ReadBuildInfo()`.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Exit codes — referenced by CI pipelines that want to distinguish bad input
// from genuine generator bugs. Keep stable across releases.
const (
	exitOK            = 0
	exitUsage         = 1 // flag misuse / missing required arg
	exitBadInput      = 2 // bad spec / unloadable file / parse failure
	exitGenerate      = 3 // generator (or write) failure
	exitWarningsError = 4 // diagnostics surfaced and -warnings-as-errors was set
)

func main() {
	os.Exit(run())
}

func run() int {
	var (
		specPath        = flag.String("spec", "", "path or http(s):// URL of OpenAPI 3.x or Swagger 2.0 spec (required)")
		outDir          = flag.String("out", "./mcp", "output directory")
		pkgName         = flag.String("package", "", "Go package name (defaults to <title>mcp)")
		clientImport    = flag.String("client-import", "", "Go import path of oapi-codegen output (required)")
		clientType      = flag.String("client-type", "ClientWithResponsesInterface", "client interface name from the oapi-codegen package")
		namePrefix      = flag.String("name-prefix", "", "static prefix added to every tool name")
		openAICompat    = flag.Bool("openai-compat", false, "emit OpenAI-tool-compatible JSON Schema")
		preferCT        = flag.String("prefer-content-type", "", "request content type to pick when an operation declares multiple (overrides the JSON → form → multipart → octet → text → xml priority)")
		listOnly        = flag.Bool("list", false, "do not generate; list operations from the spec and exit")
		emitV3          = flag.String("emit-v3", "", "do not generate code; write the spec as OpenAPI 3.x YAML to this path (useful for feeding converted Swagger 2.0 to oapi-codegen)")
		warningsAsError = flag.Bool("warnings-as-errors", false, "exit non-zero when any warning-level diagnostic fires")
		showVersion     = flag.Bool("version", false, "print version information and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("openapi-gen-go-mcp %s (commit %s, built %s)\n", resolveVersion(), commit, date)
		return exitOK
	}

	if *specPath == "" {
		fmt.Fprintln(os.Stderr, "openapi-gen-go-mcp: the -spec flag is required")
		return exitUsage
	}

	ctx := context.Background()
	doc, err := loader.Load(ctx, *specPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "openapi-gen-go-mcp: load: %v\n", err)
		return exitBadInput
	}

	if *listOnly {
		generator.ListOperations(os.Stdout, doc)
		return exitOK
	}

	if *emitV3 != "" {
		if err := loader.WriteV3YAMLJSONOnly(doc, *emitV3); err != nil {
			fmt.Fprintf(os.Stderr, "openapi-gen-go-mcp: emit-v3: %v\n", err)
			return exitGenerate
		}
		return exitOK
	}

	if *clientImport == "" {
		fmt.Fprintln(os.Stderr, "openapi-gen-go-mcp: the -client-import flag is required (path to your oapi-codegen output package)")
		return exitUsage
	}

	opts := generator.Options{
		OutDir:            *outDir,
		PackageName:       *pkgName,
		ClientImport:      *clientImport,
		ClientType:        *clientType,
		NamePrefix:        *namePrefix,
		OpenAICompat:      *openAICompat,
		PreferContentType: *preferCT,
	}
	diags, err := generator.Generate(doc, opts)
	if err != nil {
		printDiagnostics(diags)
		fmt.Fprintf(os.Stderr, "openapi-gen-go-mcp: generate: %v\n", err)
		return exitGenerate
	}
	printDiagnostics(diags)
	if *warningsAsError && countWarnings(diags) > 0 {
		return exitWarningsError
	}
	return exitOK
}

// printDiagnostics renders structured generator findings to stderr in a
// stable shape (severity-grouped, sorted) so CI logs are diffable. The
// generator also writes a free-form line per diagnostic to opts.Warnings
// (os.Stderr by default); both are kept for backwards compatibility.
func printDiagnostics(diags []generator.Diagnostic) {
	if len(diags) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "openapi-gen-go-mcp: %d diagnostic(s):\n", len(diags))
	for _, d := range diags {
		fmt.Fprintf(os.Stderr, "  [%s] %s %s: %s\n", d.Severity, d.Code, d.Path, d.Message)
	}
}

func countWarnings(diags []generator.Diagnostic) int {
	n := 0
	for _, d := range diags {
		if d.Severity == generator.SeverityWarning {
			n++
		}
	}
	return n
}

// resolveVersion returns the goreleaser-injected version when present, or
// falls back to the module version recorded in the binary's build info so
// `go install` users still see a meaningful tag.
func resolveVersion() string {
	if version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return version
	}
	return info.Main.Version
}
