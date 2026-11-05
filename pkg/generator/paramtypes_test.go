// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package generator

import (
	"io"
	"strings"
	"testing"
)

// pathParamTypesSpec covers every path-parameter shape for which oapi-codegen
// v2 emits something other than a builtin. Expected types were captured from
// oapi-codegen v2.7.0 output for this exact spec (issue #32).
const pathParamTypesSpec = `
openapi: "3.0.2"
info: {title: probe, version: "1"}
paths:
  /status/{status}:
    post:
      operationId: setTicketStatus
      parameters:
        - {name: status, in: path, required: true, schema: {type: string, enum: [Apple, Banana]}}
      responses: {"204": {description: ok}}
  /b/{petStatus}:
    get:
      operationId: get_by_ref
      parameters:
        - {name: petStatus, in: path, required: true, schema: {$ref: '#/components/schemas/pet_status'}}
      responses: {"204": {description: ok}}
  /c/{id}:
    get:
      operationId: byPrimRef
      parameters:
        - {name: id, in: path, required: true, schema: {$ref: '#/components/schemas/PetId'}}
      responses: {"204": {description: ok}}
  /d/{kind}:
    get:
      operationId: byParamRef
      parameters:
        - $ref: '#/components/parameters/KindParam'
      responses: {"204": {description: ok}}
  /e/{level}/{ticket-kind}:
    get:
      operationId: intEnum
      parameters:
        - {name: level, in: path, required: true, schema: {type: integer, enum: [1, 2]}}
        - {name: ticket-kind, in: path, required: true, schema: {type: string, enum: [x, y]}}
      responses: {"204": {description: ok}}
  /f/{custom}/{named}/{local}/{u}:
    get:
      operationId: goTypes
      parameters:
        - {name: custom, in: path, required: true, schema: {type: string, enum: [p, q], x-go-type: string}}
        - {name: named, in: path, required: true, schema: {type: string, enum: [p, q], x-go-type-name: MyNamed}}
        - {name: local, in: path, required: true, schema: {type: string, x-go-type: LocalType}}
        - {name: u, in: path, required: true, schema: {type: string, x-go-type: googleuuid.UUID, x-go-type-import: {path: github.com/google/uuid, name: googleuuid}}}
      responses: {"204": {description: ok}}
  /g/{ids}/{tags}/{refs}:
    get:
      operationId: arrays
      parameters:
        - {name: ids, in: path, required: true, schema: {type: array, items: {type: integer, format: int64}}}
        - {name: tags, in: path, required: true, schema: {type: array, items: {type: string, enum: [a, b]}}}
        - {name: refs, in: path, required: true, schema: {type: array, items: {$ref: '#/components/schemas/pet_status'}}}
      responses: {"204": {description: ok}}
components:
  parameters:
    KindParam:
      name: kind
      in: path
      required: true
      schema: {type: string, enum: [k1, k2]}
  schemas:
    pet_status: {type: string, enum: [available, sold]}
    PetId: {type: integer, format: int64}
`

func TestRender_PathParamTypesMatchOapiCodegen(t *testing.T) {
	doc := mustLoad(t, pathParamTypesSpec)
	src, err := Render(doc, Options{
		ClientImport: "example.com/probe/api",
		Warnings:     io.Discard,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := []string{
		`var status api.SetTicketStatusParamsStatus`,
		`var petStatus api.PetStatus`,
		`var id int64`,
		`var kind api.ByParamRefParamsKind`,
		`var level api.IntEnumParamsLevel`,
		`var ticket_kind api.IntEnumParamsTicketKind`,
		`var custom string`,
		`var named api.MyNamed`,
		`var local api.LocalType`,
		`var u googleuuid.UUID`,
		`googleuuid "github.com/google/uuid"`,
		`var ids []int64`,
		`var tags []api.ArraysParamsTags`,
		`var refs []api.PetStatus`,
	}
	for _, w := range want {
		if !strings.Contains(string(src), w) {
			t.Errorf("missing %q in generated source", w)
		}
	}
}

func TestRender_PathParamFormatsShadowingAndBodies(t *testing.T) {
	doc := mustLoad(t, `
openapi: "3.0.3"
info: {title: probe, version: "1"}
paths:
  /f/{i8}/{u64}/{by}/{bin}:
    get:
      operationId: formats
      parameters:
        - {name: i8, in: path, required: true, schema: {type: integer, format: int8}}
        - {name: u64, in: path, required: true, schema: {type: integer, format: uint64}}
        - {name: by, in: path, required: true, schema: {type: string, format: byte}}
        - {name: bin, in: path, required: true, schema: {type: string, format: binary}}
      responses: {"204": {description: ok}}
  /s/{resp}/{api}/{json}:
    get:
      operationId: shadow
      parameters:
        - {name: resp, in: path, required: true, schema: {type: string}}
        - {name: api, in: path, required: true, schema: {type: string}}
        - {name: json, in: path, required: true, schema: {type: string}}
      responses: {"204": {description: ok}}
  /t/{tenant}/{id}:
    parameters:
      - {name: tenant, in: path, required: true, schema: {type: string, enum: [a, b]}}
    get:
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}}
      responses: {"204": {description: ok}}
  /m:
    patch:
      operationId: mergePatch
      requestBody: {content: {application/merge-patch+json: {schema: {type: object}}}}
      responses: {"204": {description: ok}}
  /h:
    post:
      operationId: hal
      requestBody: {content: {application/hal+json: {schema: {type: object}}, application/json: {schema: {type: object}}}}
      responses: {"204": {description: ok}}
`)
	src, err := Render(doc, Options{ClientImport: "example.com/probe/api", Warnings: io.Discard})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := []string{
		`var i8 int8`,
		`var u64 uint64`,
		`var by []byte`,
		`var bin openapi_types.File`,
		// Locals must not shadow resp/err, the client alias, or imports.
		`var resp_ string`,
		`var api_ string`,
		`var json_ string`,
		`c.ShadowWithResponse(ctx, resp_, api_, json_)`,
		// PathItem-level inline enum stays primitive; no operationId -> lower-case method.
		`var tenant string`,
		`c.GetTTenantIdWithResponse(ctx, tenant, id)`,
		// Vendor JSON uses the content-type-specific method and body type.
		`var body api.MergePatchApplicationMergePatchPlusJSONRequestBody`,
		`c.MergePatchWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, body)`,
		// Exact application/json wins over +json variants.
		`var body api.HalJSONRequestBody`,
		`c.HalWithResponse(ctx, body)`,
	}
	for _, w := range want {
		if !strings.Contains(string(src), w) {
			t.Errorf("missing %q in generated source", w)
		}
	}
}

func TestJSONBodyTag_MatchesOapiCodegen(t *testing.T) {
	// Expected values captured from oapi-codegen v2.7.0 method names.
	cases := map[string]string{
		"application/json":               "JSON",
		"application/vnd.api+json":       "ApplicationVndAPIPlusJSON",
		"application/merge-patch+json":   "ApplicationMergePatchPlusJSON",
		"application/hal+json":           "ApplicationHalPlusJSON",
		"application/problem+json":       "ApplicationProblemPlusJSON",
		"application/vnd.github.v3+json": "ApplicationVndGithubV3PlusJSON",
	}
	for ct, want := range cases {
		if got := jsonBodyTag(ct); got != want {
			t.Errorf("jsonBodyTag(%q) = %q, want %q", ct, got, want)
		}
	}
}
