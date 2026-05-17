// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package loader

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestExpandSpecArg_SingleFile(t *testing.T) {
	refs, err := ExpandSpecArg(filepath.Join("..", "..", "testdata", "petstore-v3.yaml"))
	if err != nil {
		t.Fatalf("ExpandSpecArg: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].IsURL {
		t.Errorf("file path should not be marked as URL")
	}
	if !filepath.IsAbs(refs[0].Path) {
		t.Errorf("file path should be absolute; got %q", refs[0].Path)
	}
	if !strings.HasSuffix(refs[0].Path, "petstore-v3.yaml") {
		t.Errorf("unexpected resolved path: %q", refs[0].Path)
	}
}

func TestExpandSpecArg_URLPassthrough(t *testing.T) {
	refs, err := ExpandSpecArg("https://example.com/api/openapi.json")
	if err != nil {
		t.Fatalf("ExpandSpecArg: %v", err)
	}
	if len(refs) != 1 || !refs[0].IsURL {
		t.Fatalf("expected single URL ref, got %+v", refs)
	}
	if refs[0].Path != "https://example.com/api/openapi.json" {
		t.Errorf("URL should pass through verbatim; got %q", refs[0].Path)
	}
}

func TestExpandSpecArg_Directory(t *testing.T) {
	refs, err := ExpandSpecArg(filepath.Join("..", "..", "testdata"))
	if err != nil {
		t.Fatalf("ExpandSpecArg: %v", err)
	}
	if len(refs) < 3 {
		t.Fatalf("expected several specs in testdata, got %d", len(refs))
	}
	// Output must be sorted by Path for deterministic batch order.
	for i := 1; i < len(refs); i++ {
		if refs[i-1].Path >= refs[i].Path {
			t.Errorf("refs not sorted: %q before %q", refs[i-1].Path, refs[i].Path)
		}
	}
	// Every match must be a .yaml/.yml/.json file we recognise.
	for _, r := range refs {
		ext := strings.ToLower(filepath.Ext(r.Path))
		if _, ok := specExtensions[ext]; !ok {
			t.Errorf("directory walk returned non-spec extension: %q", r.Path)
		}
	}
}

func TestExpandSpecArg_DirectorySkipsHidden(t *testing.T) {
	dir := t.TempDir()
	// Plant one visible spec and one hidden spec; expansion must include
	// only the visible one.
	mustWrite(t, filepath.Join(dir, "good.yaml"), []byte("openapi: 3.0.0\n"))
	mustWrite(t, filepath.Join(dir, ".hidden.yaml"), []byte("openapi: 3.0.0\n"))
	// And a hidden subdirectory whose contents must be skipped entirely.
	hiddenDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(hiddenDir, 0o755); err != nil {
		t.Fatalf("mkdir hidden: %v", err)
	}
	mustWrite(t, filepath.Join(hiddenDir, "config.yaml"), []byte("openapi: 3.0.0\n"))

	refs, err := ExpandSpecArg(dir)
	if err != nil {
		t.Fatalf("ExpandSpecArg: %v", err)
	}
	if len(refs) != 1 || !strings.HasSuffix(refs[0].Path, "good.yaml") {
		t.Errorf("expected only good.yaml to survive filtering; got %+v", refs)
	}
}

func TestExpandSpecArg_DirectorySkipsNonSpecExtensions(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "spec.yaml"), []byte("openapi: 3.0.0\n"))
	mustWrite(t, filepath.Join(dir, "readme.md"), []byte("# notes\n"))
	mustWrite(t, filepath.Join(dir, "spec.txt"), []byte("not yaml"))
	refs, err := ExpandSpecArg(dir)
	if err != nil {
		t.Fatalf("ExpandSpecArg: %v", err)
	}
	if len(refs) != 1 || !strings.HasSuffix(refs[0].Path, "spec.yaml") {
		t.Errorf("expected only spec.yaml; got %+v", refs)
	}
}

func TestExpandSpecArg_DirectoryRecursesByDefault(t *testing.T) {
	// The whole point of directory mode is "give me everything under this
	// tree" — verify the walk actually recurses through subdirectories.
	dir := t.TempDir()
	sub := filepath.Join(dir, "v1", "billing")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	mustWrite(t, filepath.Join(sub, "api.yaml"), []byte("openapi: 3.0.0\n"))
	mustWrite(t, filepath.Join(dir, "top.yaml"), []byte("openapi: 3.0.0\n"))

	refs, err := ExpandSpecArg(dir)
	if err != nil {
		t.Fatalf("ExpandSpecArg: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs (recursive), got %d: %+v", len(refs), refs)
	}
}

func TestExpandSpecArg_EmptyDirectoryErrors(t *testing.T) {
	dir := t.TempDir()
	_, err := ExpandSpecArg(dir)
	if err == nil || !strings.Contains(err.Error(), "matched no") {
		t.Errorf("expected matched-no-files error, got %v", err)
	}
}

