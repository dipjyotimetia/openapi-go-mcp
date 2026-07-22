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
	"fmt"
	"go/token"
	"maps"
	"net/http"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/dipjyotimetia/openapi-go-mcp/pkg/loader"
	"github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime"
)

// OpenAPI parameter "in" values. The spec defines these as enum strings; we
// alias them as constants so generator code can compare without string typos.
const (
	inPath   = "path"
	inQuery  = "query"
	inHeader = "header"
	inCookie = "cookie"
)

// BodyKind classifies how an operation's request body is encoded on the wire.
// The generator dispatches on this to choose which oapi-codegen helper to call
// and how to materialise the body inside the MCP handler.
type BodyKind string

const (
	// BodyNone marks an operation with no request body.
	BodyNone BodyKind = ""
	// BodyJSON marks application/json (or any *+json) — handler decodes into
	// the typed <Op>JSONRequestBody and calls <Op>WithResponse.
	BodyJSON BodyKind = "json"
	// BodyForm marks application/x-www-form-urlencoded — handler decodes into
	// the typed <Op>FormdataRequestBody and calls <Op>WithFormdataBodyWithResponse.
	BodyForm BodyKind = "form"
	// BodyMultipart marks multipart/form-data — handler builds the body via
	// runtime.BuildMultipartBody and calls <Op>WithBodyWithResponse.
	BodyMultipart BodyKind = "multipart"
	// BodyOctet marks application/octet-stream — handler base64-decodes a
	// single body string and calls <Op>WithBodyWithResponse.
	BodyOctet BodyKind = "octet"
	// BodyText marks any text/* content type — handler takes the body as a
	// raw string and calls <Op>WithBodyWithResponse.
	BodyText BodyKind = "text"
	// BodyRaw marks application/xml and any other content type — handler
	// takes the body as a raw string and calls <Op>WithBodyWithResponse.
	BodyRaw BodyKind = "raw"
)

// RequestFilePart aliases runtime.RequestFilePart so the generator and the
// runtime can't drift on the multipart-binary-field shape they exchange.
type RequestFilePart = runtime.RequestFilePart

// Operation is the generator's internal view of one OpenAPI operation,
// pre-resolved into the values the template needs.
type Operation struct {
	// ToolName is the MCP-visible tool name (mangled to fit MaxToolNameLen).
	ToolName string
	// GoName is the Go method name on the oapi-codegen client interface
	// (e.g. FindPetByIDWithResponse). It is the canonical "<Base>WithResponse"
	// name used by paramsTypeName / bodyTypeName helpers; CallMethod records
	// which actual method the handler invokes (may differ for non-JSON bodies).
	GoName string
	// CallMethod is the oapi-codegen client method the generated handler calls.
	// JSON         → <Base>WithResponse
	// Form         → <Base>WithFormdataBodyWithResponse
	// Multipart/…  → <Base>WithBodyWithResponse
	CallMethod string
	// Description is the operation's summary/description, used as the tool
	// description. Operations marked `deprecated: true` get a "Deprecated."
	// prefix because MCP has no native flag.
	Description string
	// Annotations carries the MCP tool hints derived from the HTTP method and
	// summary (see toolAnnotations). Nil when the operation yields none; the
	// template serialises it via annotationsLit.
	Annotations *runtime.ToolAnnotations
	// Method/Path are the HTTP verb and templated path; retained for comments
	// and debug output.
	Method string
	Path   string
	// PathParams are the path-template parameters, in declaration order.
	PathParams []ParamField
	// QueryParams + HeaderParams together populate the oapi-codegen <Op>Params struct.
	QueryParams  []ParamField
	HeaderParams []ParamField
	// CookieParams are OpenAPI 3 `in: cookie` parameters. They do not
	// populate the oapi-codegen <Op>Params struct (oapi-codegen does not
	// emit struct fields for cookies); instead the generator wires them
	// through a RequestEditorFn that calls req.AddCookie() per cookie.
	CookieParams []ParamField
	// HasParamsStruct is true when at least one query or header param exists,
	// meaning oapi-codegen emitted a <Op>Params struct that the typed method
	// expects as an additional argument.
	HasParamsStruct bool
	// RequestBody is the body schema for typed kinds (JSON, Form, Multipart)
	// and nil for raw kinds (Octet, Text, Raw).
	RequestBody         *openapi3.SchemaRef
	RequestBodyRequired bool
	HasRequestBody      bool
	// RequestBodyKind classifies how the body is encoded on the wire.
	RequestBodyKind BodyKind
	// RequestContentType is the spec-declared content-type string. Emitted as
	// a literal into the generated call for raw fallback kinds.
	RequestContentType string
	// RequestFileFields lists JSON-pointer paths into the body object that
	// must be base64-decoded into multipart file parts. Populated only when
	// RequestBodyKind == BodyMultipart; sorted by Path for determinism.
	RequestFileFields []RequestFilePart
	// requestBodyEncoding mirrors the OpenAPI 3 `encoding` map for the
	// selected multipart content type. Populated only for BodyMultipart and
	// only consumed during input-schema lowering; templates don't see it.
	requestBodyEncoding openapi3.Encodings
	// ResponseKind classifies the chosen response content type (BodyJSON,
	// BodyText, BodyOctet, BodyRaw). BodyNone means the operation has no
	// response body (e.g. 204 No Content) — the handler still wraps via
	// NewToolResultJSON so an empty Body becomes an empty result.
	ResponseKind BodyKind
	// ResponseContentType is the spec-declared response content-type string,
	// emitted as a literal into NewToolResultBinary for non-JSON responses.
	ResponseContentType string
	// InputSchemaJSON is the encoded JSON Schema for the tool's input.
	InputSchemaJSON string
	// InputSchemaOpenAIJSON is the OpenAI-compatible form of InputSchemaJSON.
	// It is populated alongside the default schema unless OpenAICompat was
	// explicitly requested, which preserves the legacy single-schema output.
	InputSchemaOpenAIJSON string
	// OutputSchemaJSON is the encoded JSON Schema for the tool's output,
	// lowered from the operation's selected 2xx JSON response schema. Empty
	// when the response is not JSON or its root is not an object (MCP output
	// schemas must be objects). Note the schema describes the success payload
	// only — error results (IsError=true) carry the runtime's
	// {"status","headers","body"} envelope instead.
	OutputSchemaJSON string
	// Security lists the OpenAPI security alternatives (OR) and schemes
	// within each alternative (AND) the proxy must satisfy. It is populated
	// only in ModeProxy; companion mode leaves it nil.
	Security [][]SecurityScheme
	// Anonymous is true when the operation is explicitly callable without
	// credentials (operation-level `security: [{}]` or no security
	// declared anywhere). Proxy template uses this to skip auth wiring
	// entirely rather than producing a "credential not set" error.
	Anonymous bool
	// AuthRequired distinguishes an absent/explicitly anonymous security
	// declaration from a protected operation whose declared schemes could not
	// be lowered. The latter must fail closed at call time.
	AuthRequired bool
}

