// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package runtime

import (
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"
)

type petBody struct {
	Name string `json:"name"`
	Tag  string `json:"tag,omitempty"`
}

func TestDecodeBody(t *testing.T) {
	args := map[string]any{
		"body": map[string]any{"name": "Fido", "tag": "dog"},
	}
	var got petBody
	if err := DecodeBody(args, &got); err != nil {
		t.Fatalf("DecodeBody: %v", err)
	}
	if got.Name != "Fido" || got.Tag != "dog" {
		t.Fatalf("got %+v", got)
	}
}

func TestDecodeBody_Missing(t *testing.T) {
	args := map[string]any{}
	var got petBody
	if err := DecodeBody(args, &got); err != nil {
		t.Fatalf("missing body should be a no-op, got %v", err)
	}
}

func TestDecodePathParam_Int(t *testing.T) {
	args := map[string]any{
		"path": map[string]any{"id": float64(42)},
	}
	var id int64
	if err := DecodePathParam(args, "id", &id); err != nil {
		t.Fatalf("DecodePathParam: %v", err)
	}
	if id != 42 {
		t.Fatalf("got %d", id)
	}
}

func TestDecodePathParam_Missing(t *testing.T) {
	args := map[string]any{"path": map[string]any{}}
	var id int64
	err := DecodePathParam(args, "id", &id)
	if err == nil {
		t.Fatal("expected error for missing path param")
	}
	te, ok := err.(*ToolError)
	if !ok || te.Code != "missing_path_param" {
		t.Fatalf("expected ToolError missing_path_param, got %v", err)
	}
}

func TestNewToolResultBinary(t *testing.T) {
	raw := []byte{0x01, 0x02, 0x03}
	res := NewToolResultBinary(raw, "application/octet-stream")
	want := base64.StdEncoding.EncodeToString(raw)
	if res.Text != want {
		t.Errorf("Text: got %q, want %q", res.Text, want)
	}
	sc, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent: got %T", res.StructuredContent)
	}
	if got := sc["contentType"]; got != "application/octet-stream" {
		t.Errorf("contentType: got %v", got)
	}
	if got := sc["base64"]; got != want {
		t.Errorf("base64: got %v, want %s", got, want)
	}
}

func TestApplyConfig_NamePrefix(t *testing.T) {
	tool := Tool{Name: "getPet", RawInputSchema: []byte(`{"type":"object"}`)}
	cfg := NewConfig()
	WithNamePrefix("v1")(cfg)
	got := ApplyConfig(tool, cfg)
	if got.Name != "v1_getPet" {
		t.Fatalf("got %q", got.Name)
	}
}

