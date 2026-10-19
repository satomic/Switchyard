package routing

import (
	"testing"

	"github.com/satomic/model-router/server-go/internal/omap"
)

func userMessage(content string) *omap.Map {
	m := omap.New()
	m.Set("role", "user")
	m.Set("content", content)
	return m
}

// Copilot CLI in GPT-5 mode puts a fixed reminder after the real request; the request must win.
func TestExtractUserPromptSkipsClientReminders(t *testing.T) {
	reminder := "<system_reminder>\nIMPORTANT: The tools listed below are deferred.\n</system_reminder>"
	task := "<current_datetime>2026-10-19T21:39:15</current_datetime>\n\nFix sub() in calc3.py"
	if got := ExtractUserPrompt([]any{userMessage(task), userMessage(reminder)}); got != task {
		t.Errorf("got %q, want the task", got)
	}
	if got := ExtractUserPrompt([]any{userMessage(reminder)}); got != reminder {
		t.Errorf("with nothing else, the reminder is still better than nothing: %q", got)
	}
	mixed := "<system_reminder>note</system_reminder>\nPlease also add tests"
	if got := ExtractUserPrompt([]any{userMessage(task), userMessage(mixed)}); got != mixed {
		t.Errorf("a message with real text after a reminder is the person's: %q", got)
	}
}
