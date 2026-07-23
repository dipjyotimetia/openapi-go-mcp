// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package generator

import (
	"regexp"
	"strings"
	"testing"
)

var portableToolName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

func TestMangleHeadIfTooLong_ShortNameUnchanged(t *testing.T) {
	got := MangleHeadIfTooLong("getPet", MaxToolNameLen)
	if got != "getPet" {
		t.Errorf("short name should pass through; got %q", got)
	}
}

func TestMangleHeadIfTooLong_LongNameTruncatesAndHashesHead(t *testing.T) {
	long := strings.Repeat("a", 200) + "TAIL"
	got := MangleHeadIfTooLong(long, MaxToolNameLen)
	if len(got) > MaxToolNameLen {
		t.Errorf("mangled length %d exceeds limit %d", len(got), MaxToolNameLen)
	}
	if !strings.HasSuffix(got, "TAIL") {
		t.Errorf("mangled name should preserve TAIL suffix; got %q", got)
	}
	// Should contain an underscore separator between the hash prefix and the tail.
	if !strings.Contains(got, "_") {
		t.Errorf("mangled name should include hash/tail separator; got %q", got)
	}
}

func TestMangleHeadIfTooLong_Deterministic(t *testing.T) {
	// Same input must always produce the same output — the generator depends
	// on this for golden-file stability.
	in := strings.Repeat("x", 250) + "_op"
	a := MangleHeadIfTooLong(in, MaxToolNameLen)
	b := MangleHeadIfTooLong(in, MaxToolNameLen)
	if a != b {
		t.Errorf("mangling not deterministic: %q vs %q", a, b)
	}
}

func TestMangleHeadIfTooLong_EdgeCases(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		maxLen int
		check  func(t *testing.T, got string)
	}{
		{
			name:   "zero maxLen returns empty",
			in:     "anything",
			maxLen: 0,
			check: func(t *testing.T, got string) {
				if got != "" {
					t.Errorf("expected empty, got %q", got)
				}
			},
		},
		{
			name:   "negative maxLen returns empty",
			in:     "anything",
			maxLen: -1,
			check: func(t *testing.T, got string) {
				if got != "" {
					t.Errorf("expected empty, got %q", got)
				}
			},
		},
		{
			name:   "maxLen exactly len(name) keeps name",
			in:     "abcdef",
			maxLen: 6,
			check: func(t *testing.T, got string) {
				if got != "abcdef" {
					t.Errorf("expected verbatim, got %q", got)
				}
			},
		},
		{
			name:   "maxLen smaller than hash prefix truncates the hash",
			in:     strings.Repeat("y", 50),
			maxLen: 4,
			check: func(t *testing.T, got string) {
				if len(got) != 4 {
					t.Errorf("expected length 4, got %d (%q)", len(got), got)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, MangleHeadIfTooLong(tc.in, tc.maxLen))
		})
	}
}

func TestToolName_FallsBackToMethodPathWhenOperationIDEmpty(t *testing.T) {
	// Empty operationID exercises sanitizePath, which would otherwise sit at
	// 0% coverage.
	got := ToolName("", "GET", "/pets/{petId}/photos")
	want := "GET_pets_petId_photos"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestToolName_SanitisesIllegalCharacters(t *testing.T) {
	// "@" and "/" are not in the allowed [A-Za-z0-9_.-] set; sanitize maps
	// them to underscore.
	got := ToolName("get@pet/v1", "", "")
	if strings.ContainsAny(got, "@/") {
		t.Errorf("illegal chars not sanitised: %q", got)
	}
}

func TestToolName_PreservesAllowedPunctuation(t *testing.T) {
	// Underscore and dash are portable MCP/LLM tool-name punctuation.
	got := ToolName("get.pet-v1_alpha", "", "")
	if got != "get_pet-v1_alpha" {
		t.Errorf("portable punctuation should be retained or normalized; got %q", got)
	}
}

func TestToolName_ProducesPortableNames(t *testing.T) {
	cases := []string{
		"get-pet_v1",
		"123-pets",
		".starts-with-dot",
		"\u00fcber pets",
		strings.Repeat("a", 80),
	}
	for _, operationID := range cases {
		t.Run(operationID, func(t *testing.T) {
			got := ToolName(operationID, "", "")
			if !portableToolName.MatchString(got) {
				t.Errorf("ToolName(%q) = %q, which is not portable", operationID, got)
			}
		})
	}
}

func TestToolName_LeavesAlreadyPortableNameStable(t *testing.T) {
	const name = "get-pet_v1"
	if got := ToolName(name, "", ""); got != name {
		t.Errorf("ToolName(%q) = %q, want unchanged", name, got)
	}
}

func TestPascalCase_StripsNonASCIIAndPunctuation(t *testing.T) {
	cases := map[string]string{
		"get_pet_by_id": "GetPetById",
		"GET pet":       "GETPet",
		"pet-v1":        "PetV1",
		"":              "",
		"123only":       "123only", // leading digit is preserved but not capitalised
		"  spaces  pet": "SpacesPet",
	}
	for in, want := range cases {
		if got := PascalCase(in); got != want {
			t.Errorf("PascalCase(%q) = %q; want %q", in, want, got)
		}
	}
}

func TestSanitizePath_HandlesBraceParams(t *testing.T) {
	// sanitizePath is private but reachable through ToolName's empty-id path.
	// Direct coverage here documents the wire-encoding for poorly-tagged
	// specs that omit operationId entirely.
	got := sanitizePath("/users/{userId}/posts/{postId}")
	want := "users_userId_posts_postId"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSanitizePath_TrimsLeadingSlash(t *testing.T) {
	got := sanitizePath("/health")
	if got != "health" {
		t.Errorf("leading slash should be trimmed; got %q", got)
	}
}

func TestSanitizePath_ReplacesIllegalRunes(t *testing.T) {
	// Anything outside [A-Za-z0-9] (other than the explicit slash/brace
	// handling) maps to underscore — keeps the result a valid identifier
	// fragment.
	got := sanitizePath("/foo.bar/baz!qux")
	if strings.ContainsAny(got, ".!") {
		t.Errorf("illegal characters not sanitised: %q", got)
	}
}
