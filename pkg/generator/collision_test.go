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
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestRender_RejectsClientAliasReservedWord(t *testing.T) {
	doc := minimalDoc(t)
	_, err := Render(doc, Options{
		ClientImport: "example.com/foo/range", // "range" is a Go keyword
	})
	if err == nil || !strings.Contains(err.Error(), "Go reserved word") {
		t.Fatalf("expected reserved-word rejection, got %v", err)
	}
}

func TestRender_RejectsClientAliasStdlibClash(t *testing.T) {
	doc := minimalDoc(t)
	for _, clash := range []string{"context", "json", "runtime"} {
		_, err := Render(doc, Options{
			ClientImport: "example.com/foo/" + clash,
		})
		if err == nil || !strings.Contains(err.Error(), "collides") {
			t.Errorf("alias %q should be rejected, got %v", clash, err)
		}
	}
}

func TestValidateClientAlias_Empty(t *testing.T) {
	if err := validateClientAlias(""); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("empty alias should be rejected, got %v", err)
	}
}

func TestRender_DetectsSchemaConstCollision(t *testing.T) {
	// Two ToolNames that safeIdent-mangle to the same identifier (replacing
	// any non-alnum char with '_') would emit duplicate const declarations.
	ops := []Operation{
		{ToolName: "get-pet", InputSchemaJSON: "{}"},
		{ToolName: "get_pet", InputSchemaJSON: "{}"},
	}
	if err := validateNoSchemaConstCollisions(ops); err == nil {
		t.Fatalf("expected collision error")
	}
}

func TestRender_AcceptsNonCollidingOps(t *testing.T) {
	ops := []Operation{
		{ToolName: "get-pet"},
		{ToolName: "list-pets"},
	}
	if err := validateNoSchemaConstCollisions(ops); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRender_CallArgsErrorPropagates(t *testing.T) {
	// A typed body with an invented BodyKind exercises the new error path
	// callArgs returns instead of the old panic.
	bad := Operation{
		HasRequestBody:  true,
		RequestBodyKind: "bogus",
		GoName:          "X",
	}
	_, err := callArgs(bad)
	if err == nil || !strings.Contains(err.Error(), "unhandled BodyKind") {
		t.Fatalf("expected unhandled-kind error, got %v", err)
	}
}

func TestRender_DeduplicatesPathParamGoVars(t *testing.T) {
	spec := []byte(`openapi: 3.0.0
info: { title: PathVars, version: "1" }
paths:
  /things/{foo-bar}/{foo_bar}:
    get:
      operationId: getThing
      parameters:
        - in: path
          name: foo-bar
          required: true
          schema: { type: string }
        - in: path
          name: foo_bar
          required: true
          schema: { type: string }
      responses:
        "200": { description: ok }
`)
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(spec)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate: %v", err)
	}
	src, err := Render(doc, Options{
		ClientImport: "example.com/foo/client",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Count(string(src), "var foo_bar string") != 1 {
		t.Errorf("expected one unsuffixed foo_bar variable; got\n%s", src)
	}
	if !strings.Contains(string(src), "var foo_bar_2 string") {
		t.Errorf("expected suffixed path variable for colliding param; got\n%s", src)
	}
}

// minimalDoc builds an in-memory OpenAPI doc with one trivial operation so
// Render has something to walk. Used by the alias-validation tests where
// the spec content is irrelevant — the alias check fires before operation
// collection.
func minimalDoc(t *testing.T) *openapi3.T {
	t.Helper()
	doc := &openapi3.T{
		OpenAPI: "3.0.0",
		Info:    &openapi3.Info{Title: "Coll", Version: "1"},
		Paths:   &openapi3.Paths{},
	}
	doc.Paths.Set("/x", &openapi3.PathItem{
		Get: &openapi3.Operation{
			OperationID: "getX",
			Responses:   newOKResponses(),
		},
	})
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate: %v", err)
	}
	return doc
}

// newOKResponses constructs a Responses object with a single 200 entry —
// the minimal shape kin-openapi's validator accepts since v0.138.
func newOKResponses() *openapi3.Responses {
	r := openapi3.NewResponses()
	desc := "OK"
	r.Set("200", &openapi3.ResponseRef{Value: &openapi3.Response{Description: &desc}})
	return r
}
