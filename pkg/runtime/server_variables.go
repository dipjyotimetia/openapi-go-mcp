// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package runtime

import (
	"fmt"
	"strings"
)

// SubstituteServerVariables expands `{name}` placeholders in an OpenAPI
// server URL template using the values supplied via runtime.WithServerVariables.
//
// Error semantics are intentionally asymmetric:
//   - Unterminated `{` returns an error so a malformed template surfaces
//     loudly (it indicates a buggy spec, not a missing config).
//   - Missing variable names keep their `{name}` form in the output, no
//     error returned. This lets callers spot unsubstituted holes in their
//     output URL (e.g. via a quick `strings.Contains(url, "{")` check) and
//     fall back to a default without fighting an error return on the
//     common case of partial substitution.
//
// An empty template or empty vars map is a no-op.
//
// Caller pattern (in their main, before constructing the upstream client):
//
//	base, _ := runtime.SubstituteServerVariables(spec.Servers[0].URL, cfg.ServerVariables)
//	client, _ := upstream.NewClientWithResponses(base, ...)
func SubstituteServerVariables(template string, vars map[string]string) (string, error) {
	if template == "" || len(vars) == 0 {
		return template, nil
	}
	var b strings.Builder
	b.Grow(len(template))
	i := 0
	for i < len(template) {
		c := template[i]
		if c != '{' {
			b.WriteByte(c)
			i++
			continue
		}
		end := strings.IndexByte(template[i:], '}')
		if end < 0 {
			return "", fmt.Errorf("server URL template %q has unterminated `{`", template)
		}
		name := template[i+1 : i+end]
		if v, ok := vars[name]; ok {
			b.WriteString(v)
		} else {
			// Keep the placeholder so missing substitutions are obvious.
			b.WriteString(template[i : i+end+1])
		}
		i += end + 1
	}
	return b.String(), nil
}
