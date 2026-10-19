package wire

// The OpenAI Responses API as a third client protocol (POST /v1/responses), next to chat
// completions and Anthropic Messages.
//
// Same rule as the Anthropic edge: the request is converted to the canonical chat-completions
// payload on the way in, routing and the upstream call never see the difference, and the canonical
// result is rendered back as a `response` object -- or, when streaming, as Responses SSE events.
//
// What does not cross, and why:
//
//   - `previous_response_id` and server-side conversation state. This router keeps no response
//     store, and the next turn of a conversation may be routed to a different model or a different
//     connection, so there is nothing to continue from. Refused up front with a 400 that says to
//     resend the full input (which is what a client running with `store: false` already does).
//   - Reasoning items. Their encrypted content is only meaningful to the model that produced it,
//     and the next turn may be routed elsewhere. They are dropped from the input.
//   - Hosted tools (web_search, file_search, code_interpreter, ...). They run inside OpenAI's own
//     service, which a routed upstream is not. Dropped, and listed in the trace.
//
// Custom tools (`type: "custom"`, free-form text input) do cross: they become a function with a
// single string parameter `input`, and a call to that function is turned back into a
// `custom_tool_call` (see custom.go, shared with the chat-completions door). Coding agents declare
// their patch tool this way, so dropping it would leave them unable to edit a file.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/satomic/model-router/server-go/internal/omap"
)

// ResponsesRequest is a converted request plus what the response side needs to know about it.
type ResponsesRequest struct {
	// Payload is the canonical chat-completions request.
	Payload *omap.Map
	// CustomTools names the tools that were declared as `custom`, so their calls go back out as
	// custom_tool_call items rather than function_call items.
	CustomTools map[string]bool
	// Dropped lists what was not forwarded (hosted tools, reasoning items), for the trace.
	Dropped []string
	// Echo is the request fields a `response` object repeats back (instructions, tools, ...).
	Echo *omap.Map
}

// responsesText flattens a Responses content value (a string or a list of parts) to text.
func responsesText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		var out strings.Builder
		for _, item := range value {
			part, ok := item.(*omap.Map)
			if !ok {
				continue
			}
			switch part.Str("type") {
			case "input_text", "output_text", "text", "summary_text":
				out.WriteString(part.Str("text"))
			case "refusal":
				out.WriteString(part.Str("refusal"))
			}
		}
		return out.String()
	}
	return ""
}

// chatContent converts Responses message content into chat content: a string stays a string, a
// list of parts becomes chat parts (text and image_url), or plain text when there is no image.
func chatContent(content any) any {
	parts, ok := content.([]any)
	if !ok {
		return responsesText(content)
	}
	out := []any{}
	hasImage := false
	for _, item := range parts {
		part, ok := item.(*omap.Map)
		if !ok {
			continue
		}
		switch part.Str("type") {
		case "input_text", "output_text", "text":
			out = append(out, mapOf("type", "text", "text", part.Str("text")))
		case "refusal":
			out = append(out, mapOf("type", "text", "text", part.Str("refusal")))
		case "input_image":
			url := part.Str("image_url")
			if url == "" {
				continue // a file_id refers to OpenAI's file store, which an upstream cannot read
			}
			image := mapOf("url", url)
			if detail := part.Str("detail"); detail != "" {
				image.Set("detail", detail)
			}
			out = append(out, mapOf("type", "image_url", "image_url", image))
			hasImage = true
		}
	}
	if !hasImage {
		return responsesText(content)
	}
	return out
}

