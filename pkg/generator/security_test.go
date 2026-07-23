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
	"reflect"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestDeriveEnvVar(t *testing.T) {
	cases := map[string]string{
		"bearerAuth":   "BEARERAUTH",
		"BearerAuth":   "BEARERAUTH",
		"BEARER_AUTH":  "BEARER_AUTH",
		"api-key":      "API_KEY",
		"x.api.key":    "X_API_KEY",
		"oauth2_token": "OAUTH2_TOKEN",
		"foo--bar":     "FOO_BAR",
		"123abc":       "123ABC",
		"!@#":          "UNNAMED",
		"_underscore_": "UNDERSCORE",
	}
	for in, want := range cases {
		if got := deriveEnvVar(in); got != want {
			t.Errorf("deriveEnvVar(%q) = %q, want %q", in, got, want)
		}
	}
}

// schemeRef wraps a SecurityScheme in the *openapi3.SecuritySchemeRef shape
// the doc carries. Keeps the test cases readable.
func schemeRef(ss *openapi3.SecurityScheme) *openapi3.SecuritySchemeRef {
	return &openapi3.SecuritySchemeRef{Value: ss}
}

func docWithSchemes(schemes openapi3.SecuritySchemes) *openapi3.T {
	return &openapi3.T{Components: &openapi3.Components{SecuritySchemes: schemes}}
}

func TestParseSecuritySchemes_APIKey_HeaderQueryCookie(t *testing.T) {
	doc := docWithSchemes(openapi3.SecuritySchemes{
		"hdrKey":   schemeRef(&openapi3.SecurityScheme{Type: "apiKey", In: "header", Name: "X-API-Key"}),
		"qryKey":   schemeRef(&openapi3.SecurityScheme{Type: "apiKey", In: "query", Name: "api_key"}),
		"cookKey":  schemeRef(&openapi3.SecurityScheme{Type: "apiKey", In: "cookie", Name: "sid"}),
		"badIn":    schemeRef(&openapi3.SecurityScheme{Type: "apiKey", In: "body", Name: "x"}),
		"missingN": schemeRef(&openapi3.SecurityScheme{Type: "apiKey", In: "header"}),
	})
	sink := newDiagSink(&bytes.Buffer{})
	got := ParseSecuritySchemes(doc, sink)
	if len(got) != 3 {
		t.Fatalf("expected 3 parsed apiKey schemes, got %d: %+v", len(got), got)
	}
	// Output must be sorted by Name for stable codegen.
	wantOrder := []string{"cookKey", "hdrKey", "qryKey"}
	for i, w := range wantOrder {
		if got[i].Name != w {
			t.Errorf("got[%d].Name = %q, want %q", i, got[i].Name, w)
		}
	}
	for _, s := range got {
		switch s.Name {
		case "hdrKey":
			if s.In != "header" || s.ParamName != "X-API-Key" || s.EnvVar != "API_KEY_HDRKEY" {
				t.Errorf("hdrKey: %+v", s)
			}
		case "qryKey":
			if s.In != "query" || s.ParamName != "api_key" {
				t.Errorf("qryKey: %+v", s)
			}
		case "cookKey":
			if s.In != "cookie" || s.ParamName != "sid" {
				t.Errorf("cookKey: %+v", s)
			}
		}
	}
	// Bad / missing-name schemes must each surface as a warning.
	diags := sink.finalize()
	warnCount := 0
	for _, d := range diags {
		if d.Code == DiagUnsupportedSecurityScheme {
			warnCount++
		}
	}
	if warnCount != 2 {
		t.Errorf("expected 2 unsupported-security-scheme warnings (bad In, missing Name); got %d: %+v", warnCount, diags)
	}
}

func TestParseSecuritySchemes_XquikAPIKeyHeader(t *testing.T) {
	doc := docWithSchemes(openapi3.SecuritySchemes{
		"apiKey": schemeRef(&openapi3.SecurityScheme{Type: "apiKey", In: "header", Name: "x-api-key"}),
	})

	got := ParseSecuritySchemes(doc, newDiagSink(&bytes.Buffer{}))
	if len(got) != 1 {
		t.Fatalf("expected 1 parsed apiKey scheme, got %d: %+v", len(got), got)
	}

	scheme := got[0]
	if scheme.Name != "apiKey" || scheme.Kind != SecurityAPIKey || scheme.In != "header" ||
		scheme.ParamName != "x-api-key" || scheme.EnvVar != "API_KEY_APIKEY" {
		t.Errorf("xquik api key scheme: %+v", scheme)
	}
}

