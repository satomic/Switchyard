package routing

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/satomic/model-router/server-go/internal/config"
	"github.com/satomic/model-router/server-go/internal/omap"
	"github.com/satomic/model-router/server-go/internal/upstream"
)

// OpenAIDecisionInstructions is the choice question asked of OpenAI's Decisions API. It is the
// System One wording with `user_request` dropped: here the request is the whole `input`, not a
// named field of a state object.
const OpenAIDecisionInstructions = "Which backend model should handle this request? " +
	"Pick the option whose description best matches what the request needs. " +
	"Prefer a cheaper, faster model when the request is simple, and a stronger model only when the request needs it."

// BuildOpenAIDecisionRequest is the POST /v1/decisions body for one routing decision: the
// user's request as the input, and one choice question whose choices are the model catalog,
// each described by its catalog description. Exported so the console's preview shows it.
func BuildOpenAIDecisionRequest(truncated string, cfg *config.RouterConfig) *omap.Map {
	choices := make([]any, 0, cfg.Models.Len())
	for _, name := range cfg.Models.Keys() {
		c := mapOf("value", name)
		// A model without a description is still a valid choice; the API accepts a bare value.
		if desc := strings.TrimSpace(cfg.ModelMeta(name).Str("description")); desc != "" {
			c.Set("description", desc)
		}
		choices = append(choices, c)
	}
	return mapOf(
		"model", cfg.OpenAI.Model,
		"input", truncated,
		"questions", []any{mapOf(
			"type", "choice",
			"name", SystemOneQuestionID,
			"instructions", OpenAIDecisionInstructions,
			"choices", choices,
		)},
	)
}

// decideByOpenAI asks OpenAI's Decisions API one choice over the catalog. The answer carries a
// probability per choice and a confidence, recorded in the same analysis fields Jev's answer
// uses, so the trace reads the same whichever engine made the decision.
func decideByOpenAI(ctx context.Context, truncated string, cfg *config.RouterConfig, analysis *omap.Map) (string, error) {
	analysis.Set("decision_model", cfg.OpenAI.Model)
	analysis.Set("decision_provider", "openai")
	if cfg.OpenAI.APIKey == "" {
		return "", fmt.Errorf("no OpenAI API key: set ai_router.openai.api_key or OPENAI_API_KEY")
	}
	if n := cfg.Models.Len(); n < 2 || n > config.OpenAIDecisionsMaxOptions {
		return "", fmt.Errorf("OpenAI Decisions needs between 2 and %d candidate models, got %d", config.OpenAIDecisionsMaxOptions, n)
	}
	body := BuildOpenAIDecisionRequest(truncated, cfg)
	question := body.Value("questions").([]any)[0].(*omap.Map)
	criteria := omap.New()
	for _, c := range question.Value("choices").([]any) {
		cm := c.(*omap.Map)
		if cm.Has("description") {
			criteria.Set(cm.Str("value"), cm.Value("description"))
		} else {
			criteria.Set(cm.Str("value"), nil)
		}
	}
	analysis.Set("decision_question", mapOf("instructions", question.Value("instructions"), "criteria", criteria))

	resp, err := upstream.OpenAIDecisions(ctx, cfg.OpenAI.BaseURL, cfg.OpenAI.APIKey, body)
	if err != nil {
		return "", err
	}
	if version := resp.Str("model"); version != "" {
		analysis.Set("decision_model_version", version)
	}
	if usage := resp.Map("usage"); usage != nil {
		analysis.Set("decision_usage", mapOf("prompt_tokens", usage.Value("input_tokens"), "completion_tokens", usage.Value("output_tokens")))
	}
	var answer *omap.Map
	for _, a := range resp.Slice("answers") {
		if am, ok := a.(*omap.Map); ok && am.Str("name") == SystemOneQuestionID {
			answer = am
		}
	}
	if answer == nil {
		return "", fmt.Errorf("OpenAI Decisions returned no answer for the routing question")
	}
	if raw, err := json.Marshal(answer); err == nil {
		analysis.Set("raw_response", string(raw))
	}
	if answer.Str("type") == "refusal" {
		return "", fmt.Errorf("OpenAI Decisions refused the routing question")
	}
	// probabilities come back as [{value, probability}]; the trace keeps Jev's {name: p} shape.
	probs := omap.New()
	for _, p := range answer.Slice("probabilities") {
		if pm, ok := p.(*omap.Map); ok {
			probs.Set(pm.Str("value"), pm.Value("probability"))
		}
	}
	choice := answer.Str("choice")
	analysis.Set("probabilities", probs)
	analysis.Set("confidence", answer.Value("confidence"))
	analysis.Set("rationale", fmt.Sprintf("%s picked %s with p=%s (confidence %s)",
		cfg.OpenAI.Model, choice, numberText(probs.Value(choice)), numberText(answer.Value("confidence"))))
	return choice, nil
}
