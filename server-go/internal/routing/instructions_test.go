package routing

import (
	"testing"

	"github.com/satomic/model-router/server-go/internal/config"
	"github.com/satomic/model-router/server-go/internal/omap"
)

// Every dedicated decision engine is asked the same question: the built-in neutral default, or
// ai_router.decision_instructions word for word.
func TestDecisionInstructionsSharedByAllEngines(t *testing.T) {
	for _, tc := range []struct{ name, extra, want string }{
		{"default", "", config.DefaultDecisionInstructions},
		{"blank", "  decision_instructions: \"  \"\n", config.DefaultDecisionInstructions},
		{"custom", "  decision_instructions: Pick the cheapest model that can do it.\n", "Pick the cheapest model that can do it."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.New(fromYAML(t, "strategy: ai\nmodels:\n  small:\n    default: true\n  big: {}\nai_router:\n  decision_engine: typesafe\n"+tc.extra))
			got := map[string]any{}
			for _, engine := range []string{"typesafe", "laya", "microsoft"} {
				body := BuildSystemOneRequest("hi", cfg, config.SystemOneEndpoint{Engine: engine})
				got[engine] = body.Map("questions").Map(SystemOneQuestionID).Value("instructions")
			}
			got["openai"] = BuildOpenAIDecisionRequest("hi", cfg).Value("questions").([]any)[0].(*omap.Map).Value("instructions")
			for engine, v := range got {
				if v != tc.want {
					t.Errorf("%s instructions = %q, want %q", engine, v, tc.want)
				}
			}
		})
	}
}