// ParamField is a single OpenAPI parameter described enough to render Go code
// and the matching JSON Schema entry.
type ParamField struct {
	Name         string // OpenAPI name, e.g. "petId"
	GoVar        string // Go local variable name, e.g. "petId"
	GoType       string // Go type, e.g. "int64", "openapi_types.UUID"
	GoTypeImport string // import path required for GoType (empty for builtins)
	Required     bool
	Schema       *openapi3.SchemaRef // original parameter schema, used to build the input schema
	// Description/Example/Deprecated carry Parameter-object-level metadata
	// (as opposed to metadata on the parameter's schema). Merged into the
	// tool input schema when the schema itself doesn't already declare the
	// corresponding keyword.
	Description string
	Example     any
	Deprecated  bool
}

// CollectOperations walks the spec and returns the Operations to generate, in
// a deterministic order, alongside any non-fatal diagnostics produced during
// the walk. Each operation is rendered with its own schema converter so $defs
// are self-contained per tool. opts controls the JSON-Schema dialect, the
// preferred request content-type, and where legacy text warnings are sent.
//
// Returns an error if any operation cannot be lowered (e.g. unknown request
// body kind) so the caller can fail fast. Diagnostics are returned even on
// error so partial results can be inspected.
func CollectOperations(doc *openapi3.T, opts Options) ([]Operation, []Diagnostic, error) {
	var ops []Operation
	warnings := opts.Warnings
	if warnings == nil {
		warnings = os.Stderr
	}
	sink := newDiagSink(warnings)

	// Spec-wide diagnostics: webhooks/server variables/security requirements
	// at the document level. Emitted before the Paths nil-check so a
	// webhooks-only 3.1 document still surfaces its findings. Proxy mode
	// consumes the security requirement directly (env-var-driven auth), so we
	// skip the informational "supply credentials manually" diagnostic in that
	// mode — leaving it in would mislead users into thinking they still need
	// to wire auth themselves.
	emitSpecDiagnostics(doc, sink, opts.Mode)

	if doc.Paths == nil {
		return ops, sink.finalize(), nil
	}

	// Proxy mode reads the spec's securitySchemes and wires auth into the
	// generated code. Companion mode emits an info diagnostic instead
	// (handled by emitSpecDiagnostics / buildOperation below) and leaves
	// the parsed schemes unused.
	var parsedSchemes []SecurityScheme
	if opts.Mode == ModeProxy {
		parsedSchemes = ParseSecuritySchemes(doc, sink)
	}

	// Pre-compute the component-schema name map once; every per-operation
	// converter reuses it via Adopt.
	template := NewSchemaConverter(opts.OpenAICompat)
	template.Bind(doc)
	nameByPtr := template.NameByPtr()

	paths := doc.Paths.Map()
	pathKeys := make([]string, 0, len(paths))
	for path := range paths {
		pathKeys = append(pathKeys, path)
	}
	sort.Strings(pathKeys)

	defaultInclude := !opts.ExcludeByDefault
	for _, path := range pathKeys {
		item := paths[path]
		opByMethod := item.Operations()
		methods := make([]string, 0, len(opByMethod))
		for m := range opByMethod {
			methods = append(methods, m)
		}
		sort.Strings(methods)
		for _, method := range methods {
			specOp := opByMethod[method]
			opPath := fmt.Sprintf("%s %s", method, path)
			if !resolveXMCPInclusion(doc.Extensions, item.Extensions, specOp.Extensions, defaultInclude, opPath, sink) {
				continue
			}
			conv := NewSchemaConverter(opts.OpenAICompat)
			conv.Adopt(nameByPtr)
			op, err := buildOperation(item, specOp, method, path, conv, opts, sink)
			if err != nil {
				return nil, sink.finalize(), fmt.Errorf("%s %s: %w", method, path, err)
			}
			if opts.Mode == ModeProxy {
				policy := ResolveSecurityPolicy(specOp, doc, parsedSchemes)
				op.Security = policy.Alternatives
				op.Anonymous = policy.Anonymous
				op.AuthRequired = policy.Required
			}
			if !opts.OpenAICompat {
				openAIConv := NewSchemaConverter(true)
				openAIConv.Adopt(nameByPtr)
				openAISchema, _, err := buildInputSchema(op, openAIConv)
				if err != nil {
					return nil, sink.finalize(), fmt.Errorf("%s %s: build OpenAI input schema: %w", method, path, err)
				}
				op.InputSchemaOpenAIJSON = openAISchema
			}
			ops = append(ops, op)
		}
	}
	return ops, sink.finalize(), nil
}

// resolveXMCPInclusion is the CollectOperations adapter around
// includeOperation: it converts the (value, level, recognised) tuple into
// an include/skip decision and routes informative diagnostics through the
// sink so spec authors see exactly which level drove the choice. Returns
// true when the operation should be generated.
func resolveXMCPInclusion(rootExts, pathExts, opExts map[string]any, defaultInclude bool, opPath string, sink *diagSink) bool {
	include, level, ok := includeOperation(rootExts, pathExts, opExts, defaultInclude)
	if !ok {
		// Unrecognised x-mcp value at `level`; surface the typo and fall
		// through to the document-wide default the user intended.
		sink.warn(DiagInvalidXMCPValue, opPath,
			fmt.Sprintf("x-mcp extension at %s level is not a boolean; falling back to the document default (%v)", level, defaultInclude))
		return defaultInclude
	}
	if !include {
		// Info, not warning: x-mcp:false is the spec author asking us to
		// skip this operation — exactly the documented behaviour.
		reason := "default"
		if level != xmcpLevelDefault {
			reason = fmt.Sprintf("x-mcp:false at %s level", level)
		}
		sink.info(DiagExcludedByXMCP, opPath, fmt.Sprintf("operation excluded from MCP tool generation (%s)", reason))
		return false
	}
	return true
}

