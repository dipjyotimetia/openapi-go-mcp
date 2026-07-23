// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package loader

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestLoad_OpenAPIv3(t *testing.T) {
	doc, err := Load(context.Background(), filepath.Join("..", "..", "testdata", "petstore-v3.yaml"))
	if err != nil {
		t.Fatalf("load v3: %v", err)
	}
	if doc.OpenAPI == "" {
		t.Fatalf("expected non-empty openapi version")
	}
	gotOps := 0
	for _, item := range doc.Paths.Map() {
		gotOps += len(item.Operations())
	}
	if gotOps == 0 {
		t.Fatalf("expected at least one operation in petstore v3 fixture")
	}
}

func TestLoad_Swagger2_AutoConverts(t *testing.T) {
	doc, err := Load(context.Background(), filepath.Join("..", "..", "testdata", "petstore-v2.json"))
	if err != nil {
		t.Fatalf("load v2: %v", err)
	}
	if doc.OpenAPI == "" || doc.OpenAPI[:1] != "3" {
		t.Fatalf("expected v3 document after conversion, got openapi=%q", doc.OpenAPI)
	}
}

func TestLoad_Swagger2_ResolvesExternalRefsRelativeToSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "defs.yaml"), []byte(`swagger: "2.0"
info: {title: defs, version: "1"}
definitions:
  Thing:
    type: object
    properties: {id: {type: string}}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	root := []byte(`swagger: "2.0"
info: {title: root, version: "1"}
paths:
  /things:
    get:
      responses:
        "200":
          description: ok
          schema: {$ref: './defs.yaml#/definitions/Thing'}
`)
	path := filepath.Join(dir, "root.yaml")
	if err := os.WriteFile(path, root, 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := Load(context.Background(), path)
	if err != nil {
		t.Fatalf("Load external Swagger ref: %v", err)
	}
	if doc.Paths.Find("/things").Get.Responses.Status(200).Value.Content["application/json"].Schema.Value == nil {
		t.Fatal("converted response schema was not resolved")
	}
}

// TestLoad_Swagger2_FormDataConverts pins the contract between this loader
// and kin-openapi/openapi2conv: a Swagger 2.0 formData parameter must surface
// as an `application/x-www-form-urlencoded` request body after conversion.
// The non-JSON request-body pipeline depends on this lowering — the previous
// JSON-only loader pruning was incidentally papering over any conversion
// drift here, so this test guards against future regressions in the upstream
// library.
func TestLoad_Swagger2_FormDataConverts(t *testing.T) {
	doc, err := Load(context.Background(), filepath.Join("..", "..", "testdata", "form-swagger-v2.json"))
	if err != nil {
		t.Fatalf("load v2 form fixture: %v", err)
	}
	op := doc.Paths.Find("/login").Post
	if op == nil || op.RequestBody == nil || op.RequestBody.Value == nil {
		t.Fatalf("expected POST /login to have a request body after v2→v3 conversion")
	}
	content := op.RequestBody.Value.Content
	if _, ok := content["application/x-www-form-urlencoded"]; !ok {
		t.Errorf("expected application/x-www-form-urlencoded content type, got %v", contentKeys(content))
	}
}

func contentKeys(c map[string]*openapi3.MediaType) []string {
	out := make([]string, 0, len(c))
	for k := range c {
		out = append(out, k)
	}
	return out
}

func TestPruneNonJSONContent_KeepsRequestBodiesPrunesResponses(t *testing.T) {
	mkContent := func() openapi3.Content {
		return openapi3.Content{
			"application/json": &openapi3.MediaType{},
			"application/xml":  &openapi3.MediaType{},
			"text/plain":       &openapi3.MediaType{},
		}
	}
	doc := &openapi3.T{
		Paths: openapi3.NewPaths(),
	}
	doc.Paths.Set("/things", &openapi3.PathItem{
		Post: &openapi3.Operation{
			RequestBody: &openapi3.RequestBodyRef{
				Value: &openapi3.RequestBody{Content: mkContent()},
			},
			Responses: openapi3.NewResponses(
				openapi3.WithStatus(200, &openapi3.ResponseRef{
					Value: &openapi3.Response{Content: mkContent()},
				}),
			),
		},
	})

	pruneNonJSONContent(doc)

	op := doc.Paths.Find("/things").Post

	if got := len(op.RequestBody.Value.Content); got != 3 {
		t.Errorf("request body content types pruned: got %d, want 3", got)
	}
	respContent := op.Responses.Status(200).Value.Content
	if _, ok := respContent["application/json"]; !ok {
		t.Error("response should still carry application/json after prune")
	}
	for _, ct := range []string{"application/xml", "text/plain"} {
		if _, ok := respContent[ct]; ok {
			t.Errorf("response should have been pruned of %q", ct)
		}
	}
}

func TestIsSwagger2(t *testing.T) {
	cases := map[string]bool{
		`{"swagger":"2.0","info":{}}`:     true,
		`swagger: "2.0"`:                  true,
		`openapi: 3.0.0`:                  false,
		`{"openapi":"3.1.0"}`:             false,
		`description: "swagger: 2.0 ..."`: false, // marker in description, not top level — should NOT match the start-anchored regex
	}
	for input, want := range cases {
		if got := isSwagger2([]byte(input)); got != want {
			t.Errorf("isSwagger2(%q) = %v, want %v", input, got, want)
		}
	}
}
