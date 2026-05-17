// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package e2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// minimalSpec returns a tiny valid OpenAPI 3 document with one operation
// named opID. Kept inline so each batch test can plant unique fixtures
// without growing testdata/ — the test is about CLI orchestration, not
// schema fidelity.
func minimalSpec(opID string) string {
	return `openapi: 3.0.0
info:
  title: ` + opID + `
  version: "1.0"
paths:
  /thing:
    get:
      operationId: ` + opID + `
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
`
}

// exitCode extracts the process exit code from an exec error. Returns -1
// for non-ExitError failures (e.g. the binary couldn't start at all) so
// tests can distinguish "ran and failed" from "didn't run".
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return ee.ExitCode()
	}
	return -1
}

func TestCLI_Batch_Directory(t *testing.T) {
	// Plant two specs in a tempdir and point -spec at the dir. Both must
	// produce their own subdirectory under -out with the expected
	// <slug>mcp/<slug>mcp.mcp.go layout.
	specDir := t.TempDir()
	outDir := t.TempDir()
	mustWriteSpec(t, filepath.Join(specDir, "billing.yaml"), minimalSpec("getBilling"))
	mustWriteSpec(t, filepath.Join(specDir, "users.yaml"), minimalSpec("getUsers"))

	_, stderr, err := runCLI(t,
		"-spec", specDir,
		"-out", outDir,
		"-client-import", "github.com/example/gen",
	)
	if err != nil {
		t.Fatalf("CLI failed: %v\nstderr=%s", err, stderr)
	}

	for _, slug := range []string{"billing", "users"} {
		want := filepath.Join(outDir, slug+"mcp", slug+"mcp.mcp.go")
		body, statErr := os.ReadFile(want)
		if statErr != nil {
			t.Fatalf("expected %s to exist: %v", want, statErr)
		}
		// Verify the derived per-spec values were threaded through to the
		// generated source — confirms PlanFor and the orchestrator agree.
		if !strings.Contains(string(body), "package "+slug+"mcp") {
			t.Errorf("%s missing package declaration", want)
		}
		if !strings.Contains(string(body), `"github.com/example/gen/`+slug+`"`) {
			t.Errorf("%s missing per-spec client import", want)
		}
	}
}

func TestCLI_Batch_Glob(t *testing.T) {
	// Glob patterns are passed through to filepath.Glob. The matched
	// fixtures land in subdirectories named after each slug.
	specDir := t.TempDir()
	outDir := t.TempDir()
	mustWriteSpec(t, filepath.Join(specDir, "alpha.yaml"), minimalSpec("alphaOp"))
	mustWriteSpec(t, filepath.Join(specDir, "beta.yaml"), minimalSpec("betaOp"))
	// Plant an unrelated file too; the *.yaml glob must not match it.
	if err := os.WriteFile(filepath.Join(specDir, "notes.md"), []byte("# notes"), 0o644); err != nil {
		t.Fatalf("plant notes.md: %v", err)
	}

	_, stderr, err := runCLI(t,
		"-spec", filepath.Join(specDir, "*.yaml"),
		"-out", outDir,
		"-client-import", "example.com/g",
	)
	if err != nil {
		t.Fatalf("CLI failed: %v\nstderr=%s", err, stderr)
	}

	for _, slug := range []string{"alpha", "beta"} {
		want := filepath.Join(outDir, slug+"mcp", slug+"mcp.mcp.go")
		if _, statErr := os.Stat(want); statErr != nil {
			t.Errorf("expected %s to exist: %v", want, statErr)
		}
	}
	// notes.md must not have produced any output. A "notesmcp" subdirectory
	// would prove the extension filter is broken.
	if _, err := os.Stat(filepath.Join(outDir, "notesmcp")); err == nil {
		t.Errorf("notes.md should not have produced a notesmcp/ output dir")
	}
}

func TestCLI_Batch_CommaSeparatedFolders(t *testing.T) {
	// "multiple folders" was an explicit user requirement — verify two
	// independent folders in one invocation both produce output.
	dirA, dirB, outDir := t.TempDir(), t.TempDir(), t.TempDir()
	mustWriteSpec(t, filepath.Join(dirA, "one.yaml"), minimalSpec("oneOp"))
	mustWriteSpec(t, filepath.Join(dirB, "two.yaml"), minimalSpec("twoOp"))

	_, stderr, err := runCLI(t,
		"-spec", dirA+","+dirB,
		"-out", outDir,
		"-client-import", "example.com/g",
	)
	if err != nil {
		t.Fatalf("CLI failed: %v\nstderr=%s", err, stderr)
	}
	for _, slug := range []string{"one", "two"} {
		want := filepath.Join(outDir, slug+"mcp", slug+"mcp.mcp.go")
		if _, statErr := os.Stat(want); statErr != nil {
			t.Errorf("expected %s to exist: %v", want, statErr)
		}
	}
}

