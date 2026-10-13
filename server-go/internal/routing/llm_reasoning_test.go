package routing

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/satomic/model-router/server-go/internal/config"
	"github.com/satomic/model-router/server-go/internal/upstream"
)

// The LLM decision call must speak a reasoning model's dialect: max_completion_tokens instead of
// max_tokens, no temperature, and the configured reasoning_effort. A plain model keeps max_tokens.
func TestDecideByLLMReasoningModelParameters(t *testing.T) {
	for _, tc := range []struct {
		name, reasoning, effort string
		wantMax, wantEffort     string
	}{
		{"reasoning model with effort", "true", "none", "max_completion_tokens", "none"},
		{"reasoning model, model default effort", "true", "", "max_completion_tokens", ""},
		{"plain model", "false", "none", "max_tokens", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
					http.NotFound(w, r)
					return
				}
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &body)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"model\":\"big\",\"rationale\":\"a proof\"}"}}],"usage":{"prompt_tokens":200,"completion_tokens":30}}`)
			}))
			defer srv.Close()
			effort := ""
			if tc.effort != "" {
				effort = "\n  decision_reasoning_effort: " + tc.effort
			}
			cfg := config.New(fromYAML(t, `
strategy: ai
providers:
  p:
    base_url: `+srv.URL+`/v1
    api_key: k
    api_type: openai
models:
  small:
    description: Cheap.
    default: true
  big:
    description: Strong.
  judge:
    description: The decision model.
    reasoning: `+tc.reasoning+`
ai_router:
  decision_model: judge
  decision_provider: p
  timeout_seconds: 5`+effort+`
`))
			model, reason, analysis := RouteByAI(context.Background(), "prove it", cfg, upstream.NewPool())
			if model != "big" || reason != "ai-decision" {
				t.Fatalf("got (%s, %s): %v", model, reason, analysis.Value("error"))
			}
			if _, ok := body[tc.wantMax]; !ok {
				t.Errorf("request lacks %s: %v", tc.wantMax, body)
			}
			if tc.reasoning == "true" {
				for _, k := range []string{"max_tokens", "temperature"} {
					if _, ok := body[k]; ok {
						t.Errorf("reasoning model must not get %s", k)
					}
				}
			}
			if got, _ := body["reasoning_effort"].(string); got != tc.wantEffort {
				t.Errorf("reasoning_effort = %q, want %q", got, tc.wantEffort)
			}
		})
	}
}
