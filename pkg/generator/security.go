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
	// SecurityOAuth2 is type=oauth2. A supplied access token is used directly;
	// a clientCredentials flow can also acquire and refresh one at runtime.
	SecurityOAuth2 SecurityKind = "oauth2"
	// SecurityMTLS is OpenAPI 3.1 type=mutualTLS. The proxy requires a
	// caller-provided mTLS-capable HTTP client before it sends a request.
	SecurityMTLS SecurityKind = "mtls"
	// SecurityCustom delegates authentication to runtime.RequestAuthProvider.
	// It is used for OpenID Connect and the common aws4-hmac-sha256 HTTP
	// scheme, where the deployment owns credential acquisition/signing.
	SecurityCustom SecurityKind = "custom"
)

// SecurityScheme is the generator's lowered view of one OpenAPI security
// scheme. Only fields the proxy template actually needs are populated;
// kin-openapi's full struct is intentionally narrowed to what generated
// proxy handlers can apply without binding to a cloud-specific SDK.
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
	// ClientIDEnvVar and ClientSecretEnvVar are populated for OAuth 2.0
	// client-credentials flows. EnvVar remains the optional pre-acquired token.
	ClientIDEnvVar     string
	ClientSecretEnvVar string
	OAuthTokenURL      string
	OAuthScopes        []string
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
			if scheme == "aws4-hmac-sha256" {
				return SecurityScheme{Name: name, Kind: SecurityCustom}, true
			}
			if sink != nil {
				sink.warn(DiagUnsupportedSecurityScheme,
					"#/components/securitySchemes/"+name,
					fmt.Sprintf("http scheme %q is not supported (only bearer / basic); auth wiring skipped", ss.Scheme))
			}
			return SecurityScheme{}, false
		}
	case "oauth2":
		out := SecurityScheme{
			Name:   name,
			Kind:   SecurityOAuth2,
			EnvVar: "OAUTH2_ACCESS_TOKEN_" + envBase,
		}
		if ss.Flows != nil && ss.Flows.ClientCredentials != nil && ss.Flows.ClientCredentials.TokenURL != "" {
			flow := ss.Flows.ClientCredentials
			out.ClientIDEnvVar = "OAUTH2_CLIENT_ID_" + envBase
			out.ClientSecretEnvVar = "OAUTH2_CLIENT_SECRET_" + envBase
			out.OAuthTokenURL = flow.TokenURL
			for scope := range flow.Scopes {
				out.OAuthScopes = append(out.OAuthScopes, scope)
			}
			sort.Strings(out.OAuthScopes)
		}
		return out, true
	case "openidconnect":
		return SecurityScheme{Name: name, Kind: SecurityCustom}, true
	case "mutualtls":
		return SecurityScheme{Name: name, Kind: SecurityMTLS}, true
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

// SecurityPolicy is the proxy-mode authentication contract for one operation.
// Alternatives preserves OpenAPI's outer OR semantics; each inner slice is
// an AND requirement. Required remains true when the source declared security
// but no supported alternative could be produced, ensuring callers fail closed.
type SecurityPolicy struct {
	Alternatives [][]SecurityScheme
	Anonymous    bool
	Required     bool
}

// ResolveSecurityPolicy returns the security policy that applies to one
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
// requirements is sufficient". Every fully supported alternative is retained
// so generated code can choose based on credentials available at call time.
func ResolveSecurityPolicy(op *openapi3.Operation, doc *openapi3.T, parsed []SecurityScheme) SecurityPolicy {
	reqs := operationSecurityRequirements(op, doc)
	if reqs == nil {
		return SecurityPolicy{Anonymous: true}
	}
	// An explicitly empty security array overrides any document-level
	// requirement and means the operation can be invoked anonymously.
	if len(*reqs) == 0 {
		return SecurityPolicy{Anonymous: true}
	}

	byName := make(map[string]SecurityScheme, len(parsed))
	for _, s := range parsed {
		byName[s.Name] = s
	}
	policy := SecurityPolicy{Required: true}

	for _, req := range *reqs {
		if len(req) == 0 {
			// Empty SecurityRequirement object means anonymous.
			return SecurityPolicy{Anonymous: true}
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
			policy.Alternatives = append(policy.Alternatives, picked)
		}
	}
	return policy
}

// ResolveOperationSecurity is retained for callers compiled against the
// pre-v1.0 generator API. It returns the first supported alternative; proxy
// generation itself uses ResolveSecurityPolicy and therefore preserves all
// alternatives at runtime.
func ResolveOperationSecurity(op *openapi3.Operation, doc *openapi3.T, parsed []SecurityScheme) ([]SecurityScheme, bool) {
	policy := ResolveSecurityPolicy(op, doc, parsed)
	if len(policy.Alternatives) == 0 {
		return nil, policy.Anonymous
	}
	return policy.Alternatives[0], policy.Anonymous
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
