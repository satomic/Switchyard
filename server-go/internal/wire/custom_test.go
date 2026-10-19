package wire

import (
	"strings"
	"testing"

	"github.com/satomic/model-router/server-go/internal/omap"
)

// The request shape Copilot CLI sends in GPT-5 mode on the completions wire.
func TestBridgeChatCustomTools(t *testing.T) {
	payload := parse(t, `{
		"messages": [
			{"role": "user", "content": "fix it"},
			{"role": "assistant", "content": null, "tool_calls": [{"id": "c1", "type": "custom", "custom": {"name": "apply_patch", "input": "*** Begin Patch"}}]},
			{"role": "tool", "tool_call_id": "c1", "content": "ok"}
		],
		"tools": [
			{"type": "function", "function": {"name": "view", "parameters": {"type": "object"}}},
			{"type": "custom", "custom": {"name": "apply_patch", "description": "Edit files", "format": {"type": "grammar", "syntax": "lark", "definition": "start: x"}}}
		]
	}`)
	names := BridgeChatCustomTools(payload)
	if !names["apply_patch"] {
		t.Fatal("apply_patch must be reported as custom")
	}
	tool := payload.Slice("tools")[1].(*omap.Map)
	if tool.Str("type") != "function" || !strings.Contains(tool.Map("function").Str("description"), "start: x") {
		t.Errorf("the custom tool must become a function that keeps its grammar: %s", encode(t, tool))
	}
	call := payload.Slice("messages")[1].(*omap.Map).Slice("tool_calls")[0].(*omap.Map)
	if call.Str("type") != "function" || call.Map("function").Str("arguments") != `{"input":"*** Begin Patch"}` {
		t.Errorf("a custom call in the history must become a function call: %s", encode(t, call))
	}
	if BridgeChatCustomTools(parse(t, `{"messages": [], "tools": [{"type": "function", "function": {"name": "x"}}]}`)) != nil {
		t.Error("a request without custom tools must be left alone")
	}

	completion := parse(t, `{"choices": [{"index": 0, "finish_reason": "tool_calls", "message": {"role": "assistant",
		"tool_calls": [{"id": "c2", "type": "function", "function": {"name": "apply_patch", "arguments": "{\"input\":\"diff\"}"}},
		               {"id": "c3", "type": "function", "function": {"name": "view", "arguments": "{}"}}]}}]}`)
	RestoreChatCustomCalls(completion, names)
	calls := completion.Slice("choices")[0].(*omap.Map).Map("message").Slice("tool_calls")
	if got := encode(t, calls[0]); !strings.Contains(got, `"type":"custom"`) || !strings.Contains(got, `"input":"diff"`) {
		t.Errorf("the custom call must be restored: %s", got)
	}
	if calls[1].(*omap.Map).Str("type") != "function" {
		t.Error("an ordinary function call must stay a function call")
	}
}

func TestCustomCallRestorerStream(t *testing.T) {
	r := NewCustomCallRestorer(map[string]bool{"apply_patch": true})
	var out []*omap.Map
	for _, text := range []string{
		`{"id":"x","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c9","type":"function","function":{"name":"apply_patch","arguments":"{\"inp"}}]}}]}`,
		`{"id":"x","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ut\":\"diff\"}"}}]}}]}`,
		`{"id":"x","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	} {
		out = append(out, r.Feed(parse(t, text))...)
	}
	all := encode(t, out)
	if strings.Count(all, `"tool_calls":[`) != 1 || !strings.Contains(all, `"custom":{"name":"apply_patch","input":"diff"}`) {
		t.Errorf("the custom call must be held back and sent whole, once: %s", all)
	}
	if last := out[len(out)-1]; firstChoice(last).Str("finish_reason") != "tool_calls" {
		t.Error("the finishing chunk must still come last")
	}
}
