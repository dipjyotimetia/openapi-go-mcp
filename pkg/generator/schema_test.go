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
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func parseSchemas(t *testing.T, doc string) *openapi3.T {
	t.Helper()
	spec, err := openapi3.NewLoader().LoadFromData([]byte(doc))
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	if err := spec.Validate(context.Background()); err != nil {
		t.Fatalf("validate spec: %v", err)
	}
	return spec
}

func TestConvert_Primitive(t *testing.T) {
	c := NewSchemaConverter(false)
	ref := &openapi3.SchemaRef{Value: openapi3.NewIntegerSchema().WithMin(0).WithMax(100)}
	got := c.Convert(ref)
	if got["type"] != "integer" {
		t.Errorf("type: got %v", got["type"])
	}
	if got["minimum"] != float64(0) || got["maximum"] != float64(100) {
		t.Errorf("bounds: got %+v", got)
	}
}

func TestConvert_Nullable(t *testing.T) {
	c := NewSchemaConverter(false)
	s := openapi3.NewStringSchema()
	s.Nullable = true
	got := c.Convert(&openapi3.SchemaRef{Value: s})
	types, ok := got["type"].([]any)
	if !ok {
		t.Fatalf("expected type array, got %T (%v)", got["type"], got["type"])
	}
	if len(types) != 2 || types[0] != "string" || types[1] != "null" {
		t.Errorf("got %v", types)
	}
}

func TestConvert_NullableEnumIncludesNull(t *testing.T) {
	s := openapi3.NewStringSchema()
	s.Nullable = true
	s.Enum = []any{"active"}
	got := NewSchemaConverter(false).Convert(&openapi3.SchemaRef{Value: s})
	enum := got["enum"].([]any)
	if len(enum) != 2 || enum[1] != nil {
		t.Fatalf("nullable enum = %#v, want [active <nil>]", enum)
	}
}

func TestConvert_OpenAICompat_AllOfMergesSiblingProperties(t *testing.T) {
	doc := parseSchemas(t, `openapi: 3.0.0
info: {title: t, version: "1"}
paths: {}
components:
  schemas:
    Child:
      allOf:
        - type: object
          required: [inherited]
          properties: {inherited: {type: string}}
      type: object
      required: [own]
      properties: {own: {type: string}}
`)
	got := NewSchemaConverter(true).Convert(doc.Components.Schemas["Child"])
	props := got["properties"].(map[string]any)
	if props["inherited"] == nil || props["own"] == nil {
		t.Fatalf("flattened allOf properties = %#v", props)
	}
}

func TestConvert_NamedRef(t *testing.T) {
	doc := `
openapi: 3.0.0
info: {title: t, version: "0.0"}
paths: {}
components:
  schemas:
    Pet:
      type: object
      required: [name]
      properties:
        name: {type: string}
`
	spec := parseSchemas(t, doc)
	pet := spec.Components.Schemas["Pet"]
	c := NewSchemaConverter(false)
	c.Bind(spec)
	got := c.Convert(pet)
	if got["$ref"] != "#/$defs/Pet" {
		t.Fatalf("expected $ref to Pet, got %+v", got)
	}
	if _, ok := c.Defs()["Pet"]; !ok {
		t.Fatalf("Pet definition missing from $defs")
	}
	def := c.Defs()["Pet"].(map[string]any)
	if def["type"] != "object" {
		t.Errorf("Pet type: got %v", def["type"])
	}
}

func TestConvert_Recursive(t *testing.T) {
	doc := `
openapi: 3.0.0
info: {title: t, version: "0.0"}
paths: {}
components:
  schemas:
    Tree:
      type: object
      properties:
        name: {type: string}
        children:
          type: array
          items: {$ref: "#/components/schemas/Tree"}
`
	spec := parseSchemas(t, doc)
	tree := spec.Components.Schemas["Tree"]
	c := NewSchemaConverter(false)
	c.Bind(spec)
	got := c.Convert(tree)
	if got["$ref"] != "#/$defs/Tree" {
		t.Fatalf("root: got %+v", got)
	}
	def := c.Defs()["Tree"].(map[string]any)
	children := def["properties"].(map[string]any)["children"].(map[string]any)
	items := children["items"].(map[string]any)
	if items["$ref"] != "#/$defs/Tree" {
		t.Errorf("recursive ref not preserved, got %+v", items)
	}
}

