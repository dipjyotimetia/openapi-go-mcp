// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package generator

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/dipjyotimetia/openapi-go-mcp/pkg/loader"
)

// renderNonJSONFixture loads testdata/non-json-bodies-v3.yaml and renders the
// MCP wrapper for it. The fixture grows over the body-kind rollout, so any
// step that fails to render the current state of the file indicates a
// regression in a previously-enabled kind.
func renderNonJSONFixture(t *testing.T) string {
	t.Helper()
	doc, err := loader.Load(context.Background(),
		filepath.Join("..", "..", "testdata", "non-json-bodies-v3.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	src, err := Render(doc, Options{
		PackageName:  "nonjsonbodiesmcp",
		ClientImport: "github.com/example/nonjsonbodies",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return string(src)
}

func TestRender_BodyForm(t *testing.T) {
	src := renderNonJSONFixture(t)

	want := []string{
		// Typed form body, decoded via DecodeBody just like JSON.
		"var body nonjsonbodies.SubmitLoginFormdataRequestBody",
		"runtime.DecodeBody(req.Arguments, &body)",
		// Dispatches to the Formdata variant of the oapi-codegen client.
		"c.SubmitLoginWithFormdataBodyWithResponse(ctx, body)",
		// Input schema still presents `body` as the form-object shape.
		`"body":`,
		`"username":`,
	}
	for _, fragment := range want {
		if !strings.Contains(src, fragment) {
			t.Errorf("expected generated source to contain %q\n--- got ---\n%s", fragment, src)
		}
	}

	// Sanity: the JSON-only call shape must NOT leak in for a form op.
	if strings.Contains(src, "SubmitLoginJSONRequestBody") {
		t.Errorf("form op should not reference JSONRequestBody")
	}
}

func TestRender_BodyMultipart(t *testing.T) {
	src := renderNonJSONFixture(t)

	want := []string{
		// Handler builds the body via the multipart runtime helper and passes
		// the binary-field metadata list.
		`runtime.BuildMultipartBody(req.Arguments, []runtime.RequestFilePart{{Path: "/attachment"}})`,
		// Dispatches to the generic raw-body variant of the typed client.
		"c.UploadFileWithBodyWithResponse(ctx, contentType, body)",
		// Schema rewrite must replace format:binary with contentEncoding:base64.
		`"contentEncoding": "base64"`,
	}
	for _, fragment := range want {
		if !strings.Contains(src, fragment) {
			t.Errorf("expected generated source to contain %q\n--- got ---\n%s", fragment, src)
		}
	}

	// The binary field's original format:binary keyword must be gone.
	for _, attachmentBlock := range extractInputSchemas(src) {
		if !strings.Contains(attachmentBlock, "uploadFile") &&
			!strings.Contains(attachmentBlock, "attachment") {
			continue
		}
		if strings.Contains(attachmentBlock, `"format": "binary"`) {
			t.Errorf("multipart binary field still has format:binary\n%s", attachmentBlock)
		}
	}
}

func TestRender_BodyMultipart_EncodingContentType(t *testing.T) {
	src := renderNonJSONFixture(t)

	// The uploadAvatar op declares encoding.image.contentType: image/png; the
	// generator must propagate that into the RequestFilePart literal so the
	// runtime writes the right per-part header.
	want := `runtime.BuildMultipartBody(req.Arguments, []runtime.RequestFilePart{{Path: "/image", ContentType: "image/png"}})`
	if !strings.Contains(src, want) {
		t.Errorf("expected encoding-aware multipart literal\nwant: %s\n--- got ---\n%s", want, src)
	}
}

func TestRender_BodyMultipart_NestedBinary(t *testing.T) {
	src := renderNonJSONFixture(t)

	// createProfile declares user.avatar with format:binary. The walker must
	// produce a nested JSON-pointer path; the runtime takes care of extracting
	// the value from the surrounding object.
	want := `runtime.BuildMultipartBody(req.Arguments, []runtime.RequestFilePart{{Path: "/user/avatar"}})`
	if !strings.Contains(src, want) {
		t.Errorf("expected nested binary literal\nwant: %s\n--- got ---\n%s", want, src)
	}
	// The schema rewrite must reach the nested leaf — no `format: "binary"`
	// should survive under the createProfile input schema.
	for name, raw := range extractInputSchemas(src) {
		if !strings.Contains(name, "createProfile") {
			continue
		}
		if strings.Contains(raw, `"format": "binary"`) {
			t.Errorf("nested binary leaf still has format:binary\n%s", raw)
		}
	}
}

func TestRender_BodyOctet(t *testing.T) {
	src := renderNonJSONFixture(t)

	want := []string{
		"runtime.BuildBase64BytesBody(req.Arguments)",
		`c.UploadBlobWithBodyWithResponse(ctx, "application/octet-stream", body)`,
		`"contentEncoding": "base64"`,
	}
	for _, fragment := range want {
		if !strings.Contains(src, fragment) {
			t.Errorf("expected generated source to contain %q", fragment)
		}
	}
}

func TestRender_BodyText(t *testing.T) {
	src := renderNonJSONFixture(t)

	want := []string{
		"runtime.BuildStringBody(req.Arguments)",
		`c.PostNoteWithBodyWithResponse(ctx, "text/plain", body)`,
		`"request body (text/plain)"`,
	}
	for _, fragment := range want {
		if !strings.Contains(src, fragment) {
			t.Errorf("expected generated source to contain %q", fragment)
		}
	}
}

func TestRender_ResponseKinds(t *testing.T) {
	src := renderNonJSONFixture(t)
	// All response kinds now route through NewToolResultFromHTTP so the runtime
	// can preserve status code + curated headers. The spec-declared content
	// type is passed as the fallback so a server that omits Content-Type still
	// produces a deterministic result shape.
	cases := []struct {
		op   string
		want string
	}{
		{"downloadBlob", `"application/octet-stream"`},
		{"getLatestReport", `"text/plain"`},
		{"submitLogin", `"application/json"`},
		{"uploadBlob", `runtime.NewToolResultFromHTTP(`},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			if !strings.Contains(src, c.want) {
				t.Errorf("op %s: expected fragment %q in generated source", c.op, c.want)
			}
		})
	}
	// Every operation must funnel through NewToolResultFromHTTP rather than
	// the legacy NewToolResultJSON / NewToolResultBinary / NewToolResultText
	// helpers (which would drop StatusCode and Headers on the floor).
	for _, drop := range []string{
		"NewToolResultBinary(resp.Body",
		"NewToolResultText(string(resp.Body))",
		"NewToolResultJSON(resp.Body)",
	} {
		if strings.Contains(src, drop) {
			t.Errorf("generated source still uses legacy wrapper %q", drop)
		}
	}
}

