// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package generator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// SecurityKind classifies a parsed OpenAPI security scheme into one of the
// shapes proxy mode knows how to wire automatically. Anything outside this
// set is reported as a diagnostic and skipped — the proxy template never
// emits code for an unsupported scheme.
type SecurityKind string

const (
	// SecurityAPIKey is type=apiKey. The `In` field then specifies header /
	// query / cookie; `ParamName` is the spec's name for it.
	SecurityAPIKey SecurityKind = "apiKey"
	// SecurityHTTPBearer is type=http + scheme=bearer (or any case-insensitive
	// variant). Used by both bearer-token schemes and oauth2 schemes — the
	// latter degrade to "use this env-var token as a Bearer" since proxy mode
	// does not perform OAuth2 token-exchange flows.
	SecurityHTTPBearer SecurityKind = "httpBearer"
	// SecurityHTTPBasic is type=http + scheme=basic.
	SecurityHTTPBasic SecurityKind = "httpBasic"
	// SecurityOAuth2 is type=oauth2; treated as Bearer-from-env at runtime.
	SecurityOAuth2 SecurityKind = "oauth2"
)

// SecurityScheme is the generator's lowered view of one OpenAPI security
// scheme. Only fields the proxy template actually needs are populated;
// kin-openapi's full struct (Flows, OpenIdConnectUrl, BearerFormat) is
// ignored because proxy mode does no token exchange.
type SecurityScheme struct {
	// Name is the spec's key under components.securitySchemes (e.g. "bearerAuth").
	Name string
	// Kind classifies how the credential is placed on the wire.
	Kind SecurityKind
	// In is the SecurityScheme.In value for apiKey schemes ("header"/"query"/
	// "cookie"). Empty for non-apiKey kinds.
	In string
	// ParamName is the SecurityScheme.Name value for apiKey schemes (the
	// header / query / cookie name). Empty for non-apiKey kinds.
	ParamName string
	// EnvVar is the environment variable the generated main.go reads for
	// this scheme. Derived from Name (see deriveEnvVar). For httpBasic this
	// is the *prefix* — the username/password env vars append _USERNAME /
	// _PASSWORD.
	EnvVar string
	// UsernameEnvVar and PasswordEnvVar are only populated for httpBasic.
	UsernameEnvVar string
	PasswordEnvVar string
}

// ParseSecuritySchemes walks doc.Components.SecuritySchemes and returns a
// supported subset. Unsupported shapes (e.g. oauth2 with only the
// "implicit" flow, openIdConnect) are surfaced as warnings via sink and
// dropped from the result; callers should not treat that as a fatal
// error — companion mode keeps working regardless, and proxy mode skips
// auth wiring for the dropped scheme (the upstream will produce a 401
// the user will see and act on).
//
// The returned slice is sorted by Name for deterministic codegen output.
func ParseSecuritySchemes(doc *openapi3.T, sink *diagSink) []SecurityScheme {
	if doc == nil || doc.Components == nil || doc.Components.SecuritySchemes == nil {
		return nil
	}
	names := make([]string, 0, len(doc.Components.SecuritySchemes))
	for name := range doc.Components.SecuritySchemes {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]SecurityScheme, 0, len(names))
	for _, name := range names {
		ref := doc.Components.SecuritySchemes[name]
		if ref == nil || ref.Value == nil {
			continue
		}
		s, ok := classifySecurityScheme(name, ref.Value, sink)
		if !ok {
			continue
		}
		out = append(out, s)
	}
	return out
}

// classifySecurityScheme maps one openapi3.SecurityScheme into our internal
// SecurityScheme, or returns (_, false) and emits a warning when the shape
// isn't one proxy mode supports. Centralising classification here keeps
// ParseSecuritySchemes a simple loop.
func classifySecurityScheme(name string, ss *openapi3.SecurityScheme, sink *diagSink) (SecurityScheme, bool) {
	envBase := deriveEnvVar(name)
	switch strings.ToLower(ss.Type) {
	case "apikey":
		in := strings.ToLower(ss.In)
		if in != "header" && in != "query" && in != "cookie" {
			if sink != nil {
				sink.warn(DiagUnsupportedSecurityScheme,
					"#/components/securitySchemes/"+name,
					fmt.Sprintf("apiKey scheme has unsupported `in` value %q (expected header/query/cookie); auth wiring skipped", ss.In))
			}
			return SecurityScheme{}, false
		}
		if ss.Name == "" {
			if sink != nil {
				sink.warn(DiagUnsupportedSecurityScheme,
					"#/components/securitySchemes/"+name,
					"apiKey scheme is missing the required `name` field; auth wiring skipped")
			}
			return SecurityScheme{}, false
		}
		return SecurityScheme{
			Name:      name,
			Kind:      SecurityAPIKey,
			In:        in,
			ParamName: ss.Name,
			EnvVar:    "API_KEY_" + envBase,
		}, true
	case "http":
		scheme := strings.ToLower(ss.Scheme)
		switch scheme {
		case "bearer":
			return SecurityScheme{
				Name:   name,
				Kind:   SecurityHTTPBearer,
				EnvVar: "BEARER_TOKEN_" + envBase,
			}, true
		case "basic":
			return SecurityScheme{
				Name:           name,
				Kind:           SecurityHTTPBasic,
				EnvVar:         "BASIC_AUTH_" + envBase,
				UsernameEnvVar: "BASIC_AUTH_USERNAME_" + envBase,
				PasswordEnvVar: "BASIC_AUTH_PASSWORD_" + envBase,
			}, true
		default:
			if sink != nil {
				sink.warn(DiagUnsupportedSecurityScheme,
					"#/components/securitySchemes/"+name,
					fmt.Sprintf("http scheme %q is not supported (only bearer / basic); auth wiring skipped", ss.Scheme))
			}
			return SecurityScheme{}, false
		}
	case "oauth2":
		// We don't perform any OAuth2 flow; we treat oauth2 schemes as a
		// pre-acquired Bearer token the user supplies via env var. A note
		// in the README spells this out so users aren't surprised.
		return SecurityScheme{
			Name:   name,
			Kind:   SecurityOAuth2,
			EnvVar: "OAUTH2_ACCESS_TOKEN_" + envBase,
		}, true
	case "openidconnect":
		if sink != nil {
			sink.warn(DiagUnsupportedSecurityScheme,
				"#/components/securitySchemes/"+name,
				"openIdConnect schemes are not yet supported; auth wiring skipped (the spec consumer must inject the bearer token via a custom HTTP client)")
		}
		return SecurityScheme{}, false
	default:
		if sink != nil {
			sink.warn(DiagUnsupportedSecurityScheme,
				"#/components/securitySchemes/"+name,
				fmt.Sprintf("unknown security scheme type %q; auth wiring skipped", ss.Type))
		}
		return SecurityScheme{}, false
	}
}