func TestApplyConfig_ExtraProperties(t *testing.T) {
	tool := Tool{Name: "getPet", RawInputSchema: []byte(`{"type":"object","properties":{"id":{"type":"string"}}}`)}
	cfg := NewConfig()
	WithExtraProperties(ExtraProperty{Name: "token", Description: "auth", Required: true})(cfg)
	got := ApplyConfig(tool, cfg)
	if want := `"token"`; !contains(string(got.RawInputSchema), want) {
		t.Fatalf("expected schema to contain %s, got %s", want, got.RawInputSchema)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestBuildStringBody(t *testing.T) {
	body, err := BuildStringBody(map[string]any{"body": "hello world"})
	if err != nil {
		t.Fatalf("BuildStringBody: %v", err)
	}
	got, _ := io.ReadAll(body)
	if string(got) != "hello world" {
		t.Fatalf("got %q", got)
	}
}

func TestBuildStringBody_Missing(t *testing.T) {
	body, err := BuildStringBody(map[string]any{})
	if err != nil {
		t.Fatalf("missing body should be empty reader, got %v", err)
	}
	got, _ := io.ReadAll(body)
	if len(got) != 0 {
		t.Fatalf("expected empty reader, got %q", got)
	}
}

func TestBuildStringBody_WrongType(t *testing.T) {
	_, err := BuildStringBody(map[string]any{"body": 42})
	if err == nil {
		t.Fatal("expected ToolError for non-string body")
	}
	te, ok := err.(*ToolError)
	if !ok || te.Code != "invalid_body" {
		t.Fatalf("expected invalid_body ToolError, got %v", err)
	}
}

func TestBuildBase64BytesBody(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte{0xDE, 0xAD, 0xBE, 0xEF})
	body, err := BuildBase64BytesBody(map[string]any{"body": encoded})
	if err != nil {
		t.Fatalf("BuildBase64BytesBody: %v", err)
	}
	got, _ := io.ReadAll(body)
	if string(got) != "\xde\xad\xbe\xef" {
		t.Fatalf("got % x", got)
	}
}

func TestBuildBase64BytesBody_BadBase64(t *testing.T) {
	_, err := BuildBase64BytesBody(map[string]any{"body": "not-base64!@#"})
	if err == nil {
		t.Fatal("expected ToolError for invalid base64")
	}
	te, ok := err.(*ToolError)
	if !ok || te.Code != "invalid_body" {
		t.Fatalf("expected invalid_body ToolError, got %v", err)
	}
}

func TestBuildMultipartBody_FormFields(t *testing.T) {
	args := map[string]any{
		"body": map[string]any{
			"name":  "Fido",
			"count": float64(3),
		},
	}
	contentType, body, err := BuildMultipartBody(args, nil)
	if err != nil {
		t.Fatalf("BuildMultipartBody: %v", err)
	}
	parts := readMultipartParts(t, contentType, body)
	if got := parts["name"]; got != "Fido" {
		t.Fatalf("name part: got %q", got)
	}
	if got := parts["count"]; got != "3" {
		t.Fatalf("count part: got %q", got)
	}
}

func TestBuildMultipartBody_FileField(t *testing.T) {
	fileBytes := []byte{0x01, 0x02, 0x03}
	args := map[string]any{
		"body": map[string]any{
			"caption":    "screenshot",
			"attachment": base64.StdEncoding.EncodeToString(fileBytes),
		},
	}
	contentType, body, err := BuildMultipartBody(args, []RequestFilePart{{Path: "/attachment"}})
	if err != nil {
		t.Fatalf("BuildMultipartBody: %v", err)
	}
	parts := readMultipartParts(t, contentType, body)
	if got := parts["caption"]; got != "screenshot" {
		t.Fatalf("caption part: got %q", got)
	}
	if got := parts["attachment"]; got != string(fileBytes) {
		t.Fatalf("attachment part: got % x", got)
	}
}

func TestBuildMultipartBody_FileFieldBadBase64(t *testing.T) {
	args := map[string]any{
		"body": map[string]any{"attachment": "not-base64!@#"},
	}
	_, _, err := BuildMultipartBody(args, []RequestFilePart{{Path: "/attachment"}})
	if err == nil {
		t.Fatal("expected ToolError for invalid base64 in file field")
	}
	te, ok := err.(*ToolError)
	if !ok || te.Code != "invalid_body" {
		t.Fatalf("expected invalid_body ToolError, got %v", err)
	}
}

func TestBuildMultipartBody_BodyWrongType(t *testing.T) {
	_, _, err := BuildMultipartBody(map[string]any{"body": "string-not-object"}, nil)
	if err == nil {
		t.Fatal("expected ToolError when body is not an object")
	}
}

func TestBuildMultipartBody_MissingBody(t *testing.T) {
	contentType, body, err := BuildMultipartBody(map[string]any{}, nil)
	if err != nil {
		t.Fatalf("missing body should produce empty multipart, got %v", err)
	}
	parts := readMultipartParts(t, contentType, body)
	if len(parts) != 0 {
		t.Fatalf("expected empty multipart, got %d parts", len(parts))
	}
}

func TestBuildMultipartBody_MultipleFileFields_DeterministicOrder(t *testing.T) {
	args := map[string]any{
		"body": map[string]any{
			"primary":   base64.StdEncoding.EncodeToString([]byte{0x01}),
			"secondary": base64.StdEncoding.EncodeToString([]byte{0x02}),
			"caption":   "two files",
		},
	}
	contentType, body, err := BuildMultipartBody(args, []RequestFilePart{{Path: "/primary"}, {Path: "/secondary"}})
	if err != nil {
		t.Fatalf("BuildMultipartBody: %v", err)
	}
	// Reading the multipart should preserve sorted key order. We verify part
	// names appear in alphabetical order because BuildMultipartBody sorts
	// body keys deterministically.
	gotOrder := readMultipartPartNames(t, contentType, body)
	wantOrder := []string{"caption", "primary", "secondary"}
	if len(gotOrder) != len(wantOrder) {
		t.Fatalf("part count mismatch: got %v, want %v", gotOrder, wantOrder)
	}
	for i, want := range wantOrder {
		if gotOrder[i] != want {
			t.Fatalf("part order mismatch at %d: got %v, want %v", i, gotOrder, wantOrder)
		}
	}
}

func TestBuildMultipartBody_FileFieldNotString(t *testing.T) {
	args := map[string]any{
		"body": map[string]any{"attachment": 42},
	}
	_, _, err := BuildMultipartBody(args, []RequestFilePart{{Path: "/attachment"}})
	if err == nil {
		t.Fatal("expected ToolError when file field is not a string")
	}
	te, ok := err.(*ToolError)
	if !ok || te.Code != "invalid_body" {
		t.Fatalf("expected invalid_body ToolError, got %v", err)
	}
}

func TestBuildMultipartBody_NestedFilePath(t *testing.T) {
	fileBytes := []byte{0xDE, 0xAD}
	args := map[string]any{
		"body": map[string]any{
			"user": map[string]any{
				"name":   "alice",
				"avatar": base64.StdEncoding.EncodeToString(fileBytes),
			},
			"caption": "hi",
		},
	}
	contentType, body, err := BuildMultipartBody(args, []RequestFilePart{
		{Path: "/user/avatar"},
	})
	if err != nil {
		t.Fatalf("BuildMultipartBody: %v", err)
	}
	parts := readMultipartParts(t, contentType, body)

	// The avatar must arrive as its own part, base64-decoded back to raw bytes.
	if got := parts["avatar"]; got != string(fileBytes) {
		t.Errorf("avatar part bytes = % x, want % x", got, fileBytes)
	}
	// The residual user object should still be sent — minus the extracted
	// avatar — as a JSON form field.
	if got := parts["user"]; got != `{"name":"alice"}` {
		t.Errorf("user residual part = %q, want %q", got, `{"name":"alice"}`)
	}
	if got := parts["caption"]; got != "hi" {
		t.Errorf("caption part = %q, want %q", got, "hi")
	}
}

func TestBuildMultipartBody_NestedFilePath_EmptyParentOmitted(t *testing.T) {
	fileBytes := []byte{0x01}
	args := map[string]any{
		"body": map[string]any{
			"user": map[string]any{
				"avatar": base64.StdEncoding.EncodeToString(fileBytes),
			},
		},
	}
	contentType, body, err := BuildMultipartBody(args, []RequestFilePart{
		{Path: "/user/avatar"},
	})
	if err != nil {
		t.Fatalf("BuildMultipartBody: %v", err)
	}
	parts := readMultipartParts(t, contentType, body)

	if _, ok := parts["user"]; ok {
		t.Errorf("residual user object should be omitted when empty after extraction; got parts=%v", parts)
	}
	if got := parts["avatar"]; got != string(fileBytes) {
		t.Errorf("avatar part bytes = % x, want % x", got, fileBytes)
	}
}

func TestBuildMultipartBody_NestedFilePath_ParentNotObject(t *testing.T) {
	args := map[string]any{
		"body": map[string]any{"user": "not-an-object"},
	}
	_, _, err := BuildMultipartBody(args, []RequestFilePart{{Path: "/user/avatar"}})
	if err == nil {
		t.Fatal("expected ToolError when nested path's parent is not an object")
	}
	te, ok := err.(*ToolError)
	if !ok || te.Code != "invalid_body" {
		t.Fatalf("expected invalid_body ToolError, got %v", err)
	}
}

func TestBuildMultipartBody_FilePart_ContentTypeOverride(t *testing.T) {
	fileBytes := []byte{0xAA, 0xBB}
	args := map[string]any{
		"body": map[string]any{
			"image": base64.StdEncoding.EncodeToString(fileBytes),
		},
	}
	contentType, body, err := BuildMultipartBody(args, []RequestFilePart{
		{Path: "/image", ContentType: "image/png"},
	})
	if err != nil {
		t.Fatalf("BuildMultipartBody: %v", err)
	}
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("parse content-type: %v", err)
	}
	mr := multipart.NewReader(body, params["boundary"])
	part, err := mr.NextPart()
	if err != nil {
		t.Fatalf("next part: %v", err)
	}
	if got := part.Header.Get("Content-Type"); got != "image/png" {
		t.Errorf("part content-type: got %q, want image/png", got)
	}
}

