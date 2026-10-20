package server

import (
	"net/http/httptest"
	"testing"

	"github.com/satomic/model-router/server-go/internal/config"

	"github.com/satomic/model-router/server-go/internal/omap"
)

func keyRecord(id string) *omap.Map {
	key := omap.New()
	key.Set("id", id)
	return key
}

// The Copilot CLI over BYOK sends no interaction header, only x-initiator, and resends the same
// last user message on every turn of its tool-call loop. Those turns must share one derived id.
func TestDerivedInteractionGroupsACopilotCLILoop(t *testing.T) {
	prompt := "<current_datetime>2026-09-25T00:35:26.053+08:00</current_datetime>\n\nCreate fib.py"
	ids := map[any]bool{}
	for _, initiator := range []string{"user", "agent", "agent"} {
		r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		r.Header.Set("x-initiator", initiator)
		ids[derivedInteractionID(r, keyRecord("k1"), prompt)] = true
	}
	if len(ids) != 1 || ids[nil] {
		t.Fatalf("the turns of one loop should share one derived id, got %v", ids)
	}

	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("x-initiator", "user")
	next := derivedInteractionID(r, keyRecord("k1"), "<current_datetime>2026-09-25T00:40:00</current_datetime>\n\nCreate fib.py")
	if ids[next] {
		t.Error("a new question (a new datetime stamp) must start a new interaction")
	}
	if ids[derivedInteractionID(r, keyRecord("k2"), prompt)] {
		t.Error("the same message on another API key must not share the interaction")
	}
}

// Without x-initiator the client never claimed to run a loop, so identical requests stay
// separate decisions; and a real interaction header always wins over derivation.
func TestDerivedInteractionNeedsTheInitiatorHeader(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	if id := derivedInteractionID(r, keyRecord("k1"), "What is 2+2?"); id != nil {
		t.Errorf("no x-initiator should derive nothing, got %v", id)
	}
	r.Header.Set("x-initiator", "user")
	if id := derivedInteractionID(r, keyRecord("k1"), "   "); id != nil {
		t.Errorf("an empty prompt should derive nothing, got %v", id)
	}
	r.Header.Set("x-interaction-id", "from-the-client")
	if got := interactionID(r); got != "from-the-client" {
		t.Errorf("the client's own header should be used as-is, got %v", got)
	}
}

// VS Code Copilot Chat 0.71 keeps one x-interaction-id for a whole chat window. A tool loop
// resends the same user message and must stay one interaction; the next prompt must not.
func TestScopedInteractionSeparatesPromptsOfOneChat(t *testing.T) {
	loop := "<current_datetime>2026-10-19T22:45:31</current_datetime>\n\n写一首诗"
	next := "<current_datetime>2026-10-19T22:46:37</current_datetime>\n\n要一个英文版的"
	first := scopedInteractionID("chat-1", loop)
	if scopedInteractionID("chat-1", loop) != first {
		t.Error("turns of one tool loop must share the interaction")
	}
	if scopedInteractionID("chat-1", next) == first {
		t.Error("a new prompt in the same chat window must start a new interaction")
	}
	if scopedInteractionID("chat-1", "") != "chat-1" {
		t.Error("with no prompt to go by, the client's id stands")
	}
}

// A client's background task goes to the default model -- within what the caller may use -- with
// no decision.
func TestBackgroundRouteUsesTheDefaultWithinPolicy(t *testing.T) {
	cfg := config.New(mustMap(t, `{"models": {"big": {"provider": "p"}, "small": {"provider": "p", "default": true}}}`))
	if model, reason, _ := backgroundRoute(cfg, nil, "conversation-background"); model != "small" || reason != "client-background" {
		t.Errorf("unrestricted caller: got %s / %s, want the default model", model, reason)
	}
	if model, _, _ := backgroundRoute(cfg, []string{"big"}, "conversation-background"); model != "big" {
		t.Errorf("a caller who may not use the default must get a model they may use, got %s", model)
	}
}

func mustMap(t *testing.T, text string) *omap.Map {
	t.Helper()
	value, err := omap.FromJSON([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return value.(*omap.Map)
}
