// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package generator

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestToolAnnotations_MethodMapping(t *testing.T) {
	cases := []struct {
		method  string
		summary string
		want    string // annotationsLit rendering of the derived annotations
	}{
		{"GET", "", "&runtime.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}"},
		{"HEAD", "", "&runtime.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}"},
		{"PUT", "", "&runtime.ToolAnnotations{IdempotentHint: true}"},
		{"DELETE", "", "&runtime.ToolAnnotations{IdempotentHint: true, DestructiveHint: runtime.BoolPtr(true)}"},
		{"POST", "", ""},
		{"PATCH", "", ""},
		{"POST", "Create a widget", `&runtime.ToolAnnotations{Title: "Create a widget"}`},
		{"GET", "List widgets", `&runtime.ToolAnnotations{Title: "List widgets", ReadOnlyHint: true, IdempotentHint: true}`},
	}
	for _, tc := range cases {
		got := annotationsLit(Operation{Annotations: toolAnnotations(tc.method, tc.summary)})
		if got != tc.want {
			t.Errorf("toolAnnotations(%s, summary=%q):\n got  %s\n want %s", tc.method, tc.summary, got, tc.want)
		}
	}
}

func TestChooseDescription_DeprecatedPrefix(t *testing.T) {
	op := &openapi3.Operation{Summary: "Old thing", Deprecated: true}
	if got := chooseDescription(op); got != "Deprecated. Old thing" {
		t.Errorf("deprecated prefix missing: %q", got)
	}
	op = &openapi3.Operation{Deprecated: true}
	if got := chooseDescription(op); got != "Deprecated." {
		t.Errorf("bare deprecated marker missing: %q", got)
	}
	op = &openapi3.Operation{Summary: "Current thing"}
	if got := chooseDescription(op); got != "Current thing" {
		t.Errorf("non-deprecated op must not be prefixed: %q", got)
	}
}