func TestBuildMultipartBody_FilePart_FieldNameOverride(t *testing.T) {
	fileBytes := []byte{0xCC}
	args := map[string]any{
		"body": map[string]any{
			"image": base64.StdEncoding.EncodeToString(fileBytes),
		},
	}
	contentType, body, err := BuildMultipartBody(args, []RequestFilePart{
		{Path: "/image", FieldName: "renamed"},
	})
	if err != nil {
		t.Fatalf("BuildMultipartBody: %v", err)
	}
	names := readMultipartPartNames(t, contentType, body)
	if len(names) != 1 || names[0] != "renamed" {
		t.Errorf("FieldName override should rename the part: got %v, want [renamed]", names)
	}
}

func TestBuildMultipartBody_FormFieldTypes(t *testing.T) {
	args := map[string]any{
		"body": map[string]any{
			"flag":    true,
			"count":   float64(7),
			"tags":    []any{"a", "b"},
			"nothing": nil,
			"plain":   "abc",
		},
	}
	contentType, body, err := BuildMultipartBody(args, nil)
	if err != nil {
		t.Fatalf("BuildMultipartBody: %v", err)
	}
	parts := readMultipartParts(t, contentType, body)
	cases := map[string]string{
		"flag":    "true",
		"count":   "7",
		"tags":    `["a","b"]`,
		"nothing": "",
		"plain":   "abc",
	}
	for k, want := range cases {
		if got := parts[k]; got != want {
			t.Errorf("part %q: got %q, want %q", k, got, want)
		}
	}
}

