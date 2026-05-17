// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package generator

import (
	"encoding/json"
	"strings"
)

const xmcpExtensionKey = "x-mcp"

// xmcpLevel identifies which level of the OpenAPI document an x-mcp
// extension was resolved at. Typed so callers can't pass arbitrary strings
// into diagnostic messages.
type xmcpLevel string

const (
	xmcpLevelOperation xmcpLevel = "operation"
	xmcpLevelPath      xmcpLevel = "path"
	xmcpLevelRoot      xmcpLevel = "root"
	xmcpLevelDefault   xmcpLevel = "default"
)

// parseXMCPExtension reads an x-mcp extension value off an Extensions map.
// Returns (value, true) when the value resolves to a boolean (true / false /
// "true" / "false"); returns (_, false) when the key is absent or the value
// is not a recognised boolean. kin-openapi v0.138 stores YAML and JSON
// extension values as native Go types (bool, string); json.RawMessage is
// accepted defensively in case some loading path leaves the value
// un-decoded (e.g. an older library version or a hand-built doc).
func parseXMCPExtension(v any) (bool, bool) {
	if v == nil {
		return false, false
	}
	switch x := v.(type) {
	case bool:
		return x, true
	case string:
		return parseBoolString(x)
	case json.RawMessage:
		var b bool
		if err := json.Unmarshal(x, &b); err == nil {
			return b, true
		}
		var s string
		if err := json.Unmarshal(x, &s); err == nil {
			return parseBoolString(s)
		}
	}
	return false, false
}

// parseBoolString is YAML-friendly: case-insensitive, whitespace-tolerant,
// "true"/"false" only. We deliberately do NOT delegate to strconv.ParseBool,
// which also accepts "1"/"0"/"t"/"f" — none of those round-trip through
// YAML's boolean type and would surprise a spec author who wrote `x-mcp: 1`
// expecting an integer.
func parseBoolString(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

// includeOperation applies the x-mcp precedence rule to decide whether an
// operation should be turned into an MCP tool. Precedence (highest to
// lowest): operation > path-item > document > defaultInclude. opExts /
// pathExts / rootExts may be nil.
//
// Returns:
//   - include: the resolved decision.
//   - level:   where the decision came from (operation/path/root/default).
//     Callers use this to phrase diagnostics like "x-mcp:false at path level".
//   - recognised: false iff a value WAS present at `level` but did not
//     parse as a boolean. The decision then falls back to defaultInclude;
//     callers should warn so the spec author notices a typo like
//     `x-mcp: maybe` or `x-mcp: 1`.
func includeOperation(rootExts, pathExts, opExts map[string]any, defaultInclude bool) (include bool, level xmcpLevel, recognised bool) {
	for _, lv := range []struct {
		name xmcpLevel
		exts map[string]any
	}{
		{xmcpLevelOperation, opExts},
		{xmcpLevelPath, pathExts},
		{xmcpLevelRoot, rootExts},
	} {
		if lv.exts == nil {
			continue
		}
		raw, present := lv.exts[xmcpExtensionKey]
		if !present {
			continue
		}
		if v, ok := parseXMCPExtension(raw); ok {
			return v, lv.name, true
		}
		// Value present but not a boolean — stop here and report the level
		// so the caller can pin the warning to it. Falling back without
		// reporting would let typos slip past review.
		return defaultInclude, lv.name, false
	}
	return defaultInclude, xmcpLevelDefault, true
}
