// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package runtime

import "testing"

func TestExtractExtraProperty_PresentString(t *testing.T) {
	args := map[string]any{"tenant": "acme", "body": map[string]any{"x": 1}}
	got, ok := ExtractExtraProperty(args, "tenant")
	if !ok || got != "acme" {
		t.Errorf("ExtractExtraProperty = (%q, %v), want (\"acme\", true)", got, ok)
	}
	// Crucial side-effect: extras are stripped from args so downstream body
	// decoders don't see them and fail with "unknown field".
	if _, still := args["tenant"]; still {
		t.Errorf("extra property must be removed from args; map=%+v", args)
	}
	if _, gone := args["body"]; !gone {
		t.Errorf("unrelated args must remain untouched")
	}
}

func TestExtractExtraProperty_Absent(t *testing.T) {
	args := map[string]any{}
	got, ok := ExtractExtraProperty(args, "tenant")
	if ok || got != "" {
		t.Errorf("absent key should return (\"\", false); got (%q, %v)", got, ok)
	}
}

func TestExtractExtraProperty_NonStringValueRemovedButReturnedEmpty(t *testing.T) {
	// The current contract returns "" for non-string values, even though it
	// still deletes the key. Pinning the behaviour so a refactor that
	// returned the raw value would surface as a test failure (the public
	// contract is "string or absent").
	args := map[string]any{"limit": 42}
	got, ok := ExtractExtraProperty(args, "limit")
	if ok || got != "" {
		t.Errorf("non-string value should return (\"\", false); got (%q, %v)", got, ok)
	}
	if _, still := args["limit"]; still {
		t.Errorf("key should be deleted even when value type was wrong; map=%+v", args)
	}
}

func TestNewToolResultText_Basic(t *testing.T) {
	got := NewToolResultText("hello")
	if got == nil {
		t.Fatal("NewToolResultText returned nil")
	}
	if got.Text != "hello" {
		t.Errorf("Text = %q, want \"hello\"", got.Text)
	}
	if got.IsError {
		t.Errorf("text result should not be IsError")
	}
	if got.StructuredContent != nil {
		t.Errorf("text result should leave StructuredContent unset; got %v", got.StructuredContent)
	}
}

func TestNewToolResultText_EmptyAllowed(t *testing.T) {
	// Empty text is a valid result — used by 204-style operations that fall
	// through to NewToolResultText("").
	got := NewToolResultText("")
	if got == nil || got.Text != "" || got.IsError {
		t.Errorf("empty text result malformed: %+v", got)
	}
}
