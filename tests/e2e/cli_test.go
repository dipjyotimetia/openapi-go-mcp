// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dipjyotimetia/openapi-go-mcp/pkg/loader"
)

// repoRoot resolves the module root by walking up from cwd until it finds a
// go.mod. Used by per-test helpers so tests run from any cwd.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("%v", err)
	}
	return root
}

// cliPath is the path to the CLI binary built once in TestMain.
var cliPath string

// findRepoRoot walks up from cwd until it finds a go.mod, returning the
// directory that contains it. Returns an error rather than aborting so both
// *testing.T-aware callers and TestMain (which can't t.Fatalf) can use it.
func findRepoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find go.mod from %q", wd)
		}
		dir = parent
	}
}

// TestMain builds the CLI binary into a package-wide temp directory before
// any test runs, so every test reuses the same binary instead of rebuilding.
// Cleanup runs before os.Exit because deferred functions do not — see
// gocritic's exitAfterDefer check.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "openapi-go-mcp-e2e-")
	if err != nil {
		panic("create temp dir: " + err.Error())
	}

	cliPath = filepath.Join(dir, "openapi-go-mcp")
	cmd := exec.Command("go", "build", "-o", cliPath, "./cmd/openapi-go-mcp")
	root, err := findRepoRoot()
	if err != nil {
		_ = os.RemoveAll(dir)
		panic(err)
	}
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(dir)
		panic("build CLI: " + err.Error() + "\n" + string(out))
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func runCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, cliPath, args...)
	cmd.Dir = repoRoot(t)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

func TestCLI_List_OpenAPIv3(t *testing.T) {
	stdout, stderr, err := runCLI(t, "-spec", "testdata/petstore-v3.yaml", "-list")
	if err != nil {
		t.Fatalf("CLI failed: %v\nstderr=%s", err, stderr)
	}
	for _, want := range []string{"findPets", "addPet", "deletePet", "findPetByID"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("-list output missing %q\nstdout=%s", want, stdout)
		}
	}
}

func TestCLI_List_Swagger2_AutoConverts(t *testing.T) {
	stdout, stderr, err := runCLI(t, "-spec", "testdata/petstore-v2.json", "-list")
	if err != nil {
		t.Fatalf("CLI failed: %v\nstderr=%s", err, stderr)
	}
	// Swagger 2.0 -> v3 conversion preserves operationIds; petstore-v2 uses
	// findPetById (lowercase 'd'), which the converter passes through.
	for _, want := range []string{"findPets", "addPet", "deletePet", "findPetById"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("-list output missing %q after v2 conversion\nstdout=%s", want, stdout)
		}
	}
}

func TestCLI_MissingSpec_ExitsNonZero(t *testing.T) {
	_, stderr, err := runCLI(t)
	if err == nil {
		t.Fatal("expected non-zero exit with no -spec")
	}
	if !strings.Contains(stderr, "-spec") {
		t.Errorf("stderr should mention missing -spec flag, got %q", stderr)
	}
}

func TestCLI_EmitV3_RoundTrip(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "petstore-v3.yaml")
	_, stderr, err := runCLI(t, "-spec", "testdata/petstore-v2.json", "-emit-v3", dst)
	if err != nil {
		t.Fatalf("CLI failed: %v\nstderr=%s", err, stderr)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("emitted file missing: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("emitted v3 file is empty")
	}

	// Re-load the emitted spec to confirm it round-trips through kin-openapi.
	doc, err := loader.Load(context.Background(), dst)
	if err != nil {
		t.Fatalf("reload emitted v3: %v", err)
	}
	if doc.OpenAPI == "" || !strings.HasPrefix(doc.OpenAPI, "3") {
		t.Errorf("emitted file is not OpenAPI 3: openapi=%q", doc.OpenAPI)
	}

	gotOps := 0
	for _, item := range doc.Paths.Map() {
		gotOps += len(item.Operations())
	}
	// The petstore v2 fixture has 4 operations (findPets, addPet, findPetById, deletePet).
	if gotOps != 4 {
		t.Errorf("emitted v3 has %d operations, want 4", gotOps)
	}

	// Pruning must have removed non-JSON content from every response. Request
	// bodies are intentionally NOT pruned so the generator can emit form /
	// multipart / raw handlers for them.
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			if op.Responses == nil {
				continue
			}
			for code, respRef := range op.Responses.Map() {
				if respRef.Value == nil {
					continue
				}
				for ct := range respRef.Value.Content {
					if !loader.IsJSONContentType(ct) {
						t.Errorf("%s %s [%s]: non-JSON content type %q survived response pruning",
							method, path, code, ct)
					}
				}
			}
		}
	}
}

func TestCLI_Generate_ProducesCompilingFile(t *testing.T) {
	outDir := t.TempDir()
	_, stderr, err := runCLI(t,
		"-spec", "testdata/petstore-v3.yaml",
		"-out", outDir,
		"-package", "petmcptest",
		"-client-import", "github.com/example/petstore",
	)
	if err != nil {
		t.Fatalf("CLI failed: %v\nstderr=%s", err, stderr)
	}

	files, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("read output dir: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("CLI produced no output files")
	}

	var path string
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".mcp.go") {
			path = filepath.Join(outDir, f.Name())
			break
		}
	}
	if path == "" {
		t.Fatalf("no *.mcp.go file in output: %v", files)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	// Sanity-check structural invariants that would break downstream compilation.
	for _, must := range []string{
		"package petmcptest",
		`"github.com/example/petstore"`,
		`"github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime"`,
		"RegisterSwaggerPetstoreClient",
	} {
		if !strings.Contains(string(content), must) {
			t.Errorf("generated file missing expected substring %q", must)
		}
	}
}