func TestExpandSpecArg_Glob(t *testing.T) {
	pattern := filepath.Join("..", "..", "testdata", "*.yaml")
	refs, err := ExpandSpecArg(pattern)
	if err != nil {
		t.Fatalf("ExpandSpecArg: %v", err)
	}
	if len(refs) == 0 {
		t.Fatal("expected glob to match several files")
	}
	for _, r := range refs {
		if !strings.HasSuffix(r.Path, ".yaml") {
			t.Errorf("glob *.yaml returned non-yaml: %q", r.Path)
		}
	}
}

func TestExpandSpecArg_GlobNoMatchErrors(t *testing.T) {
	pattern := filepath.Join(t.TempDir(), "*.nope")
	_, err := ExpandSpecArg(pattern)
	if err == nil || !strings.Contains(err.Error(), "matched no files") {
		t.Errorf("expected matched-no-files error, got %v", err)
	}
}

func TestExpandSpecArg_CommaSeparated(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.yaml"), []byte("openapi: 3.0.0\n"))
	other := t.TempDir()
	mustWrite(t, filepath.Join(other, "b.yaml"), []byte("openapi: 3.0.0\n"))

	refs, err := ExpandSpecArg(dir + "," + other)
	if err != nil {
		t.Fatalf("ExpandSpecArg: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs across two folders, got %d", len(refs))
	}
	// Whitespace and empty entries between commas must be tolerated so
	// users can format multi-line shell args.
	refs2, err := ExpandSpecArg("  " + dir + " , , " + other + "  ")
	if err != nil {
		t.Fatalf("ExpandSpecArg with whitespace: %v", err)
	}
	if len(refs2) != 2 {
		t.Errorf("expected 2 refs after whitespace handling, got %d", len(refs2))
	}
}

func TestExpandSpecArg_CommaDeduplicates(t *testing.T) {
	// The same file referenced twice (e.g. once directly, once via a
	// directory walk) must collapse to a single entry so the generator
	// doesn't redundantly render the same output.
	file := filepath.Join("..", "..", "testdata", "petstore-v3.yaml")
	dir := filepath.Join("..", "..", "testdata")
	refs, err := ExpandSpecArg(file + "," + dir)
	if err != nil {
		t.Fatalf("ExpandSpecArg: %v", err)
	}
	seen := map[string]int{}
	for _, r := range refs {
		seen[r.Path]++
	}
	for path, count := range seen {
		if count > 1 {
			t.Errorf("path %q appears %d times after dedup", path, count)
		}
	}
}

func TestExpandSpecArg_MissingPathErrors(t *testing.T) {
	_, err := ExpandSpecArg("/no/such/path/spec.yaml")
	if err == nil {
		t.Fatal("expected error for missing path")
	}
	if !strings.Contains(err.Error(), "/no/such/path/spec.yaml") {
		t.Errorf("error should quote the offending path; got %v", err)
	}
}

func TestExpandSpecArg_EmptyArgErrors(t *testing.T) {
	if _, err := ExpandSpecArg(""); err == nil {
		t.Errorf("expected error for empty arg")
	}
	if _, err := ExpandSpecArg("   "); err == nil {
		t.Errorf("expected error for whitespace-only arg")
	}
}

func TestExpandSpecArg_GlobMatchingDirectoryWalksIt(t *testing.T) {
	// A glob pattern that resolves to a directory should walk that
	// directory rather than silently dropping it — the user wrote the
	// pattern, they probably meant the contents.
	parent := t.TempDir()
	sub := filepath.Join(parent, "billing")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	mustWrite(t, filepath.Join(sub, "api.yaml"), []byte("openapi: 3.0.0\n"))

	pattern := filepath.Join(parent, "*")
	refs, err := ExpandSpecArg(pattern)
	if err != nil {
		t.Fatalf("ExpandSpecArg: %v", err)
	}
	if len(refs) != 1 || !strings.HasSuffix(refs[0].Path, "api.yaml") {
		t.Errorf("expected glob to walk into the matched directory; got %+v", refs)
	}
}

func TestHasGlobMeta(t *testing.T) {
	cases := map[string]bool{
		"plain/path.yaml": false,
		"*.yaml":          true,
		"a?b":             true,
		"[ab].yaml":       true,
		"no/meta/here":    false,
		"https://x.com/s": false,
	}
	for in, want := range cases {
		if got := hasGlobMeta(in); got != want {
			t.Errorf("hasGlobMeta(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestDedupAndSort_EmptyAndSingle(t *testing.T) {
	// Short-circuit: 0 or 1 ref returns input verbatim, no allocation.
	if got := dedupAndSort(nil); got != nil {
		t.Errorf("nil input should pass through; got %+v", got)
	}
	one := []SpecRef{{Path: "/a"}}
	if got := dedupAndSort(one); len(got) != 1 || got[0].Path != "/a" {
		t.Errorf("single input should pass through; got %+v", got)
	}
}

func TestDedupAndSort_DedupsAdjacent(t *testing.T) {
	in := []SpecRef{
		{Path: "/b"},
		{Path: "/a"},
		{Path: "/a"},
		{Path: "/c"},
		{Path: "/b"},
	}
	out := dedupAndSort(in)
	got := make([]string, len(out))
	for i, r := range out {
		got[i] = r.Path
	}
	want := []string{"/a", "/b", "/c"}
	sort.Strings(want)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
			break
		}
	}
}

func mustWrite(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
