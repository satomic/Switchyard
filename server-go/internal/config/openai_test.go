package config

import (
	"strings"
	"testing"
)

func TestValidateOpenAIDecisions(t *testing.T) {
	saved := EnvOpenAIAPIKey
	t.Cleanup(func() { EnvOpenAIAPIKey = saved })
	EnvOpenAIAPIKey = ""
	cases := []struct{ name, doc, wantErr string }{
		{"with key", "decision_engine: openai\nopenai:\n  api_key: sk-x\n", ""},
		{"no key", "decision_engine: openai\nopenai:\n  model: gpt-6-luna\n", "openai.api_key is required"},
		{"no section", "decision_engine: openai\n", "openai.api_key is required"},
		{"bad base_url", "decision_engine: openai\nopenai:\n  api_key: k\n  base_url: api.openai.com\n", "openai.base_url must start"},
		{"unused section", "decision_engine: llm\nopenai:\n  model: gpt-6-luna\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := strings.Join(validateAIRouter(aiRouterDoc(t, tc.doc), nil), "; ")
			if tc.wantErr == "" && errs != "" {
				t.Errorf("unexpected errors: %s", errs)
			}
			if tc.wantErr != "" && !strings.Contains(errs, tc.wantErr) {
				t.Errorf("errors %q do not mention %q", errs, tc.wantErr)
			}
		})
	}
	// The environment supplies the key: no section needed.
	EnvOpenAIAPIKey = "sk-env"
	if errs := validateAIRouter(aiRouterDoc(t, "decision_engine: openai\n"), nil); len(errs) != 0 {
		t.Errorf("env key should satisfy validation, got %v", errs)
	}
}

func TestOpenAIDecisionsDefaults(t *testing.T) {
	saved := EnvOpenAIAPIKey
	t.Cleanup(func() { EnvOpenAIAPIKey = saved })
	EnvOpenAIAPIKey = "sk-env"
	cfg := New(asMap(t, aiRouterDoc(t, "ai_router:\n  decision_engine: openai\n")))
	if cfg.OpenAI.APIKey != "sk-env" || cfg.OpenAI.Model != OpenAIDecisionsDefaultModel || cfg.OpenAI.BaseURL != OpenAIDecisionsDefaultBaseURL {
		t.Fatalf("defaults = %+v", cfg.OpenAI)
	}
}

func TestValidateDecisionReasoningEffort(t *testing.T) {
	if errs := validateAIRouter(aiRouterDoc(t, "decision_reasoning_effort: none\n"), nil); len(errs) != 0 {
		t.Errorf("none should be valid: %v", errs)
	}
	errs := strings.Join(validateAIRouter(aiRouterDoc(t, "decision_reasoning_effort: turbo\n"), nil), "; ")
	if !strings.Contains(errs, "decision_reasoning_effort must be one of") {
		t.Errorf("errors = %q", errs)
	}
}

func TestValidateDecisionInstructions(t *testing.T) {
	if errs := validateAIRouter(aiRouterDoc(t, "decision_instructions: Pick one.\n"), nil); len(errs) != 0 {
		t.Errorf("a string should be valid: %v", errs)
	}
	errs := strings.Join(validateAIRouter(aiRouterDoc(t, "decision_instructions: [a, b]\n"), nil), "; ")
	if !strings.Contains(errs, "decision_instructions must be a string") {
		t.Errorf("errors = %q", errs)
	}
	long := strings.Repeat("x", MaxDecisionInstructionsChars+1)
	errs = strings.Join(validateAIRouter(aiRouterDoc(t, "decision_instructions: "+long+"\n"), nil), "; ")
	if !strings.Contains(errs, "at most") {
		t.Errorf("errors = %q", errs)
	}
}
