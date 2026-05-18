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

func TestRender_CookieParams_EndToEnd(t *testing.T) {
	doc, err := loader.Load(context.Background(), "../../testdata/cookie-params-v3.yaml")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	src, diags, err := RenderWithDiagnostics(doc, Options{
		ClientImport: "example.com/cookies/gen/cookies",
		Warnings:     io.Discard,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// 1. Cookie group must appear in the input schema.
	if !strings.Contains(string(src), `"cookie"`) {
		t.Errorf("generated source must include a cookie input group:\n%s", src)
	}
	// 2. CookieRequestEditor and DecodeCookieParam must be wired in.
	for _, want := range []string{
		`runtime.CookieValues{}`,
		`runtime.DecodeCookieParam(req.Arguments, "session"`,
		`runtime.DecodeCookieParam(req.Arguments, "csrf"`,
		`runtime.CookieRequestEditor(cookieValues)`,
		"cookieEditor", // passed as trailing arg
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("generated source missing %q", want)
		}
	}
	// 3. The dropped-cookie-param diagnostic must NOT fire — cookies are now
	// fully supported.
	for _, d := range diags {
		if d.Code == DiagDroppedCookieParam {
			t.Errorf("cookie diagnostic still fires after support landed: %+v", d)
		}
	}
}

func TestRender_NoCookiesNoCookieEditor(t *testing.T) {
	// Sanity: petstore has no cookies, so no cookieEditor declaration
	// should appear in its generated source.
	doc, err := loader.Load(context.Background(), "../../testdata/petstore-v3.yaml")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	src, err := Render(doc, Options{
		ClientImport: "example.com/pet/gen/pet",
		Warnings:     io.Discard,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(string(src), "cookieEditor") {
		t.Errorf("petstore has no cookies; cookieEditor must not be emitted")
	}
}

func TestRender_CompanionMode_AppliesRuntimeOptions(t *testing.T) {
	doc, err := loader.Load(context.Background(), "../../testdata/petstore-v3.yaml")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	src, err := Render(doc, Options{
		ClientImport: "example.com/pet/gen/pet",
		Warnings:     io.Discard,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"runtime.ApplyExtraPropertiesToContext(ctx, req.Arguments, cfg.ExtraProperties)",
		"context.WithTimeout(ctx, cfg.RequestTimeout)",
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("companion output missing %q", want)
		}
	}
}
