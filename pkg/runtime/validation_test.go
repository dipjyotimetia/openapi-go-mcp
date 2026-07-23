// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package runtime

import (
	"encoding/json"
	"testing"
)

func TestCompileInputValidator_ValidatesDraft7SchemaWithDefs(t *testing.T) {
	validator := CompileInputValidator(json.RawMessage(`{
		"type": "object",
		"properties": {"name": {"$ref": "#/$defs/name"}},
		"required": ["name"],
		"additionalProperties": false,
		"$defs": {"name": {"type": "string"}}
	}`))

	if err := validator.Validate(map[string]any{"name": "ok"}); err != nil {
		t.Fatalf("valid arguments: %v", err)
	}
	for _, args := range []map[string]any{
		{},
		{"name": 7},
		{"name": "ok", "extra": true},
	} {
		if err := validator.Validate(args); err == nil {
			t.Errorf("Validate(%v) succeeded, want schema validation error", args)
		}
	}
}