// deriveEnvVar lowers a spec key (typically camelCase like "bearerAuth")
// into an UPPER_SNAKE form suitable for an env var. Non-alphanumeric runs
// collapse to a single underscore; ALL-CAPS input is preserved as-is.
// Leading/trailing underscores trimmed so "API.Key" doesn't yield "_API_KEY_".
func deriveEnvVar(name string) string {
	var b strings.Builder
	prevSep := true
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
			prevSep = false
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 32)
			prevSep = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			prevSep = false
		default:
			if !prevSep {
				b.WriteByte('_')
				prevSep = true
			}
		}
	}
	s := b.String()
	s = strings.Trim(s, "_")
	if s == "" {
		// Fully-non-alphanumeric scheme names shouldn't happen — OpenAPI
		// requires a non-empty identifier — but degrade gracefully so the
		// generator never panics on a hostile spec.
		return "UNNAMED"
	}
	return s
}

// ResolveOperationSecurity returns the security schemes that apply to one
// operation. OpenAPI precedence: an operation-level `security` (even if
// empty []) overrides the document-level `security`; a missing
// op.Security falls back to doc.Security.
//
// A `security: []` requirement (note: an empty *list*, not a missing one)
// means "anonymous"; the operation is callable without credentials and we
// return an empty slice with anonymous=true so the proxy template knows
// to skip auth entirely.
//
// Within a non-empty requirement list, OpenAPI says "either of these
// requirements is sufficient". Proxy mode picks the first requirement
// that references only schemes we successfully parsed — anything else
// would require user code at runtime to choose. This is documented in
// design-decisions §14.
func ResolveOperationSecurity(op *openapi3.Operation, doc *openapi3.T, parsed []SecurityScheme) (schemes []SecurityScheme, anonymous bool) {
	reqs := operationSecurityRequirements(op, doc)
	if reqs == nil {
		// No security at either level → effectively anonymous, but
		// distinct from an explicit `security: []` — callers may still
		// log a "no auth declared" advisory. We surface it the same way:
		// no schemes, anonymous=true.
		return nil, true
	}

	byName := make(map[string]SecurityScheme, len(parsed))
	for _, s := range parsed {
		byName[s.Name] = s
	}

	for _, req := range *reqs {
		if len(req) == 0 {
			// Empty SecurityRequirement object means anonymous.
			return nil, true
		}
		picked := make([]SecurityScheme, 0, len(req))
		complete := true
		names := make([]string, 0, len(req))
		for n := range req {
			names = append(names, n)
		}
		sort.Strings(names) // deterministic codegen
		for _, n := range names {
			s, ok := byName[n]
			if !ok {
				complete = false
				break
			}
			picked = append(picked, s)
		}
		if complete {
			return picked, false
		}
	}
	// No requirement could be fully satisfied. Fall back to anonymous so
	// the proxy still generates a callable handler — the upstream will
	// 401 and the user will see a real error rather than silent failure.
	return nil, true
}

// operationSecurityRequirements returns the effective SecurityRequirements
// list for op, honouring OpenAPI's "operation overrides document" rule.
// Distinguishes "op has no security field" (fall back to doc) from "op
// has security: [{}]" (explicit anonymous override) — both produce the
// same downstream behaviour but matter for precedence.
func operationSecurityRequirements(op *openapi3.Operation, doc *openapi3.T) *openapi3.SecurityRequirements {
	if op != nil && op.Security != nil {
		return op.Security
	}
	if doc != nil && len(doc.Security) > 0 {
		// kin-openapi's doc.Security is already a value type, not a
		// pointer; wrap into the same pointer-to-slice shape op.Security
		// uses so the caller has one path.
		s := doc.Security
		return &s
	}
	return nil
}
