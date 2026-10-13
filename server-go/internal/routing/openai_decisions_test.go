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

// stubDecisions serves POST /v1/decisions only, so a request sent to any other path fails.
func stubDecisions(t *testing.T, status int, answer string) (*httptest.Server, *map[string]any, *http.Header) {
	t.Helper()
	var body map[string]any
	var header http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/decisions" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		header = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	return srv, &body, &header
}

func openaiConfig(t *testing.T, baseURL, key string) *config.RouterConfig {
	t.Helper()
	return config.New(fromYAML(t, `
strategy: ai
models:
  small:
    description: Cheap model for chit-chat.
    default: true
  big:
    description: Strong reasoning model.
  bare: {}
ai_router:
  decision_engine: openai
  timeout_seconds: 5
  typesafe:
    api_key: must-not-be-sent-to-openai
  openai:
    api_key: `+key+`
    base_url: `+baseURL+`/v1
`))
}

// The Decisions API's actual response shape.
const openaiAnswer = `{"model":"gpt-6-luna","answers":[{"type":"choice","name":"model","choice":"big",
	"probabilities":[{"value":"small","probability":0.02},{"value":"big","probability":0.97},{"value":"bare","probability":0.01}],
	"confidence":0.96}],"usage":{"input_tokens":170,"output_tokens":0,"total_tokens":170}}`

func TestRouteByAIOpenAIDecisions(t *testing.T) {
	srv, body, header := stubDecisions(t, 200, openaiAnswer)
	cfg := openaiConfig(t, srv.URL, "sk-test")

	model, reason, analysis := RouteByAI(context.Background(), "prove it", cfg, upstream.NewPool())
	if model != "big" || reason != "ai-decision" {
		t.Fatalf("got (%s, %s), want (big, ai-decision); analysis=%v", model, reason, analysis)
	}
	if got := (*header).Get("Authorization"); got != "Bearer sk-test" {
		t.Errorf("Authorization = %q, want the OpenAI key", got)
	}
	b := *body
	if b["model"] != "gpt-6-luna" || b["input"] != "prove it" {
		t.Errorf("model/input = %v / %v", b["model"], b["input"])
	}
	q := b["questions"].([]any)[0].(map[string]any)
	if q["type"] != "choice" || q["name"] != "model" {
		t.Errorf("question = %v", q)
	}
	choices := q["choices"].([]any)
	if len(choices) != 3 || choices[0].(map[string]any)["description"] != "Cheap model for chit-chat." {
		t.Errorf("choices = %v", choices)
	}
	if _, ok := choices[2].(map[string]any)["description"]; ok {
		t.Errorf("a model without a description must be sent as a bare value: %v", choices[2])
	}
	for key, want := range map[string]string{"decision_engine": "openai", "decision_provider": "openai", "decision_model": "gpt-6-luna", "decision_model_version": "gpt-6-luna"} {
		if got := analysis.Str(key); got != want {
			t.Errorf("analysis[%s] = %q, want %q", key, got, want)
		}
	}
	if p := analysis.Map("probabilities"); p == nil || p.Str("big") != "0.97" {
		t.Errorf("probabilities = %v", analysis.Value("probabilities"))
	}
	if !strings.HasPrefix(analysis.Str("rationale"), "gpt-6-luna picked big with p=0.97") {
		t.Errorf("rationale = %q", analysis.Str("rationale"))
	}
}

// OpenAI's error envelope must reach the trace, and the request is still served by the default.
func TestRouteByAIOpenAIDecisionsErrorFallsBack(t *testing.T) {
	srv, _, _ := stubDecisions(t, 401, `{"error":{"message":"Incorrect API key provided: sk-bad.","type":"invalid_request_error","code":"invalid_api_key"}}`)
	cfg := openaiConfig(t, srv.URL, "sk-bad")
	model, reason, analysis := RouteByAI(context.Background(), "hi", cfg, upstream.NewPool())
	if model != "small" || reason != "ai-fallback-default" {
		t.Fatalf("got (%s, %s), want the default model", model, reason)
	}
	if got := analysis.Str("error"); got != "401: Incorrect API key provided: sk-bad." {
		t.Errorf("error = %q", got)
	}
}

func TestRouteByAIOpenAIDecisionsRefusalFallsBack(t *testing.T) {
	srv, _, _ := stubDecisions(t, 200, `{"model":"gpt-6-luna","answers":[{"type":"refusal","name":"model"}],"usage":{"input_tokens":50}}`)
	cfg := openaiConfig(t, srv.URL, "sk-test")
	model, reason, analysis := RouteByAI(context.Background(), "hi", cfg, upstream.NewPool())
	if model != "small" || reason != "ai-fallback-default" || !strings.Contains(analysis.Str("error"), "refused") {
		t.Fatalf("got (%s, %s, %q), want a refusal fallback", model, reason, analysis.Str("error"))
	}
}

func TestRouteByAIOpenAIDecisionsWithoutKeyFallsBack(t *testing.T) {
	saved := config.EnvOpenAIAPIKey
	t.Cleanup(func() { config.EnvOpenAIAPIKey = saved })
	config.EnvOpenAIAPIKey = ""
	cfg := openaiConfig(t, "http://127.0.0.1:1", "''")
	model, reason, analysis := RouteByAI(context.Background(), "hi", cfg, upstream.NewPool())
	if model != "small" || reason != "ai-fallback-default" || !strings.Contains(analysis.Str("error"), "OpenAI API key") {
		t.Fatalf("got (%s, %s, %q), want a fallback naming the key", model, reason, analysis.Str("error"))
	}
}