func TestCLI_Batch_PartialFailure(t *testing.T) {
	// One good spec + one malformed: the run must keep going and report
	// both, with exit code 3 (exitGenerate) per the documented contract.
	specDir := t.TempDir()
	outDir := t.TempDir()
	mustWriteSpec(t, filepath.Join(specDir, "good.yaml"), minimalSpec("goodOp"))
	mustWriteSpec(t, filepath.Join(specDir, "broken.yaml"), "not: a: valid: openapi: spec\n")

	_, stderr, err := runCLI(t,
		"-spec", specDir,
		"-out", outDir,
		"-client-import", "example.com/g",
	)
	if got := exitCode(err); got != 3 {
		t.Fatalf("expected exit code 3 (exitGenerate), got %d\nstderr=%s", got, stderr)
	}
	// The bad spec must be named in stderr so the user knows which file
	// to fix. The good spec must still have produced its output.
	if !strings.Contains(stderr, "broken.yaml") {
		t.Errorf("stderr should name the failing spec; got %s", stderr)
	}
	if _, err := os.Stat(filepath.Join(outDir, "goodmcp", "goodmcp.mcp.go")); err != nil {
		t.Errorf("partial-failure run should still produce good.yaml's output: %v", err)
	}
}

func TestCLI_Batch_RejectsPackageFlag(t *testing.T) {
	// In batch mode -package is ambiguous (every spec would share a
	// PackageName, files would overwrite each other). Must exit with
	// exitUsage=1 BEFORE loading any spec.
	specDir := t.TempDir()
	outDir := t.TempDir()
	mustWriteSpec(t, filepath.Join(specDir, "a.yaml"), minimalSpec("aOp"))
	mustWriteSpec(t, filepath.Join(specDir, "b.yaml"), minimalSpec("bOp"))

	_, stderr, err := runCLI(t,
		"-spec", specDir,
		"-out", outDir,
		"-package", "shared",
		"-client-import", "example.com/g",
	)
	if got := exitCode(err); got != 1 {
		t.Fatalf("expected exit code 1 (exitUsage), got %d\nstderr=%s", got, stderr)
	}
	if !strings.Contains(stderr, "-package") {
		t.Errorf("stderr should mention the -package flag; got %s", stderr)
	}
	// Neither spec should have been processed.
	for _, slug := range []string{"a", "b"} {
		path := filepath.Join(outDir, slug+"mcp", slug+"mcp.mcp.go")
		if _, statErr := os.Stat(path); statErr == nil {
			t.Errorf("flag-rejection should abort before writing files; found %s", path)
		}
	}
}

func TestCLI_Batch_RejectsEmitV3(t *testing.T) {
	// -emit-v3 writes ONE YAML file — incompatible with multi-spec input.
	// Reject up-front so users don't lose data to surprise overwrites.
	specDir := t.TempDir()
	mustWriteSpec(t, filepath.Join(specDir, "a.yaml"), minimalSpec("aOp"))
	mustWriteSpec(t, filepath.Join(specDir, "b.yaml"), minimalSpec("bOp"))
	emitTo := filepath.Join(t.TempDir(), "out.yaml")

	_, stderr, err := runCLI(t,
		"-spec", specDir,
		"-emit-v3", emitTo,
	)
	if got := exitCode(err); got != 1 {
		t.Fatalf("expected exit code 1 (exitUsage), got %d\nstderr=%s", got, stderr)
	}
	if !strings.Contains(stderr, "-emit-v3") {
		t.Errorf("stderr should mention the -emit-v3 flag; got %s", stderr)
	}
	if _, statErr := os.Stat(emitTo); statErr == nil {
		t.Errorf("rejection must happen before writing; found %s", emitTo)
	}
}

func TestCLI_Batch_ListGroupsBySpec(t *testing.T) {
	// -list with multiple specs should print a header per spec so the
	// user can tell which operations came from which file.
	specDir := t.TempDir()
	mustWriteSpec(t, filepath.Join(specDir, "alpha.yaml"), minimalSpec("alphaOp"))
	mustWriteSpec(t, filepath.Join(specDir, "beta.yaml"), minimalSpec("betaOp"))

	stdout, stderr, err := runCLI(t, "-spec", specDir, "-list")
	if err != nil {
		t.Fatalf("CLI failed: %v\nstderr=%s", err, stderr)
	}
	for _, want := range []string{"alpha.yaml", "beta.yaml", "alphaOp", "betaOp"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("-list output missing %q\nstdout=%s", want, stdout)
		}
	}
	// Each batched entry should be delimited by a "===" header so the
	// output is grep-friendly.
	if strings.Count(stdout, "===") < 2 {
		t.Errorf("expected >=2 '===' header markers; got stdout=%s", stdout)
	}
}

