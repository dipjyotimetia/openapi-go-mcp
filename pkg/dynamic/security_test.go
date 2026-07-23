// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package dynamic

import (
	"testing"

	"github.com/dipjyotimetia/openapi-go-mcp/pkg/generator"
)

func TestHasClientCredentialsFlow(t *testing.T) {
	ops := []generator.Operation{{Security: [][]generator.SecurityScheme{{{
		Name: "oauth", Kind: generator.SecurityOAuth2, OAuthTokenURL: "https://issuer.example/token",
	}}}}}
	if !hasClientCredentialsFlow(ops) {
		t.Fatal("client credentials flow was not detected")
	}
	if hasClientCredentialsFlow([]generator.Operation{{Security: [][]generator.SecurityScheme{{{
		Name: "oauth", Kind: generator.SecurityOAuth2,
	}}}}}) {
		t.Fatal("pre-acquired OAuth token was treated as a client-credentials flow")
	}
}

func TestRejectExternalReferencesFollowsYAMLAliases(t *testing.T) {
	err := rejectExternalReferences([]byte(`external: &external https://metadata.example/openapi.yaml
paths:
  /things:
    get:
      responses:
        "200":
          $ref: *external
`))
	if err == nil {
		t.Fatal("external $ref hidden behind YAML alias was accepted")
	}
}
