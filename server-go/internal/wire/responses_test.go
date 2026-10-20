package wire

import (
	"strings"
	"testing"

	"github.com/satomic/model-router/server-go/internal/omap"
)

// A whole agent turn as a Responses client sends it: instructions, a developer note, a user
// message, a reasoning item, an assistant message followed by two calls, and their outputs.
func TestResponsesRequestToOpenAIAgentTurn(t *testing.T) {
	body := parse(t, `{
		"model": "auto",
		"instructions": "You are a coding agent.",
		"input": [
			{"role": "developer", "content": "Prefer small diffs."},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "fix the bug"}]},
			{"type": "reasoning", "id": "rs_1", "encrypted_content": "xxx", "summary": []},
			{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "Looking."}]},
			{"type": "function_call", "call_id": "call_a", "name": "read_file", "arguments": "{\"path\":\"a.go\"}"},
			{"type": "custom_tool_call", "call_id": "call_b", "name": "apply_patch", "input": "*** Begin Patch"},
			{"type": "function_call_output", "call_id": "call_a", "output": "package a"},
			{"type": "custom_tool_call_output", "call_id": "call_b", "output": [{"type": "input_text", "text": "Done"}]}
		],
		"tools": [
			{"type": "function", "name": "read_file", "parameters": {"type": "object"}},
			{"type": "custom", "name": "apply_patch", "description": "Edit files"},
			{"type": "web_search"}
		],
		"tool_choice": "auto",
		"max_output_tokens": 512,
		"stream": true,
		"store": false
	}`)
	req, err := ResponsesRequestToOpenAI(body)
	if err != nil {
		t.Fatal(err)
	}
	messages := req.Payload.Slice("messages")
	roles := []string{}
	for _, m := range messages {
		roles = append(roles, m.(*omap.Map).Str("role"))
	}
	if got := strings.Join(roles, ","); got != "system,system,user,assistant,tool,tool" {
		t.Fatalf("roles = %s", got)
	}
	assistant := messages[3].(*omap.Map)
	if assistant.Str("content") != "Looking." || len(assistant.Slice("tool_calls")) != 2 {
		t.Errorf("text and the two calls must form one assistant turn: %s", encode(t, assistant))
	}
	custom := assistant.Slice("tool_calls")[1].(*omap.Map).Map("function")
	if custom.Str("arguments") != `{"input":"*** Begin Patch"}` {
		t.Errorf("a custom call's input must be wrapped as the function's input argument: %s", custom.Str("arguments"))
	}
	if messages[5].(*omap.Map).Str("content") != "Done" {
		t.Errorf("tool output parts must flatten to text: %s", encode(t, messages[5]))
	}
	tools := req.Payload.Slice("tools")
	if len(tools) != 2 {
		t.Fatalf("the hosted web_search tool must be dropped, got %s", encode(t, tools))
	}
	if !req.CustomTools["apply_patch"] {
		t.Error("apply_patch must be remembered as a custom tool")
	}
	if req.Payload.Value("max_tokens") == nil || !req.Payload.Bool("stream", false) {
		t.Errorf("max_output_tokens and stream must carry over: %s", encode(t, req.Payload))
	}
	if strings.Join(req.Dropped, ",") == "" {
		t.Error("the dropped reasoning item and hosted tool must be reported")
	}
}

func TestResponsesRequestNeedsCompleteInput(t *testing.T) {
	if _, err := ResponsesRequestToOpenAI(parse(t, `{"input": [{"type": "item_reference", "id": "msg_x"}]}`)); err == nil {
		t.Error("an item reference the server could not resolve must be refused")
	}
	req, err := ResponsesRequestToOpenAI(parse(t, `{"input": "hello"}`))
	if err != nil || req.Payload.Slice("messages")[0].(*omap.Map).Str("content") != "hello" {
		t.Errorf("a string input is one user message: %v", err)
	}
}

func TestOpenAIResponseToResponses(t *testing.T) {
	req, _ := ResponsesRequestToOpenAI(parse(t, `{"input": "x", "tools": [{"type": "custom", "name": "apply_patch"}]}`))
	completion := parse(t, `{
		"choices": [{"index": 0, "finish_reason": "tool_calls", "message": {"role": "assistant",
			"content": "Patching.",
			"tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "apply_patch", "arguments": "{\"input\":\"diff\"}"}}]}}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 4, "total_tokens": 14}
	}`)
	out := OpenAIResponseToResponses(completion, "gpt-x", req)
	if out.Str("object") != "response" || out.Str("status") != "completed" {
		t.Fatalf("envelope: %s", encode(t, out))
	}
	output := out.Slice("output")
	if len(output) != 2 || output[0].(*omap.Map).Str("type") != "message" {
		t.Fatalf("output: %s", encode(t, output))
	}
	call := output[1].(*omap.Map)
	if call.Str("type") != "custom_tool_call" || call.Str("input") != "diff" || call.Str("call_id") != "call_1" {
		t.Errorf("a call to a custom tool must go back as custom_tool_call: %s", encode(t, call))
	}
	if usage := out.Map("usage"); usage.Int("input_tokens", 0) != 10 || usage.Int("output_tokens", 0) != 4 {
		t.Errorf("usage: %s", encode(t, usage))
	}
}

// The event sequence a Responses client depends on: created, the text item opened, deltas, the
// text item closed before a tool call starts, the call's argument deltas, completed with usage.
func TestResponsesEncoderStream(t *testing.T) {
	enc := NewResponsesEncoder("gpt-x", nil)
	chunks := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"lo"}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_9","type":"function","function":{"name":"run","arguments":"{\"a\""}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":":1}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`,
	}
	var frames []string
	var usage *omap.Map
	for _, text := range chunks {
		chunk := parse(t, text)
		if u := chunk.Map("usage"); u != nil {
			usage = u
		}
		frames = append(frames, enc.Feed(chunk)...)
	}
	frames = append(frames, enc.Finish(usage)...)

	names := []string{}
	for _, frame := range frames {
		names = append(names, strings.TrimPrefix(strings.SplitN(frame, "\n", 2)[0], "event: "))
	}
	want := []string{
		"response.created", "response.in_progress",
		"response.output_item.added", "response.content_part.added",
		"response.output_text.delta", "response.output_text.delta",
		"response.output_text.done", "response.content_part.done", "response.output_item.done",
		"response.output_item.added", "response.function_call_arguments.delta", "response.function_call_arguments.delta",
		"response.function_call_arguments.done", "response.output_item.done",
		"response.completed",
	}
	if strings.Join(names, " ") != strings.Join(want, " ") {
		t.Fatalf("event order\n got  %v\n want %v", names, want)
	}
	last := frames[len(frames)-1]
	for _, needle := range []string{`"text":"Hello"`, `"arguments":"{\"a\":1}"`, `"call_id":"call_9"`, `"input_tokens":3`, `"status":"completed"`} {
		if !strings.Contains(last, needle) {
			t.Errorf("response.completed is missing %s:\n%s", needle, last)
		}
	}
	if !strings.Contains(frames[0], `"sequence_number":0`) || !strings.Contains(last, `"sequence_number":14`) {
		t.Error("sequence numbers must run from 0 without gaps")
	}
}
