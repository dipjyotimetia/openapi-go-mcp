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
	"fmt"
	"net/http"
	"net/url"
)

// AuthLocation is the slot an apiKey credential occupies on an outgoing
// request: header, query, or cookie. The values mirror OpenAPI 3's
// SecurityScheme.In field. Typed so the generator/template and the
// runtime can't drift on the spelling.
type AuthLocation string

const (
	// AuthInHeader places the credential in the named request header.
	AuthInHeader AuthLocation = "header"
	// AuthInQuery appends the credential to the request URL's query string.
	AuthInQuery AuthLocation = "query"
	// AuthInCookie attaches the credential as a request cookie.
	AuthInCookie AuthLocation = "cookie"
)

// MissingCredentialError is returned by Apply* helpers when the generated
// proxy server can't find a value for a required security scheme. The
// SchemeName is the spec's securitySchemes key; EnvVar is the env var the
// generator wired up for it (e.g. BEARER_TOKEN_GITHUB). Surfacing both
// gives the user an actionable error rather than a silent 401 downstream.
type MissingCredentialError struct {
	SchemeName string
	EnvVar     string
}

func (e *MissingCredentialError) Error() string {
	return fmt.Sprintf("missing credential for security scheme %q: set %s in the environment", e.SchemeName, e.EnvVar)
}

// ApplyAPIKey sets an apiKey credential on req according to the OpenAPI
// SecurityScheme's `in` field. paramName is the spec's name for the
// header/query parameter / cookie; value is the credential to inject.
// Returns an error if `in` is not one of the three OpenAPI-recognised
// locations — that would indicate a generator/spec mismatch, not user error.
func ApplyAPIKey(req *http.Request, in AuthLocation, paramName, value string) error {
	if value == "" {
		return fmt.Errorf("apiKey value is empty for %q", paramName)
	}
	switch in {
	case AuthInHeader:
		req.Header.Set(paramName, value)
		return nil
	case AuthInQuery:
		// Re-encode the URL's RawQuery so we don't mutate any existing
		// values the operation already encoded.
		q := req.URL.Query()
		q.Set(paramName, value)
		req.URL.RawQuery = q.Encode()
		return nil
	case AuthInCookie:
		// Secure/HttpOnly/SameSite are response-cookie attributes; a
		// request cookie sent by the client cannot meaningfully set them.
		// gosec G124 is a false positive here, matched to cookies.go.
		req.AddCookie(&http.Cookie{Name: paramName, Value: value}) // #nosec G124
		return nil
	default:
		return fmt.Errorf("unsupported apiKey location %q (expected header/query/cookie)", in)
	}
}

// ApplyBearer sets the Authorization header to "Bearer <token>". Used by
// both http+bearer schemes and oauth2 schemes (we treat oauth2 as a
// Bearer token the user has already obtained out-of-band, since proxy
// mode does not perform OAuth2 flows).
func ApplyBearer(req *http.Request, token string) error {
	if token == "" {
		return fmt.Errorf("bearer token is empty")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

// ApplyBasic sets the Authorization header to "Basic <base64(user:pass)>"
// per RFC 7617. Either field being empty is treated as a configuration
// error — Basic auth with one half blank reliably produces 401s and is
// almost always a missing env var rather than a deliberate choice.
func ApplyBasic(req *http.Request, user, pass string) error {
	if user == "" || pass == "" {
		return fmt.Errorf("basic auth requires both username and password")
	}
	creds := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
	req.Header.Set("Authorization", "Basic "+creds)
	return nil
}

// AppendQuery is a small convenience used by the proxy template when an
// operation's path contains an existing query string and we want to add
// to it idempotently without rebuilding the URL from scratch. Generated
// code uses this for static query parameters that don't go through
// url.Values; tests pin the encoding shape.
func AppendQuery(req *http.Request, name, value string) {
	q := req.URL.Query()
	q.Add(name, value)
	req.URL.RawQuery = q.Encode()
}

// QueryEscape is re-exported so the generator template doesn't need to
// import net/url directly. Keeping all encoding behind the runtime
// surface means the runtime alone defines correctness — a future RFC
// quirk gets fixed here, not in every generated file.
func QueryEscape(s string) string { return url.QueryEscape(s) }

// PathEscape is re-exported for generated proxy path parameters. It uses
// path-segment escaping, not query escaping, so spaces become %20 rather
// than '+' and slashes stay encoded as data.
func PathEscape(s string) string { return url.PathEscape(s) }
