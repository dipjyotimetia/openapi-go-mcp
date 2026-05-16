// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package generator

import (
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// SchemaConverter converts kin-openapi schemas into JSON Schema (draft-07
// compatible, with $defs for recursive references). One converter should be
// instantiated per MCP tool; converted definitions accumulate in Defs() and
// must be mounted on the resulting root schema under the "$defs" key.
type SchemaConverter struct {
	// OpenAICompat narrows the output to the subset accepted by OpenAI tool
	// calls: $ref/oneOf/anyOf/allOf are flattened, additionalProperties=false
	// is forced on objects.
	OpenAICompat bool

	defs     map[string]any
	inFlight map[*openapi3.Schema]string
	// nameByPtr maps component-schema pointers to their component name, so
	// any reference to a named component (direct or via $ref) is hoisted into
	// $defs even if kin-openapi has already inlined the SchemaRef.
	nameByPtr map[*openapi3.Schema]string
}

// NewSchemaConverter returns a fresh converter.
func NewSchemaConverter(openAICompat bool) *SchemaConverter {
	return &SchemaConverter{
		OpenAICompat: openAICompat,
		defs:         map[string]any{},
		inFlight:     map[*openapi3.Schema]string{},
		nameByPtr:    map[*openapi3.Schema]string{},
	}
}

// Bind pre-registers the spec's component schemas by pointer so subsequent
// Convert calls promote them into $defs even when they are accessed inline
// (kin-openapi resolves $refs eagerly, so the same *openapi3.Schema may be
// reached either via Ref="..." or directly as the value of components.schemas).
func (c *SchemaConverter) Bind(spec *openapi3.T) {
	if spec == nil || spec.Components == nil {
		return
	}
	for name, ref := range spec.Components.Schemas {
		if ref != nil && ref.Value != nil {
			c.nameByPtr[ref.Value] = name
		}
	}
}

// NameByPtr returns the converter's component-name lookup map. Callers must
// not mutate the returned map; pass it to Adopt on another converter to reuse
// the work of Bind.
func (c *SchemaConverter) NameByPtr() map[*openapi3.Schema]string {
	return c.nameByPtr
}

// Adopt installs a pre-built name map (typically from another converter's
// NameByPtr) as this converter's component lookup, skipping the O(N) Bind walk.
// Useful when generating many operations from the same spec.
func (c *SchemaConverter) Adopt(nameByPtr map[*openapi3.Schema]string) {
	if nameByPtr != nil {
		c.nameByPtr = nameByPtr
	}
}

// Defs returns the accumulated named definitions.
func (c *SchemaConverter) Defs() map[string]any { return c.defs }

// Convert returns the JSON Schema representation of ref.
func (c *SchemaConverter) Convert(ref *openapi3.SchemaRef) map[string]any {
	if ref == nil {
		return map[string]any{}
	}

	if !c.OpenAICompat {
		// Resolve the canonical name for this schema. Prefer the explicit
		// $ref string; otherwise consult the pointer registry populated by
		// Bind.
		name := refName(ref.Ref)
		if name == "" {
			name = c.nameByPtr[ref.Value]
		}
		if name != "" {
			if _, alreadyDone := c.defs[name]; alreadyDone {
				return map[string]any{"$ref": "#/$defs/" + name}
			}
			if _, busy := c.inFlight[ref.Value]; busy {
				return map[string]any{"$ref": "#/$defs/" + name}
			}
			c.inFlight[ref.Value] = name
			c.defs[name] = c.convertSchema(ref.Value)
			delete(c.inFlight, ref.Value)
			return map[string]any{"$ref": "#/$defs/" + name}
		}
	}

	return c.convertSchema(ref.Value)
}

func (c *SchemaConverter) convertSchema(s *openapi3.Schema) map[string]any {
	if s == nil {
		return map[string]any{}
	}
	out := map[string]any{}

	c.copyTypeAndFormat(s, out)
	c.copyStringConstraints(s, out)
	c.copyNumericConstraints(s, out)
	c.copyArrayConstraints(s, out)
	c.copyObjectConstraints(s, out)
	c.copyDocFields(s, out)
	c.copyComposition(s, out)

	if s.Items != nil {
		out["items"] = c.Convert(s.Items)
	}

	if len(s.Properties) > 0 {
		props := make(map[string]any, len(s.Properties))
		for name, sub := range s.Properties {
			props[name] = c.Convert(sub)
		}
		out["properties"] = props
	}

	if len(s.Required) > 0 {
		req := make([]any, len(s.Required))
		for i, r := range s.Required {
			req[i] = r
		}
		out["required"] = req
	}

	if s.AdditionalProperties.Has != nil {
		out["additionalProperties"] = *s.AdditionalProperties.Has
	} else if s.AdditionalProperties.Schema != nil {
		out["additionalProperties"] = c.Convert(s.AdditionalProperties.Schema)
	} else if c.OpenAICompat && typeIs(out, "object") {
		out["additionalProperties"] = false
	}

	return out
}

func (c *SchemaConverter) copyTypeAndFormat(s *openapi3.Schema, out map[string]any) {
	types := normaliseTypes(s.Type)
	if len(types) == 0 {
		// OpenAPI specs commonly omit `type: object` / `type: array` when other
		// keywords imply it. JSON Schema validators differ on whether they
		// infer — be explicit so the MCP client sees a complete schema.
		switch {
		case len(s.Properties) > 0 || s.AdditionalProperties.Has != nil || s.AdditionalProperties.Schema != nil || s.MinProps > 0 || s.MaxProps != nil:
			types = []string{"object"}
		case s.Items != nil || s.MinItems > 0 || s.MaxItems != nil || s.UniqueItems:
			types = []string{"array"}
		}
	}
	if s.Nullable {
		types = appendUnique(types, "null")
	}
	switch len(types) {
	case 0:
		// leave unset
	case 1:
		out["type"] = types[0]
	default:
		ts := make([]any, len(types))
		for i, t := range types {
			ts[i] = t
		}
		out["type"] = ts
	}
	if s.Format != "" {
		out["format"] = s.Format
	}
}

func (c *SchemaConverter) copyStringConstraints(s *openapi3.Schema, out map[string]any) {
	if s.MinLength > 0 {
		out["minLength"] = s.MinLength
	}
	if s.MaxLength != nil {
		out["maxLength"] = *s.MaxLength
	}
	if s.Pattern != "" {
		out["pattern"] = s.Pattern
	}
	if len(s.Enum) > 0 {
		out["enum"] = append([]any(nil), s.Enum...)
	}
}

func (c *SchemaConverter) copyNumericConstraints(s *openapi3.Schema, out map[string]any) {
	if s.Min != nil {
		if s.ExclusiveMin.IsTrue() {
			out["exclusiveMinimum"] = *s.Min
		} else {
			out["minimum"] = *s.Min
			if s.ExclusiveMin.Value != nil {
				out["exclusiveMinimum"] = *s.ExclusiveMin.Value
			}
		}
	} else if s.ExclusiveMin.Value != nil {
		out["exclusiveMinimum"] = *s.ExclusiveMin.Value
	}
	if s.Max != nil {
		if s.ExclusiveMax.IsTrue() {
			out["exclusiveMaximum"] = *s.Max
		} else {
			out["maximum"] = *s.Max
			if s.ExclusiveMax.Value != nil {
				out["exclusiveMaximum"] = *s.ExclusiveMax.Value
			}
		}
	} else if s.ExclusiveMax.Value != nil {
		out["exclusiveMaximum"] = *s.ExclusiveMax.Value
	}
	if s.MultipleOf != nil {
		out["multipleOf"] = *s.MultipleOf
	}
}

func (c *SchemaConverter) copyArrayConstraints(s *openapi3.Schema, out map[string]any) {
	if s.MinItems > 0 {
		out["minItems"] = s.MinItems
	}
	if s.MaxItems != nil {
		out["maxItems"] = *s.MaxItems
	}
	if s.UniqueItems {
		out["uniqueItems"] = true
	}
}

func (c *SchemaConverter) copyObjectConstraints(s *openapi3.Schema, out map[string]any) {
	if s.MinProps > 0 {
		out["minProperties"] = s.MinProps
	}
	if s.MaxProps != nil {
		out["maxProperties"] = *s.MaxProps
	}
}

func (c *SchemaConverter) copyDocFields(s *openapi3.Schema, out map[string]any) {
	if s.Title != "" {
		out["title"] = s.Title
	}
	if s.Description != "" {
		out["description"] = s.Description
	}
	if s.Default != nil {
		out["default"] = s.Default
	}
	if s.Example != nil {
		out["examples"] = []any{s.Example}
	}
	if s.Deprecated {
		out["deprecated"] = true
	}
	if s.ReadOnly {
		out["readOnly"] = true
	}
	if s.WriteOnly {
		out["writeOnly"] = true
	}
}

func (c *SchemaConverter) copyComposition(s *openapi3.Schema, out map[string]any) {
	if c.OpenAICompat {
		// Flatten: take the first branch of oneOf/anyOf if present and fold its
		// fields into out. allOf entries are merged shallowly.
		switch {
		case len(s.AnyOf) > 0:
			c.mergeInto(out, c.Convert(s.AnyOf[0]))
		case len(s.OneOf) > 0:
			c.mergeInto(out, c.Convert(s.OneOf[0]))
		}
		for _, branch := range s.AllOf {
			c.mergeInto(out, c.Convert(branch))
		}
		appendDiscriminatorHint(s, out)
		return
	}
	if len(s.OneOf) > 0 {
		out["oneOf"] = c.convertList(s.OneOf)
	}
	if len(s.AnyOf) > 0 {
		out["anyOf"] = c.convertList(s.AnyOf)
	}
	if len(s.AllOf) > 0 {
		out["allOf"] = c.convertList(s.AllOf)
	}
	if s.Not != nil {
		out["not"] = c.Convert(s.Not)
	}
	appendDiscriminatorHint(s, out)
}

// appendDiscriminatorHint surfaces OpenAPI's `discriminator` semantics (which
// JSON Schema has no native equivalent for) as plain text in the schema's
// description. The hint names the discriminator property and, when a mapping
// table is present, lists the legal values in sorted order so callers can
// pick the right branch even without seeing the original spec.
func appendDiscriminatorHint(s *openapi3.Schema, out map[string]any) {
	if s.Discriminator == nil || s.Discriminator.PropertyName == "" {
		return
	}
	parts := []string{"Discriminator: " + s.Discriminator.PropertyName}
	if len(s.Discriminator.Mapping) > 0 {
		keys := make([]string, 0, len(s.Discriminator.Mapping))
		for k := range s.Discriminator.Mapping {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts = append(parts, "Values: "+strings.Join(keys, ", "))
	}
	hint := strings.Join(parts, ". ") + "."
	if existing, ok := out["description"].(string); ok && existing != "" {
		out["description"] = existing + "\n\n" + hint
	} else {
		out["description"] = hint
	}
}

func (c *SchemaConverter) convertList(refs openapi3.SchemaRefs) []any {
	out := make([]any, len(refs))
	for i, r := range refs {
		out[i] = c.Convert(r)
	}
	return out
}

func (c *SchemaConverter) mergeInto(dst, src map[string]any) {
	for k, v := range src {
		if _, exists := dst[k]; exists && k == "properties" {
			if dstProps, ok1 := dst["properties"].(map[string]any); ok1 {
				if srcProps, ok2 := v.(map[string]any); ok2 {
					for pn, pv := range srcProps {
						dstProps[pn] = pv
					}
					continue
				}
			}
		}
		if _, exists := dst[k]; !exists {
			dst[k] = v
		}
	}
}

func refName(ref string) string {
	const prefix = "#/components/schemas/"
	if strings.HasPrefix(ref, prefix) {
		return ref[len(prefix):]
	}
	// Generic fallback: last path segment.
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}
	return ""
}

func normaliseTypes(t *openapi3.Types) []string {
	if t == nil {
		return nil
	}
	// *openapi3.Types is a []string under the hood.
	out := make([]string, 0, len(*t))
	for _, v := range *t {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func appendUnique(ts []string, v string) []string {
	for _, t := range ts {
		if t == v {
			return ts
		}
	}
	return append(ts, v)
}

func typeIs(out map[string]any, want string) bool {
	t, ok := out["type"]
	if !ok {
		return false
	}
	if s, ok := t.(string); ok {
		return s == want
	}
	if arr, ok := t.([]any); ok {
		for _, v := range arr {
			if s, ok := v.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}
