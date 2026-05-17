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
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

// The -emit-v3 CLI flag wires through WriteV3YAMLJSONOnly. It's the only
// path the flag has — and previously it was exercised only by `make
// regen-examples`. These tests pin the contract: pruning happens on a
// clone, JSON content types survive, non-JSON response bodies are dropped,
// request bodies are preserved verbatim.

func TestWriteV3YAMLJSONOnly_RoundTrips(t *testing.T) {
	doc, err := Load(context.Background(), filepath.Join("..", "..", "testdata", "petstore-v3.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	out := filepath.Join(t.TempDir(), "petstore.v3.yaml")
	if err := WriteV3YAMLJSONOnly(doc, out); err != nil {
		t.Fatalf("WriteV3YAMLJSONOnly: %v", err)
	}
	// Reloading the emitted file should succeed and produce another valid v3
	// document with the same operation set — the operation walk is the
	// generator's primary input, so any silent drop here would be a bug.
	reloaded, err := Load(context.Background(), out)
	if err != nil {
		t.Fatalf("reload emitted v3: %v", err)
	}
	if countOps(reloaded) != countOps(doc) {
		t.Errorf("operation count drifted: original=%d, emitted=%d", countOps(doc), countOps(reloaded))
	}
}

func TestWriteV3YAMLJSONOnly_PrunesNonJSONResponses(t *testing.T) {
	const spec = `openapi: 3.0.0
info: {title: Mixed, version: "1"}
paths:
  /thing:
    get:
      operationId: getThing
      responses:
        "200":
          description: ok
          content:
            application/json: {schema: {type: object}}
            application/xml: {schema: {type: object}}
            text/csv: {schema: {type: string}}
`
	l := openapi3.NewLoader()
	doc, err := l.LoadFromData([]byte(spec))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	out := filepath.Join(t.TempDir(), "pruned.yaml")
	if err := WriteV3YAMLJSONOnly(doc, out); err != nil {
		t.Fatalf("WriteV3YAMLJSONOnly: %v", err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read emitted: %v", err)
	}
	if strings.Contains(string(body), "application/xml") || strings.Contains(string(body), "text/csv") {
		t.Errorf("non-JSON response content types should have been pruned:\n%s", body)
	}
	if !strings.Contains(string(body), "application/json") {
		t.Errorf("application/json response should have survived pruning:\n%s", body)
	}
}

func TestWriteV3YAMLJSONOnly_PreservesNonJSONRequestBodies(t *testing.T) {
	// Request bodies must NOT be pruned — oapi-codegen needs the original
	// content map to emit Formdata / Multipart / WithBody helpers. This is
	// the contract that lets the swagger2-petstore example survive after
	// emit-v3 → oapi-codegen.
	const spec = `openapi: 3.0.0
info: {title: ReqBody, version: "1"}
paths:
  /upload:
    post:
      operationId: upload
      requestBody:
        content:
          multipart/form-data:
            schema: {type: object, properties: {file: {type: string, format: binary}}}
          application/x-www-form-urlencoded:
            schema: {type: object}
      responses: {"200": {description: ok}}
`
	l := openapi3.NewLoader()
	doc, err := l.LoadFromData([]byte(spec))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	out := filepath.Join(t.TempDir(), "request-preserved.yaml")
	if err := WriteV3YAMLJSONOnly(doc, out); err != nil {
		t.Fatalf("WriteV3YAMLJSONOnly: %v", err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read emitted: %v", err)
	}
	if !strings.Contains(string(body), "multipart/form-data") {
		t.Errorf("multipart request body must survive pruning:\n%s", body)
	}
	if !strings.Contains(string(body), "application/x-www-form-urlencoded") {
		t.Errorf("form-urlencoded request body must survive pruning:\n%s", body)
	}
}

func TestWriteV3YAMLJSONOnly_DoesNotMutateInputDocument(t *testing.T) {
	// The contract is "pruning happens on a clone." If the input doc gets
	// mutated, downstream callers would see surprising side effects after
	// emit-v3. Verify by checking the response content map before and after.
	const spec = `openapi: 3.0.0
info: {title: Immutable, version: "1"}
paths:
  /a:
    get:
      operationId: a
      responses:
        "200":
          description: ok
          content:
            application/json: {schema: {type: object}}
            application/xml: {schema: {type: object}}
`
	l := openapi3.NewLoader()
	doc, err := l.LoadFromData([]byte(spec))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	out := filepath.Join(t.TempDir(), "clone.yaml")
	if err := WriteV3YAMLJSONOnly(doc, out); err != nil {
		t.Fatalf("WriteV3YAMLJSONOnly: %v", err)
	}
	// Original document still has BOTH content types — pruning happened on
	// a clone, not in-place.
	resp := doc.Paths.Map()["/a"].Get.Responses.Map()["200"].Value
	if _, ok := resp.Content["application/xml"]; !ok {
		t.Errorf("WriteV3YAMLJSONOnly mutated the input document; xml content was removed from original")
	}
}

func TestWriteV3YAMLJSONOnly_NilDocReturnsError(t *testing.T) {
	if err := WriteV3YAMLJSONOnly(nil, filepath.Join(t.TempDir(), "x.yaml")); err == nil {
		t.Errorf("expected error for nil document")
	}
}

func TestWriteV3YAMLJSONOnly_EmitsValidYAML(t *testing.T) {
	const spec = `openapi: 3.0.0
info: {title: V, version: "1"}
paths:
  /x:
    get: {operationId: x, responses: {"200": {description: ok}}}
`
	l := openapi3.NewLoader()
	doc, err := l.LoadFromData([]byte(spec))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	out := filepath.Join(t.TempDir(), "emit.yaml")
	if err := WriteV3YAMLJSONOnly(doc, out); err != nil {
		t.Fatalf("WriteV3YAMLJSONOnly: %v", err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read emitted: %v", err)
	}
	// Sanity-parse the YAML so a future change that produces malformed
	// output (e.g. drops the trailing newline, breaks indentation) is
	// caught directly, not only via downstream Load.
	var anyNode any
	if err := yaml.Unmarshal(body, &anyNode); err != nil {
		t.Fatalf("emitted file is not valid YAML: %v", err)
	}
}

func countOps(doc *openapi3.T) int {
	n := 0
	for _, item := range doc.Paths.Map() {
		n += len(item.Operations())
	}
	return n
}
