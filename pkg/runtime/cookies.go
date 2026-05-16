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
func CookieRequestEditor(values CookieValues) func(ctx context.Context, req *http.Request) error {
	return func(_ context.Context, req *http.Request) error {
		for name, v := range values {
			if v == "" {
				continue
			}
			req.AddCookie(&http.Cookie{Name: name, Value: v})
		}
		return nil
	}
}
