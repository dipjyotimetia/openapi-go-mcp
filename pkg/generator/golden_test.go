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
	"testing"

	"github.com/dipjyotimetia/openapi-go-mcp/pkg/loader"
)

// TestGolden_Petstore renders the petstore v3 fixture and compares the result
// against the checked-in golden file. Run with UPDATE_GOLDEN=1 to refresh.
func TestGolden_Petstore(t *testing.T) {
	specPath := filepath.Join("..", "..", "testdata", "petstore-v3.yaml")
	goldenPath := filepath.Join("..", "..", "testdata", "golden", "petstore.mcp.go.golden")

	doc, err := loader.Load(context.Background(), specPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	got, err := Render(doc, Options{
		PackageName:  "petstoremcp",
		ClientImport: "github.com/example/petstore",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("update golden: %v", err)
		}
		t.Logf("updated %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("generated output differs from golden.\nFirst-diff offset: %d\n--- got ---\n%s\n--- want ---\n%s",
			firstDiff(got, want), prefix(got, 800), prefix(want, 800))
	}
}

func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

func prefix(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
