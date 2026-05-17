// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

// Command openapi-go-mcp generates MCP-server Go code from an OpenAPI
// 3.x or Swagger 2.0 specification.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/dipjyotimetia/openapi-go-mcp/pkg/batch"
	"github.com/dipjyotimetia/openapi-go-mcp/pkg/generator"
	"github.com/dipjyotimetia/openapi-go-mcp/pkg/loader"
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
		specPath        = flag.String("spec", "", "path, http(s):// URL, glob pattern, or directory of OpenAPI 3.x / Swagger 2.0 specs. Comma-separated entries are allowed for batch generation across multiple folders. (required)")
		outDir          = flag.String("out", "./mcp", "output directory; in batch mode each spec gets its own subdirectory under this path")
		pkgName         = flag.String("package", "", "Go package name (defaults to <title>mcp); rejected in batch mode where each spec auto-derives its own package name")
		clientImport    = flag.String("client-import", "", "Go import path of oapi-codegen output (required for codegen); in batch mode treated as a base path and the per-spec slug is appended")
		clientType      = flag.String("client-type", "ClientWithResponsesInterface", "client interface name from the oapi-codegen package")
		namePrefix      = flag.String("name-prefix", "", "static prefix added to every tool name")
		openAICompat    = flag.Bool("openai-compat", false, "emit OpenAI-tool-compatible JSON Schema")
		preferCT        = flag.String("prefer-content-type", "", "request content type to pick when an operation declares multiple (overrides the JSON → form → multipart → octet → text → xml priority)")
		excludeDefault  = flag.Bool("exclude-by-default", false, "invert x-mcp filtering: when set, only operations explicitly opted in with `x-mcp: true` are generated; the default (false) generates every operation unless explicitly opted out with `x-mcp: false`")
		force           = flag.Bool("force", false, "overwrite the generated *.mcp.go file if it already exists; without this, an existing file is a fatal error")
		listOnly        = flag.Bool("list", false, "do not generate; list operations from the spec(s) and exit")
		emitV3          = flag.String("emit-v3", "", "do not generate code; write the spec as OpenAPI 3.x YAML to this path (useful for feeding converted Swagger 2.0 to oapi-codegen). Rejected in batch mode.")
		warningsAsError = flag.Bool("warnings-as-errors", false, "exit non-zero when any warning-level diagnostic fires")
		mode            = flag.String("mode", "companion", "emission mode: \"companion\" (default — emit a *.mcp.go that delegates to a user-supplied oapi-codegen client) or \"proxy\" (emit a runnable Go module: *.mcp.go + main.go + go.mod + README, with env-var-driven auth derived from the spec's securitySchemes)")
		modulePath      = flag.String("module", "", "Go module path written into the scaffold's go.mod; required when -mode=proxy, rejected otherwise. In batch mode treated as a base path; per-spec slug is appended.")
		sdk             = flag.String("sdk", "gosdk", "MCP SDK adapter the proxy scaffold imports: \"gosdk\" (default, modelcontextprotocol/go-sdk) or \"mark3labs\" (mark3labs/mcp-go). Ignored in companion mode.")
		showVersion     = flag.Bool("version", false, "print version information and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("openapi-go-mcp %s (commit %s, built %s)\n", resolveVersion(), commit, date)
		return exitOK
	}

	if *specPath == "" {
		fmt.Fprintln(os.Stderr, "openapi-go-mcp: the -spec flag is required")
		return exitUsage
	}

	// Validate -mode and its companions before any spec is loaded so
	// flag-misuse errors surface fast.
	var genMode generator.Mode
	switch *mode {
	case "companion":
		genMode = generator.ModeCompanion
	case "proxy":
		genMode = generator.ModeProxy
	default:
		fmt.Fprintf(os.Stderr, "openapi-go-mcp: -mode must be \"companion\" or \"proxy\"; got %q\n", *mode)
		return exitUsage
	}
	if genMode == generator.ModeProxy {
		if *modulePath == "" {
			fmt.Fprintln(os.Stderr, "openapi-go-mcp: -module is required when -mode=proxy (the Go import path written into the generated go.mod)")
			return exitUsage
		}
		if *sdk != "gosdk" && *sdk != "mark3labs" {
			fmt.Fprintf(os.Stderr, "openapi-go-mcp: -sdk must be \"gosdk\" or \"mark3labs\"; got %q\n", *sdk)
			return exitUsage
		}
		if *emitV3 != "" {
			fmt.Fprintln(os.Stderr, "openapi-go-mcp: -emit-v3 cannot be combined with -mode=proxy (proxy emits Go, not YAML)")
			return exitUsage
		}
	} else if *modulePath != "" {
		fmt.Fprintln(os.Stderr, "openapi-go-mcp: -module is only valid with -mode=proxy")
		return exitUsage
	}

	refs, err := loader.ExpandSpecArg(*specPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "openapi-go-mcp: %v\n", err)
		return exitBadInput
	}
	isBatch := len(refs) > 1

	// Reject flag combinations that only make sense in single-spec mode.
	// Done up front so we don't load any specs only to abort.
	if isBatch {
		if *pkgName != "" {
			fmt.Fprintln(os.Stderr, "openapi-go-mcp: -package cannot be combined with multi-spec input (each spec auto-derives its package name from its filename)")
			return exitUsage
		}
		if *emitV3 != "" {
			fmt.Fprintln(os.Stderr, "openapi-go-mcp: -emit-v3 cannot be combined with multi-spec input (it writes a single file)")
			return exitUsage
		}
	}

	baseOpts := generator.Options{
		Mode:              genMode,
		ModulePath:        *modulePath,
		SDK:               *sdk,
		OutDir:            *outDir,
		PackageName:       *pkgName,
		ClientImport:      *clientImport,
		ClientType:        *clientType,
		NamePrefix:        *namePrefix,
		OpenAICompat:      *openAICompat,
		PreferContentType: *preferCT,
		ExcludeByDefault:  *excludeDefault,
		Force:             *force,
	}

	// Build per-spec plans. Collisions are reported before any file is
	// written so the user fixes them in one pass.
	plans := make([]batch.SpecPlan, 0, len(refs))
	for _, ref := range refs {
		plan, planErr := batch.PlanFor(ref, baseOpts, isBatch)
		if planErr != nil {
			fmt.Fprintf(os.Stderr, "openapi-go-mcp: %v\n", planErr)
			return exitBadInput
		}
		plans = append(plans, plan)
	}
	if isBatch {
		if collErr := batch.DetectCollisions(plans); collErr != nil {
			fmt.Fprintf(os.Stderr, "openapi-go-mcp: %v\n", collErr)
			return exitBadInput
		}
	}

	ctx := context.Background()

	// Pre-flight: companion-mode codegen invocations need -client-import to
	// dispatch into an oapi-codegen package. Proxy mode does its own HTTP
	// so the flag is irrelevant; -list and -emit-v3 don't generate code at
	// all. Gate the check on the actual mode the CLI is in.
	codegenMode := !*listOnly && *emitV3 == ""
	if codegenMode && genMode == generator.ModeCompanion && *clientImport == "" {
		fmt.Fprintln(os.Stderr, "openapi-go-mcp: the -client-import flag is required in companion mode (path to your oapi-codegen output package)")
		return exitUsage
	}

	// Orchestrate. Errors are accumulated so a single bad spec doesn't
	// stop the rest of the batch — CI gets a complete picture in one run.
	cwd, _ := os.Getwd() // best-effort; empty cwd just disables relative rendering.
	exitCode := exitOK
	for _, plan := range plans {
		prefix := displayPath(plan.Ref.Path, cwd)
		doc, loadErr := loader.Load(ctx, plan.Ref.Path)
		if loadErr != nil {
			fmt.Fprintf(os.Stderr, "openapi-go-mcp [%s]: load: %v\n", prefix, loadErr)
			if !isBatch {
				return exitBadInput
			}
			exitCode = exitGenerate // batch partial failure rolls up to "generate failure"
			continue
		}

		if *listOnly {
			if isBatch {
				_, _ = fmt.Fprintf(os.Stdout, "=== %s ===\n", prefix)
			}
			generator.ListOperations(os.Stdout, doc)
			continue
		}

		if *emitV3 != "" {
			// Reached only in single-spec mode (batch rejects -emit-v3 above).
			if emitErr := loader.WriteV3YAMLJSONOnly(doc, *emitV3); emitErr != nil {
				fmt.Fprintf(os.Stderr, "openapi-go-mcp: emit-v3: %v\n", emitErr)
				return exitGenerate
			}
			return exitOK
		}

		diags, genErr := generator.Generate(doc, plan.Opts)
		if genErr != nil {
			printDiagnostics(diags, prefix, isBatch)
			fmt.Fprintf(os.Stderr, "openapi-go-mcp [%s]: generate: %v\n", prefix, genErr)
			if !isBatch {
				return exitGenerate
			}
			exitCode = exitGenerate
			continue
		}
		printDiagnostics(diags, prefix, isBatch)
		if *warningsAsError && countWarnings(diags) > 0 && exitCode == exitOK {
			exitCode = exitWarningsError
		}
	}
	return exitCode
}

