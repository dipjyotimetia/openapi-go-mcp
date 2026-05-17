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
	"encoding/json"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/dipjyotimetia/openapi-go-mcp/pkg/loader"
)

// TestRender_OpenAICompat runs the generator with OpenAICompat=true across
// every fixture that exercises a distinct schema surface, and verifies each
// emitted input schema satisfies OpenAI tool-schema rules:
//
//  1. no $ref anywhere — schemas must be self-contained
//  2. no oneOf/anyOf/allOf — composition is flattened
//  3. every object has additionalProperties: false
//
// The fixtures together cover:
//   - object schemas, refs, query/path params, JSON request body (petstore)
//   - form / multipart (with format:binary rewrite) / octet / text / xml
//     request bodies (non-json-bodies) — guards the multipart binary-rewrite
//     against accidentally producing structures incompatible with the compat
//     transform.
func TestRender_OpenAICompat(t *testing.T) {
	fixtures := []struct {
		name, spec, pkg string
	}{
		{"Petstore", "petstore-v3.yaml", "petstoremcp"},
		{"NonJSONBodies", "non-json-bodies-v3.yaml", "nonjsonbodiesmcp"},
	}
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			doc, err := loader.Load(context.Background(),
				filepath.Join("..", "..", "testdata", fx.spec))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			src, err := Render(doc, Options{
				PackageName:  fx.pkg,
				ClientImport: "github.com/example/" + fx.pkg,
				OpenAICompat: true,
			})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			schemas := extractInputSchemas(string(src))
			if len(schemas) == 0 {
				t.Fatal("no input_* schema constants found in generated source")
			}
			for name, raw := range schemas {
				t.Run(name, func(t *testing.T) {
					var parsed any
					if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
						t.Fatalf("unmarshal schema: %v\n%s", err, raw)
					}
					walkSchema(parsed, "", func(path string, m map[string]any) {
						if v, ok := m["$ref"]; ok {
							t.Errorf("%s: $ref leaked into OpenAI-compat schema: %v", path, v)
						}
						for _, kw := range []string{"oneOf", "anyOf", "allOf"} {
							if _, ok := m[kw]; ok {
								t.Errorf("%s: %s leaked into OpenAI-compat schema", path, kw)
							}
						}
						if isObjectSchema(m) {
							if v, ok := m["additionalProperties"]; !ok || v != false {
								t.Errorf("%s: object schema must have additionalProperties:false, got %v",
									path, m["additionalProperties"])
							}
						}
					})
				})
			}
		})
	}
}

func extractInputSchemas(src string) map[string]string {
	re := regexp.MustCompile("(?s)const (input_[a-zA-Z0-9_]+) = `([^`]+)`")
	out := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		out[m[1]] = strings.TrimSpace(m[2])
	}
	return out
}

// walkSchema visits every map node in a parsed JSON Schema, calling visit
// with a dotted path used by error messages to locate the failing node.
func walkSchema(v any, path string, visit func(path string, m map[string]any)) {
	switch x := v.(type) {
	case map[string]any:
		visit(path, x)
		for k, child := range x {
			walkSchema(child, path+"."+k, visit)
		}
	case []any:
		for i, child := range x {
			walkSchema(child, path+"["+strconv.Itoa(i)+"]", visit)
		}
	}
}

// isObjectSchema reports whether m is an object schema. It reuses the
// generator's typeIs helper, plus the OpenAPI convention that a schema with
// "properties" but no explicit "type" is also an object.
func isObjectSchema(m map[string]any) bool {
	if typeIs(m, "object") {
		return true
	}
	if _, hasProps := m["properties"]; hasProps && m["type"] == nil {
		return true
	}
	return false
}
