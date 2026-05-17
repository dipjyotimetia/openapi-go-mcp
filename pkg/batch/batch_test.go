// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package batch

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dipjyotimetia/openapi-go-mcp/pkg/generator"
	"github.com/dipjyotimetia/openapi-go-mcp/pkg/loader"
)

func TestSlug_FilenameStem(t *testing.T) {
	cases := map[string]string{
		"petstore.yaml":            "petstore",
		"path/to/billing-api.yml":  "billingapi",
		"v1/users-api.json":        "usersapi",
		"UPPER.YAML":               "upper",
		"weird_chars!@#.yaml":      "weirdchars",
		"a.b.c.yaml":               "abc", // strip last ext only
		"./relative/Spec.YAML":     "spec",
		"with spaces in name.yaml": "withspacesinname",
	}
	for in, want := range cases {
		got, err := Slug(in)
		if err != nil {
			t.Errorf("Slug(%q) unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlug_EmptyStemErrors(t *testing.T) {
	cases := []string{
		"___.yaml",
		"!.json",
		".yaml", // dotfile with no name
	}
	for _, in := range cases {
		_, err := Slug(in)
		if err == nil {
			t.Errorf("Slug(%q) expected error for empty stem", in)
		}
	}
}

func TestSlug_DigitLeadingStemErrors(t *testing.T) {
	// Go package names cannot start with a digit. The Slug helper must
	// reject these up-front rather than producing output (e.g. "999mcp")
	// that fails to compile downstream.
	cases := []string{
		"999.yaml",
		"123-numeric.yaml",
		"2024-api.yaml",
	}
	for _, in := range cases {
		_, err := Slug(in)
		if err == nil {
			t.Errorf("Slug(%q) expected error for digit-leading stem", in)
			continue
		}
		if !strings.Contains(err.Error(), "digit") {
			t.Errorf("Slug(%q) error should mention digit; got %v", in, err)
		}
	}
}

func TestPlanFor_SingleSpecModePassesOptsUnchanged(t *testing.T) {
	// Backwards-compat guarantor: when batch=false, Options must be
	// returned verbatim so the existing golden test still passes
	// byte-for-byte. Any divergence here would silently break every
	// user's existing single-spec workflow.
	ref := loader.SpecRef{Path: "/tmp/petstore.yaml"}
	base := generator.Options{
		OutDir:       "/out",
		PackageName:  "custompkg",
		ClientImport: "github.com/me/pet",
		Force:        true,
		OpenAICompat: true,
	}
	plan, err := PlanFor(ref, base, false)
	if err != nil {
		t.Fatalf("PlanFor: %v", err)
	}
	if plan.Opts.OutDir != base.OutDir ||
		plan.Opts.PackageName != base.PackageName ||
		plan.Opts.ClientImport != base.ClientImport ||
		plan.Opts.Force != base.Force ||
		plan.Opts.OpenAICompat != base.OpenAICompat {
		t.Errorf("single-mode Opts diverged from base: got %+v, want %+v", plan.Opts, base)
	}
	if plan.Slug != "petstore" {
		t.Errorf("slug should still be derived even in single mode; got %q", plan.Slug)
	}
}

func TestPlanFor_BatchModeDerivesAllThreeFields(t *testing.T) {
	ref := loader.SpecRef{Path: "/tmp/billing-api.yaml"}
	base := generator.Options{
		OutDir:       "gen",
		ClientImport: "github.com/me/gen",
		Force:        true,
		OpenAICompat: true,
	}
	plan, err := PlanFor(ref, base, true)
	if err != nil {
		t.Fatalf("PlanFor: %v", err)
	}
	if plan.Slug != "billingapi" {
		t.Errorf("slug: got %q", plan.Slug)
	}
	if plan.Opts.PackageName != "billingapimcp" {
		t.Errorf("PackageName: got %q", plan.Opts.PackageName)
	}
	wantOut := filepath.Join("gen", "billingapimcp")
	if plan.Opts.OutDir != wantOut {
		t.Errorf("OutDir: got %q, want %q", plan.Opts.OutDir, wantOut)
	}
	if plan.Opts.ClientImport != "github.com/me/gen/billingapi" {
		t.Errorf("ClientImport: got %q", plan.Opts.ClientImport)
	}
	// Shared fields must pass through untouched.
	if !plan.Opts.Force || !plan.Opts.OpenAICompat {
		t.Errorf("shared opts (Force, OpenAICompat) must be preserved: %+v", plan.Opts)
	}
}

func TestPlanFor_BatchModeUsesForwardSlashInImport(t *testing.T) {
	// Go import paths use forward slashes on every OS. If the
	// implementation accidentally used filepath.Join (which becomes
	// backslash on Windows), the generated file's import line would
	// fail to compile on Windows.
	ref := loader.SpecRef{Path: "/anywhere/users.yaml"}
	base := generator.Options{ClientImport: "github.com/me/gen"}
	plan, err := PlanFor(ref, base, true)
	if err != nil {
		t.Fatalf("PlanFor: %v", err)
	}
	if strings.Contains(plan.Opts.ClientImport, `\`) {
		t.Errorf("ClientImport must not contain backslashes; got %q", plan.Opts.ClientImport)
	}
	if plan.Opts.ClientImport != "github.com/me/gen/users" {
		t.Errorf("ClientImport: got %q", plan.Opts.ClientImport)
	}
}

func TestPlanFor_URLInBatchModeErrors(t *testing.T) {
	// URLs have no stable filesystem-derived slug; rejecting in batch is
	// safer than guessing one from the URL path.
	ref := loader.SpecRef{Path: "https://example.com/spec.json", IsURL: true}
	_, err := PlanFor(ref, generator.Options{}, true)
	if err == nil || !strings.Contains(err.Error(), "URL") {
		t.Errorf("expected URL-in-batch error, got %v", err)
	}
}

func TestPlanFor_URLInSingleModePassesThrough(t *testing.T) {
	// Single-spec URLs are still supported — they always have been.
	ref := loader.SpecRef{Path: "https://example.com/spec.json", IsURL: true}
	base := generator.Options{ClientImport: "github.com/me/pet"}
	plan, err := PlanFor(ref, base, false)
	if err != nil {
		t.Fatalf("URL in single mode should work: %v", err)
	}
	if plan.Opts.ClientImport != base.ClientImport {
		t.Errorf("opts must pass through for single-mode URL")
	}
}

func TestPlanFor_UnusableStemErrors(t *testing.T) {
	ref := loader.SpecRef{Path: "/tmp/___.yaml"}
	_, err := PlanFor(ref, generator.Options{}, true)
	if err == nil || !strings.Contains(err.Error(), "slug") {
		t.Errorf("expected slug-derivation error, got %v", err)
	}
}

func TestDetectCollisions_NoCollisions(t *testing.T) {
	plans := []SpecPlan{
		{Slug: "billing", Ref: loader.SpecRef{Path: "/apis/billing.yaml"}},
		{Slug: "users", Ref: loader.SpecRef{Path: "/apis/users.yaml"}},
	}
	if err := DetectCollisions(plans); err != nil {
		t.Errorf("unexpected collision error: %v", err)
	}
}

func TestDetectCollisions_OneCollision(t *testing.T) {
	plans := []SpecPlan{
		{Slug: "api", Ref: loader.SpecRef{Path: "/v1/api.yaml"}},
		{Slug: "api", Ref: loader.SpecRef{Path: "/v2/api.yaml"}},
		{Slug: "users", Ref: loader.SpecRef{Path: "/users.yaml"}},
	}
	err := DetectCollisions(plans)
	if err == nil {
		t.Fatal("expected collision error")
	}
	msg := err.Error()
	// Both source paths must appear in the error so the user can fix in
	// one go rather than playing whack-a-mole.
	if !strings.Contains(msg, "/v1/api.yaml") || !strings.Contains(msg, "/v2/api.yaml") {
		t.Errorf("error should name both colliding paths; got %s", msg)
	}
	if !strings.Contains(msg, "\"api\"") {
		t.Errorf("error should quote the colliding slug; got %s", msg)
	}
}

func TestDetectCollisions_MultipleSlugsCollideReportsAll(t *testing.T) {
	// When two different slugs collide, the user must see both pairs in
	// one error so they can fix the whole batch in one round.
	plans := []SpecPlan{
		{Slug: "api", Ref: loader.SpecRef{Path: "/v1/api.yaml"}},
		{Slug: "api", Ref: loader.SpecRef{Path: "/v2/api.yaml"}},
		{Slug: "x", Ref: loader.SpecRef{Path: "/a/x.yaml"}},
		{Slug: "x", Ref: loader.SpecRef{Path: "/b/x.yaml"}},
	}
	err := DetectCollisions(plans)
	if err == nil {
		t.Fatal("expected collision error")
	}
	for _, want := range []string{"/v1/api.yaml", "/v2/api.yaml", "/a/x.yaml", "/b/x.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q; got %s", want, err.Error())
		}
	}
}

func TestDetectCollisions_SkipsEmptySlug(t *testing.T) {
	// URL refs in single-spec mode have no slug — they must not be
	// treated as "colliding with each other on the empty string".
	plans := []SpecPlan{
		{Slug: "", Ref: loader.SpecRef{Path: "https://a.example.com/spec.yaml", IsURL: true}},
		{Slug: "", Ref: loader.SpecRef{Path: "https://b.example.com/spec.yaml", IsURL: true}},
	}
	if err := DetectCollisions(plans); err != nil {
		t.Errorf("empty slugs must be skipped; got %v", err)
	}
}
