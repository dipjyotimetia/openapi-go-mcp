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

func TestDiagnostics_DeepObjectStyleIsSupported(t *testing.T) {
	doc := docWith(t, "/x", func(op *openapi3.Operation, _ *openapi3.PathItem) {
		op.Parameters = openapi3.Parameters{
			{Value: &openapi3.Parameter{Name: "filter", In: "query", Style: "deepObject", Schema: openapi3.NewObjectSchema().NewRef()}},
		}
	})
	if hasDiagCode(diagsFor(t, doc), DiagUnsupportedParameterStyle) {
		t.Errorf("did not expect %q diagnostic", DiagUnsupportedParameterStyle)
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

func TestDiagnostics_DroppedWebhook(t *testing.T) {
	doc := docWith(t, "/x", func(_ *openapi3.Operation, _ *openapi3.PathItem) {})
	doc.Webhooks = map[string]*openapi3.PathItem{
		"newUser": {Post: &openapi3.Operation{OperationID: "newUserHook", Responses: newOKResponses()}},
	}
	if !hasDiagCode(diagsFor(t, doc), DiagDroppedWebhook) {
		t.Errorf("expected %q diagnostic", DiagDroppedWebhook)
	}
}

func TestDiagnostics_DroppedWebhook_WebhooksOnlyDoc(t *testing.T) {
	// A 3.1 document may declare webhooks with no paths at all; the
	// diagnostic must still fire even though CollectOperations has no
	// operations to walk.
	doc := &openapi3.T{
		OpenAPI: "3.1.0",
		Info:    &openapi3.Info{Title: "Diag", Version: "1"},
		Webhooks: map[string]*openapi3.PathItem{
			"ping": {Post: &openapi3.Operation{OperationID: "pingHook", Responses: newOKResponses()}},
		},
	}
	if !hasDiagCode(diagsFor(t, doc), DiagDroppedWebhook) {
		t.Errorf("expected %q diagnostic on a webhooks-only document", DiagDroppedWebhook)
	}
}

func TestDiagnostics_DroppedLink(t *testing.T) {
	doc := docWith(t, "/x", func(op *openapi3.Operation, _ *openapi3.PathItem) {
		resp := op.Responses.Value("200")
		resp.Value.Links = openapi3.Links{
			"getRelated": {Value: &openapi3.Link{OperationID: "related"}},
		}
	})
	diags := diagsFor(t, doc)
	if !hasDiagCode(diags, DiagDroppedLink) {
		t.Fatalf("expected %q diagnostic", DiagDroppedLink)
	}
	for _, d := range diags {
		if d.Code == DiagDroppedLink && !strings.Contains(d.Message, "200.getRelated") {
			t.Errorf("link diagnostic should name the status.link pair, got %q", d.Message)
		}
	}
}

// multipartEncodingDoc builds a one-operation doc whose multipart body has a
// binary property named "avatar" with encoding metadata keyed by that name.
// When nested is true the property sits inside a "user" object, so the
// encoding key can't reach it; when false it is top-level and applies.
func multipartEncodingDoc(t *testing.T, nested bool) *openapi3.T {
	t.Helper()
	return docWith(t, "/x", func(op *openapi3.Operation, _ *openapi3.PathItem) {
		binary := openapi3.NewStringSchema()
		binary.Format = "binary"
		body := openapi3.NewObjectSchema()
		if nested {
			user := openapi3.NewObjectSchema()
			user.Properties = openapi3.Schemas{"avatar": binary.NewRef()}
			body.Properties = openapi3.Schemas{"user": user.NewRef()}
		} else {
			body.Properties = openapi3.Schemas{"avatar": binary.NewRef()}
		}
		mt := openapi3.NewMediaType().WithSchema(body)
		mt.Encoding = map[string]*openapi3.Encoding{
			"avatar": {ContentType: "image/png"},
		}
		op.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Content: openapi3.Content{"multipart/form-data": mt},
		}}
	})
}

func TestDiagnostics_NestedMultipartEncoding(t *testing.T) {
	// Encoding keyed by a nested leaf's name cannot apply — must warn.
	if !hasDiagCode(diagsFor(t, multipartEncodingDoc(t, true)), DiagNestedMultipartEncoding) {
		t.Errorf("expected %q diagnostic", DiagNestedMultipartEncoding)
	}
}

func TestDiagnostics_NestedMultipartEncoding_NotFiredForTopLevel(t *testing.T) {
	// encoding metadata on a top-level binary property is honoured, not
	// dropped — the diagnostic must stay quiet.
	if hasDiagCode(diagsFor(t, multipartEncodingDoc(t, false)), DiagNestedMultipartEncoding) {
		t.Errorf("%q must not fire when the encoding key addresses a top-level property", DiagNestedMultipartEncoding)
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