// toolOutputText flattens a function_call_output's `output`, which may be a string or parts.
func toolOutputText(output any) string {
	if text, ok := output.(string); ok {
		return text
	}
	if text := responsesText(output); text != "" {
		return text
	}
	if output == nil {
		return ""
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// ResponsesRequestToOpenAI converts a Responses API request body into the canonical
// chat-completions payload.
func ResponsesRequestToOpenAI(body *omap.Map) (*ResponsesRequest, error) {
	if id := body.Str("previous_response_id"); id != "" {
		return nil, fmt.Errorf("previous_response_id is not supported by this router: it keeps no " +
			"response store, and the next turn may be routed to another model. Send the whole " +
			"conversation in `input` instead (store: false)")
	}
	if body.Value("conversation") != nil {
		return nil, fmt.Errorf("conversation is not supported by this router; send the whole " +
			"conversation in `input` instead")
	}

	req := &ResponsesRequest{CustomTools: map[string]bool{}, Echo: omap.New()}
	dropped := map[string]bool{}
	messages := []any{}

	if instructions := strings.TrimSpace(responsesText(body.Value("instructions"))); instructions != "" {
		messages = append(messages, mapOf("role", "system", "content", instructions))
	}

	// The assistant message the next function_call items attach to. A run of calls -- with or
	// without the text that preceded them -- is one assistant turn in the chat shape.
	var pending *omap.Map
	flush := func() {
		if pending != nil {
			messages = append(messages, pending)
			pending = nil
		}
	}
	addCall := func(id, name, arguments string) {
		if pending == nil {
			pending = mapOf("role", "assistant", "content", nil)
		}
		calls, _ := pending.Value("tool_calls").([]any)
		if arguments == "" {
			arguments = "{}"
		}
		pending.Set("tool_calls", append(calls, mapOf(
			"id", id, "type", "function",
			"function", mapOf("name", name, "arguments", arguments),
		)))
	}

	switch input := body.Value("input").(type) {
	case string:
		messages = append(messages, mapOf("role", "user", "content", input))
	case []any:
		for _, entry := range input {
			item, ok := entry.(*omap.Map)
			if !ok {
				continue
			}
			kind := item.Str("type")
			if kind == "" && item.Has("role") {
				kind = "message"
			}
			switch kind {
			case "message":
				role := item.Str("role")
				if role == "developer" {
					// Chat completions has a developer role on OpenAI itself, but neither Azure's
					// older deployments nor Anthropic know it; system means the same everywhere.
					role = "system"
				}
				if role == "assistant" {
					flush()
					pending = mapOf("role", "assistant", "content", responsesText(item.Value("content")))
					continue
				}
				flush()
				messages = append(messages, mapOf("role", role, "content", chatContent(item.Value("content"))))
			case "function_call":
				addCall(item.Str("call_id"), item.Str("name"), item.Str("arguments"))
			case "custom_tool_call":
				arguments, _ := json.Marshal(map[string]string{"input": item.Str("input")})
				addCall(item.Str("call_id"), item.Str("name"), string(arguments))
			case "function_call_output", "custom_tool_call_output":
				flush()
				messages = append(messages, mapOf(
					"role", "tool",
					"tool_call_id", item.Str("call_id"),
					"content", toolOutputText(item.Value("output")),
				))
			case "reasoning":
				dropped["reasoning items"] = true
			case "item_reference":
				return nil, fmt.Errorf("item_reference is not supported by this router: it keeps no " +
					"response store. Send the referenced item itself")
			default:
				dropped[kind+" items"] = true
			}
		}
		flush()
	case nil:
		return nil, fmt.Errorf("input is required")
	default:
		return nil, fmt.Errorf("input must be a string or a list of items")
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("input is required")
	}

	payload := mapOf("model", body.Value("model"), "messages", messages)

	if tools := body.Slice("tools"); len(tools) > 0 {
		converted := []any{}
		for _, entry := range tools {
			tool, ok := entry.(*omap.Map)
			if !ok {
				continue
			}
			switch tool.Str("type") {
			case "function":
				fn := mapOf("name", tool.Value("name"))
				if tool.Has("description") {
					fn.Set("description", tool.Value("description"))
				}
				parameters := tool.Value("parameters")
				if parameters == nil {
					parameters = mapOf("type", "object", "properties", omap.New())
				}
				fn.Set("parameters", parameters)
				if tool.Has("strict") {
					fn.Set("strict", tool.Value("strict"))
				}
				converted = append(converted, mapOf("type", "function", "function", fn))
			case "custom":
				name := tool.Str("name")
				req.CustomTools[name] = true
				converted = append(converted, customToolFunction(name, tool.Str("description"), tool.Map("format")))
			default:
				dropped[tool.Str("type")+" tool"] = true
			}
		}
		if len(converted) > 0 {
			payload.Set("tools", converted)
		}
	}

	if choice := body.Value("tool_choice"); choice != nil && payload.Has("tools") {
		switch value := choice.(type) {
		case string:
			payload.Set("tool_choice", value)
		case *omap.Map:
			if name := value.Str("name"); name != "" && (value.Str("type") == "function" || value.Str("type") == "custom") {
				payload.Set("tool_choice", mapOf("type", "function", "function", mapOf("name", name)))
			}
		}
	}

	if maxOut := body.Value("max_output_tokens"); maxOut != nil {
		payload.Set("max_tokens", maxOut)
	}
	for _, field := range []string{"temperature", "top_p", "user", "stream"} {
		if body.Has(field) {
			payload.Set(field, body.Value(field))
		}
	}
	if text := body.Map("text"); text != nil {
		if format := text.Map("format"); format != nil {
			switch format.Str("type") {
			case "json_schema":
				schema := mapOf("name", format.Value("name"), "schema", format.Value("schema"))
				if format.Has("strict") {
					schema.Set("strict", format.Value("strict"))
				}
				if format.Has("description") {
					schema.Set("description", format.Value("description"))
				}
				payload.Set("response_format", mapOf("type", "json_schema", "json_schema", schema))
			case "json_object":
				payload.Set("response_format", mapOf("type", "json_object"))
			}
		}
	}

	for name := range dropped {
		req.Dropped = append(req.Dropped, name)
	}
	for _, field := range []string{"instructions", "tools", "tool_choice", "parallel_tool_calls",
		"text", "reasoning", "temperature", "top_p", "max_output_tokens", "metadata", "store", "truncation", "user"} {
		if body.Has(field) {
			req.Echo.Set(field, body.Value(field))
		}
	}
	req.Payload = payload
	return req, nil
}

// ---------------------------------------------------------------------------
// chat.completion -> response
// ---------------------------------------------------------------------------

// ResponsesUsage converts chat usage to the Responses shape. nil stays nil.
func ResponsesUsage(usage *omap.Map) any {
	if usage == nil || usage.Len() == 0 {
		return nil
	}
	input := int(usage.Float("prompt_tokens", usage.Float("input_tokens", 0)))
	output := int(usage.Float("completion_tokens", usage.Float("output_tokens", 0)))
	cached, reasoning := 0, 0
	if details := usage.Map("prompt_tokens_details"); details != nil {
		cached = int(details.Float("cached_tokens", 0))
	}
	if details := usage.Map("completion_tokens_details"); details != nil {
		reasoning = int(details.Float("reasoning_tokens", 0))
	}
	return mapOf(
		"input_tokens", intNumber(input),
		"input_tokens_details", mapOf("cached_tokens", intNumber(cached)),
		"output_tokens", intNumber(output),
		"output_tokens_details", mapOf("reasoning_tokens", intNumber(reasoning)),
		"total_tokens", intNumber(input+output),
	)
}

// callItem renders one tool call as a function_call or custom_tool_call output item.
func callItem(id, callID, name, arguments, status string, custom map[string]bool) *omap.Map {
	if custom[name] {
		return mapOf("id", id, "type", "custom_tool_call", "status", status,
			"call_id", callID, "name", name, "input", customInput(arguments))
	}
	return mapOf("id", id, "type", "function_call", "status", status,
		"call_id", callID, "name", name, "arguments", arguments)
}

func messageItem(id, text, status string) *omap.Map {
	return mapOf("id", id, "type", "message", "status", status, "role", "assistant",
		"content", []any{mapOf("type", "output_text", "text", text, "annotations", []any{})})
}

// responseObject builds the envelope every Responses answer shares.
func responseObject(id, model, status string, createdAt int64, output []any, usage any,
	req *ResponsesRequest) *omap.Map {
	var incomplete any
	if status == "incomplete" {
		incomplete = mapOf("reason", "max_output_tokens")
	}
	out := mapOf(
		"id", id,
		"object", "response",
		"created_at", json.Number(fmt.Sprintf("%d", createdAt)),
		"status", status,
		"error", nil,
		"incomplete_details", incomplete,
		"model", model,
		"output", output,
		"parallel_tool_calls", true,
		"tool_choice", "auto",
		"tools", []any{},
		"usage", usage,
	)
	if req != nil {
		for _, field := range req.Echo.Keys() {
			out.Set(field, req.Echo.Value(field))
		}
	}
	return out
}

func statusFor(finish string) string {
	if finish == "length" {
		return "incomplete"
	}
	return "completed"
}

// OpenAIResponseToResponses converts a chat.completion into a Responses `response` object.
func OpenAIResponseToResponses(data *omap.Map, model string, req *ResponsesRequest) *omap.Map {
	choice := firstChoice(data)
	message := choice.Map("message")
	if message == nil {
		message = omap.New()
	}
	custom := map[string]bool{}
	if req != nil {
		custom = req.CustomTools
	}
	output := []any{}
	if text := TextOf(message.Value("content")); text != "" {
		output = append(output, messageItem(NewID("msg_"), text, "completed"))
	}
	for _, entry := range message.Slice("tool_calls") {
		call, ok := entry.(*omap.Map)
		if !ok {
			continue
		}
		fn := call.Map("function")
		if fn == nil {
			fn = omap.New()
		}
		callID := call.Str("id")
		if callID == "" {
			callID = NewID("call_")
		}
		output = append(output, callItem(NewID("fc_"), callID, fn.Str("name"), fn.Str("arguments"), "completed", custom))
	}
	finish := choice.Str("finish_reason")
	return responseObject(NewID("resp_"), model, statusFor(finish), time.Now().Unix(), output,
		ResponsesUsage(data.Map("usage")), req)
}

// ---------------------------------------------------------------------------
// chat.completion.chunk stream -> Responses SSE events
// ---------------------------------------------------------------------------

// ResponsesEncoder emits Responses streaming events from canonical chat chunks.
//
// Stateful for the same reason as the Anthropic encoder: every output item has to be announced
// (`output_item.added`) before its deltas and closed (`output_item.done`) after them, and the chat
// stream marks none of those boundaries. The text message, when there is one, is output item 0;
// each tool call gets the next index in the order it first appears.
type ResponsesEncoder struct {
	id        string
	model     string
	createdAt int64
	req       *ResponsesRequest
	seq       int

	started bool
	text    strings.Builder
	textIdx int // output index of the message item; -1 while none is open
	textID  string
	nextIdx int
	finish  string

	calls []*streamCall       // in output order
	byIdx map[int]*streamCall // chat tool_call index -> call
	order map[int]*omap.Map   // output index -> finished item
}

type streamCall struct {
	outIdx    int
	itemID    string
	callID    string
	name      string
	arguments strings.Builder
}

func NewResponsesEncoder(model string, req *ResponsesRequest) *ResponsesEncoder {
	return &ResponsesEncoder{
		id: NewID("resp_"), model: model, createdAt: time.Now().Unix(), req: req,
		textIdx: -1, byIdx: map[int]*streamCall{}, order: map[int]*omap.Map{},
	}
}

func (e *ResponsesEncoder) emit(name string, data *omap.Map) string {
	data.Set("sequence_number", intNumber(e.seq))
	e.seq++
	// Field order: type first, as OpenAI sends it. Purely cosmetic, but it makes a captured
	// stream diff cleanly against the real service.
	ordered := mapOf("type", name)
	for _, key := range data.Keys() {
		ordered.Set(key, data.Value(key))
	}
	return event(name, ordered)
}

func (e *ResponsesEncoder) envelope(status string, output []any, usage any) *omap.Map {
	return responseObject(e.id, e.model, status, e.createdAt, output, usage, e.req)
}

func (e *ResponsesEncoder) start() []string {
	e.started = true
	return []string{
		e.emit("response.created", mapOf("response", e.envelope("in_progress", []any{}, nil))),
		e.emit("response.in_progress", mapOf("response", e.envelope("in_progress", []any{}, nil))),
	}
}

func (e *ResponsesEncoder) custom() map[string]bool {
	if e.req == nil {
		return map[string]bool{}
	}
	return e.req.CustomTools
}

// closeText ends the open message item, if any.
func (e *ResponsesEncoder) closeText() []string {
	if e.textIdx < 0 {
		return nil
	}
	text := e.text.String()
	part := mapOf("type", "output_text", "text", text, "annotations", []any{})
	item := messageItem(e.textID, text, "completed")
	e.order[e.textIdx] = item
	idx := e.textIdx
	e.textIdx = -1
	return []string{
		e.emit("response.output_text.done", mapOf("item_id", e.textID, "output_index", intNumber(idx),
			"content_index", intNumber(0), "text", text)),
		e.emit("response.content_part.done", mapOf("item_id", e.textID, "output_index", intNumber(idx),
			"content_index", intNumber(0), "part", part)),
		e.emit("response.output_item.done", mapOf("output_index", intNumber(idx), "item", item)),
	}
}

// Feed takes one canonical chunk and returns zero or more SSE frames.
func (e *ResponsesEncoder) Feed(chunk *omap.Map) []string {
	frames := []string{}
	if !e.started {
		frames = append(frames, e.start()...)
	}
	choice := firstChoice(chunk)
	delta := choice.Map("delta")
	if delta == nil {
		delta = omap.New()
	}
	if content := delta.Str("content"); content != "" {
		if e.textIdx < 0 && e.text.Len() == 0 {
			e.textIdx = e.nextIdx
			e.nextIdx++
			e.textID = NewID("msg_")
			frames = append(frames,
				e.emit("response.output_item.added", mapOf("output_index", intNumber(e.textIdx),
					"item", mapOf("id", e.textID, "type", "message", "status", "in_progress",
						"role", "assistant", "content", []any{}))),
				e.emit("response.content_part.added", mapOf("item_id", e.textID,
					"output_index", intNumber(e.textIdx), "content_index", intNumber(0),
					"part", mapOf("type", "output_text", "text", "", "annotations", []any{}))),
			)
		}
		if e.textIdx >= 0 {
			e.text.WriteString(content)
			frames = append(frames, e.emit("response.output_text.delta", mapOf("item_id", e.textID,
				"output_index", intNumber(e.textIdx), "content_index", intNumber(0), "delta", content)))
		}
	}
	for _, entry := range delta.Slice("tool_calls") {
		tc, ok := entry.(*omap.Map)
		if !ok {
			continue
		}
		index := tc.Int("index", 0)
		fn := tc.Map("function")
		if fn == nil {
			fn = omap.New()
		}
		call := e.byIdx[index]
		if call == nil {
			// Text that came before a tool call is finished once the call starts: chat models do
			// not interleave the two, and an open message item would stall a client that waits for
			// it to close before running the tool.
			frames = append(frames, e.closeText()...)
			call = &streamCall{outIdx: e.nextIdx, itemID: NewID("fc_"), callID: tc.Str("id"), name: fn.Str("name")}
			if call.callID == "" {
				call.callID = NewID("call_")
			}
			e.nextIdx++
			e.byIdx[index] = call
			e.calls = append(e.calls, call)
			item := callItem(call.itemID, call.callID, call.name, "", "in_progress", e.custom())
			frames = append(frames, e.emit("response.output_item.added",
				mapOf("output_index", intNumber(call.outIdx), "item", item)))
		}
		if args := fn.Str("arguments"); args != "" {
			call.arguments.WriteString(args)
			// A custom tool's input is only known once the JSON wrapper is complete, so its
			// deltas are held back and sent whole at the end.
			if !e.custom()[call.name] {
				frames = append(frames, e.emit("response.function_call_arguments.delta", mapOf(
					"item_id", call.itemID, "output_index", intNumber(call.outIdx), "delta", args)))
			}
		}
	}
	if finish := choice.Str("finish_reason"); finish != "" {
		e.finish = finish
	}
	return frames
}

// Finish closes every open item and ends the response. Safe to call after zero chunks.
func (e *ResponsesEncoder) Finish(usage *omap.Map) []string {
	frames := []string{}
	if !e.started {
		frames = append(frames, e.start()...)
	}
	frames = append(frames, e.closeText()...)
	for _, call := range e.calls {
		arguments := call.arguments.String()
		if arguments == "" {
			arguments = "{}"
		}
		item := callItem(call.itemID, call.callID, call.name, arguments, "completed", e.custom())
		if item.Str("type") == "custom_tool_call" {
			frames = append(frames,
				e.emit("response.custom_tool_call_input.delta", mapOf("item_id", call.itemID,
					"output_index", intNumber(call.outIdx), "delta", item.Str("input"))),
				e.emit("response.custom_tool_call_input.done", mapOf("item_id", call.itemID,
					"output_index", intNumber(call.outIdx), "input", item.Str("input"))))
		} else {
			frames = append(frames, e.emit("response.function_call_arguments.done", mapOf(
				"item_id", call.itemID, "output_index", intNumber(call.outIdx), "arguments", arguments)))
		}
		frames = append(frames, e.emit("response.output_item.done",
			mapOf("output_index", intNumber(call.outIdx), "item", item)))
		e.order[call.outIdx] = item
	}
	output := make([]any, 0, len(e.order))
	for i := 0; i < e.nextIdx; i++ {
		if item := e.order[i]; item != nil {
			output = append(output, item)
		}
	}
	status := statusFor(e.finish)
	name := "response.completed"
	if status == "incomplete" {
		name = "response.incomplete"
	}
	frames = append(frames, e.emit(name, mapOf("response", e.envelope(status, output, ResponsesUsage(usage)))))
	return frames
}

// Error is a `response.failed` event, for a failure after the stream has opened.
func (e *ResponsesEncoder) Error(message string) []string {
	frames := []string{}
	if !e.started {
		frames = append(frames, e.start()...)
	}
	failed := e.envelope("failed", []any{}, nil)
	failed.Set("error", mapOf("code", "server_error", "message", message))
	frames = append(frames, e.emit("response.failed", mapOf("response", failed)))
	return frames
}
