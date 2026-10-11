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

// stubFoundry serves Foundry's System One path only, so a request sent to Jev's /v1/systemone
// fails the test with a 404 rather than passing by accident.
func stubFoundry(t *testing.T, status int, answer string) (*httptest.Server, *map[string]any, *http.Header) {
	t.Helper()
	var body map[string]any
	var header http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != config.MicrosoftSystemOnePath || r.Method != http.MethodPost {
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

// microsoftConfig builds a config whose decision engine is Microsoft-Decision-1. `section` holds
// the keys of ai_router.microsoft, indented; `foundry` is the base_url of a backend connection
// named foundry that the section may borrow from.
func microsoftConfig(t *testing.T, foundry, section string) *config.RouterConfig {
	t.Helper()
	doc := `
strategy: ai
providers:
  foundry:
    base_url: ` + foundry + `
    api_key: provider-key
    api_type: azure
models:
  small:
    description: Cheap model for chit-chat.
    default: true
  big:
    description: Strong reasoning model.
ai_router:
  decision_engine: microsoft
  timeout_seconds: 5
  typesafe:
    api_key: must-not-be-sent-to-foundry
  microsoft:
` + section
	return config.New(fromYAML(t, doc))
}

// Foundry's actual response shape, as returned by a Microsoft-Decision-1 deployment.
const microsoftAnswer = `{"model":"microsoft-decision-1","answers":{"model":{"choice":"big",
	"confidence":0.9994,"probabilities":{"small":0.0003,"big":0.9997},"type":"choice"}},
	"usage":{"input_tokens":108,"output_tokens":1}}`

func TestRouteByAIMicrosoftOwnSettings(t *testing.T) {
	srv, body, header := stubFoundry(t, 200, microsoftAnswer)
	// The /models suffix is what the Foundry portal shows as the endpoint; it must be dropped.
	cfg := microsoftConfig(t, "https://unused.example", "    base_url: "+srv.URL+"/models\n"+
		"    api_key: foundry-key\n    model: msft-decision-1\n")

	model, reason, analysis := RouteByAI(context.Background(), "prove it", cfg, upstream.NewPool())
	if model != "big" || reason != "ai-decision" {
		t.Fatalf("got (%s, %s), want (big, ai-decision); analysis=%v", model, reason, analysis)
	}
	if got := (*header).Get("api-key"); got != "foundry-key" {
		t.Errorf("api-key = %q, want the Foundry key", got)
	}
	if got := (*header).Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want none", got)
	}
	if (*body)["model"] != "msft-decision-1" {
		t.Errorf("model = %v, want the deployment name", (*body)["model"])
	}
	for key, want := range map[string]string{
		"decision_engine": "microsoft", "decision_provider": "microsoft", "decision_model": "msft-decision-1",
		"decision_model_version": "microsoft-decision-1",
	} {
		if got := analysis.Str(key); got != want {
			t.Errorf("analysis[%s] = %q, want %q", key, got, want)
		}
	}
	if !strings.HasPrefix(analysis.Str("rationale"), "Decision-1 picked big with p=1.00") {
		t.Errorf("rationale = %q", analysis.Str("rationale"))
	}
}

func TestRouteByAIMicrosoftBorrowsTheProvider(t *testing.T) {
	srv, body, header := stubFoundry(t, 200, microsoftAnswer)
	cfg := microsoftConfig(t, srv.URL+"/openai/v1/", "    provider: foundry\n")

	if model, _, analysis := RouteByAI(context.Background(), "prove it", cfg, upstream.NewPool()); model != "big" {
		t.Fatalf("model = %s, want big; analysis=%v", model, analysis)
	}
	if got := (*header).Get("api-key"); got != "provider-key" {
		t.Errorf("api-key = %q, want the connection's key", got)
	}
	if (*body)["model"] != config.MicrosoftDefaultModel {
		t.Errorf("model = %v, want the default deployment name", (*body)["model"])
	}
}

// Foundry reports errors as {"error": {"code", "message"}}; the trace must carry the message,
// and the request must still be served by the default model.
func TestRouteByAIMicrosoftErrorFallsBack(t *testing.T) {
	srv, _, _ := stubFoundry(t, 404, `{"error":{"code":"DeploymentNotFound","message":"The API deployment nope does not exist."}}`)
	cfg := microsoftConfig(t, "https://unused.example", "    base_url: "+srv.URL+"\n    api_key: k\n    model: nope\n")

	model, reason, analysis := RouteByAI(context.Background(), "hi", cfg, upstream.NewPool())
	if model != "small" || reason != "ai-fallback-default" {
		t.Fatalf("got (%s, %s), want the default model", model, reason)
	}
	if got := analysis.Str("error"); got != "404: The API deployment nope does not exist." {
		t.Errorf("error = %q", got)
	}
}

func TestRouteByAIMicrosoftWithoutKeyFallsBack(t *testing.T) {
	cfg := microsoftConfig(t, "https://unused.example", "    base_url: https://r.services.ai.azure.com\n")
	model, reason, analysis := RouteByAI(context.Background(), "hi", cfg, upstream.NewPool())
	if model != "small" || reason != "ai-fallback-default" || !strings.Contains(analysis.Str("error"), "Microsoft Foundry API key") {
		t.Fatalf("got (%s, %s, %q), want a fallback naming the key", model, reason, analysis.Str("error"))
	}
}
