// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package generator

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestParseXMCPExtension(t *testing.T) {
	cases := []struct {
		name    string
		input   any
		wantVal bool
		wantOk  bool
	}{
		{"bool true", true, true, true},
		{"bool false", false, false, true},
		{"string lowercase true", "true", true, true},
		{"string mixed case", "TRUE", true, true},
		{"string false", "false", false, true},
		{"string with spaces", "  true  ", true, true},
		{"string yes (unrecognised)", "yes", false, false},
		{"raw json bool true", json.RawMessage(`true`), true, true},
		{"raw json bool false", json.RawMessage(`false`), false, true},
		{"raw json string true", json.RawMessage(`"true"`), true, true},
		{"raw json number (unrecognised)", json.RawMessage(`1`), false, false},
		{"int (unrecognised)", 1, false, false},
		{"nil", nil, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			val, ok := parseXMCPExtension(tc.input)
			if val != tc.wantVal || ok != tc.wantOk {
				t.Errorf("parseXMCPExtension(%#v) = (%v, %v); want (%v, %v)",
					tc.input, val, ok, tc.wantVal, tc.wantOk)
			}
		})
	}
}

func TestIncludeOperation_PrecedenceTable(t *testing.T) {
	// The table covers all 8 combinations of the three explicit levels plus
	// the two values of defaultInclude. Each case asserts the include
	// decision, the level reported, and whether the value was recognised.
	type extMap = map[string]any
	cases := []struct {
		name           string
		root, path, op extMap
		defaultInc     bool
		wantInclude    bool
		wantLevel      xmcpLevel
		wantOk         bool
	}{
		{"all empty, default-include", nil, nil, nil, true, true, xmcpLevelDefault, true},
		{"all empty, default-exclude", nil, nil, nil, false, false, xmcpLevelDefault, true},
		{"op true wins over path false", extMap{"x-mcp": false}, extMap{"x-mcp": false}, extMap{"x-mcp": true}, false, true, xmcpLevelOperation, true},
		{"op false wins over path true", extMap{"x-mcp": true}, extMap{"x-mcp": true}, extMap{"x-mcp": false}, true, false, xmcpLevelOperation, true},
		{"path true wins over root false (op absent)", extMap{"x-mcp": false}, extMap{"x-mcp": true}, nil, false, true, xmcpLevelPath, true},
		{"path false wins over root true (op absent)", extMap{"x-mcp": true}, extMap{"x-mcp": false}, nil, true, false, xmcpLevelPath, true},
		{"root only", extMap{"x-mcp": true}, nil, nil, false, true, xmcpLevelRoot, true},
		{"root false only", extMap{"x-mcp": false}, nil, nil, true, false, xmcpLevelRoot, true},
		{"unrecognised op value falls back to default", nil, nil, extMap{"x-mcp": "maybe"}, true, true, xmcpLevelOperation, false},
		{"unrecognised root value falls back to default-false", extMap{"x-mcp": 42}, nil, nil, true, true, xmcpLevelRoot, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			include, level, ok := includeOperation(tc.root, tc.path, tc.op, tc.defaultInc)
			if include != tc.wantInclude || level != tc.wantLevel || ok != tc.wantOk {
				t.Errorf("includeOperation = (%v, %q, %v); want (%v, %q, %v)",
					include, level, ok, tc.wantInclude, tc.wantLevel, tc.wantOk)
			}
		})
	}
}

func TestCollectOperations_ExcludesByXMCP(t *testing.T) {
	const spec = `openapi: 3.0.0
info: {title: Filter, version: "1"}
paths:
  /kept:
    get:
      operationId: getKept
      responses: {"200": {description: ok}}
  /dropped:
    get:
      operationId: getDropped
      x-mcp: false
      responses: {"200": {description: ok}}
`
	doc := mustLoad(t, spec)
	var warnings bytes.Buffer
	ops, diags, err := CollectOperations(doc, Options{Warnings: &warnings})
	if err != nil {
		t.Fatalf("CollectOperations: %v", err)
	}
	if len(ops) != 1 || ops[0].ToolName != "getKept" {
		t.Fatalf("expected only getKept to survive filtering, got %v", toolNames(ops))
	}
	if !containsDiag(diags, DiagExcludedByXMCP, "GET /dropped") {
		t.Errorf("expected excluded-by-x-mcp info diagnostic for /dropped, got %+v", diags)
	}
}

