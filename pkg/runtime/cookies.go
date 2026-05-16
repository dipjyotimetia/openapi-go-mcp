// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package runtime

import (
	"context"
	"net/http"
	"slices"
)

// CookieValues is a typed alias for the decoded cookies the generated
// handler extracts from MCP tool arguments before delegating to the
// upstream client. Generated code populates it from args["cookie"].
type CookieValues map[string]string

// CookieRequestEditor returns a request editor that adds each non-empty
// cookie in values to outgoing HTTP requests via req.AddCookie. The returned
// function's signature matches oapi-codegen's RequestEditorFn (which is
// `func(context.Context, *http.Request) error` underneath) so generated
// code can pass it as the trailing variadic reqEditors argument without
// importing oapi-codegen's runtime package.
//
// Cookies are emitted in sorted name order so VCR-style HTTP traces remain
// deterministic across runs.
func CookieRequestEditor(values CookieValues) func(ctx context.Context, req *http.Request) error {
	return func(_ context.Context, req *http.Request) error {
		names := make([]string, 0, len(values))
		for name := range values {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			v := values[name]
			if v == "" {
				continue
			}
			// gosec G124 wants Secure/HttpOnly/SameSite attributes, which are
			// server-side directives telling browsers how to store a cookie.
			// This is the client side: we're attaching a Cookie request
			// header, where only Name+Value are wire-relevant per RFC 6265 §4.2.
			req.AddCookie(&http.Cookie{Name: name, Value: v}) // #nosec G124
		}
		return nil
	}
}