func TestBuildBase64BytesBody_Missing(t *testing.T) {
	body, err := BuildBase64BytesBody(map[string]any{})
	if err != nil {
		t.Fatalf("missing body should be empty reader, got %v", err)
	}
	got, _ := io.ReadAll(body)
	if len(got) != 0 {
		t.Fatalf("expected empty reader, got %q", got)
	}
}

func TestBuildBase64BytesBody_WrongType(t *testing.T) {
	_, err := BuildBase64BytesBody(map[string]any{"body": 42})
	if err == nil {
		t.Fatal("expected ToolError when body is not a string")
	}
}

func readMultipartPartNames(t *testing.T, contentType string, body io.Reader) []string {
	t.Helper()
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("parse content-type %q: %v", contentType, err)
	}
	mr := multipart.NewReader(body, params["boundary"])
	var names []string
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next part: %v", err)
		}
		names = append(names, part.FormName())
		_, _ = io.Copy(io.Discard, part)
	}
	return names
}

func readMultipartParts(t *testing.T, contentType string, body io.Reader) map[string]string {
	t.Helper()
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("parse content-type %q: %v", contentType, err)
	}
	boundary := params["boundary"]
	if boundary == "" {
		t.Fatalf("no boundary in content-type %q", contentType)
	}
	mr := multipart.NewReader(body, boundary)
	out := map[string]string{}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next part: %v", err)
		}
		buf := new(strings.Builder)
		if _, err := io.Copy(buf, part); err != nil {
			t.Fatalf("read part: %v", err)
		}
		out[part.FormName()] = buf.String()
	}
	return out
}
