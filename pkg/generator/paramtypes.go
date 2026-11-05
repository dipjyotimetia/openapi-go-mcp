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
	"path"
	"regexp"
	"strings"
	"unicode"

	"github.com/getkin/kin-openapi/openapi3"
)

const componentSchemaRefPrefix = "#/components/schemas/"

// GoTypeExpr renders the parameter's Go type as it must appear in generated
// companion code. Types that oapi-codegen declares in the client package
// (enums, x-go-type-name, $ref'd components) are qualified with clientAlias.
func (f ParamField) GoTypeExpr(clientAlias string) string {
	t := f.GoType
	if f.GoTypeQualified {
		t = clientAlias + "." + t
	}
	return strings.Repeat("[]", f.GoTypeSliceDepth) + t
}

// applyPathParamGoType sets f's Go type to the one oapi-codegen uses for the
// positional path argument of the typed client method. Path params are the
// only parameters the generated code declares as standalone locals, so their
// type must match the client signature exactly; query/header params decode
// into the client's own <Op>Params struct and need no type resolution.
//
// opBase is the PascalCase operation name without the "WithResponse" suffix.
// opLevel reports whether the parameter is declared on the Operation (as
// opposed to the PathItem): oapi-codegen only emits <Op>Params<Name> enum
// types for operation-level parameters and keeps PathItem-level inline enums
// as their primitive type.
func applyPathParamGoType(f *ParamField, opBase string, opLevel bool) {
	inlineName := ""
	if opLevel {
		inlineName = opBase + "Params" + PascalCase(f.Name)
	}
	r := resolveParamGoType(f.Schema, inlineName)
	f.GoType = r.name
	f.GoTypeQualified = r.qualified
	f.GoTypeSliceDepth = r.sliceDepth
	f.GoTypeImport = r.importPath
	f.GoTypeImportAlias = r.importAlias
}

type paramGoType struct {
	name        string
	qualified   bool
	sliceDepth  int
	importPath  string
	importAlias string
}

// resolveParamGoType mirrors oapi-codegen v2's type selection for a
// parameter schema. inlineName is the type name oapi-codegen derives for an
// inline enum (<Op>Params<Name>); array items reuse it unchanged. An empty
// inlineName means inline enums keep their primitive type.
func resolveParamGoType(ref *openapi3.SchemaRef, inlineName string) paramGoType {
	if ref == nil || ref.Value == nil {
		return paramGoType{name: "string"}
	}
	s := ref.Value

	if ref.Ref != "" {
		if comp, ok := strings.CutPrefix(ref.Ref, componentSchemaRefPrefix); ok && !isPlainPrimitive(s) {
			// oapi-codegen emits a named type (or alias) for every component
			// schema and references it by name. Plain primitives are aliases
			// (`type PetId = int64`), so the builtin stays interchangeable and
			// is kept to avoid churning existing output.
			return paramGoType{name: componentTypeName(comp), qualified: true}
		}
		if !strings.HasPrefix(ref.Ref, "#/") {
			// External refs are mapped through oapi-codegen import mappings
			// we cannot see; keep the best-effort primitive mapping.
			t, imp := goTypeForSchema(ref)
			return paramGoType{name: t, importPath: imp}
		}
	}

	if goType := extString(s.Extensions, "x-go-type"); goType != "" {
		path, alias := extImport(s.Extensions)
		return paramGoType{name: goType, qualified: !isQualifiedOrBuiltinGoType(goType), importPath: path, importAlias: alias}
	}
	if name := extString(s.Extensions, "x-go-type-name"); name != "" {
		return paramGoType{name: name, qualified: true}
	}

	types := normaliseTypes(s.Type)
	if len(types) > 0 && types[0] == "array" {
		inner := resolveParamGoType(s.Items, inlineName)
		inner.sliceDepth++
		return inner
	}
	if inlineName != "" && len(s.Enum) > 0 && len(types) > 0 && isPrimitiveType(types[0]) {
		return paramGoType{name: inlineName, qualified: true}
	}
	t, imp := goTypeForSchema(ref)
	return paramGoType{name: t, importPath: imp}
}

// isPlainPrimitive reports whether s is a primitive schema oapi-codegen
// renders as a type alias of a builtin (no enum, no Go-type overrides).
func isPlainPrimitive(s *openapi3.Schema) bool {
	types := normaliseTypes(s.Type)
	if len(types) == 0 || !isPrimitiveType(types[0]) {
		return false
	}
	return len(s.Enum) == 0 &&
		extString(s.Extensions, "x-go-type") == "" &&
		extString(s.Extensions, "x-go-type-name") == ""
}

func isPrimitiveType(t string) bool {
	switch t {
	case "string", "integer", "number", "boolean":
		return true
	}
	return false
}

// componentTypeName mirrors oapi-codegen's SchemaNameToTypeName.
func componentTypeName(name string) string {
	if name == "$" {
		return "DollarSign"
	}
	n := PascalCase(name)
	if n != "" && unicode.IsDigit(rune(n[0])) {
		n = "N" + n
	}
	return n
}

var goPredeclaredTypes = map[string]struct{}{
	"any": {}, "bool": {}, "byte": {}, "complex64": {}, "complex128": {},
	"error": {}, "float32": {}, "float64": {}, "int": {}, "int8": {},
	"int16": {}, "int32": {}, "int64": {}, "rune": {}, "string": {},
	"uint": {}, "uint8": {}, "uint16": {}, "uint32": {}, "uint64": {},
	"uintptr": {},
}

