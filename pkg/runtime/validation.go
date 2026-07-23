// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// InputValidator validates MCP tool arguments against a generated input
// schema. It compiles the schema once during tool registration so individual
// tool calls only perform validation.
type InputValidator struct {
	schema *jsonschema.Schema
	err    error
}

// CompileInputValidator prepares a validator for a generated raw input
// schema. Generated schemas use draft-07 keywords and $defs; the compiler
// resolves local refs into $defs even though the generated dialect otherwise
// follows draft-07 semantics.
// Invalid schemas are retained as validation failures so adapters can return a
// normal IsError tool result instead of failing registration or the transport.
func CompileInputValidator(raw json.RawMessage) *InputValidator {
	if len(raw) == 0 {
		return &InputValidator{err: fmt.Errorf("input schema is empty")}
	}

	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return &InputValidator{err: fmt.Errorf("parse input schema: %w", err)}
	}

	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft7)
	if err := compiler.AddResource("input-schema.json", doc); err != nil {
		return &InputValidator{err: fmt.Errorf("add input schema: %w", err)}
	}
	schema, err := compiler.Compile("input-schema.json")
	if err != nil {
		return &InputValidator{err: fmt.Errorf("compile input schema: %w", err)}
	}
	return &InputValidator{schema: schema}
}

// Validate reports a ToolError suitable for HandleError when arguments do not
// conform to the generated input schema.
func (v *InputValidator) Validate(arguments map[string]any) error {
	if v == nil || v.err != nil {
		var cause error
		if v != nil {
			cause = v.err
		}
		if cause == nil {
			cause = fmt.Errorf("input validator is nil")
		}
		return &ToolError{
			Code:    "invalid_input_schema",
			Message: fmt.Sprintf("unable to validate tool arguments: %v", cause),
			Cause:   cause,
		}
	}
	if err := v.schema.Validate(arguments); err != nil {
		return &ToolError{
			Code:    "invalid_arguments",
			Message: fmt.Sprintf("tool arguments do not match input schema: %v", err),
			Cause:   err,
		}
	}
	return nil
}
