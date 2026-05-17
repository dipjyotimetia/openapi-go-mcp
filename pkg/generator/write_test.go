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
	"errors"
	"strings"
	"testing"
)

// Write is a public API convenience that streams rendered output to any
// io.Writer. Tests pin both the happy path (text matches Render) and the
// io error propagation (failing writers surface their error).

func TestWrite_StreamsIdenticalToRender(t *testing.T) {
	const spec = `openapi: 3.0.0
info: {title: Write, version: "1"}
paths:
  /x:
    get: {operationId: x, responses: {"200": {description: ok}}}
`
	doc := mustLoad(t, spec)
	opts := Options{ClientImport: "example.com/foo/writeclient"}

	want, err := Render(doc, opts)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	var got bytes.Buffer
	if err := Write(doc, opts, &got); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !bytes.Equal(want, got.Bytes()) {
		t.Errorf("Write output diverged from Render: %d vs %d bytes", len(want), got.Len())
	}
}

// failingWriter returns its sentinel error on every Write call so we can
// verify error propagation through generator.Write.
type failingWriter struct{ err error }

func (f *failingWriter) Write(_ []byte) (int, error) { return 0, f.err }

func TestWrite_PropagatesWriterError(t *testing.T) {
	const spec = `openapi: 3.0.0
info: {title: WriteFail, version: "1"}
paths:
  /x:
    get: {operationId: x, responses: {"200": {description: ok}}}
`
	doc := mustLoad(t, spec)
	sentinel := errors.New("simulated write failure")
	err := Write(doc, Options{ClientImport: "example.com/foo/failclient"}, &failingWriter{err: sentinel})
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel write error to surface; got %v", err)
	}
}

func TestWrite_RenderErrorShortCircuits(t *testing.T) {
	// A missing ClientImport causes Render to fail before any bytes are
	// produced; Write should surface that error rather than silently
	// writing nothing.
	doc := mustLoad(t, `openapi: 3.0.0
info: {title: X, version: "1"}
paths:
  /x:
    get: {operationId: x, responses: {"200": {description: ok}}}
`)
	var buf bytes.Buffer
	err := Write(doc, Options{}, &buf) // empty ClientImport → normalize fails
	if err == nil {
		t.Fatal("expected error from Write when Render fails, got nil")
	}
	if !strings.Contains(err.Error(), "ClientImport") {
		t.Errorf("expected ClientImport error; got %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("no bytes should be written on render failure; got %d bytes", buf.Len())
	}
}
