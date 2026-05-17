// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package loader

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SpecRef identifies one spec input for the generator. Path is an absolute
// filesystem path for files (resolved via filepath.Abs) or the raw URL
// when IsURL is true. SpecRefs are sortable by Path for deterministic batch
// order.
type SpecRef struct {
	Path  string
	IsURL bool
}

// specExtensions are the file suffixes ExpandSpecArg accepts when walking a
// directory. The loader itself is content-driven (it inspects the file body
// to detect Swagger 2.0 vs OpenAPI 3.x), so this list is purely a directory-
// walk filter — files in the tree that don't end in one of these are
// skipped. Single-file mode bypasses the filter entirely so users can still
// load a spec with an unusual extension by naming it explicitly.
var specExtensions = map[string]struct{}{
	".yaml": {},
	".yml":  {},
	".json": {},
}

// ExpandSpecArg resolves a -spec value into one or more SpecRefs.
//
// The value may be a single token or a comma-separated list. Each token is
// resolved independently:
//
//   - An http(s):// URL becomes a single SpecRef with IsURL=true. URLs are
//     never globbed and never directory-walked.
//   - A path containing glob metacharacters ('*', '?', '[') is expanded
//     with filepath.Glob. '**' is NOT supported in v1; use directory mode
//     for recursive walks.
//   - A path that resolves to a directory is walked recursively. Files with
//     an extension in specExtensions are included; dot-files and dot-
//     directories are skipped, symlinks are not followed.
//   - Any other path is treated as a single file.
//
// Tokens are concatenated, sorted by Path, and deduplicated. An empty match
// (e.g. a glob that resolves to nothing, or an entirely empty directory) is
// an error that names the offending token so the user can see which input
// is unproductive.
func ExpandSpecArg(spec string) ([]SpecRef, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, errors.New("empty spec argument")
	}
	tokens := splitCommaList(spec)
	var all []SpecRef
	for _, tok := range tokens {
		refs, err := expandToken(tok)
		if err != nil {
			return nil, err
		}
		all = append(all, refs...)
	}
	return dedupAndSort(all), nil
}

// expandToken resolves one comma-separated entry. URL detection happens
// first so a future user passing an http(s) URL with a query string that
// happens to contain a '*' is treated as a URL rather than as a glob.
func expandToken(tok string) ([]SpecRef, error) {
	if isHTTPURL(tok) {
		return []SpecRef{{Path: tok, IsURL: true}}, nil
	}
	if hasGlobMeta(tok) {
		return expandGlob(tok)
	}
	info, err := os.Stat(tok)
	if err != nil {
		// Don't try to be clever — a missing path is an error, not an
		// empty match. Empty-match only applies to globs/directories that
		// existed but produced no spec candidates.
		return nil, fmt.Errorf("spec %q: %w", tok, err)
	}
	if info.IsDir() {
		return expandDir(tok)
	}
	abs, err := absPath(tok)
	if err != nil {
		return nil, err
	}
	return []SpecRef{{Path: abs}}, nil
}

// absPath is the single canonicalisation entry point used by every path
// returned from this package. Centralising the call (a) keeps the
// fmt.Errorf format consistent across expansion modes, and (b) means the
// "absolute paths are required for deterministic batch order" contract is
// expressed in exactly one place.
func absPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", p, err)
	}
	return abs, nil
}

func expandGlob(pattern string) ([]SpecRef, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob %q: %w", pattern, err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("spec %q: matched no files", pattern)
	}
	out := make([]SpecRef, 0, len(matches))
	for _, m := range matches {
		info, statErr := os.Stat(m)
		if statErr != nil {
			// Glob returned a path that has since vanished. Surface
			// rather than silently drop.
			return nil, fmt.Errorf("stat glob match %q: %w", m, statErr)
		}
		if info.IsDir() {
			// A glob that matched a directory is treated as a single
			// directory-walk request, not skipped — the user wrote it,
			// they probably meant it.
			subRefs, err := expandDir(m)
			if err != nil {
				return nil, err
			}
			out = append(out, subRefs...)
			continue
		}
		abs, err := absPath(m)
		if err != nil {
			return nil, err
		}
		out = append(out, SpecRef{Path: abs})
	}
	return out, nil
}

func expandDir(dir string) ([]SpecRef, error) {
	var out []SpecRef
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		base := d.Name()
		// Skip dot-files at every level. The walk root itself may legally
		// start with '.', so only the test on `base` matters here.
		if path != dir && strings.HasPrefix(base, ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		// Symlinks are not followed: WalkDir reports them as files (not
		// dirs), and reading their target would let a stray link escape
		// the user's intended tree. Better to skip and let the user
		// resolve the link explicitly if they want it.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(base))
		if _, ok := specExtensions[ext]; !ok {
			return nil
		}
		abs, err := absPath(path)
		if err != nil {
			return err
		}
		out = append(out, SpecRef{Path: abs})
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("walk %q: %w", dir, walkErr)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("spec %q: matched no .yaml/.yml/.json files", dir)
	}
	return out, nil
}

// splitCommaList splits a comma-separated list, trimming whitespace from
// each entry and dropping empties. Two adjacent commas (",,") are silently
// elided so users can comment-out an entry by deleting its name without
// leaving a syntax error.
//
// A token that starts with http(s):// is NOT split further on commas —
// matrix params and some content-negotiation URLs legally contain commas,
// and silently splitting them would produce confusing "matched no files"
// errors. Once a URL token starts, we scan to the next top-level comma
// that is followed by either end-of-input or another entry that is itself
// trimmable to non-empty content. Practically: any commas appearing inside
// what looks like a URL are kept verbatim; the next top-level entry must
// be separated by ",<whitespace>" — same as before — for the split to
// re-engage.
func splitCommaList(s string) []string {
	out := make([]string, 0, 4)
	for len(s) > 0 {
		s = strings.TrimLeft(s, ", \t")
		if s == "" {
			break
		}
		// If the next token is a URL, consume up to the FIRST comma that
		// is followed by whitespace — that's our heuristic for "next CLI
		// entry" vs "comma inside the URL". This is intentionally simple:
		// users with truly hostile URLs can pass them one at a time.
		if isHTTPURL(s) {
			end := findURLBoundary(s)
			tok := strings.TrimSpace(s[:end])
			if tok != "" {
				out = append(out, tok)
			}
			s = s[end:]
			continue
		}
		idx := strings.IndexByte(s, ',')
		if idx < 0 {
			tok := strings.TrimSpace(s)
			if tok != "" {
				out = append(out, tok)
			}
			break
		}
		tok := strings.TrimSpace(s[:idx])
		if tok != "" {
			out = append(out, tok)
		}
		s = s[idx+1:]
	}
	return out
}

// findURLBoundary returns the index in s where the URL token ends. The
// heuristic: the URL extends to the next ",<whitespace>" sequence, or to
// end-of-string. A bare "," with no trailing whitespace is assumed to be
// part of the URL (matrix params, OData, etc.).
func findURLBoundary(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] != ',' {
			continue
		}
		if i+1 >= len(s) {
			return i
		}
		next := s[i+1]
		if next == ' ' || next == '\t' {
			return i
		}
	}
	return len(s)
}

func hasGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

func dedupAndSort(refs []SpecRef) []SpecRef {
	if len(refs) < 2 {
		return refs
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Path < refs[j].Path })
	out := refs[:1]
	for _, r := range refs[1:] {
		if r.Path != out[len(out)-1].Path {
			out = append(out, r)
		}
	}
	return out
}