func TestParseSecuritySchemes_HTTPBearerAndBasic(t *testing.T) {
	doc := docWithSchemes(openapi3.SecuritySchemes{
		"bearerAuth": schemeRef(&openapi3.SecurityScheme{Type: "http", Scheme: "bearer"}),
		"basicAuth":  schemeRef(&openapi3.SecurityScheme{Type: "http", Scheme: "Basic"}),  // case insensitive
		"digestAuth": schemeRef(&openapi3.SecurityScheme{Type: "http", Scheme: "digest"}), // unsupported
	})
	sink := newDiagSink(&bytes.Buffer{})
	got := ParseSecuritySchemes(doc, sink)
	if len(got) != 2 {
		t.Fatalf("expected bearer + basic (2); got %d: %+v", len(got), got)
	}
	for _, s := range got {
		switch s.Name {
		case "bearerAuth":
			if s.Kind != SecurityHTTPBearer || s.EnvVar != "BEARER_TOKEN_BEARERAUTH" {
				t.Errorf("bearer: %+v", s)
			}
		case "basicAuth":
			if s.Kind != SecurityHTTPBasic || s.UsernameEnvVar != "BASIC_AUTH_USERNAME_BASICAUTH" ||
				s.PasswordEnvVar != "BASIC_AUTH_PASSWORD_BASICAUTH" {
				t.Errorf("basic: %+v", s)
			}
		}
	}
	// digestAuth must produce a diagnostic.
	hasDigestWarn := false
	for _, d := range sink.finalize() {
		if d.Code == DiagUnsupportedSecurityScheme && d.Path == "#/components/securitySchemes/digestAuth" {
			hasDigestWarn = true
		}
	}
	if !hasDigestWarn {
		t.Errorf("expected unsupported-security-scheme warning for digestAuth")
	}
}

func TestParseSecuritySchemes_OAuth2AsBearer(t *testing.T) {
	doc := docWithSchemes(openapi3.SecuritySchemes{
		"githubOAuth": schemeRef(&openapi3.SecurityScheme{Type: "oauth2"}),
	})
	got := ParseSecuritySchemes(doc, newDiagSink(&bytes.Buffer{}))
	if len(got) != 1 || got[0].Kind != SecurityOAuth2 {
		t.Fatalf("oauth2 should be parsed: %+v", got)
	}
	if got[0].EnvVar != "OAUTH2_ACCESS_TOKEN_GITHUBOAUTH" {
		t.Errorf("oauth2 env var: %q", got[0].EnvVar)
	}
}

func TestParseSecuritySchemes_OAuth2ClientCredentials(t *testing.T) {
	doc := docWithSchemes(openapi3.SecuritySchemes{
		"service": schemeRef(&openapi3.SecurityScheme{Type: "oauth2", Flows: &openapi3.OAuthFlows{ClientCredentials: &openapi3.OAuthFlow{
			TokenURL: "https://issuer.example/token",
			Scopes:   openapi3.StringMap[string]{"write": "Write", "read": "Read"},
		}}}),
	})
	got := ParseSecuritySchemes(doc, newDiagSink(&bytes.Buffer{}))
	if len(got) != 1 {
		t.Fatalf("client credentials scheme: %+v", got)
	}
	if got[0].OAuthTokenURL != "https://issuer.example/token" || got[0].ClientIDEnvVar != "OAUTH2_CLIENT_ID_SERVICE" || got[0].ClientSecretEnvVar != "OAUTH2_CLIENT_SECRET_SERVICE" {
		t.Errorf("client credentials fields: %+v", got[0])
	}
	if strings.Join(got[0].OAuthScopes, ",") != "read,write" {
		t.Errorf("scopes must be sorted: %+v", got[0].OAuthScopes)
	}
}

func TestParseSecuritySchemes_MTLSSupported(t *testing.T) {
	doc := docWithSchemes(openapi3.SecuritySchemes{
		"mtls": schemeRef(&openapi3.SecurityScheme{Type: "mutualTLS"}),
	})
	got := ParseSecuritySchemes(doc, newDiagSink(&bytes.Buffer{}))
	if len(got) != 1 || got[0].Kind != SecurityMTLS {
		t.Errorf("mutualTLS must be supported: %+v", got)
	}
}