// printDiagnostics renders structured generator findings to stderr in a
// stable shape (severity-grouped, sorted) so CI logs are diffable. In
// batch mode each finding is prefixed with its spec source so users can
// trace a diagnostic back to the file that produced it. The generator
// also writes a free-form line per diagnostic to opts.Warnings (os.Stderr
// by default); both are kept for backwards compatibility.
func printDiagnostics(diags []generator.Diagnostic, prefix string, isBatch bool) {
	if len(diags) == 0 {
		return
	}
	header := "openapi-go-mcp"
	if isBatch {
		header = fmt.Sprintf("openapi-go-mcp [%s]", prefix)
	}
	fmt.Fprintf(os.Stderr, "%s: %d diagnostic(s):\n", header, len(diags))
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

// displayPath renders abs as a path relative to cwd when the relative form
// is shorter and doesn't escape the working directory with "..". URL
// inputs (which `loader.ExpandSpecArg` returns untouched) and any path
// where relative rendering would not help are returned verbatim. Used for
// CLI log prefixes only — the underlying loader still operates on the
// absolute path.
func displayPath(abs, cwd string) string {
	if cwd == "" {
		return abs
	}
	if !filepath.IsAbs(abs) {
		return abs
	}
	rel, err := filepath.Rel(cwd, abs)
	if err != nil {
		return abs
	}
	// Don't render paths that escape the working directory; "../../foo"
	// is harder to scan than the absolute form.
	if rel == "." || rel == ".." || filepath.IsAbs(rel) ||
		(len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator)) {
		return abs
	}
	if len(rel) >= len(abs) {
		return abs
	}
	return rel
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