// outputSchemaFor runs CollectOperations over a one-operation doc whose GET
// 200 response carries the given schema, returning the resulting
// OutputSchemaJSON.
func outputSchemaFor(t *testing.T, respSchema *openapi3.Schema) string {
	t.Helper()
	desc := "OK"
	responses := openapi3.NewResponses()
	resp := &openapi3.Response{Description: &desc}
	if respSchema != nil {
		resp.Content = openapi3.Content{
			"application/json": openapi3.NewMediaType().WithSchema(respSchema),
		}
	}
	responses.Set("200", &openapi3.ResponseRef{Value: resp})
	doc := docWith(t, "/x", func(o *openapi3.Operation, _ *openapi3.PathItem) {
		o.Responses = responses
	})
	ops, _, err := CollectOperations(doc, Options{ClientImport: "ex/cli"})
	if err != nil {
		t.Fatalf("CollectOperations: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	return ops[0].OutputSchemaJSON
}

func TestOutputSchema_ObjectResponse(t *testing.T) {
	schema := openapi3.NewObjectSchema()
	schema.Properties = openapi3.Schemas{"id": openapi3.NewInt64Schema().NewRef()}
	out := outputSchemaFor(t, schema)
	if out == "" {
		t.Fatal("object-rooted JSON response must produce an output schema")
	}
	if !strings.Contains(out, `"type": "object"`) {
		t.Errorf("output schema must declare an object root:\n%s", out)
	}
}

func TestOutputSchema_ArrayResponseSkipped(t *testing.T) {
	arr := openapi3.NewArraySchema()
	arr.Items = openapi3.NewStringSchema().NewRef()
	if out := outputSchemaFor(t, arr); out != "" {
		t.Errorf("array-rooted response must not produce an output schema, got:\n%s", out)
	}
}

func TestOutputSchema_NoJSONResponseSkipped(t *testing.T) {
	if out := outputSchemaFor(t, nil); out != "" {
		t.Errorf("contentless response must not produce an output schema, got:\n%s", out)
	}
}

func TestOutputSchema_DefaultNextTo2xxSkipped(t *testing.T) {
	// The classic pattern: 204 No Content success + "default" error response.
	// The default (error) schema must NOT be advertised as the tool's output.
	errSchema := openapi3.NewObjectSchema()
	errSchema.Properties = openapi3.Schemas{"message": openapi3.NewStringSchema().NewRef()}
	desc := "No Content"
	errDesc := "Error"
	responses := openapi3.NewResponses()
	responses.Set("204", &openapi3.ResponseRef{Value: &openapi3.Response{Description: &desc}})
	responses.Set("default", &openapi3.ResponseRef{Value: &openapi3.Response{
		Description: &errDesc,
		Content: openapi3.Content{
			"application/json": openapi3.NewMediaType().WithSchema(errSchema),
		},
	}})
	doc := docWith(t, "/x", func(o *openapi3.Operation, _ *openapi3.PathItem) {
		o.Responses = responses
	})
	ops, _, err := CollectOperations(doc, Options{ClientImport: "ex/cli"})
	if err != nil {
		t.Fatalf("CollectOperations: %v", err)
	}
	if ops[0].OutputSchemaJSON != "" {
		t.Errorf("default-response schema next to a contentless 2xx must not become the output schema, got:\n%s", ops[0].OutputSchemaJSON)
	}
	// The wrapper still uses default for content-type selection.
	if ops[0].ResponseKind != BodyJSON {
		t.Errorf("ResponseKind should still fall back to default, got %q", ops[0].ResponseKind)
	}
}

func TestOutputSchema_DefaultOnlyUsed(t *testing.T) {
	// An operation with ONLY a default response: default is all the spec says
	// about success, so it may become the output schema.
	schema := openapi3.NewObjectSchema()
	schema.Properties = openapi3.Schemas{"ok": openapi3.NewBoolSchema().NewRef()}
	desc := "the only response"
	responses := openapi3.NewResponses()
	responses.Set("default", &openapi3.ResponseRef{Value: &openapi3.Response{
		Description: &desc,
		Content: openapi3.Content{
			"application/json": openapi3.NewMediaType().WithSchema(schema),
		},
	}})
	doc := docWith(t, "/x", func(o *openapi3.Operation, _ *openapi3.PathItem) {
		o.Responses = responses
	})
	ops, _, err := CollectOperations(doc, Options{ClientImport: "ex/cli"})
	if err != nil {
		t.Fatalf("CollectOperations: %v", err)
	}
	if ops[0].OutputSchemaJSON == "" {
		t.Error("default-only operation should emit an output schema from the default response")
	}
}

func TestInputSchema_ParameterObjectMetadata(t *testing.T) {
	doc := docWith(t, "/x", func(op *openapi3.Operation, _ *openapi3.PathItem) {
		op.Parameters = openapi3.Parameters{
			{Value: &openapi3.Parameter{
				Name:        "limit",
				In:          "query",
				Description: "max results per page",
				Example:     25,
				Deprecated:  true,
				Schema:      openapi3.NewIntegerSchema().NewRef(),
			}},
		}
	})
	ops, _, err := CollectOperations(doc, Options{ClientImport: "ex/cli"})
	if err != nil {
		t.Fatalf("CollectOperations: %v", err)
	}
	schema := ops[0].InputSchemaJSON
	for _, want := range []string{"max results per page", `"deprecated": true`, `"examples"`} {
		if !strings.Contains(schema, want) {
			t.Errorf("input schema missing parameter-object metadata %q:\n%s", want, schema)
		}
	}
}

func TestInputSchema_SchemaDescriptionWinsOverParameter(t *testing.T) {
	paramSchema := openapi3.NewIntegerSchema()
	paramSchema.Description = "schema-level description"
	doc := docWith(t, "/x", func(op *openapi3.Operation, _ *openapi3.PathItem) {
		op.Parameters = openapi3.Parameters{
			{Value: &openapi3.Parameter{
				Name:        "limit",
				In:          "query",
				Description: "parameter-level description",
				Schema:      paramSchema.NewRef(),
			}},
		}
	})
	ops, _, err := CollectOperations(doc, Options{ClientImport: "ex/cli"})
	if err != nil {
		t.Fatalf("CollectOperations: %v", err)
	}
	if !strings.Contains(ops[0].InputSchemaJSON, "schema-level description") {
		t.Error("schema-level description must be preserved")
	}
	if strings.Contains(ops[0].InputSchemaJSON, "parameter-level description") {
		t.Error("parameter-level description must not override the schema's own")
	}
}