// emitSpecDiagnostics records spec-wide findings that don't belong to a
// single operation: server-variable substitutions the runtime can't perform,
// and security requirements without an explicit credential consumer.
//
// The mode argument lets us suppress the "supply credentials manually"
// security advisory in proxy mode — proxy mode generates the auth wiring
// from securitySchemes automatically, so the advisory would mislead users
// into doing redundant work.
func emitSpecDiagnostics(doc *openapi3.T, sink *diagSink, mode Mode) {
	if len(doc.Webhooks) > 0 {
		names := slices.Sorted(maps.Keys(doc.Webhooks))
		sink.warn(DiagDroppedWebhook, "#/webhooks",
			"webhooks describe requests the API makes to consumers and are not modelled as MCP tools; dropped: "+strings.Join(names, ", "))
	}
	for i, server := range doc.Servers {
		if len(server.Variables) > 0 {
			sink.info(DiagDroppedServerVariables,
				fmt.Sprintf("servers[%d]", i),
				fmt.Sprintf("server URL %q declares variables; supply substitutions via runtime.WithServerVariables when constructing the upstream client", server.URL))
		}
	}
	if mode == ModeProxy {
		// Proxy mode owns the credential plumbing; the "drop" diagnostic
		// would be incorrect.
		return
	}
	if len(doc.Security) > 0 {
		sink.info(DiagDroppedSecurityRequirement,
			"#/security",
			"global security requirement is informational; supply credentials via runtime.WithExtraProperties or an HTTP client request editor. Schemes referenced: "+strings.Join(dedupSchemeNames(doc.Security), ", "))
	}
}

