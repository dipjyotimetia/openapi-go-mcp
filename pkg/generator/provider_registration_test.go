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
	"path/filepath"
	"strings"
	"testing"

	"github.com/dipjyotimetia/openapi-go-mcp/pkg/loader"
)

func TestRender_DefaultMode_EmitsProviderRegistrationAndBothInputSchemas(t *testing.T) {
	doc, err := loader.Load(context.Background(), filepath.Join("..", "..", "testdata", "complex-schemas-v3.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	src, err := Render(doc, Options{
		PackageName:  "complexmcp",
		ClientImport: "example.com/complexclient",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	registerFunc := registerFuncName(doc)
	for _, want := range []string{
		"func " + registerFunc + "OpenAI(s runtime.MCPServer, c complexclient.ClientWithResponsesInterface, opts ...runtime.Option)",
		"func " + registerFunc + "WithProvider(s runtime.MCPServer, c complexclient.ClientWithResponsesInterface, provider runtime.LLMProvider, opts ...runtime.Option)",
		"runtime.LLMProviderStandard",
		"runtime.LLMProviderOpenAI",
		"const input_openai_",
		"RawInputSchema: inputSchemaForProvider(provider, input_",
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("generated source missing %q\n%s", want, prefix(src, 3000))
		}
	}
}

func TestRender_ProxyMode_EmitsProviderRegistration(t *testing.T) {
	doc, err := loader.Load(context.Background(), filepath.Join("..", "..", "testdata", "petstore-v3.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	src, err := Render(doc, Options{
		Mode:        ModeProxy,
		PackageName: "petstoremcp",
		ModulePath:  "example.com/petstoremcp",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	registerFunc := registerFuncName(doc)
	for _, want := range []string{
		"func " + registerFunc + "OpenAI(s runtime.MCPServer, opts ...runtime.Option)",
		"func " + registerFunc + "WithProvider(s runtime.MCPServer, provider runtime.LLMProvider, opts ...runtime.Option)",
		"RawInputSchema: inputSchemaForProvider(provider, input_",
		"const input_openai_",
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("generated proxy source missing %q\n%s", want, prefix(src, 3000))
		}
	}
}

func TestRender_OpenAICompat_PreservesLegacyRegistrationSurface(t *testing.T) {
	doc, err := loader.Load(context.Background(), filepath.Join("..", "..", "testdata", "petstore-v3.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	src, err := Render(doc, Options{
		PackageName:  "petstoremcp",
		ClientImport: "example.com/petstore",
		OpenAICompat: true,
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	registerFunc := registerFuncName(doc)
	for _, forbidden := range []string{registerFunc + "OpenAI", registerFunc + "WithProvider", "input_openai_"} {
		if strings.Contains(string(src), forbidden) {
			t.Errorf("legacy OpenAICompat output must not contain %q", forbidden)
		}
	}
	if strings.Contains(string(src), "with the standard\n// MCP-compatible input schema") {
		t.Error("legacy OpenAICompat output must retain the original Register comment")
	}
	if !strings.Contains(string(src), "Each tool delegates to the supplied oapi-codegen client.\nfunc "+registerFunc) {
		t.Error("legacy OpenAICompat output must retain the original Register comment spacing")
	}
}
