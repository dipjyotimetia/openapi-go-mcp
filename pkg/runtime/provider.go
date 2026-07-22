// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package runtime

// LLMProvider selects the input-schema dialect exposed by generated tool
// registrations. Generated code treats unknown values as standard.
type LLMProvider string

const (
	// LLMProviderStandard selects the default MCP-compatible JSON Schema.
	LLMProviderStandard LLMProvider = "standard"
	// LLMProviderOpenAI selects OpenAI's strict, $ref-free schema dialect.
	LLMProviderOpenAI LLMProvider = "openai"
)
