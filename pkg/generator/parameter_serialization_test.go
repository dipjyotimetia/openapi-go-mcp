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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dipjyotimetia/openapi-go-mcp/pkg/loader"
)

func TestProxyParameterSerializationMetadataIsRendered(t *testing.T) {
	spec := []byte(`openapi: 3.0.0
info: {title: Parameter serialization, version: "1"}
paths:
  /things/{id}:
    get:
      operationId: getThing
      parameters:
        - {name: id, in: path, required: true, style: matrix, explode: true, schema: {type: object}}
        - {name: tags, in: query, style: pipeDelimited, explode: false, allowReserved: true, schema: {type: array, items: {type: string}}}
        - {name: filter, in: query, style: deepObject, explode: true, schema: {type: object}}
        - {name: X-Fields, in: header, schema: {type: object}}
        - {name: preferences, in: cookie, schema: {type: array, items: {type: string}}}
      responses: {"200": {description: ok}}
`)
	path := filepath.Join(t.TempDir(), "spec.yaml")
	if err := os.WriteFile(path, spec, 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := loader.Load(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	ops, _, err := CollectOperations(doc, Options{Mode: ModeProxy})
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("operations = %d, want 1", len(ops))
	}
	if got := ops[0].PathParams[0]; got.Style != "matrix" || !got.Explode {
		t.Errorf("path metadata = %+v", got)
	}
	if got := ops[0].QueryParams[0]; got.Name != "filter" || got.Style != "deepObject" || !got.Explode {
		t.Errorf("deepObject metadata = %+v", got)
	}
	if got := ops[0].QueryParams[1]; got.Name != "tags" || got.Style != "pipeDelimited" || got.Explode || !got.AllowReserved {
		t.Errorf("query metadata = %+v", got)
	}
	if got := ops[0].HeaderParams[0]; got.Style != "simple" || got.Explode {
		t.Errorf("header defaults = %+v", got)
	}
	if got := ops[0].CookieParams[0]; got.Style != "form" || !got.Explode {
		t.Errorf("cookie defaults = %+v", got)
	}

	got, err := Render(doc, Options{Mode: ModeProxy, PackageName: "thingmcp", ModulePath: "example.com/thing"})
	if err != nil {
		t.Fatal(err)
	}
	src := string(got)
	for _, want := range []string{
		`runtime.ProxyParamSpec{Name: "id", In: "path", Style: "matrix", Explode: true, AllowReserved: false}`,
		`runtime.ProxyParamSpec{Name: "tags", In: "query", Style: "pipeDelimited", Explode: false, AllowReserved: true}`,
		`runtime.ProxyParamSpec{Name: "filter", In: "query", Style: "deepObject", Explode: true, AllowReserved: false}`,
		`q = append(q, param.Query...)`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated proxy source missing %q", want)
		}
	}
}
