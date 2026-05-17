// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

// Package batch lowers a list of OpenAPI spec inputs into per-spec
// generator.Options the CLI can render in a loop. It is the only place that
// derives a Go package name / oapi-codegen import path from a spec
// filename — the generator core stays unaware of batch concerns.
package batch

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dipjyotimetia/openapi-go-mcp/pkg/generator"
	"github.com/dipjyotimetia/openapi-go-mcp/pkg/loader"
)

// SpecPlan pairs one spec input with the generator.Options that should be
// applied when rendering it. The CLI builds one SpecPlan per matched spec
// and feeds them to generator.Generate in deterministic order.
type SpecPlan struct {
	Ref  loader.SpecRef
	Slug string
	Opts generator.Options
}

// PlanFor builds a SpecPlan from a spec ref + the CLI's shared options.
//
// When batch is false (single-spec mode), Opts is returned unchanged so the
// existing golden test continues to pass byte-for-byte. Slug is still set
// so callers can use it for log prefixes and the like.
//
// When batch is true, three Opts fields are derived from the filename:
//
//   - PackageName  = slug + "mcp"   (matches the single-spec default).
//   - OutDir       = filepath.Join(baseOpts.OutDir, slug+"mcp")
//     so each spec gets its own subdirectory under the user's chosen
//     -out base.
//   - ClientImport = path.Join(baseOpts.ClientImport, slug). path.Join
//     (not filepath.Join) is deliberate: Go import paths use forward
//     slashes on every OS.
//
// URL refs are not currently supported in batch mode — they would have no
// stable slug — and PlanFor returns an error if asked. Single-spec mode
// (batch=false) accepts URLs unchanged because the existing pipeline does.
func PlanFor(ref loader.SpecRef, baseOpts generator.Options, batch bool) (SpecPlan, error) {
	if ref.IsURL {
		if batch {
			return SpecPlan{}, fmt.Errorf("URL %q cannot be used in batch mode (no stable slug)", ref.Path)
		}
		return SpecPlan{Ref: ref, Opts: baseOpts}, nil
	}

	slug, err := Slug(ref.Path)
	if err != nil {
		return SpecPlan{}, fmt.Errorf("derive slug for %q: %w", ref.Path, err)
	}
	if !batch {
		return SpecPlan{Ref: ref, Slug: slug, Opts: baseOpts}, nil
	}

	pkgName := slug + generator.MCPPackageSuffix
	opts := baseOpts
	opts.PackageName = pkgName
	// OutDir is a filesystem path → filepath.Join (OS-specific separator).
	opts.OutDir = filepath.Join(baseOpts.OutDir, pkgName)
	// ClientImport (companion) and ModulePath (proxy) are Go import paths
	// → path.Join (always forward slash, even on Windows, otherwise the
	// generated source won't compile there). We append the slug in both
	// modes; the unused field per mode is ignored downstream.
	if baseOpts.ClientImport != "" {
		opts.ClientImport = path.Join(baseOpts.ClientImport, slug)
	}
	if baseOpts.ModulePath != "" {
		opts.ModulePath = path.Join(baseOpts.ModulePath, slug)
	}
	return SpecPlan{Ref: ref, Slug: slug, Opts: opts}, nil
}

// DetectCollisions scans plans for duplicate slugs and returns an error
// listing every colliding slug with all its source paths, so the user can
// resolve every collision in one fix. Returns nil when every slug is
// unique. The check runs BEFORE any file is written so a renamed spec
// doesn't silently end up in the wrong subdirectory.
func DetectCollisions(plans []SpecPlan) error {
	bySlug := map[string][]string{}
	for _, p := range plans {
		if p.Slug == "" {
			// URLs in single-spec mode have no slug; skip them.
			continue
		}
		bySlug[p.Slug] = append(bySlug[p.Slug], p.Ref.Path)
	}
	colliding := make([]string, 0)
	for slug, paths := range bySlug {
		if len(paths) > 1 {
			sort.Strings(paths)
			colliding = append(colliding, fmt.Sprintf("  %q ← %s", slug, strings.Join(paths, ", ")))
		}
	}
	if len(colliding) == 0 {
		return nil
	}
	sort.Strings(colliding)
	return fmt.Errorf("slug collisions (rename one spec on each line):\n%s", strings.Join(colliding, "\n"))
}

// Slug returns the canonical identifier for a spec path. It lowercases the
// filename stem and keeps only [a-z0-9]; everything else is dropped. The
// stem alone is used (not parent directories) because the resulting slug
// becomes a Go package name segment and an oapi-codegen import suffix —
// both of which must be valid identifiers / import names.
//
// Returns an error when the stem sanitises to the empty string OR starts
// with a digit. Go identifiers cannot start with a digit, so a stem like
// "123-numeric" would slug to "123numeric" and produce a package name
// "123numericmcp" that fails to compile. Callers should surface this so
// the user can rename the offending file rather than seeing a cryptic
// codegen failure downstream.
func Slug(p string) (string, error) {
	stem := filepath.Base(p)
	if ext := filepath.Ext(stem); ext != "" {
		stem = strings.TrimSuffix(stem, ext)
	}
	var b strings.Builder
	for _, r := range strings.ToLower(stem) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "", fmt.Errorf("path %q has no alphanumeric stem; rename it to a [a-z0-9] form", p)
	}
	s := b.String()
	if s[0] >= '0' && s[0] <= '9' {
		return "", fmt.Errorf("path %q produces slug %q which starts with a digit; Go package names cannot start with a digit — rename the file to begin with a letter", p, s)
	}
	return s, nil
}
