// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package generator

import (
	"fmt"
	"io"
	"sort"
)

// Severity classifies a Diagnostic. Warnings indicate a spec feature was
// dropped or partially handled; info is purely advisory.
type Severity string

const (
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// Diagnostic is a structured non-fatal generator finding. It exists alongside
// the legacy io.Writer warnings channel so callers (CLI, tests) can consume
// findings as data — group by Severity, filter by Code, etc. — rather than
// scraping free-form strings.
//
// Code values are stable, kebab-cased, and documented; tests and CI assertions
// may match against them.
type Diagnostic struct {
	Severity Severity
	// Code is a stable machine-readable identifier (e.g.
	// "dropped-cookie-param", "shadowed-parameter").
	Code string
	// Path is an optional OpenAPI location — typically "<METHOD> <path>" for
	// per-operation findings, or a component pointer for spec-wide findings.
	Path string
	// Message is the human-readable description, suitable for stderr output.
	Message string
}

// Stable diagnostic codes. Add new codes here so all in-tree emitters can
// reference them and tests can assert against them.
const (
	DiagDroppedCookieParam         = "dropped-cookie-param"
	DiagDroppedCallback            = "dropped-callback"
	DiagDroppedLink                = "dropped-link"
	DiagDroppedWebhook             = "dropped-webhook"
	DiagDroppedSecurityRequirement = "dropped-security-requirement"
	DiagDroppedServerVariables     = "dropped-server-variables"
	DiagUnsupportedParameterStyle  = "unsupported-parameter-style"
	DiagShadowedParameter          = "shadowed-parameter"
	DiagMissingPathParam           = "missing-path-param"
	DiagNestedMultipartEncoding    = "nested-multipart-encoding"
	DiagContentTypeHeaderOverride  = "content-type-header-override"
	// DiagExcludedByXMCP is emitted (info) when an operation is skipped due
	// to an `x-mcp: false` extension at the operation, path-item, or
	// document level. The Path field carries "<METHOD> <path>", the Message
	// names the level that drove the decision.
	DiagExcludedByXMCP = "excluded-by-x-mcp"
	// DiagInvalidXMCPValue is emitted (warning) when an `x-mcp` extension
	// value is neither a boolean nor a "true"/"false" string. The decision
	// falls through to the next precedence level (or the document-wide
	// default); the warning lets the spec author notice and fix typos like
	// `x-mcp: "yes"` or `x-mcp: 1`.
	DiagInvalidXMCPValue = "invalid-x-mcp-value"
	// DiagUnsupportedSecurityScheme is emitted (warning) when a security
	// scheme cannot be lowered into one of the kinds proxy mode wires
	// (apiKey-header/query/cookie, http-bearer, http-basic, oauth2-as-bearer).
	// The scheme is dropped from auth generation; companion mode is
	// unaffected because it never consumes the parsed schemes anyway.
	DiagUnsupportedSecurityScheme = "unsupported-security-scheme"
)

// diagSink collects diagnostics during a single CollectOperations run. It
// also mirrors warning-level findings to the legacy io.Writer so existing
// pipelines that scrape stderr continue to work.
type diagSink struct {
	out []Diagnostic
	w   io.Writer
}

func newDiagSink(w io.Writer) *diagSink {
	return &diagSink{w: w}
}

func (s *diagSink) warn(code, path, message string) {
	s.emit(SeverityWarning, code, path, message)
}

func (s *diagSink) info(code, path, message string) {
	s.emit(SeverityInfo, code, path, message)
}

func (s *diagSink) emit(sev Severity, code, path, message string) {
	s.out = append(s.out, Diagnostic{Severity: sev, Code: code, Path: path, Message: message})
	if s.w != nil {
		_, _ = fmt.Fprintf(s.w, "openapi-go-mcp: %s: %s [%s]: %s\n", sev, path, code, message)
	}
}

func (s *diagSink) finalize() []Diagnostic {
	if len(s.out) == 0 {
		return nil
	}
	sort.SliceStable(s.out, func(i, j int) bool {
		if s.out[i].Severity != s.out[j].Severity {
			// warnings before info so the CLI's grouped output is consistent.
			return s.out[i].Severity == SeverityWarning
		}
		if s.out[i].Path != s.out[j].Path {
			return s.out[i].Path < s.out[j].Path
		}
		if s.out[i].Code != s.out[j].Code {
			return s.out[i].Code < s.out[j].Code
		}
		return s.out[i].Message < s.out[j].Message
	})
	return s.out
}