func TestResolveSecurityPolicy_UsesOnlyOperationOAuthScopes(t *testing.T) {
	reqs := openapi3.SecurityRequirements{{"oauth": []string{"write"}}}
	policy := ResolveSecurityPolicy(&openapi3.Operation{Security: &reqs}, &openapi3.T{}, []SecurityScheme{{
		Name: "oauth", Kind: SecurityOAuth2, OAuthTokenURL: "https://issuer.example/token", OAuthScopes: []string{"read", "write"},
	}})
	if len(policy.Alternatives) != 1 || len(policy.Alternatives[0]) != 1 {
		t.Fatalf("policy = %+v", policy)
	}
	if got := policy.Alternatives[0][0].OAuthRequestScopes; !reflect.DeepEqual(got, []string{"write"}) {
		t.Errorf("request scopes = %v, want [write]", got)
	}
}

func TestParseSecuritySchemes_OpenIDConnectUsesCustomProvider(t *testing.T) {
	doc := docWithSchemes(openapi3.SecuritySchemes{
		"oidc": schemeRef(&openapi3.SecurityScheme{Type: "openIdConnect"}),
	})
	got := ParseSecuritySchemes(doc, newDiagSink(&bytes.Buffer{}))
	if len(got) != 1 || got[0].Kind != SecurityCustom {
		t.Errorf("openIdConnect must use a custom provider; got %+v", got)
	}
}

func TestParseSecuritySchemes_NilDocOrComponents(t *testing.T) {
	if got := ParseSecuritySchemes(nil, nil); got != nil {
		t.Errorf("nil doc: %+v", got)
	}
	if got := ParseSecuritySchemes(&openapi3.T{}, nil); got != nil {
		t.Errorf("nil components: %+v", got)
	}
}

func TestResolveOperationSecurity_OperationOverridesDocument(t *testing.T) {
	doc := &openapi3.T{
		Security: openapi3.SecurityRequirements{
			openapi3.SecurityRequirement{"docBearer": {}},
		},
	}
	parsed := []SecurityScheme{
		{Name: "docBearer", Kind: SecurityHTTPBearer, EnvVar: "BEARER_TOKEN_DOCBEARER"},
		{Name: "opKey", Kind: SecurityAPIKey, In: "header", ParamName: "X-API-Key", EnvVar: "API_KEY_OPKEY"},
	}
	opReq := openapi3.SecurityRequirements{openapi3.SecurityRequirement{"opKey": {}}}
	op := &openapi3.Operation{Security: &opReq}

	got, anon := ResolveOperationSecurity(op, doc, parsed)
	if anon {
		t.Fatal("op-level requirement is not anonymous")
	}
	if len(got) != 1 || got[0].Name != "opKey" {
		t.Errorf("op-level should override doc-level: %+v", got)
	}
}

func TestResolveOperationSecurity_DocLevelFallback(t *testing.T) {
	doc := &openapi3.T{
		Security: openapi3.SecurityRequirements{openapi3.SecurityRequirement{"docBearer": {}}},
	}
	parsed := []SecurityScheme{{Name: "docBearer", Kind: SecurityHTTPBearer, EnvVar: "BEARER_TOKEN_DOCBEARER"}}
	op := &openapi3.Operation{} // no op.Security set
	got, anon := ResolveOperationSecurity(op, doc, parsed)
	if anon || len(got) != 1 || got[0].Name != "docBearer" {
		t.Errorf("expected doc-level fallback; got anon=%v schemes=%+v", anon, got)
	}
}

func TestResolveOperationSecurity_EmptyOpSecurityIsAnonymous(t *testing.T) {
	// security: [{}] at the operation level means "anonymous", overriding
	// any doc-level requirement.
	doc := &openapi3.T{
		Security: openapi3.SecurityRequirements{openapi3.SecurityRequirement{"docBearer": {}}},
	}
	parsed := []SecurityScheme{{Name: "docBearer", Kind: SecurityHTTPBearer, EnvVar: "X"}}
	opReq := openapi3.SecurityRequirements{openapi3.SecurityRequirement{}}
	op := &openapi3.Operation{Security: &opReq}
	got, anon := ResolveOperationSecurity(op, doc, parsed)
	if !anon || len(got) != 0 {
		t.Errorf("explicit empty SecurityRequirement should be anonymous; got anon=%v schemes=%+v", anon, got)
	}
}

