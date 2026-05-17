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
	"strings"
	"testing"
)

func TestListOperations_PrintsSortedTable(t *testing.T) {
	// The -list CLI flag pipes its output to a shell; the table must be
	// deterministic for diff-friendly CI consumption. Verify by feeding a
	// spec with paths/methods in non-sorted order and asserting the header
	// + at least one expected row.
	const spec = `openapi: 3.0.0
info: {title: ListMe, version: "1"}
paths:
  /z-last:
    get:
      operationId: zLast
      responses: {"200": {description: ok}}
  /a-first:
    post:
      operationId: aFirst
      responses: {"200": {description: ok}}
    get:
      operationId: getA
      responses: {"200": {description: ok}}
`
	doc := mustLoad(t, spec)
	var buf bytes.Buffer
	ListOperations(&buf, doc)
	out := buf.String()

	if !strings.HasPrefix(out, "METHOD") {
		t.Errorf("expected header row first, got %q", firstLine(out))
	}
	// /a-first must appear before /z-last (alphabetical path sort).
	aIdx := strings.Index(out, "/a-first")
	zIdx := strings.Index(out, "/z-last")
	if aIdx < 0 || zIdx < 0 || aIdx >= zIdx {
		t.Errorf("paths not sorted lexicographically; output:\n%s", out)
	}
	// Within /a-first the methods are sorted: GET before POST.
	getIdx := strings.Index(out, "getA")
	postIdx := strings.Index(out, "aFirst")
	if getIdx < 0 || postIdx < 0 || getIdx >= postIdx {
		t.Errorf("methods not sorted within path; output:\n%s", out)
	}
}

func TestListOperations_RendersMissingPlaceholder(t *testing.T) {
	// Operations without an operationId print "(missing)" so spec authors
	// can spot the gap at a glance.
	const spec = `openapi: 3.0.0
info: {title: NoOpID, version: "1"}
paths:
  /thing:
    get:
      responses: {"200": {description: ok}}
`
	doc := mustLoad(t, spec)
	var buf bytes.Buffer
	ListOperations(&buf, doc)
	if !strings.Contains(buf.String(), "(missing)") {
		t.Errorf("missing operationId should render placeholder; got:\n%s", buf.String())
	}
}

func TestBodyKindForContentType_AllBranches(t *testing.T) {
	// Exercises every branch of the switch so the -prefer-content-type flag
	// behaves predictably for any spec-declared content type.
	cases := []struct {
		ct   string
		want BodyKind
	}{
		{"application/json", BodyJSON},
		{"application/problem+json", BodyJSON},
		{"application/x-www-form-urlencoded", BodyForm},
		{"multipart/form-data", BodyMultipart},
		{"application/octet-stream", BodyOctet},
		{"text/plain", BodyText},
		{"text/csv", BodyText},
		{"application/xml", BodyRaw},
		{"application/x-protobuf", BodyRaw},
		{"", BodyRaw},
	}
	for _, tc := range cases {
		t.Run(tc.ct, func(t *testing.T) {
			if got := bodyKindForContentType(tc.ct); got != tc.want {
				t.Errorf("bodyKindForContentType(%q) = %q; want %q", tc.ct, got, tc.want)
			}
		})
	}
}
