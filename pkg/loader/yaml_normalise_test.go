// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package loader

import (
	"reflect"
	"testing"
)

// normaliseYAMLMaps converts the legacy gopkg.in/yaml.v3 map[any]any shape
// into the JSON-compatible map[string]any so a Swagger 2.0 document
// round-trips through openapi2conv. The function is recursive — these tests
// document the contract across the three shapes that matter.

func TestNormaliseYAMLMaps_ConvertsMapAnyAny(t *testing.T) {
	in := map[any]any{"key": "value"}
	got := normaliseYAMLMaps(in)
	want := map[string]any{"key": "value"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestNormaliseYAMLMaps_StringifiesNonStringKeys(t *testing.T) {
	// YAML allows non-string map keys (e.g. integers); JSON does not. The
	// helper coerces every key via fmt.Sprint so downstream marshalers don't
	// crash on `int` keys.
	in := map[any]any{42: "answer", true: "yes"}
	got, ok := normaliseYAMLMaps(in).(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", got)
	}
	if got["42"] != "answer" || got["true"] != "yes" {
		t.Errorf("non-string keys not stringified; got %#v", got)
	}
}

func TestNormaliseYAMLMaps_RecursesIntoNestedMaps(t *testing.T) {
	in := map[any]any{
		"outer": map[any]any{
			"inner": map[any]any{"deep": 1},
		},
	}
	got := normaliseYAMLMaps(in)
	out, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("top level: expected map[string]any, got %T", got)
	}
	outer, ok := out["outer"].(map[string]any)
	if !ok {
		t.Fatalf("first nested: expected map[string]any, got %T", out["outer"])
	}
	inner, ok := outer["inner"].(map[string]any)
	if !ok {
		t.Fatalf("second nested: expected map[string]any, got %T", outer["inner"])
	}
	if inner["deep"] != 1 {
		t.Errorf("deep value lost in conversion: %#v", inner)
	}
}

func TestNormaliseYAMLMaps_WalksSlices(t *testing.T) {
	// Arrays may contain map[any]any items — those items must be converted
	// too, otherwise downstream marshalling fails on the first nested map.
	in := []any{
		map[any]any{"a": 1},
		"plain",
		map[any]any{"b": 2},
	}
	got, ok := normaliseYAMLMaps(in).([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", got)
	}
	first, ok := got[0].(map[string]any)
	if !ok || first["a"] != 1 {
		t.Errorf("first slice element not converted: %#v", got[0])
	}
	last, ok := got[2].(map[string]any)
	if !ok || last["b"] != 2 {
		t.Errorf("last slice element not converted: %#v", got[2])
	}
	if got[1] != "plain" {
		t.Errorf("scalar slice element should pass through; got %#v", got[1])
	}
}

func TestNormaliseYAMLMaps_PassesThroughMapStringAny(t *testing.T) {
	// Already-normalised inputs should be left structurally intact — the
	// function still recurses into values to handle mixed-shape inputs, but
	// the map itself is returned in place.
	in := map[string]any{"k": map[any]any{"x": 1}}
	got, ok := normaliseYAMLMaps(in).(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", got)
	}
	nested, ok := got["k"].(map[string]any)
	if !ok || nested["x"] != 1 {
		t.Errorf("inner map[any]any not normalised: %#v", got["k"])
	}
}

func TestNormaliseYAMLMaps_ScalarsPassThrough(t *testing.T) {
	for _, v := range []any{nil, 1, 1.5, true, "hello", []byte("bytes")} {
		got := normaliseYAMLMaps(v)
		if !reflect.DeepEqual(got, v) {
			t.Errorf("scalar %v (%T) mutated to %v (%T)", v, v, got, got)
		}
	}
}
