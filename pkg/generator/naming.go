// Copyright 2026 Dipjyoti Metia.
// Portions copyright 2025 Redpanda Data, Inc. (MangleHeadIfTooLong adapted
// from redpanda-data/protoc-gen-go-mcp, Apache-2.0).
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package generator

import (
	"crypto/sha256"
	"math/big"
	"strings"
	"unicode"
)

// MaxToolNameLen is the portable MCP/LLM tool-name length limit.
const MaxToolNameLen = 64

// MangleHeadIfTooLong truncates name to fit within maxLen, replacing the head
// with a deterministic base-36 SHA-256 prefix so the most-specific tail is
// preserved.
func MangleHeadIfTooLong(name string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(name) <= maxLen {
		return name
	}
	hash := sha256.Sum256([]byte(name))
	fullHash := base36(hash[:])
	hashPrefix := fullHash
	if len(hashPrefix) > 10 {
		hashPrefix = hashPrefix[:10]
	}
	if maxLen <= len(hashPrefix) {
		return hashPrefix[:maxLen]
	}
	available := maxLen - len(hashPrefix) - 1
	if available <= 0 {
		return hashPrefix
	}
	tail := name[len(name)-available:]
	return hashPrefix + "_" + tail
}

func base36(b []byte) string {
	return new(big.Int).SetBytes(b).Text(36)
}

// ToolName builds a tool name from an OpenAPI operationId, falling back to a
// METHOD_path-based name if operationId is empty. The result satisfies the
// portable MCP/LLM tool-name pattern [A-Za-z][A-Za-z0-9_-]{0,63}.
func ToolName(operationID, httpMethod, path string) string {
	raw := operationID
	if raw == "" {
		raw = httpMethod + "_" + sanitizePath(path)
	}
	name := sanitize(raw)
	if name == "" || !isASCIILetter(name[0]) {
		name = "tool_" + name
	}
	name = MangleHeadIfTooLong(name, MaxToolNameLen)
	if !isASCIILetter(name[0]) {
		// MangleHeadIfTooLong uses a base-36 hash, which may begin with a
		// digit. Replacing that character retains the deterministic hash/tail
		// shape while preserving the required leading letter.
		name = "t" + name[1:]
	}
	return name
}

func isASCIILetter(b byte) bool {
	return b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
}

// PascalCase produces an UpperCamelCase Go identifier from an operationId.
// It is conservative — non-ASCII characters are dropped — so the result is a
// valid identifier as long as the input contains at least one ASCII letter.
func PascalCase(s string) string {
	var b strings.Builder
	upper := true
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
			if upper {
				b.WriteRune(unicode.ToUpper(r))
			} else {
				b.WriteRune(r)
			}
			upper = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			upper = false
		default:
			upper = true
		}
	}
	return b.String()
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// sanitizePath turns "/pets/{petId}" into "pets_petId".
func sanitizePath(p string) string {
	p = strings.TrimPrefix(p, "/")
	var b strings.Builder
	for _, r := range p {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '/':
			b.WriteRune('_')
		case r == '{', r == '}':
			// drop
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
