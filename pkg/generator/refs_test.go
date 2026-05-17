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
	"io"
	"strings"
	"testing"

	"github.com/dipjyotimetia/openapi-go-mcp/pkg/loader"
)

// TestRender_RefsEverywhere proves that kin-openapi resolves $ref through
// requestBodies, responses, parameters, and schema components so the
// generated tool exposes the right shape regardless of how the spec authors
// chose to factor their definitions.
func TestRender_RefsEverywhere(t *testing.T) {
	doc, err := loader.Load(context.Background(), "../../testdata/refs-everywhere-v3.yaml")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	src, err := Render(doc, Options{
		ClientImport: "example.com/widgets/gen/widgets",
		Warnings:     io.Discard,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := []string{
		// Header param resolved through #/components/parameters
		`"X-Request-ID"`,
		// requestBody resolved + schema $ref expanded into $defs
		`"#/$defs/Widget"`,
		// "createWidget" tool name preserved
		`createWidget`,
	}
	for _, w := range want {
		if !strings.Contains(string(src), w) {
			t.Errorf("missing fragment %q in generated source", w)
		}
	}
}