func TestRender_BodyRaw_XML(t *testing.T) {
	src := renderNonJSONFixture(t)

	want := []string{
		"runtime.BuildStringBody(req.Arguments)",
		`c.ImportXMLWithBodyWithResponse(ctx, "application/xml", body)`,
		`"request body (application/xml)"`,
	}
	for _, fragment := range want {
		if !strings.Contains(src, fragment) {
			t.Errorf("expected generated source to contain %q", fragment)
		}
	}
}

func TestRender_PreferContentType_OverridesPriority(t *testing.T) {
	// Operation declares JSON + form + multipart. Default priority picks JSON.
	doc := `
openapi: 3.0.0
info: {title: prefer-ct, version: "0.1"}
paths:
  /thing:
    post:
      operationId: doThing
      requestBody:
        required: true
        content:
          application/json:
            schema: {type: object, properties: {name: {type: string}}}
          application/x-www-form-urlencoded:
            schema: {type: object, properties: {name: {type: string}}}
          multipart/form-data:
            schema:
              type: object
              properties:
                file: {type: string, format: binary}
      responses:
        "200": {description: OK}
`
	spec, err := openapi3.NewLoader().LoadFromData([]byte(doc))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := spec.Validate(context.Background()); err != nil {
		t.Fatalf("validate: %v", err)
	}

	// Default (no preference): JSON wins.
	defaultSrc, err := Render(spec, Options{
		PackageName:  "prefctmcp",
		ClientImport: "github.com/example/prefct",
	})
	if err != nil {
		t.Fatalf("render default: %v", err)
	}
	if !strings.Contains(string(defaultSrc), "DoThingJSONRequestBody") {
		t.Errorf("default render should use JSON typed body\n%s", defaultSrc)
	}

	// With preference: multipart wins.
	preferredSrc, err := Render(spec, Options{
		PackageName:       "prefctmcp",
		ClientImport:      "github.com/example/prefct",
		PreferContentType: "multipart/form-data",
	})
	if err != nil {
		t.Fatalf("render preferred: %v", err)
	}
	want := []string{
		"runtime.BuildMultipartBody",
		"DoThingWithBodyWithResponse(ctx, contentType, body)",
	}
	for _, s := range want {
		if !strings.Contains(string(preferredSrc), s) {
			t.Errorf("expected preferred render to contain %q\n%s", s, preferredSrc)
		}
	}
	if strings.Contains(string(preferredSrc), "DoThingJSONRequestBody") {
		t.Errorf("preferred render should NOT use JSON typed body")
	}
}

