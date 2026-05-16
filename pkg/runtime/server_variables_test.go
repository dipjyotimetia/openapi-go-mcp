// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package runtime

import "testing"

func TestSubstituteServerVariables_Substitutes(t *testing.T) {
	got, err := SubstituteServerVariables("{scheme}://{host}/api/{version}", map[string]string{
		"scheme":  "https",
		"host":    "api.example.com",
		"version": "v2",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "https://api.example.com/api/v2" {
		t.Errorf("got %q", got)
	}
}

func TestSubstituteServerVariables_LeavesMissingPlaceholders(t *testing.T) {
	got, err := SubstituteServerVariables("{scheme}://api/{ver}", map[string]string{
		"scheme": "https",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "https://api/{ver}" {
		t.Errorf("missing placeholder should be retained verbatim, got %q", got)
	}
}

func TestSubstituteServerVariables_NoVars(t *testing.T) {
	for _, in := range []string{"", "https://example.com"} {
		got, err := SubstituteServerVariables(in, nil)
		if err != nil || got != in {
			t.Errorf("no-vars: got %q,%v want %q", got, err, in)
		}
	}
}

func TestSubstituteServerVariables_UnterminatedBrace(t *testing.T) {
	_, err := SubstituteServerVariables("{scheme://api", map[string]string{"scheme": "https"})
	if err == nil {
		t.Fatal("expected error for unterminated `{`")
	}
}
