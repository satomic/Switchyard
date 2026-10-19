package wire

// Custom (free-form) tools on chat completions.
//
// A GPT-5 client may declare a tool as `{"type": "custom", "custom": {"name", "description",
// "format"}}` -- Copilot CLI does for apply_patch -- and expect calls back as
// `{"type": "custom", "custom": {"name", "input"}}`. Most models a router can pick do not know the
// type, and Azure refuses the whole request (400 "Invalid value: 'custom'"). So the tool is
// declared to the upstream as a function with one string argument `input`, and calls to it are
// turned back into custom calls on the way out. The Responses door does the same for its own
// custom tools (responses.go).

import (
	"encoding/json"
	"strings"

	"github.com/satomic/model-router/server-go/internal/omap"
)

// customToolFunction is the function a custom tool is declared as.
func customToolFunction(name, description string, format *omap.Map) *omap.Map {
	if format != nil && format.Str("type") == "grammar" {
		description = strings.TrimSpace(description + "\n\nThe input must follow this " +
			format.Str("syntax") + " grammar:\n" + format.Str("definition"))
	}
	description = strings.TrimSpace(description + "\n\nPass the raw tool input, exactly as " +
		"described above, as the string argument `input`.")
	return mapOf("type", "function", "function", mapOf(
		"name", name,
		"description", description,
		"parameters", mapOf(
			"type", "object",
			"properties", mapOf("input", mapOf("type", "string", "description", "The raw input to the tool.")),
			"required", []any{"input"},
		),
	))
}

// customInput unwraps {"input": "..."}; anything else is passed through as the input itself.
func customInput(arguments string) string {
	var parsed struct {
		Input *string `json:"input"`
	}
	if json.Unmarshal([]byte(arguments), &parsed) == nil && parsed.Input != nil {
		return *parsed.Input
	}
	return arguments
}

// BridgeChatCustomTools rewrites custom tools (and custom calls already in the history) of a chat
// request into functions, in place, and returns the names that were custom. nil when none were.
func BridgeChatCustomTools(payload *omap.Map) map[string]bool {
	names := map[string]bool{}
	tools := payload.Slice("tools")
	for i, entry := range tools {
		tool, ok := entry.(*omap.Map)
		if !ok || tool.Str("type") != "custom" {
			continue
		}
		spec := tool.Map("custom")
		if spec == nil {
			continue
		}
		names[spec.Str("name")] = true
		tools[i] = customToolFunction(spec.Str("name"), spec.Str("description"), spec.Map("format"))
	}
	if len(names) == 0 {
		return nil
	}
	payload.Set("tools", tools)
	if choice := payload.Map("tool_choice"); choice != nil && choice.Str("type") == "custom" {
		if spec := choice.Map("custom"); spec != nil {
			payload.Set("tool_choice", mapOf("type", "function", "function", mapOf("name", spec.Str("name"))))
		}
	}
	for _, entry := range payload.Slice("messages") {
		msg, ok := entry.(*omap.Map)
		if !ok {
			continue
		}
		for _, item := range msg.Slice("tool_calls") {
			call, ok := item.(*omap.Map)
			if !ok || call.Str("type") != "custom" {
				continue
			}
			spec := call.Map("custom")
			if spec == nil {
				continue
			}
			arguments, _ := json.Marshal(map[string]string{"input": spec.Str("input")})
			call.Set("type", "function")
			call.Delete("custom")
			call.Set("function", mapOf("name", spec.Str("name"), "arguments", string(arguments)))
		}
	}
	return names
}

// customCall turns one function call to a custom tool back into a custom call.
func customCall(call *omap.Map, names map[string]bool) {
	fn := call.Map("function")
	if fn == nil || !names[fn.Str("name")] {
		return
	}
	call.Set("type", "custom")
	call.Delete("function")
	call.Set("custom", mapOf("name", fn.Str("name"), "input", customInput(fn.Str("arguments"))))
}

// RestoreChatCustomCalls turns calls to bridged tools in a chat.completion back into custom calls.
func RestoreChatCustomCalls(completion *omap.Map, names map[string]bool) {
	if len(names) == 0 {
		return
	}
	for _, entry := range completion.Slice("choices") {
		choice, ok := entry.(*omap.Map)
		if !ok {
			continue
		}
		message := choice.Map("message")
		if message == nil {
			continue
		}
		for _, item := range message.Slice("tool_calls") {
			if call, ok := item.(*omap.Map); ok {
				customCall(call, names)
			}
		}
	}
}

// CustomCallRestorer does the same for a stream. A custom call's input is only known once the
// JSON wrapper is complete, so its argument deltas are held back and the call is sent whole, as a
// single delta, just before the chunk that finishes the choice.
type CustomCallRestorer struct {
	names   map[string]bool
	held    map[int]*omap.Map // stream tool-call index -> accumulated call
	order   []int
	nameFor map[int]string
}

func NewCustomCallRestorer(names map[string]bool) *CustomCallRestorer {
	return &CustomCallRestorer{names: names, held: map[int]*omap.Map{}, nameFor: map[int]string{}}
}

// Feed rewrites one chunk and returns the chunks to send in its place (zero, one or two).
func (r *CustomCallRestorer) Feed(chunk *omap.Map) []*omap.Map {
	if len(r.names) == 0 {
		return []*omap.Map{chunk}
	}
	choice := firstChoice(chunk)
	delta := choice.Map("delta")
	if delta != nil {
		kept := []any{}
		for _, item := range delta.Slice("tool_calls") {
			call, ok := item.(*omap.Map)
			if !ok {
				continue
			}
			index := call.Int("index", 0)
			if fn := call.Map("function"); fn != nil && fn.Str("name") != "" {
				r.nameFor[index] = fn.Str("name")
			}
			if !r.names[r.nameFor[index]] {
				kept = append(kept, call)
				continue
			}
			slot := r.held[index]
			if slot == nil {
				slot = mapOf("index", call.Value("index"), "id", call.Value("id"), "arguments", "")
				r.held[index] = slot
				r.order = append(r.order, index)
			}
			if id := call.Str("id"); id != "" {
				slot.Set("id", id)
			}
			if fn := call.Map("function"); fn != nil {
				slot.Set("arguments", slot.Str("arguments")+fn.Str("arguments"))
			}
		}
		if delta.Has("tool_calls") {
			if len(kept) > 0 {
				delta.Set("tool_calls", kept)
			} else {
				delta.Delete("tool_calls")
			}
		}
	}
	finish := choice.Str("finish_reason")
	if finish == "" || len(r.order) == 0 {
		return []*omap.Map{chunk}
	}
	calls := []any{}
	for _, index := range r.order {
		slot := r.held[index]
		calls = append(calls, mapOf(
			"index", slot.Value("index"), "id", slot.Value("id"), "type", "custom",
			"custom", mapOf("name", r.nameFor[index], "input", customInput(slot.Str("arguments"))),
		))
	}
	r.order = nil
	// Same envelope as the chunk it precedes, minus usage, which belongs to the end of the stream.
	whole := chunk.Clone()
	whole.Delete("usage")
	whole.Set("choices", []any{mapOf("index", choice.Value("index"), "delta", mapOf("tool_calls", calls), "finish_reason", nil)})
	return []*omap.Map{whole, chunk}
}
