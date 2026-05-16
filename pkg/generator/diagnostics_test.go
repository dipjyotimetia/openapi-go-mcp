// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package generator

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// docWith builds an OpenAPI doc with one operation customised by mutate.
// Helps every diagnostic test stay terse and intent-driven.
func docWith(t *testing.T, path string, mutate func(*openapi3.Operation, *openapi3.PathItem)) *openapi3.T {
	t.Helper()
	op := &openapi3.Operation{
		OperationID: "op",
		Responses:   newOKResponses(),
	}
	item := &openapi3.PathItem{Get: op}
	mutate(op, item)
	doc := &openapi3.T{
		OpenAPI: "3.0.0",
		Info:    &openapi3.Info{Title: "Diag", Version: "1"},
		Paths:   &openapi3.Paths{},
	}
	doc.Paths.Set(path, item)
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate: %v", err)
	}
	return doc
}

func diagsFor(t *testing.T, doc *openapi3.T) []Diagnostic {
	t.Helper()
	_, diags, err := CollectOperations(doc, Options{
		ClientImport: "ex/cli",
		Warnings:     io.Discard,
	})
	if err != nil {
		t.Fatalf("CollectOperations: %v", err)
	}
	return diags
}

func hasDiagCode(diags []Diagnostic, code string) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}

func TestDiagnostics_CookieParam_NowSupported(t *testing.T) {
	// Cookies are first-class parameters now — the "dropped" diagnostic must
	// NOT fire so users don't get confused when their cookie params show up
	// in the generated tool schema.
	doc := docWith(t, "/x", func(op *openapi3.Operation, _ *openapi3.PathItem) {
		op.Parameters = openapi3.Parameters{
			{Value: &openapi3.Parameter{Name: "sid", In: "cookie", Schema: openapi3.NewStringSchema().NewRef()}},
		}
	})
	if hasDiagCode(diagsFor(t, doc), DiagDroppedCookieParam) {
		t.Errorf("dropped-cookie-param must NOT fire after cookie support landed")
	}
}

func TestDiagnostics_Callback(t *testing.T) {
	doc := docWith(t, "/x", func(op *openapi3.Operation, _ *openapi3.PathItem) {
		op.Callbacks = openapi3.Callbacks{
			"onEvent": {Value: &openapi3.Callback{}},
		}
	})
	if !hasDiagCode(diagsFor(t, doc), DiagDroppedCallback) {
		t.Errorf("expected %q diagnostic", DiagDroppedCallback)
	}
}

func TestDiagnostics_UnsupportedParameterStyle_DeepObject(t *testing.T) {
	doc := docWith(t, "/x", func(op *openapi3.Operation, _ *openapi3.PathItem) {
		op.Parameters = openapi3.Parameters{
			{Value: &openapi3.Parameter{Name: "filter", In: "query", Style: "deepObject", Schema: openapi3.NewObjectSchema().NewRef()}},
		}
	})
	if !hasDiagCode(diagsFor(t, doc), DiagUnsupportedParameterStyle) {
		t.Errorf("expected %q diagnostic", DiagUnsupportedParameterStyle)
	}
}

func TestDiagnostics_ShadowedParameter(t *testing.T) {
	doc := docWith(t, "/x", func(op *openapi3.Operation, item *openapi3.PathItem) {
		item.Parameters = openapi3.Parameters{
			{Value: &openapi3.Parameter{Name: "limit", In: "query", Schema: openapi3.NewIntegerSchema().NewRef()}},
		}
		op.Parameters = openapi3.Parameters{
			{Value: &openapi3.Parameter{Name: "limit", In: "query", Schema: openapi3.NewIntegerSchema().NewRef()}},
		}
	})
	if !hasDiagCode(diagsFor(t, doc), DiagShadowedParameter) {
		t.Errorf("expected %q diagnostic", DiagShadowedParameter)
	}
}

func TestDiagnostics_SecurityRequirement_PerOperation(t *testing.T) {
	doc := docWith(t, "/x", func(op *openapi3.Operation, _ *openapi3.PathItem) {
		req := openapi3.SecurityRequirements{
			openapi3.SecurityRequirement{"bearerAuth": {}},
		}
		op.Security = &req
	})
	if !hasDiagCode(diagsFor(t, doc), DiagDroppedSecurityRequirement) {
		t.Errorf("expected %q diagnostic", DiagDroppedSecurityRequirement)
	}
}

func TestDiagnostics_ServerVariables(t *testing.T) {
	doc := &openapi3.T{
		OpenAPI: "3.0.0",
		Info:    &openapi3.Info{Title: "Diag", Version: "1"},
		Paths:   &openapi3.Paths{},
		Servers: openapi3.Servers{
			{URL: "{scheme}://api.example.com",
				Variables: map[string]*openapi3.ServerVariable{
					"scheme": {Default: "https", Enum: []string{"http", "https"}},
				},
			},
		},
	}
	doc.Paths.Set("/x", &openapi3.PathItem{Get: &openapi3.Operation{
		OperationID: "op", Responses: newOKResponses(),
	}})
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !hasDiagCode(diagsFor(t, doc), DiagDroppedServerVariables) {
		t.Errorf("expected %q diagnostic", DiagDroppedServerVariables)
	}
}

func TestDiagnostics_StableOrdering(t *testing.T) {
	// Combine a callback (warning) with a per-op security requirement (info)
	// so the sink has at least one diagnostic of each severity to order.
	doc := docWith(t, "/x", func(op *openapi3.Operation, item *openapi3.PathItem) {
		req := openapi3.SecurityRequirements{
			openapi3.SecurityRequirement{"bearerAuth": {}},
		}
		op.Security = &req
		op.Callbacks = openapi3.Callbacks{"x": {Value: &openapi3.Callback{}}}
		_ = item
	})
	diags := diagsFor(t, doc)
	if len(diags) < 2 {
		t.Fatalf("expected >=2 diagnostics, got %d", len(diags))
	}
	for i := 1; i < len(diags); i++ {
		prev, cur := diags[i-1], diags[i]
		if prev.Severity != cur.Severity {
			if prev.Severity != "warning" {
				t.Errorf("warnings must come before info; got %q before %q", prev.Severity, cur.Severity)
			}
			continue
		}
		if prev.Path > cur.Path || (prev.Path == cur.Path && prev.Code > cur.Code) {
			t.Errorf("diag ordering violated at %d: %#v vs %#v", i, prev, cur)
		}
	}
}

func TestDiagnostics_LegacyWarningsMirror(t *testing.T) {
	// Diagnostics must continue to be written to opts.Warnings for backwards
	// compatibility with shell pipelines that scrape stderr.
	doc := docWith(t, "/x", func(op *openapi3.Operation, _ *openapi3.PathItem) {
		op.Callbacks = openapi3.Callbacks{"x": {Value: &openapi3.Callback{}}}
	})
	var buf strings.Builder
	_, _, err := CollectOperations(doc, Options{
		ClientImport: "ex/cli",
		Warnings:     &buf,
	})
	if err != nil {
		t.Fatalf("CollectOperations: %v", err)
	}
	if !strings.Contains(buf.String(), DiagDroppedCallback) {
		t.Errorf("expected legacy warning to mention %q, got %s", DiagDroppedCallback, buf.String())
	}
}
