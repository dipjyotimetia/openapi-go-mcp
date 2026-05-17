// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const forceTestSpec = `openapi: 3.0.0
info: {title: Force, version: "1"}
paths:
  /a:
    get:
      operationId: getA
      responses: {"200": {description: ok}}
`

func TestGenerate_RefusesExistingFileWithoutForce(t *testing.T) {
	doc := mustLoad(t, forceTestSpec)
	dir := t.TempDir()
	opts := Options{
		OutDir:       dir,
		PackageName:  "forcepkg",
		ClientImport: "example.com/foo/forceclient",
	}

	// First write — succeeds and creates the file.
	if _, err := Generate(doc, opts); err != nil {
		t.Fatalf("first Generate: %v", err)
	}
	outFile := filepath.Join(dir, "forcepkg.mcp.go")
	if _, err := os.Stat(outFile); err != nil {
		t.Fatalf("expected output file %s to exist: %v", outFile, err)
	}

	// Second write — must refuse because the file already exists.
	_, err := Generate(doc, opts)
	if err == nil {
		t.Fatal("expected second Generate to fail, got nil")
	}
	if !strings.Contains(err.Error(), "-force") {
		t.Errorf("expected error to mention -force, got %q", err.Error())
	}
}

func TestGenerate_ForceOverwritesExistingFile(t *testing.T) {
	doc := mustLoad(t, forceTestSpec)
	dir := t.TempDir()
	opts := Options{
		OutDir:       dir,
		PackageName:  "forcepkg",
		ClientImport: "example.com/foo/forceclient",
	}

	if _, err := Generate(doc, opts); err != nil {
		t.Fatalf("first Generate: %v", err)
	}
	outFile := filepath.Join(dir, "forcepkg.mcp.go")

	// Tamper with the file so we can prove the second pass overwrote it.
	if err := os.WriteFile(outFile, []byte("// tampered\n"), 0o644); err != nil {
		t.Fatalf("tamper write: %v", err)
	}
	opts.Force = true
	if _, err := Generate(doc, opts); err != nil {
		t.Fatalf("second Generate with -force: %v", err)
	}

	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if strings.HasPrefix(string(got), "// tampered") {
		t.Errorf("expected -force to overwrite tampered file, but tampered prefix remains")
	}
	if !strings.Contains(string(got), "package forcepkg") {
		t.Errorf("expected regenerated file to declare package forcepkg, got prefix %q", firstLine(string(got)))
	}
}

func TestGenerate_RefusesDirectoryAtOutputPath(t *testing.T) {
	doc := mustLoad(t, forceTestSpec)
	dir := t.TempDir()
	// Plant a directory where the output file would land. This shouldn't be
	// silently clobbered even with -force, since removing a directory is
	// destructive in a way overwriting a file isn't.
	collision := filepath.Join(dir, "forcepkg.mcp.go")
	if err := os.Mkdir(collision, 0o755); err != nil {
		t.Fatalf("plant collision dir: %v", err)
	}

	opts := Options{
		OutDir:       dir,
		PackageName:  "forcepkg",
		ClientImport: "example.com/foo/forceclient",
	}
	_, err := Generate(doc, opts)
	if err == nil || !strings.Contains(err.Error(), "directory") {
		t.Errorf("expected directory-at-output error, got %v", err)
	}
}

func firstLine(s string) string {
	first, _, _ := strings.Cut(s, "\n")
	return first
}