func TestRender_ContentTypeHeaderCollision_Warns(t *testing.T) {
	doc := `
openapi: 3.0.0
info: {title: ct-header, version: "0.1"}
paths:
  /upload:
    post:
      operationId: doUpload
      parameters:
        - in: header
          name: Content-Type
          schema: {type: string}
      requestBody:
        required: true
        content:
          multipart/form-data:
            schema:
              type: object
              properties:
                file: {type: string, format: binary}
      responses:
        "200": {description: OK}
`
	spec, err := openapi3.NewLoader().LoadFromData([]byte(doc))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := spec.Validate(context.Background()); err != nil {
		t.Fatalf("validate: %v", err)
	}

	var buf bytes.Buffer
	if _, err := Render(spec, Options{
		PackageName:  "ctmcp",
		ClientImport: "github.com/example/ct",
		Warnings:     &buf,
	}); err != nil {
		t.Fatalf("render: %v", err)
	}

	got := buf.String()
	for _, want := range []string{
		"POST /upload",
		"Content-Type header parameter",
		"multipart/form-data",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("warning missing %q\n--- got ---\n%s", want, got)
		}
	}
}

// TestRender_Swagger2_FormData confirms that a Swagger 2.0 spec with formData
// parameters survives openapi2conv's lowering to application/x-www-form-urlencoded
// and is rendered as a BodyForm operation by the generator. This is the
// regression cover the previous JSON-only loader pruning was incidentally
// providing.
func TestRender_Swagger2_FormData(t *testing.T) {
	doc, err := loader.Load(context.Background(),
		filepath.Join("..", "..", "testdata", "form-swagger-v2.json"))
	if err != nil {
		t.Fatalf("load swagger 2.0: %v", err)
	}
	src, err := Render(doc, Options{
		PackageName:  "formloginmcp",
		ClientImport: "github.com/example/formlogin",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	want := []string{
		"var body formlogin.SwaggerLoginFormdataRequestBody",
		"runtime.DecodeBody(req.Arguments, &body)",
		"c.SwaggerLoginWithFormdataBodyWithResponse(ctx, body)",
	}
	for _, fragment := range want {
		if !strings.Contains(string(src), fragment) {
			t.Errorf("expected generated source to contain %q\n--- got ---\n%s", fragment, src)
		}
	}
}
