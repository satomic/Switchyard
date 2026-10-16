package server

import (
	"testing"

	"github.com/satomic/model-router/server-go/internal/omap"
	"github.com/satomic/model-router/server-go/internal/upstream"
)

// fakeLuna behaves like gpt-6-luna on Azure /chat/completions: tools are refused unless
// reasoning_effort is "none".
func fakeLuna(calls *[]*omap.Map) func(*omap.Map) (string, error) {
	return func(b *omap.Map) (string, error) {
		*calls = append(*calls, b.Clone())
		if hasTools(b) && b.Str("reasoning_effort") != "none" {
			return "", &upstream.Error{Status: 400, Message: "400: Function tools with reasoning_effort are not supported for this model in /v1/chat/completions. To use function tools, use /v1/responses or set reasoning_effort to 'none'."}
		}
		return "ok", nil
	}
}

func toolBody() *omap.Map {
	b := omap.New()
	b.Set("messages", []any{mapOf("role", "user", "content", "write a poem")})
	b.Set("tools", []any{mapOf("type", "function", "function", mapOf("name", "view"))})
	return b
}

func TestEffortRetryThenRemembered(t *testing.T) {
	noEffortWithTools.Delete(effortKey("p1", "gpt-6-luna"))
	var calls []*omap.Map
	var notes []string
	note := func(s string) { notes = append(notes, s) }

	out, err := withEffortRetry(true, "p1", "gpt-6-luna", toolBody(), note, fakeLuna(&calls))
	if err != nil || out != "ok" {
		t.Fatalf("first request: got %q, %v", out, err)
	}
	if len(calls) != 2 || calls[0].Has("reasoning_effort") || calls[1].Str("reasoning_effort") != "none" {
		t.Fatalf("first request should be refused once and retried with none, calls=%d", len(calls))
	}
	if len(notes) != 1 {
		t.Fatalf("the retry should be noted for the trace, notes=%v", notes)
	}

	// The model is remembered: the next tool request goes out with none straight away.
	calls = nil
	if out, err := withEffortRetry(true, "p1", "gpt-6-luna", toolBody(), note, fakeLuna(&calls)); err != nil || out != "ok" {
		t.Fatalf("second request: got %q, %v", out, err)
	}
	if len(calls) != 1 || calls[0].Str("reasoning_effort") != "none" {
		t.Fatalf("second request should need one call with none, calls=%d", len(calls))
	}
}

func TestEffortUntouchedWhenNotNeeded(t *testing.T) {
	noEffortWithTools.Delete(effortKey("p1", "gpt-5.4"))
	ok := func(calls *[]*omap.Map) func(*omap.Map) (string, error) {
		return func(b *omap.Map) (string, error) { *calls = append(*calls, b.Clone()); return "ok", nil }
	}
	never := func(string) { t.Fatal("body should not be adjusted") }

	// A reasoning model that accepts tools at its default effort keeps it.
	var calls []*omap.Map
	withEffortRetry(true, "p1", "gpt-5.4", toolBody(), never, ok(&calls))
	if calls[0].Has("reasoning_effort") {
		t.Fatal("gpt-5.4 should keep its default effort")
	}

	// No tools: luna is fine as it is, nothing to change.
	calls = nil
	noTools := omap.New()
	noTools.Set("messages", []any{})
	if _, err := withEffortRetry(true, "p1", "gpt-6-luna-x", noTools, never, fakeLuna(&calls)); err != nil || len(calls) != 1 {
		t.Fatalf("a request without tools should pass untouched, err=%v calls=%d", err, len(calls))
	}

	// An effort the caller chose is not overridden, and its refusal is returned as is.
	calls = nil
	b := toolBody()
	b.Set("reasoning_effort", "high")
	if _, err := withEffortRetry(true, "p1", "gpt-6-luna-y", b, never, fakeLuna(&calls)); err == nil || len(calls) != 1 {
		t.Fatalf("an explicit effort should not be retried, err=%v calls=%d", err, len(calls))
	}

	// Another upstream error is not retried.
	calls = nil
	other := func(b *omap.Map) (string, error) {
		calls = append(calls, b)
		return "", &upstream.Error{Status: 400, Message: "400: some other problem"}
	}
	if _, err := withEffortRetry(true, "p1", "gpt-6-luna-z", toolBody(), never, other); err == nil || len(calls) != 1 {
		t.Fatalf("an unrelated 400 should not be retried, calls=%d", len(calls))
	}
}