// isQualifiedOrBuiltinGoType reports whether an x-go-type value can be used
// verbatim outside the client package. A bare non-builtin identifier refers
// to a type declared in the oapi-codegen package and must be qualified.
func isQualifiedOrBuiltinGoType(t string) bool {
	if _, ok := goPredeclaredTypes[t]; ok {
		return true
	}
	return strings.ContainsAny(t, ".[]*{} ")
}

// extString reads a string-valued vendor extension. kin-openapi surfaces
// extension values either already decoded or as json.RawMessage.
func extString(ext map[string]any, key string) string {
	raw, ok := ext[key]
	if !ok {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case json.RawMessage:
		var s string
		if json.Unmarshal(v, &s) == nil {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// extImport reads x-go-type-import ({path, name}) or the plain-string form.
func extImport(ext map[string]any) (path, alias string) {
	raw, ok := ext["x-go-type-import"]
	if !ok {
		return "", ""
	}
	if rm, isRaw := raw.(json.RawMessage); isRaw {
		var decoded any
		if json.Unmarshal(rm, &decoded) != nil {
			return "", ""
		}
		raw = decoded
	}
	switch v := raw.(type) {
	case string:
		return v, ""
	case map[string]any:
		p, _ := v["path"].(string)
		n, _ := v["name"].(string)
		return p, n
	}
	return "", ""
}

// declaredOn reports whether p is one of the operation-level parameters.
// kin-openapi shares *Parameter pointers between a $ref and its target, so
// pointer identity distinguishes Operation- from PathItem-level declarations.
func declaredOn(params openapi3.Parameters, p *openapi3.Parameter) bool {
	if p == nil {
		return false
	}
	for _, ref := range params {
		if ref != nil && ref.Value == p {
			return true
		}
	}
	return false
}

// handlerIdents are identifiers the companion handler template declares or
// references inside each tool closure. A path-param local with one of these
// names would shadow it (e.g. `var resp string` breaks `resp, err := ...`).
var handlerIdents = map[string]struct{}{
	"ctx": {}, "req": {}, "c": {}, "s": {}, "cfg": {}, "cancel": {},
	"opts": {}, "o": {}, "provider": {}, "resp": {}, "err": {},
	"params": {}, "body": {}, "contentType": {}, "cookieValues": {},
	"cookieEditor": {}, "param": {}, "present": {}, "headerOf": {},
	"runtime": {}, "json": {}, "http": {}, "context": {},
}

// assignPathParamGoVars gives every path param a unique local name that
// doesn't shadow a handler identifier, the client package alias, or a
// package qualifier used by any path param type.
func assignPathParamGoVars(fields []ParamField, clientImport string, unique func(string) string) {
	reserved := map[string]struct{}{}
	clientAlias := path.Base(clientImport)
	reserved[clientAlias] = struct{}{}
	for _, f := range fields {
		expr := strings.TrimLeft(f.GoTypeExpr(clientAlias), "[]")
		if pkg, _, ok := strings.Cut(expr, "."); ok {
			reserved[pkg] = struct{}{}
		}
	}
	for i := range fields {
		base := fields[i].GoVar
		_, isHandler := handlerIdents[base]
		_, isPkg := reserved[base]
		if isHandler || isPkg {
			base += "_"
		}
		fields[i].GoVar = unique(base)
	}
}

// jsonBodyTag mirrors oapi-codegen's request-body name tag for a JSON media
// type: "JSON" for exactly application/json, otherwise the media type in
// CamelCase with initialisms ("application/vnd.api+json" →
// "ApplicationVndAPIPlusJSON"). The tag selects both the client method
// (<Op>With<Tag>BodyWithResponse) and the body type (<Op><Tag>RequestBody).
func jsonBodyTag(contentType string) string {
	if contentType == "" || contentType == "application/json" {
		return "JSON"
	}
	camel := PascalCase(strings.ReplaceAll(contentType, "+", "-plus-"))
	parts := camelCaseParts.FindAllString(camel, -1)
	for i, part := range parts {
		if v, ok := oapiInitialisms[strings.ToLower(part)]; ok {
			parts[i] = v
		}
	}
	return strings.Join(parts, "")
}

var camelCaseParts = regexp.MustCompile(`[\p{Lu}\d]+([\p{Ll}\d]+|$)`)

// oapiInitialisms is oapi-codegen's initialism list used when naming
// content-type-specific request bodies.
var oapiInitialisms = func() map[string]string {
	m := map[string]string{}
	for _, w := range []string{
		"ACL", "API", "ASCII", "CPU", "CSS", "DB", "DNS", "EOF", "GUID", "HTML",
		"HTTP", "HTTPS", "ID", "IP", "JSON", "QPS", "RAM", "RPC", "SLA", "SMTP",
		"SQL", "SSH", "TCP", "TLS", "TTL", "UDP", "UI", "GID", "UID", "UUID",
		"URI", "URL", "UTF8", "VM", "XML", "XMPP", "XSRF", "XSS", "SIP", "RTP",
		"AMQP", "TS",
	} {
		m[strings.ToLower(w)] = w
	}
	return m
}()
