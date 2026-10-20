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
	if got := ExtractUserPrompt([]any{userMessage(task), userMessage(mixed)}); got != "Please also add tests" {
		t.Errorf("a message with real text around a reminder is the person's, minus the reminder: %q", got)
	}
}

// VS Code Copilot Chat 0.71 appends the reminder inside the user's own message.
func TestExtractUserPromptStripsInlineReminder(t *testing.T) {
	msg := "<current_datetime>2026-10-19T22:46:37.988+08:00</current_datetime>\n\n要一个英文版的\n\n" +
		"<system_reminder>\nReminder: This chat currently has an auto-generated title. call `rename_chat`\n</system_reminder>"
	got := ExtractUserPrompt([]any{userMessage(msg)})
	if got != "<current_datetime>2026-10-19T22:46:37.988+08:00</current_datetime>\n\n要一个英文版的" {
		t.Errorf("the reminder must go and the timestamp stay (it identifies the request): %q", got)
	}
	if DecisionPrompt(got) != "要一个英文版的" {
		t.Errorf("the decision input is the request alone: %q", DecisionPrompt(got))
	}
	if got := ExtractUserPrompt([]any{userMessage("fix it <system_reminder>unclosed note")}); got != "fix it" {
		t.Errorf("an unclosed reminder runs to the end and is removed: %q", got)
	}
}
