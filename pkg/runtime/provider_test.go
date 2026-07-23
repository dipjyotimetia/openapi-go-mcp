// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package runtime

import "testing"

func TestLLMProviderValues(t *testing.T) {
	if LLMProviderStandard != "standard" {
		t.Errorf("LLMProviderStandard = %q, want standard", LLMProviderStandard)
	}
	if LLMProviderOpenAI != "openai" {
		t.Errorf("LLMProviderOpenAI = %q, want openai", LLMProviderOpenAI)
	}
}