func TestCLI_Batch_SlugCollisionRejected(t *testing.T) {
	// Two specs in different subdirs that derive the same slug must be
	// reported BEFORE any file is written. Subdir-aware
	// disambiguation was a deliberately-rejected design choice — we want
	// the user to fix the filename rather than silently differ.
	specDir := t.TempDir()
	v1 := filepath.Join(specDir, "v1")
	v2 := filepath.Join(specDir, "v2")
	for _, d := range []string{v1, v2} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	mustWriteSpec(t, filepath.Join(v1, "api.yaml"), minimalSpec("aOp"))
	mustWriteSpec(t, filepath.Join(v2, "api.yaml"), minimalSpec("bOp"))

	outDir := t.TempDir()
	_, stderr, err := runCLI(t,
		"-spec", specDir,
		"-out", outDir,
		"-client-import", "example.com/g",
	)
	if got := exitCode(err); got != 2 {
		t.Fatalf("expected exit code 2 (exitBadInput) for slug collision, got %d\nstderr=%s", got, stderr)
	}
	for _, want := range []string{"collision", "v1/api.yaml", "v2/api.yaml"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr should mention %q; got %s", want, stderr)
		}
	}
	if _, statErr := os.Stat(filepath.Join(outDir, "apimcp")); statErr == nil {
		t.Errorf("collision detection must abort before any write; found apimcp/")
	}
}

// specWithBadXMCP returns a tiny OpenAPI 3 document carrying an
// unrecognised `x-mcp` value. That value produces an `invalid-x-mcp-value`
// warning during generation — exactly the trigger needed to test the
// -warnings-as-errors flag.
func specWithBadXMCP(opID string) string {
	return `openapi: 3.0.0
info:
  title: ` + opID + `
  version: "1.0"
paths:
  /thing:
    get:
      operationId: ` + opID + `
      x-mcp: maybe
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
`
}

func TestCLI_Batch_WarningsAsErrors(t *testing.T) {
	// One good spec + one spec that emits a warning during generation.
	// With -warnings-as-errors the run must finish (no spec is rejected
	// outright) but exit with code 4. The good spec's output must still
	// land on disk — warnings escalate to non-zero exit, not "abort the
	// remaining work".
	specDir := t.TempDir()
	outDir := t.TempDir()
	mustWriteSpec(t, filepath.Join(specDir, "good.yaml"), minimalSpec("goodOp"))
	mustWriteSpec(t, filepath.Join(specDir, "warny.yaml"), specWithBadXMCP("warnyOp"))

	_, stderr, err := runCLI(t,
		"-spec", specDir,
		"-out", outDir,
		"-client-import", "example.com/g",
		"-warnings-as-errors",
	)
	if got := exitCode(err); got != 4 {
		t.Fatalf("expected exit code 4 (exitWarningsError), got %d\nstderr=%s", got, stderr)
	}
	if !strings.Contains(stderr, "invalid-x-mcp-value") {
		t.Errorf("stderr should mention the warning code; got %s", stderr)
	}
	// Good spec's output must exist — warnings are an exit-code signal,
	// not a reason to skip remaining work.
	if _, err := os.Stat(filepath.Join(outDir, "goodmcp", "goodmcp.mcp.go")); err != nil {
		t.Errorf("warning-as-error run should still produce non-warning specs' output: %v", err)
	}
}

func TestCLI_Batch_GenerateFailureTrumpsWarning(t *testing.T) {
	// When a batch run has BOTH a warning-laden spec and an outright
	// failure, the exit code must be 3 (exitGenerate) — not 4
	// (exitWarningsError). The contract is "the strongest failure wins".
	specDir := t.TempDir()
	outDir := t.TempDir()
	mustWriteSpec(t, filepath.Join(specDir, "warny.yaml"), specWithBadXMCP("warnyOp"))
	mustWriteSpec(t, filepath.Join(specDir, "broken.yaml"), "not: a: valid: openapi: spec\n")

	_, stderr, err := runCLI(t,
		"-spec", specDir,
		"-out", outDir,
		"-client-import", "example.com/g",
		"-warnings-as-errors",
	)
	if got := exitCode(err); got != 3 {
		t.Fatalf("expected exit code 3 (exitGenerate trumps exitWarningsError), got %d\nstderr=%s", got, stderr)
	}
}

func TestCLI_Batch_SingleSpecBehavesAsBefore(t *testing.T) {
	// A single matching spec must NOT enter batch mode — the existing
	// -package flag still works and output lands directly in -out (no
	// per-spec subdir). This is the byte-for-byte backwards-compat guard
	// the broader golden test also enforces.
	outDir := t.TempDir()
	_, stderr, err := runCLI(t,
		"-spec", "testdata/petstore-v3.yaml",
		"-out", outDir,
		"-package", "petmcpsolo",
		"-client-import", "github.com/example/petstore",
	)
	if err != nil {
		t.Fatalf("CLI failed: %v\nstderr=%s", err, stderr)
	}
	// Single-spec mode writes directly to -out/<package>.mcp.go, NOT to
	// a subdir. If a subdirectory appears here, single vs. batch detection
	// is broken.
	if _, err := os.Stat(filepath.Join(outDir, "petmcpsolo.mcp.go")); err != nil {
		t.Errorf("single-spec mode should write directly to -out; got %v", err)
	}
	if entries, _ := os.ReadDir(outDir); len(entries) != 1 {
		t.Errorf("single-spec mode should produce one entry under -out; got %d", len(entries))
	}
}

func mustWriteSpec(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