// dedupSchemeNames returns the alphabetically-sorted, deduplicated set of
// security-scheme names referenced anywhere in reqs. OpenAPI's
// SecurityRequirements is a slice of maps (the outer slice is "or",
// the inner map is "and"); we flatten and dedupe for diagnostic output.
func dedupSchemeNames(reqs openapi3.SecurityRequirements) []string {
	seen := map[string]struct{}{}
	for _, req := range reqs {
		for name := range req {
			seen[name] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

var pathParamRe = regexp.MustCompile(`\{([^}]+)\}`)

func buildOperation(item *openapi3.PathItem, op *openapi3.Operation, method, path string, conv *SchemaConverter, opts Options, sink *diagSink) (Operation, error) {
	goName := goMethodName(op.OperationID, method, path)
	out := Operation{
		ToolName:    ToolName(op.OperationID, method, path),
		GoName:      goName,
		CallMethod:  goName,
		Description: chooseDescription(op),
		Annotations: toolAnnotations(method, op.Summary),
		Method:      method,
		Path:        path,
	}
	opPath := fmt.Sprintf("%s %s", method, path)

	mergedParams := mergeParametersWithShadowWarning(item.Parameters, op.Parameters, opPath, sink)
	paramByIn := groupParameters(mergedParams)
	pathGoVar := newGoVarUniquer()

	for _, m := range pathParamRe.FindAllStringSubmatch(path, -1) {
		name := m[1]
		p, ok := paramByIn[inPath][name]
		if !ok {
			// Spec declared a {param} in the URL template but no matching
			// parameters[].in=path entry. kin-openapi validation should catch
			// this earlier; record it as a diagnostic for visibility.
			sink.warn(DiagMissingPathParam, opPath,
				fmt.Sprintf("path parameter %q is referenced in the URL but has no parameter definition; treating as a required string", name))
		}
		f := paramFieldFromSpec(name, p, true)
		f.GoVar = pathGoVar(f.GoVar)
		out.PathParams = append(out.PathParams, f)
		if p != nil {
			emitParameterStyleDiagnostic(p, opPath, sink)
		}
	}
	out.QueryParams = collectParamsWithDiagnostics(paramByIn[inQuery], opPath, sink)
	out.HeaderParams = collectParamsWithDiagnostics(paramByIn[inHeader], opPath, sink)
	out.CookieParams = collectParamsWithDiagnostics(paramByIn[inCookie], opPath, sink)
	out.HasParamsStruct = len(out.QueryParams)+len(out.HeaderParams) > 0

	if len(op.Callbacks) > 0 {
		names := make([]string, 0, len(op.Callbacks))
		for name := range op.Callbacks {
			names = append(names, name)
		}
		sort.Strings(names)
		sink.warn(DiagDroppedCallback, opPath,
			"callbacks are not modelled as MCP tools; dropped: "+strings.Join(names, ", "))
	}
	if op.Security != nil && len(*op.Security) > 0 && opts.Mode != ModeProxy {
		// Proxy mode wires this automatically from env vars; the diagnostic
		// would mislead the user into doing redundant manual work.
		sink.info(DiagDroppedSecurityRequirement, opPath,
			"per-operation security requirement is informational; supply credentials via runtime.WithExtraProperties / request editor. Schemes: "+strings.Join(dedupSchemeNames(*op.Security), ", "))
	}

	if op.RequestBody != nil && op.RequestBody.Value != nil {
		body := op.RequestBody.Value
		out.RequestBodyRequired = body.Required
		if len(body.Content) > 0 {
			kind, ct, schema := pickRequestContent(body.Content, opts.PreferContentType)
			out.HasRequestBody = true
			out.RequestBodyKind = kind
			out.RequestContentType = ct
			out.CallMethod = callMethodFor(goName, kind)
			// Typed kinds keep the schema for input-schema lowering and
			// (multipart) binary-field rewriting. Raw kinds intentionally
			// drop the spec schema — the MCP input is a single base64 /
			// plain-text string regardless of what the body looks like on
			// the wire.
			switch kind {
			case BodyJSON, BodyForm, BodyMultipart:
				out.RequestBody = schema
			case BodyOctet, BodyText, BodyRaw:
				out.RequestBody = nil
			default:
				return out, fmt.Errorf("unhandled body kind %q for content types %v", kind, contentKeys(body.Content))
			}
			if kind == BodyMultipart {
				if mt := body.Content[ct]; mt != nil {
					out.requestBodyEncoding = mt.Encoding
				}
			}
			if kind != BodyJSON && hasContentTypeHeaderParam(out.HeaderParams) {
				sink.warn(DiagContentTypeHeaderOverride, opPath,
					fmt.Sprintf("Content-Type header parameter is silently overridden by the %s request body", ct))
			}
		}
	}

	pick := pickResponseContent(op.Responses)
	out.ResponseKind, out.ResponseContentType = pick.Kind, pick.ContentType
	emitDroppedLinkDiagnostic(op.Responses, opPath, sink)

	// Lower the response schema into the tool's output schema — but only when
	// it plausibly describes the success payload: an explicit 2xx response, or
	// a "default" response on an operation that declares no 2xx at all. A
	// "default" next to a contentless 2xx (the classic 204 + default-error
	// pattern) is the error branch and must not be advertised as the output.
	if pick.Kind == BodyJSON && (pick.Code != "default" || !pick.Has2xx) {
		outputSchema, err := buildOutputSchema(pick.Schema, conv)
		if err != nil {
			return out, err
		}
		out.OutputSchemaJSON = outputSchema
	}

	schema, fileFields, err := buildInputSchema(out, conv)
	if err != nil {
		return out, err
	}
	out.InputSchemaJSON = schema
	out.RequestFileFields = fileFields
	emitNestedMultipartEncodingDiagnostics(fileFields, out.requestBodyEncoding, opPath, sink)
	return out, nil
}

// emitNestedMultipartEncodingDiagnostics surfaces OpenAPI `encoding` entries
// whose key names a nested binary field. Encoding keys address top-level
// multipart properties only, so such metadata cannot reach the part it names
// and the runtime default per-part content type applies. Derived post-hoc
// from the walk's returned parts: a RequestFilePart with a multi-segment
// JSON-pointer Path is a nested leaf, and its last segment is the name an
// encoding key would (mistakenly) target.
func emitNestedMultipartEncodingDiagnostics(parts []RequestFilePart, encoding openapi3.Encodings, opPath string, sink *diagSink) {
	if len(encoding) == 0 || sink == nil {
		return
	}
	for _, part := range parts {
		segments := strings.Split(strings.TrimPrefix(part.Path, "/"), "/")
		if len(segments) < 2 {
			continue // top-level part: encoding metadata applies and was consumed
		}
		leaf := segments[len(segments)-1]
		if enc, ok := encoding[leaf]; ok && enc != nil {
			sink.warn(DiagNestedMultipartEncoding, opPath,
				fmt.Sprintf("encoding metadata %q does not apply to the nested binary field at %q; OpenAPI encoding keys address top-level multipart properties only, the part falls back to the runtime default content type", leaf, part.Path))
		}
	}
}

// pickResponseContent walks an operation's responses to pick the canonical
// response content type — the one the generated handler will wrap. The
// chosen response is the 2xx with the lowest status code; if none of those
// declare a content map, "default" is consulted; if still nothing is found,
// BodyNone is returned (NewToolResultJSON happily wraps an empty body).
//
// Within the chosen response, the same priority order pickRequestContent
// uses applies (JSON → form → multipart → octet → text → xml → other), so
// JSON responses keep their dedicated wrapper and the rest dispatch to
// text/binary wrappers downstream.
func pickResponseContent(responses *openapi3.Responses) responsePick {
	if responses == nil {
		return responsePick{Kind: BodyNone}
	}
	respMap := responses.Map()
	if len(respMap) == 0 {
		return responsePick{Kind: BodyNone}
	}
	// Sort status codes; pick the smallest 2xx first, then "default".
	codes := make([]string, 0, len(respMap))
	for c := range respMap {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	tryOrder := make([]string, 0, len(codes))
	for _, c := range codes {
		if len(c) == 3 && c[0] == '2' {
			tryOrder = append(tryOrder, c)
		}
	}
	has2xx := len(tryOrder) > 0
	for _, c := range codes {
		if c == "default" {
			tryOrder = append(tryOrder, c)
		}
	}
	for _, code := range tryOrder {
		ref := respMap[code]
		if ref == nil || ref.Value == nil || len(ref.Value.Content) == 0 {
			continue
		}
		kind, ct, schema := pickRequestContent(ref.Value.Content, "")
		if kind != BodyNone {
			return responsePick{Kind: kind, ContentType: ct, Schema: schema, Code: code, Has2xx: has2xx}
		}
	}
	return responsePick{Kind: BodyNone, Has2xx: has2xx}
}

// responsePick is pickResponseContent's result: the chosen response content
// type, the media type's schema (nil when it declares none), the status code
// it came from ("" when nothing matched), and whether the operation declares
// any explicit 2xx response at all — the last lets callers tell a
// default-as-only-response apart from a default-as-error-branch.
type responsePick struct {
	Kind        BodyKind
	ContentType string
	Schema      *openapi3.SchemaRef
	Code        string
	Has2xx      bool
}

// isObjectSchemaRef reports whether the schema describes a JSON object: an
// explicit single `type: object`, object-implying keywords (properties /
// additionalProperties), or an allOf composition with at least one object
// branch. seen guards against reference cycles. A schema with any explicit
// non-object type (including nullable unions like ["object","null"]) is not
// treated as a plain object — MCP SDKs require an unambiguous object root.
func isObjectSchemaRef(ref *openapi3.SchemaRef, seen map[*openapi3.Schema]bool) bool {
	if ref == nil || ref.Value == nil {
		return false
	}
	s := ref.Value
	if seen[s] {
		return false
	}
	seen[s] = true
	if types := normaliseTypes(s.Type); len(types) > 0 {
		return len(types) == 1 && types[0] == "object" && !s.Nullable
	}
	if len(s.Properties) > 0 || s.AdditionalProperties.Has != nil || s.AdditionalProperties.Schema != nil {
		return !s.Nullable
	}
	for _, branch := range s.AllOf {
		if isObjectSchemaRef(branch, seen) {
			return true
		}
	}
	return false
}

// buildOutputSchema lowers the selected 2xx JSON response schema into the
// tool's MCP output schema. Returns "" (no output schema) when the response
// declares no schema or when its root is not a JSON object — both MCP SDKs
// require output schemas to have root type "object", and array/scalar
// responses have no faithful object representation.
//
// The emitted schema describes the success payload only. Error results
// (IsError=true) carry the runtime's {"status","headers","body"} envelope in
// StructuredContent, and empty 2xx bodies (e.g. 204) carry none — a note in
// the schema description tells schema-aware clients what to expect.
func buildOutputSchema(ref *openapi3.SchemaRef, inputConv *SchemaConverter) (string, error) {
	if ref == nil || ref.Value == nil {
		return "", nil
	}
	if !isObjectSchemaRef(ref, map[*openapi3.Schema]bool{}) {
		return "", nil
	}
	// A separate converter (same dialect, shared component-name map) keeps the
	// output schema's $defs independent of the input schema's — each is a
	// self-contained JSON Schema document.
	conv := NewSchemaConverter(inputConv.OpenAICompat)
	conv.Adopt(inputConv.NameByPtr())
	root := conv.Convert(ref)
	defs := conv.Defs()
	if ptr, ok := root["$ref"].(string); ok {
		// Convert hoisted the named component into $defs and returned a bare
		// $ref root. MCP SDKs require the root to declare type "object", so
		// re-anchor the reference under allOf.
		root = map[string]any{
			"type":  "object",
			"allOf": []any{map[string]any{"$ref": ptr}},
		}
	} else if t, _ := root["type"].(string); t != "object" {
		if _, has := root["type"]; has {
			// A type array like ["object","null"] fails the go-sdk's strict
			// root check; skip rather than silently tighten the contract.
			return "", nil
		}
		// Inline allOf-composed root with no explicit type — object-ness was
		// established above, and MCP SDKs require it declared.
		root["type"] = "object"
	}
	appendDescription(root, "Describes the tool's success payload. Error results (isError=true) instead carry a {status, headers, body} envelope; empty upstream bodies produce no structured content.")
	if len(defs) > 0 {
		root["$defs"] = defs
	}
	buf, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal output schema: %w", err)
	}
	return string(buf), nil
}

// emitDroppedLinkDiagnostic surfaces OpenAPI response `links` objects, which
// describe operation-to-operation relationships MCP tools have no channel
// for. The tool itself is generated normally; only the link metadata is
// dropped, and this diagnostic makes that drop visible. One diagnostic per
// operation, naming every "<status>.<linkName>" pair in sorted order.
func emitDroppedLinkDiagnostic(responses *openapi3.Responses, opPath string, sink *diagSink) {
	if responses == nil || sink == nil {
		return
	}
	var dropped []string
	respMap := responses.Map()
	for _, code := range slices.Sorted(maps.Keys(respMap)) {
		ref := respMap[code]
		if ref == nil || ref.Value == nil || len(ref.Value.Links) == 0 {
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(ref.Value.Links)) {
			dropped = append(dropped, code+"."+name)
		}
	}
	if len(dropped) > 0 {
		sink.warn(DiagDroppedLink, opPath,
			"response links are not modelled by MCP tools; dropped: "+strings.Join(dropped, ", "))
	}
}

func groupParameters(params openapi3.Parameters) map[string]map[string]*openapi3.Parameter {
	out := make(map[string]map[string]*openapi3.Parameter)
	for _, ref := range params {
		if ref == nil || ref.Value == nil {
			continue
		}
		p := ref.Value
		if out[p.In] == nil {
			out[p.In] = map[string]*openapi3.Parameter{}
		}
		out[p.In][p.Name] = p
	}
	return out
}

func collectParams(in map[string]*openapi3.Parameter) []ParamField {
	if len(in) == 0 {
		return nil
	}
	out := make([]ParamField, 0, len(in))
	for name, p := range in {
		out = append(out, paramFieldFromSpec(name, p, p.Required))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// collectParamsWithDiagnostics is collectParams plus per-parameter style/explode
// diagnostics. Kept as a separate path so callers that don't want
// diagnostics (tests, future tooling) can still call collectParams cheaply.
func collectParamsWithDiagnostics(in map[string]*openapi3.Parameter, opPath string, sink *diagSink) []ParamField {
	out := collectParams(in)
	if sink == nil {
		return out
	}
	// Walk in deterministic (name) order so diagnostic emission is stable.
	names := make([]string, 0, len(in))
	for name := range in {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		emitParameterStyleDiagnostic(in[name], opPath, sink)
	}
	return out
}

// supportedParameterStyles enumerates the OpenAPI parameter `style` values
// the runtime+oapi-codegen pipeline handles correctly today. Other styles
// (deepObject, matrix, label, spaceDelimited, pipeDelimited) generate code
// that may not match the spec's wire encoding; emit a diagnostic so the user
// knows their spec was parsed but the encoding may differ.
var supportedParameterStyles = map[string]struct{}{
	"":       {}, // default
	"form":   {},
	"simple": {},
}

func emitParameterStyleDiagnostic(p *openapi3.Parameter, opPath string, sink *diagSink) {
	if p == nil || sink == nil {
		return
	}
	if _, ok := supportedParameterStyles[p.Style]; ok {
		return
	}
	sink.warn(DiagUnsupportedParameterStyle, opPath,
		fmt.Sprintf("parameter %q (in=%s) uses style=%q which is not lowered by the generator; wire encoding may diverge from the spec", p.Name, p.In, p.Style))
}

// mergeParametersWithShadowWarning concatenates pathItem-level + operation-
// level parameters (operation wins on collision, matching OpenAPI semantics)
// and emits a diagnostic for each shadowed entry so the user knows two
// declarations were silently deduplicated.
func mergeParametersWithShadowWarning(pathItem, op openapi3.Parameters, opPath string, sink *diagSink) openapi3.Parameters {
	merged := append(openapi3.Parameters{}, pathItem...)
	merged = append(merged, op...)
	if sink == nil {
		return merged
	}
	type key struct{ in, name string }
	first := map[key]struct{}{}
	for _, ref := range pathItem {
		if ref == nil || ref.Value == nil {
			continue
		}
		first[key{ref.Value.In, ref.Value.Name}] = struct{}{}
	}
	for _, ref := range op {
		if ref == nil || ref.Value == nil {
			continue
		}
		k := key{ref.Value.In, ref.Value.Name}
		if _, dup := first[k]; dup {
			sink.warn(DiagShadowedParameter, opPath,
				fmt.Sprintf("parameter %q (in=%s) is declared at both PathItem and Operation level; Operation-level definition wins", ref.Value.Name, ref.Value.In))
		}
	}
	return merged
}

// hasContentTypeHeaderParam reports whether any header param in the operation
// has the (case-insensitive) name "Content-Type". When such a param coexists
// with a non-JSON body, oapi-codegen's `<Op>WithBodyWithResponse` overrides
// whatever the caller set, so callers expecting their header to win will be
// silently surprised — buildOperation emits a warning to call this out.
func hasContentTypeHeaderParam(headers []ParamField) bool {
	for _, h := range headers {
		if strings.EqualFold(h.Name, "Content-Type") {
			return true
		}
	}
	return false
}

func paramFieldFromSpec(name string, p *openapi3.Parameter, required bool) ParamField {
	f := ParamField{
		Name:     name,
		GoVar:    goSafeIdent(name),
		GoType:   "string",
		Required: required,
	}
	if p != nil {
		f.GoType, f.GoTypeImport = goTypeForSchema(p.Schema)
		f.Schema = p.Schema
		f.Description = p.Description
		f.Example = p.Example
		f.Deprecated = p.Deprecated
	}
	return f
}

// pickRequestContent chooses the request content-type for an operation that
// declares one or more bodies. When prefer is non-empty and present in c, it
// wins; otherwise the priority is fixed and deterministic:
//
//  1. application/json (and any *+json variant) — preferred.
//  2. application/x-www-form-urlencoded
//  3. multipart/form-data
//  4. application/octet-stream
//  5. text/*
//  6. application/xml
//  7. anything else — first key in lexicographic order.
//
// Iterating with a sorted key slice in every bucket guarantees deterministic
// output even when a content map declares multiple JSON suffix variants or
// multiple text/* subtypes.
//
// Returns BodyNone with empty values when the content map is empty.
func pickRequestContent(c openapi3.Content, prefer string) (BodyKind, string, *openapi3.SchemaRef) {
	if len(c) == 0 {
		return BodyNone, "", nil
	}
	if prefer != "" {
		if mt, ok := c[prefer]; ok {
			return bodyKindForContentType(prefer), prefer, schemaOf(mt)
		}
	}
	keys := contentKeys(c) // sorted

	// 1. JSON family.
	for _, ct := range keys {
		if loader.IsJSONContentType(ct) {
			return BodyJSON, ct, schemaOf(c[ct])
		}
	}
	// 2. application/x-www-form-urlencoded.
	for _, ct := range keys {
		if ct == "application/x-www-form-urlencoded" {
			return BodyForm, ct, schemaOf(c[ct])
		}
	}
	// 3. multipart/form-data.
	for _, ct := range keys {
		if ct == "multipart/form-data" {
			return BodyMultipart, ct, schemaOf(c[ct])
		}
	}
	// 4. application/octet-stream.
	for _, ct := range keys {
		if ct == "application/octet-stream" {
			return BodyOctet, ct, schemaOf(c[ct])
		}
	}
	// 5. text/*
	for _, ct := range keys {
		if strings.HasPrefix(ct, "text/") {
			return BodyText, ct, schemaOf(c[ct])
		}
	}
	// 6. application/xml.
	for _, ct := range keys {
		if ct == "application/xml" {
			return BodyRaw, ct, schemaOf(c[ct])
		}
	}
	// 7. Catch-all: first key in sorted order.
	ct := keys[0]
	return BodyRaw, ct, schemaOf(c[ct])
}

// bodyKindForContentType classifies a content-type string into the BodyKind
// family the generator dispatches on. The same buckets `pickRequestContent`
// walks, exposed so the -prefer-content-type flag can pick any spec-declared
// type without re-running the priority loop.
func bodyKindForContentType(ct string) BodyKind {
	switch {
	case loader.IsJSONContentType(ct):
		return BodyJSON
	case ct == "application/x-www-form-urlencoded":
		return BodyForm
	case ct == "multipart/form-data":
		return BodyMultipart
	case ct == "application/octet-stream":
		return BodyOctet
	case strings.HasPrefix(ct, "text/"):
		return BodyText
	default:
		return BodyRaw
	}
}

// schemaOf returns the schema ref carried by a MediaType, or nil if the entry
// has no schema attached (e.g. an empty value placeholder).
func schemaOf(mt *openapi3.MediaType) *openapi3.SchemaRef {
	if mt == nil {
		return nil
	}
	return mt.Schema
}

// bodyInputSchema returns the JSON Schema map that describes the MCP `body`
// argument for the given operation, plus the list of multipart file fields
// the runtime must base64-decode at request time (nil for non-multipart
// kinds). Typed kinds (JSON/Form/Multipart) lower the spec body schema
// through the SchemaConverter; raw kinds present the body as a single string.
func bodyInputSchema(op Operation, conv *SchemaConverter) (map[string]any, []RequestFilePart) {
	switch op.RequestBodyKind {
	case BodyOctet:
		return map[string]any{
			"type":            "string",
			"contentEncoding": "base64",
			"description":     "request body (application/octet-stream), base64-encoded",
		}, nil
	case BodyText, BodyRaw:
		return map[string]any{
			"type":        "string",
			"description": "request body (" + op.RequestContentType + ")",
		}, nil
	}
	bodySchema := conv.Convert(op.RequestBody)
	var fileFields []RequestFilePart
	if op.RequestBodyKind == BodyMultipart {
		fileFields = rewriteMultipartBinaryFields(bodySchema, op.requestBodyEncoding)
	}
	return bodySchema, fileFields
}

// rewriteMultipartBinaryFields walks the properties of a converted multipart
// body schema and rewrites every {type:"string", format:"binary"} leaf into
// a base64-encoded-string shape suitable for an MCP JSON argument. It returns
// one RequestFilePart per rewritten field — top-level paths like "/avatar" or
// nested paths like "/user/avatar". OpenAPI `encoding[name]` metadata
// populates ContentType for top-level binary properties only; nested leaves
// fall back to the runtime's default per-part content type because the spec
// has no equivalent metadata for them.
//
// Arrays of binary items are deliberately not walked in v1 — sending one
// multipart part per array element requires a runtime contract this release
// doesn't ship.
func rewriteMultipartBinaryFields(root map[string]any, encoding openapi3.Encodings) []RequestFilePart {
	var parts []RequestFilePart
	walkMultipartProperties(root, "", encoding, &parts)
	return parts
}

// walkMultipartProperties recurses into the `properties` map of node, rewriting
// binary leaves and appending RequestFileParts. prefix is the JSON-pointer
// path that locates node within the body object (empty for the root).
func walkMultipartProperties(node map[string]any, prefix string, encoding openapi3.Encodings, parts *[]RequestFilePart) {
	propsAny, ok := node["properties"]
	if !ok {
		return
	}
	props, ok := propsAny.(map[string]any)
	if !ok || len(props) == 0 {
		return
	}
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sub, ok := props[name].(map[string]any)
		if !ok {
			continue
		}
		path := prefix + "/" + name
		if isBinaryStringLeaf(sub) {
			delete(sub, "format")
			sub["contentEncoding"] = "base64"
			if _, has := sub["description"]; !has {
				sub["description"] = "base64-encoded binary"
			}
			part := RequestFilePart{Path: path}
			// OpenAPI `encoding` keys match only top-level properties of a
			// multipart body — nested binary leaves have no spec-defined
			// per-part metadata and fall back to runtime defaults.
			if prefix == "" {
				if enc, ok := encoding[name]; ok && enc != nil {
					part.ContentType = enc.ContentType
				}
			}
			*parts = append(*parts, part)
			continue
		}
		// Recurse into nested objects. Arrays / oneOf branches are not walked
		// for binary content in v1.
		if typeIs(sub, "object") || sub["properties"] != nil {
			walkMultipartProperties(sub, path, encoding, parts)
		}
	}
}

// isBinaryStringLeaf reports whether m is a schema leaf of the form
// {type:"string", format:"binary"}. Other modifiers (description, title, …)
// are allowed; presence of "properties" disqualifies it (that would be an
// object, not a leaf).
func isBinaryStringLeaf(m map[string]any) bool {
	if !typeIs(m, "string") {
		return false
	}
	if f, _ := m["format"].(string); f != "binary" {
		return false
	}
	if _, hasProps := m["properties"]; hasProps {
		return false
	}
	return true
}

func contentKeys(c openapi3.Content) []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func buildInputSchema(op Operation, conv *SchemaConverter) (string, []RequestFilePart, error) {
	root := map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
	props := root["properties"].(map[string]any)
	var required []any
	var fileFields []RequestFilePart

	addGroup := func(group string, fields []ParamField) {
		if len(fields) == 0 {
			return
		}
		groupProps := make(map[string]any, len(fields))
		var groupRequired []any
		for _, f := range fields {
			var fieldSchema map[string]any
			if f.Schema == nil {
				fieldSchema = map[string]any{"type": "string"}
			} else {
				fieldSchema = conv.Convert(f.Schema)
			}
			mergeParamMetadata(fieldSchema, f)
			groupProps[f.Name] = fieldSchema
			if f.Required {
				groupRequired = append(groupRequired, f.Name)
			}
		}
		groupSchema := map[string]any{"type": "object", "properties": groupProps}
		if len(groupRequired) > 0 {
			groupSchema["required"] = groupRequired
		}
		if conv.OpenAICompat {
			groupSchema["additionalProperties"] = false
		}
		props[group] = groupSchema
		if len(groupRequired) > 0 {
			required = append(required, group)
		}
	}

	addGroup(inPath, op.PathParams)
	addGroup(inQuery, op.QueryParams)
	addGroup(inHeader, op.HeaderParams)
	addGroup(inCookie, op.CookieParams)

	if op.HasRequestBody {
		bodySchema, parts := bodyInputSchema(op, conv)
		props["body"] = bodySchema
		fileFields = parts
		if op.RequestBodyRequired {
			required = append(required, "body")
		}
	}

	if len(required) > 0 {
		root["required"] = required
	}
	if conv.OpenAICompat {
		enforceOpenAIStrictObjects(root)
	}
	if defs := conv.Defs(); len(defs) > 0 {
		root["$defs"] = defs
	}

	buf, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", nil, fmt.Errorf("marshal input schema: %w", err)
	}
	return string(buf), fileFields, nil
}

// enforceOpenAIStrictObjects applies the constraints OpenAI's strict tool
// schema dialect imposes beyond ordinary JSON Schema: every declared object
// property must be listed in required and objects must reject undeclared
// properties. OpenAPI optional properties remain optional in meaning by being
// made nullable, which lets a strict caller send null as the absence marker.
//
// It runs after the complete input envelope has been assembled so it covers
// both converted OpenAPI schemas and synthetic path/query/header groups.
func enforceOpenAIStrictObjects(node any) {
	switch value := node.(type) {
	case map[string]any:
		props, hasProps := value["properties"].(map[string]any)
		if hasProps {
			required := requiredPropertyNames(value["required"])
			names := make([]string, 0, len(props))
			for name := range props {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				if !required[name] {
					makeSchemaNullable(props[name])
					required[name] = true
				}
			}
			value["required"] = names
			value["additionalProperties"] = false
		}
		for _, child := range value {
			enforceOpenAIStrictObjects(child)
		}
	case []any:
		for _, child := range value {
			enforceOpenAIStrictObjects(child)
		}
	}
}

func requiredPropertyNames(raw any) map[string]bool {
	set := map[string]bool{}
	names, _ := raw.([]any)
	for _, name := range names {
		if s, ok := name.(string); ok {
			set[s] = true
		}
	}
	return set
}

func makeSchemaNullable(raw any) {
	schema, ok := raw.(map[string]any)
	if !ok {
		return
	}
	switch typ := schema["type"].(type) {
	case string:
		if typ != "null" {
			schema["type"] = []any{typ, "null"}
		}
	case []any:
		for _, candidate := range typ {
			if candidate == "null" {
				return
			}
		}
		schema["type"] = append(typ, "null")
	}
}

// mergeParamMetadata copies Parameter-object-level description / example /
// deprecated onto the converted schema map. The schema's own keywords win on
// collision (OpenAPI says the schema is the authoritative type description;
// the Parameter fields are supplementary), so each key is only set when
// absent. A bare {"$ref":...} schema still receives the keys — draft-07
// validators ignore $ref siblings but schema-aware MCP clients and 2020-12
// consumers surface them, and losing the description entirely is worse.
func mergeParamMetadata(schema map[string]any, f ParamField) {
	if f.Description != "" {
		if _, ok := schema["description"]; !ok {
			schema["description"] = f.Description
		}
	}
	if f.Example != nil {
		if _, ok := schema["examples"]; !ok {
			schema["examples"] = []any{f.Example}
		}
	}
	if f.Deprecated {
		if _, ok := schema["deprecated"]; !ok {
			schema["deprecated"] = true
		}
	}
}

func goMethodName(operationID, method, path string) string {
	if operationID == "" {
		operationID = method + " " + path
	}
	return PascalCase(operationID) + "WithResponse"
}

// callMethodFor returns the oapi-codegen client method name the generated
// handler invokes for the given body kind. The Go name of an operation is
// always "<Base>WithResponse"; non-JSON kinds dispatch to differently-named
// helpers that oapi-codegen emits on the same client interface.
func callMethodFor(goName string, kind BodyKind) string {
	switch kind {
	case BodyForm:
		base := strings.TrimSuffix(goName, "WithResponse")
		return base + "WithFormdataBodyWithResponse"
	case BodyMultipart, BodyOctet, BodyText, BodyRaw:
		base := strings.TrimSuffix(goName, "WithResponse")
		return base + "WithBodyWithResponse"
	default:
		return goName
	}
}

// toolAnnotations derives the MCP tool annotation hints from the operation's
// HTTP method (RFC 9110 semantics) and summary:
//
//   - GET / HEAD / OPTIONS / TRACE — safe methods: read-only + idempotent.
//   - PUT — idempotent.
//   - DELETE — idempotent, and explicitly destructive (the protocol default
//     for destructiveHint is already true; DELETE states it outright).
//   - POST / PATCH — no hints; the protocol defaults (not read-only, not
//     idempotent, possibly destructive) already describe them.
//
// The summary becomes the Title (human-readable display name). Returns nil
// when the operation yields no annotations at all. method is expected in the
// uppercase form kin-openapi's PathItem.Operations() emits.
func toolAnnotations(method, summary string) *runtime.ToolAnnotations {
	a := runtime.ToolAnnotations{Title: summary}
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		a.ReadOnlyHint = true
		a.IdempotentHint = true
	case http.MethodPut:
		a.IdempotentHint = true
	case http.MethodDelete:
		a.IdempotentHint = true
		a.DestructiveHint = runtime.BoolPtr(true)
	default:
		if summary == "" {
			return nil
		}
	}
	return &a
}

func chooseDescription(op *openapi3.Operation) string {
	var desc string
	switch {
	case op.Summary != "" && op.Description != "":
		desc = op.Summary + "\n\n" + op.Description
	case op.Summary != "":
		desc = op.Summary
	default:
		desc = op.Description
	}
	if op.Deprecated {
		// MCP has no native deprecated flag on tools; a description prefix is
		// the only channel every client (and the LLM) sees.
		if desc == "" {
			return "Deprecated."
		}
		return "Deprecated. " + desc
	}
	return desc
}

// goSafeIdent turns an OpenAPI parameter name into a valid Go identifier.
// Note: this is intentionally distinct from naming.sanitize, which preserves
// dot/dash for MCP tool names but produces invalid Go identifiers.
func goSafeIdent(s string) string {
	if s == "" {
		return "_"
	}
	var b strings.Builder
	for i, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_':
			b.WriteRune(r)
		case i > 0 && r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	id := b.String()
	if token.IsKeyword(id) {
		id += "_"
	}
	return id
}

func newGoVarUniquer() func(string) string {
	seen := map[string]int{}
	return func(base string) string {
		if base == "" {
			base = "_"
		}
		seen[base]++
		if seen[base] == 1 {
			return base
		}
		return fmt.Sprintf("%s_%d", base, seen[base])
	}
}

// oapiTypesImport is the import path of the oapi-codegen helper types package.
// It's pulled in whenever a path parameter has a format (uuid/email/date)
// that oapi-codegen maps to a typed wrapper rather than a plain string.
const oapiTypesImport = "github.com/oapi-codegen/runtime/types"

// goTypeForSchema returns the Go type oapi-codegen emits for a primitive
// parameter schema, along with the import path required to reference it
// (empty when only stdlib types are needed). Anything non-primitive falls
// back to string.
func goTypeForSchema(ref *openapi3.SchemaRef) (goType, importPath string) {
	if ref == nil || ref.Value == nil {
		return "string", ""
	}
	s := ref.Value
	types := normaliseTypes(s.Type)
	if len(types) == 0 {
		return "string", ""
	}
	switch types[0] {
	case "string":
		switch s.Format {
		case "uuid":
			return "openapi_types.UUID", oapiTypesImport
		case "email":
			return "openapi_types.Email", oapiTypesImport
		case "date":
			return "openapi_types.Date", oapiTypesImport
		case "date-time":
			return "time.Time", "time"
		}
		return "string", ""
	case "boolean":
		return "bool", ""
	case "integer":
		switch s.Format {
		case "int64":
			return "int64", ""
		case "int32":
			return "int32", ""
		}
		return "int", ""
	case "number":
		if s.Format == "float" {
			return "float32", ""
		}
		return "float64", ""
	}
	return "string", ""
}