func TestResolveSecurityPolicy_EmptySecurityListIsAnonymous(t *testing.T) {
	doc := &openapi3.T{Security: openapi3.SecurityRequirements{openapi3.SecurityRequirement{"docBearer": {}}}}
	empty := openapi3.SecurityRequirements{}
	policy := ResolveSecurityPolicy(&openapi3.Operation{Security: &empty}, doc, []SecurityScheme{{Name: "docBearer"}})
	if !policy.Anonymous || policy.Required || len(policy.Alternatives) != 0 {
		t.Errorf("security: [] must override global auth as anonymous, got %+v", policy)
	}
}

func TestResolveOperationSecurity_NoSecurityAnywhereIsAnonymous(t *testing.T) {
	got, anon := ResolveOperationSecurity(&openapi3.Operation{}, &openapi3.T{}, nil)
	if !anon || got != nil {
		t.Errorf("no-security spec should be anonymous; got anon=%v schemes=%+v", anon, got)
	}
}

func TestResolveOperationSecurity_PicksFirstCompleteRequirement(t *testing.T) {
	// First requirement references a scheme we couldn't parse → skip;
	// second references only parsed schemes → pick it. Documented in
	// design-decisions §14.
	parsed := []SecurityScheme{
		{Name: "good", Kind: SecurityHTTPBearer, EnvVar: "BEARER_TOKEN_GOOD"},
	}
	opReq := openapi3.SecurityRequirements{
		openapi3.SecurityRequirement{"unparsed": {}},
		openapi3.SecurityRequirement{"good": {}},
	}
	op := &openapi3.Operation{Security: &opReq}
	got, anon := ResolveOperationSecurity(op, &openapi3.T{}, parsed)
	if anon {
		t.Fatal("a complete requirement is available")
	}
	if len(got) != 1 || got[0].Name != "good" {
		t.Errorf("expected to skip first requirement; got %+v", got)
	}
}

func TestResolveOperationSecurity_DeterministicOrderWithinRequirement(t *testing.T) {
	parsed := []SecurityScheme{
		{Name: "a", Kind: SecurityHTTPBearer},
		{Name: "b", Kind: SecurityHTTPBearer},
		{Name: "c", Kind: SecurityHTTPBearer},
	}
	opReq := openapi3.SecurityRequirements{openapi3.SecurityRequirement{
		"c": {}, "a": {}, "b": {},
	}}
	op := &openapi3.Operation{Security: &opReq}
	got, _ := ResolveOperationSecurity(op, &openapi3.T{}, parsed)
	names := []string{got[0].Name, got[1].Name, got[2].Name}
	if !reflect.DeepEqual(names, []string{"a", "b", "c"}) {
		t.Errorf("expected alphabetic order within a requirement; got %v", names)
	}
}

func TestResolveSecurityPolicy_PreservesORAlternativesAndFailsClosed(t *testing.T) {
	parsed := []SecurityScheme{
		{Name: "apiKey", Kind: SecurityAPIKey, EnvVar: "API_KEY"},
		{Name: "bearer", Kind: SecurityHTTPBearer, EnvVar: "BEARER_TOKEN"},
	}
	reqs := openapi3.SecurityRequirements{
		openapi3.SecurityRequirement{"apiKey": {}},
		openapi3.SecurityRequirement{"bearer": {}},
	}

	policy := ResolveSecurityPolicy(&openapi3.Operation{Security: &reqs}, &openapi3.T{}, parsed)
	if policy.Anonymous {
		t.Fatal("declared alternatives must not be treated as anonymous")
	}
	if !policy.Required {
		t.Fatal("declared security must remain required even when no credential is configured")
	}
	if len(policy.Alternatives) != 2 {
		t.Fatalf("alternatives = %d, want 2", len(policy.Alternatives))
	}
	if got := policy.Alternatives[0][0].Name; got != "apiKey" {
		t.Errorf("first alternative = %q, want apiKey", got)
	}
	if got := policy.Alternatives[1][0].Name; got != "bearer" {
		t.Errorf("second alternative = %q, want bearer", got)
	}

	unsupportedReqs := openapi3.SecurityRequirements{openapi3.SecurityRequirement{"unparsed": {}}}
	unsupported := ResolveSecurityPolicy(&openapi3.Operation{Security: &unsupportedReqs}, &openapi3.T{}, parsed)
	if unsupported.Anonymous || !unsupported.Required || len(unsupported.Alternatives) != 0 {
		t.Errorf("unsupported declared security must fail closed: %+v", unsupported)
	}
}
