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

func main() {
	var (
		specPath     = flag.String("spec", "", "path to OpenAPI 3.x or Swagger 2.0 spec (required)")
		outDir       = flag.String("out", "./mcp", "output directory")
		pkgName      = flag.String("package", "", "Go package name (defaults to <title>mcp)")
		clientImport = flag.String("client-import", "", "Go import path of oapi-codegen output (required)")
		clientType   = flag.String("client-type", "ClientWithResponsesInterface", "client interface name from the oapi-codegen package")
		namePrefix   = flag.String("name-prefix", "", "static prefix added to every tool name")
		openAICompat = flag.Bool("openai-compat", false, "emit OpenAI-tool-compatible JSON Schema")
		preferCT     = flag.String("prefer-content-type", "", "request content type to pick when an operation declares multiple (overrides the JSON → form → multipart → octet → text → xml priority)")
		listOnly     = flag.Bool("list", false, "do not generate; list operations from the spec and exit")
		emitV3       = flag.String("emit-v3", "", "do not generate code; write the spec as OpenAPI 3.x YAML to this path (useful for feeding converted Swagger 2.0 to oapi-codegen)")
		showVersion  = flag.Bool("version", false, "print version information and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("openapi-gen-go-mcp %s (commit %s, built %s)\n", resolveVersion(), commit, date)
		return
	}

	if *specPath == "" {
		fatal("the -spec flag is required")
	}

	ctx := context.Background()
	doc, err := loader.Load(ctx, *specPath)
	if err != nil {
		fatal("load: %v", err)
	}

	if *listOnly {
		generator.ListOperations(os.Stdout, doc)
		return
	}

	if *emitV3 != "" {
		if err := loader.WriteV3YAMLJSONOnly(doc, *emitV3); err != nil {
			fatal("emit-v3: %v", err)
		}
		return
	}

	if *clientImport == "" {
		fatal("the -client-import flag is required (path to your oapi-codegen output package)")
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
	if err := generator.Generate(doc, opts); err != nil {
		fatal("generate: %v", err)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "openapi-gen-go-mcp: "+format+"\n", args...)
	os.Exit(1)
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