func TestConvert_OneOf(t *testing.T) {
	doc := `
openapi: 3.0.0
info: {title: t, version: "0.0"}
paths: {}
components:
  schemas:
    Shape:
      oneOf:
        - {type: object, properties: {kind: {type: string, enum: [circle]}, radius: {type: number}}}
        - {type: object, properties: {kind: {type: string, enum: [square]}, side: {type: number}}}
`
	spec := parseSchemas(t, doc)
	c := NewSchemaConverter(false)
	c.Bind(spec)
	if got := c.Convert(spec.Components.Schemas["Shape"]); got["$ref"] != "#/$defs/Shape" {
		t.Fatalf("root: got %+v", got)
	}
	def := c.Defs()["Shape"].(map[string]any)
	if _, ok := def["oneOf"].([]any); !ok {
		t.Fatalf("expected oneOf array, got %+v", def)
	}
}

func TestConvert_OpenAICompat_FlattensOneOf(t *testing.T) {
	doc := `
openapi: 3.0.0
info: {title: t, version: "0.0"}
paths: {}
components:
  schemas:
    Shape:
      oneOf:
        - {type: object, properties: {radius: {type: number}}}
        - {type: object, properties: {side: {type: number}}}
`
	spec := parseSchemas(t, doc)
	c := NewSchemaConverter(true)
	got := c.Convert(spec.Components.Schemas["Shape"])
	if _, hasOneOf := got["oneOf"]; hasOneOf {
		t.Errorf("OpenAI-compat should drop oneOf; got %+v", got)
	}
	// First branch's properties should be inlined.
	props, ok := got["properties"].(map[string]any)
	if !ok || props["radius"] == nil {
		t.Errorf("expected radius property to be inlined, got %+v", got)
	}
}

func TestConvert_Required(t *testing.T) {
	c := NewSchemaConverter(false)
	s := openapi3.NewObjectSchema()
	s.WithProperty("name", openapi3.NewStringSchema())
	s.Required = []string{"name"}
	got := c.Convert(&openapi3.SchemaRef{Value: s})
	req, ok := got["required"].([]any)
	if !ok || len(req) != 1 || req[0] != "name" {
		t.Errorf("required: got %+v", got["required"])
	}
}

func TestConvert_DiscriminatorHint(t *testing.T) {
	doc := `
openapi: 3.0.0
info: {title: t, version: "0.0"}
paths: {}
components:
  schemas:
    Cat:
      type: object
      properties:
        kind: {type: string}
        purr: {type: boolean}
    Dog:
      type: object
      properties:
        kind: {type: string}
        bark: {type: boolean}
    Pet:
      oneOf:
        - $ref: "#/components/schemas/Cat"
        - $ref: "#/components/schemas/Dog"
      discriminator:
        propertyName: kind
        mapping:
          cat: "#/components/schemas/Cat"
          dog: "#/components/schemas/Dog"
`
	spec := parseSchemas(t, doc)

	for _, mode := range []struct {
		name          string
		openAICompat  bool
		mustNotInvent []string
		mustHaveOneOf bool
	}{
		{"default", false, []string{"if", "then", "else"}, true},
		{"openai-compat", true, []string{"if", "then", "else"}, false},
	} {
		t.Run(mode.name, func(t *testing.T) {
			c := NewSchemaConverter(mode.openAICompat)
			c.Bind(spec)
			got := c.Convert(spec.Components.Schemas["Pet"])
			// Default mode hoists named components into $defs and returns a
			// $ref; openai-compat inlines, so the hint lives on `got` itself.
			petSchema := got
			if !mode.openAICompat {
				defs := c.Defs()
				inDef, ok := defs["Pet"].(map[string]any)
				if !ok {
					t.Fatalf("expected Pet in $defs, got %+v", defs)
				}
				petSchema = inDef
			}

			desc, _ := petSchema["description"].(string)
			for _, want := range []string{"Discriminator: kind", "Values: cat, dog"} {
				if !strings.Contains(desc, want) {
					t.Errorf("description missing %q\ngot %q", want, desc)
				}
			}
			for _, banned := range mode.mustNotInvent {
				if _, ok := petSchema[banned]; ok {
					t.Errorf("discriminator hint must not invent %q keyword", banned)
				}
			}
			if mode.mustHaveOneOf {
				if _, ok := petSchema["oneOf"]; !ok {
					t.Errorf("default mode should preserve oneOf alongside the hint")
				}
			}
		})
	}
}

func TestConvert_JSONMarshallable(t *testing.T) {
	doc := `
openapi: 3.0.0
info: {title: t, version: "0.0"}
paths: {}
components:
  schemas:
    Pet:
      type: object
      properties:
        id: {type: integer, format: int64}
        name: {type: string, minLength: 1}
        tags:
          type: array
          items: {type: string}
`
	spec := parseSchemas(t, doc)
	c := NewSchemaConverter(false)
	c.Bind(spec)
	c.Convert(spec.Components.Schemas["Pet"])
	if _, err := json.Marshal(c.Defs()); err != nil {
		t.Fatalf("$defs not JSON-marshallable: %v", err)
	}
}