func TestCollectOperations_PathLevelExclude(t *testing.T) {
	// Path-item-level x-mcp:false drops every operation on that path unless
	// an operation overrides with x-mcp:true.
	const spec = `openapi: 3.0.0
info: {title: Filter, version: "1"}
paths:
  /admin:
    x-mcp: false
    get:
      operationId: listAdmin
      responses: {"200": {description: ok}}
    delete:
      operationId: deleteAdmin
      x-mcp: true
      responses: {"200": {description: ok}}
  /public:
    get:
      operationId: listPublic
      responses: {"200": {description: ok}}
`
	doc := mustLoad(t, spec)
	ops, _, err := CollectOperations(doc, Options{Warnings: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("CollectOperations: %v", err)
	}
	got := toolNames(ops)
	want := []string{"deleteAdmin", "listPublic"}
	if !equalSorted(got, want) {
		t.Errorf("got tools %v, want %v", got, want)
	}
}

func TestCollectOperations_ExcludeByDefault(t *testing.T) {
	// With ExcludeByDefault=true, only operations explicitly opted in are kept.
	const spec = `openapi: 3.0.0
info: {title: OptIn, version: "1"}
paths:
  /default:
    get: {operationId: defaultOp, responses: {"200": {description: ok}}}
  /opted-in:
    get:
      operationId: optedIn
      x-mcp: true
      responses: {"200": {description: ok}}
`
	doc := mustLoad(t, spec)
	ops, _, err := CollectOperations(doc, Options{ExcludeByDefault: true, Warnings: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("CollectOperations: %v", err)
	}
	if len(ops) != 1 || ops[0].ToolName != "optedIn" {
		t.Errorf("expected only optedIn under ExcludeByDefault=true, got %v", toolNames(ops))
	}
}

func TestCollectOperations_RootLevelInverts(t *testing.T) {
	// Document-level x-mcp:false flips the default for the whole spec; an
	// operation can still opt back in with x-mcp:true.
	const spec = `openapi: 3.0.0
info: {title: RootGate, version: "1"}
x-mcp: false
paths:
  /one:
    get: {operationId: one, responses: {"200": {description: ok}}}
  /two:
    get:
      operationId: two
      x-mcp: true
      responses: {"200": {description: ok}}
`
	doc := mustLoad(t, spec)
	ops, _, err := CollectOperations(doc, Options{Warnings: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("CollectOperations: %v", err)
	}
	if len(ops) != 1 || ops[0].ToolName != "two" {
		t.Errorf("expected only `two` to survive root-level x-mcp:false, got %v", toolNames(ops))
	}
}

func TestCollectOperations_UnrecognisedXMCPEmitsWarning(t *testing.T) {
	const spec = `openapi: 3.0.0
info: {title: Bad, version: "1"}
paths:
  /weird:
    get:
      operationId: weird
      x-mcp: maybe
      responses: {"200": {description: ok}}
`
	doc := mustLoad(t, spec)
	ops, diags, err := CollectOperations(doc, Options{Warnings: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("CollectOperations: %v", err)
	}
	// Default-include is true, so an unrecognised value falls through to
	// include — the op is still generated.
	if len(ops) != 1 {
		t.Fatalf("expected one surviving op, got %v", toolNames(ops))
	}
	if !containsDiag(diags, DiagInvalidXMCPValue, "GET /weird") {
		t.Errorf("expected invalid-x-mcp-value warning, got %+v", diags)
	}
}

// helpers

func mustLoad(t *testing.T, spec string) *openapi3.T {
	t.Helper()
	l := openapi3.NewLoader()
	doc, err := l.LoadFromData([]byte(spec))
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate spec: %v", err)
	}
	return doc
}

func toolNames(ops []Operation) []string {
	out := make([]string, len(ops))
	for i, op := range ops {
		out[i] = op.ToolName
	}
	return out
}

func equalSorted(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsDiag(diags []Diagnostic, code, opPath string) bool {
	for _, d := range diags {
		if d.Code == code && strings.Contains(d.Path, opPath) {
			return true
		}
	}
	return false
}
