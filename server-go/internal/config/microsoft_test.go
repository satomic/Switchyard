package config

import (
	"strings"
	"testing"

	"github.com/satomic/model-router/server-go/internal/omap"
)

func asMap(t *testing.T, v any) *omap.Map {
	t.Helper()
	m, ok := v.(*omap.Map)
	if !ok {
		t.Fatalf("not a map: %T", v)
	}
	return m
}

func TestValidateMicrosoftEngine(t *testing.T) {
	providers := aiRouterDoc(t, "foundry:\n  base_url: https://r.openai.azure.com/\n  api_key: k\n  api_type: azure\n"+
		"claude:\n  base_url: https://api.anthropic.com\n  api_key: k\n  api_type: anthropic\n")
	cases := []struct {
		name, doc, wantErr string
	}{
		{"own address and key", "decision_engine: microsoft\nmicrosoft:\n  base_url: https://r.services.ai.azure.com/models\n  api_key: k\n  model: msft-decision-1\n", ""},
		{"borrowed from a provider", "decision_engine: microsoft\nmicrosoft:\n  provider: foundry\n", ""},
		{"provider for the key only", "decision_engine: microsoft\nmicrosoft:\n  base_url: https://other.services.ai.azure.com\n  provider: foundry\n", ""},
		{"no section", "decision_engine: microsoft\n", "ai_router.microsoft needs base_url and api_key"},
		{"no key", "decision_engine: microsoft\nmicrosoft:\n  base_url: https://r.services.ai.azure.com\n", "ai_router.microsoft needs base_url and api_key"},
		{"no address", "decision_engine: microsoft\nmicrosoft:\n  api_key: k\n", "ai_router.microsoft needs base_url and api_key"},
		{"bad address", "decision_engine: microsoft\nmicrosoft:\n  base_url: r.services.ai.azure.com\n  api_key: k\n", "microsoft.base_url must start"},
		{"unknown provider", "decision_engine: microsoft\nmicrosoft:\n  provider: nope\n", "provider 'nope' is not in providers"},
		{"anthropic provider", "decision_engine: microsoft\nmicrosoft:\n  provider: claude\n", "is an Anthropic connection"},
		// Kept but unused while another engine is selected.
		{"unused section", "decision_engine: llm\nmicrosoft:\n  model: msft-decision-1\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := strings.Join(validateAIRouter(aiRouterDoc(t, tc.doc), asMap(t, providers)), "; ")
			if tc.wantErr == "" && errs != "" {
				t.Errorf("unexpected errors: %s", errs)
			}
			if tc.wantErr != "" && !strings.Contains(errs, tc.wantErr) {
				t.Errorf("errors %q do not mention %q", errs, tc.wantErr)
			}
		})
	}
}

func TestFoundryResourceURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://r.services.ai.azure.com/models":                           "https://r.services.ai.azure.com",
		"https://r.services.ai.azure.com/models/":                          "https://r.services.ai.azure.com",
		"https://r.services.ai.azure.com/openai/v1/":                       "https://r.services.ai.azure.com",
		"https://r.openai.azure.com/":                                      "https://r.openai.azure.com",
		"https://r.cognitiveservices.azure.com/openai":                     "https://r.cognitiveservices.azure.com",
		"https://r.services.ai.azure.com/api/projects/p1":                  "https://r.services.ai.azure.com",
		"https://r.services.ai.azure.com/providers/microsoft/v1/systemone": "https://r.services.ai.azure.com",
		" https://gateway.example.com/foundry ":                            "https://gateway.example.com/foundry",
		"":                                                                 "",
	} {
		if got := FoundryResourceURL(in); got != want {
			t.Errorf("FoundryResourceURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMicrosoftEndpoint(t *testing.T) {
	cfg := New(asMap(t, aiRouterDoc(t, `
providers:
  foundry:
    base_url: https://r.openai.azure.com/
    api_key: provider-key
    api_type: azure
ai_router:
  decision_engine: microsoft
  microsoft:
    provider: foundry
`)))
	ep, ok := cfg.SystemOne()
	if !ok {
		t.Fatal("microsoft is not a System One engine")
	}
	if got := ep.URL(); got != "https://r.openai.azure.com/providers/microsoft/v1/systemone" {
		t.Errorf("URL = %q", got)
	}
	if ep.APIKey != "provider-key" || ep.KeyHeader != "api-key" || ep.Model != MicrosoftDefaultModel {
		t.Errorf("endpoint = %+v", ep)
	}

	// The section's own fields win over the connection's.
	cfg = New(asMap(t, aiRouterDoc(t, `
providers:
  foundry:
    base_url: https://r.openai.azure.com/
    api_key: provider-key
ai_router:
  decision_engine: microsoft
  microsoft:
    provider: foundry
    base_url: https://d.services.ai.azure.com/models
    api_key: own-key
    model: msft-decision-1
`)))
	ep, _ = cfg.SystemOne()
	if ep.URL() != "https://d.services.ai.azure.com/providers/microsoft/v1/systemone" || ep.APIKey != "own-key" || ep.Model != "msft-decision-1" {
		t.Errorf("endpoint = %+v (%s)", ep, ep.URL())
	}
}
