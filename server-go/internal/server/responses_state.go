package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/satomic/model-router/server-go/internal/omap"
	"github.com/satomic/model-router/server-go/internal/respstore"
	"github.com/satomic/model-router/server-go/internal/wire"
)

// The Responses API's server-side state, on top of internal/respstore:
//
//	POST   /v1/responses                         previous_response_id, conversation, item_reference
//	GET    /v1/responses/{id}                    a stored response
//	DELETE /v1/responses/{id}
//	GET    /v1/responses/{id}/input_items
//	POST   /v1/conversations                     the Conversations API
//	GET    /v1/conversations/{id}
//	POST   /v1/conversations/{id}                update metadata
//	DELETE /v1/conversations/{id}
//	GET    /v1/conversations/{id}/items
//	POST   /v1/conversations/{id}/items
//	GET    /v1/conversations/{id}/items/{item_id}
//	DELETE /v1/conversations/{id}/items/{item_id}
//
// All of it is per user: an id created with one person's key is "not found" with anybody else's.
// Error bodies keep this router's {"detail": ...} shape, like every other endpoint.

// responsesState is what a Responses request leaves to be stored once its answer is in.
type responsesState struct {
	user           string
	inputItems     []any  // this request's own input, with ids
	history        []any  // the items before it (from previous_response_id or the conversation)
	conversationID string // "" when the request named no conversation
}

// itemPrefixes gives an item without an id one of the right kind.
var itemPrefixes = map[string]string{
	"message": "msg", "function_call": "fc", "function_call_output": "fco",
	"custom_tool_call": "ctc", "custom_tool_call_output": "ctco", "reasoning": "rs",
}

// normalizeItems turns a request's `input` (or a conversation's `items`) into a list of items,
// each with a type and an id, with item references replaced by the items they name.
func normalizeItems(value any, user string) ([]any, error) {
	store := respstore.Active()
	var list []any
	switch v := value.(type) {
	case nil:
		return []any{}, nil
	case string:
		list = []any{mapOf("type", "message", "role", "user",
			"content", []any{mapOf("type", "input_text", "text", v)})}
	case []any:
		list = v
	default:
		return nil, errorf(http.StatusBadRequest, "input must be a string or a list of items")
	}
	out := []any{}
	for _, entry := range list {
		item, ok := entry.(*omap.Map)
		if !ok {
			continue
		}
		item = item.Clone()
		kind := item.Str("type")
		if kind == "" && item.Has("role") {
			kind = "message"
			item.Set("type", kind)
		}
		if kind == "item_reference" {
			var found *omap.Map
			if store != nil {
				found, _ = store.Item(item.Str("id"), user)
			}
			if found == nil {
				return nil, errorf(http.StatusBadRequest, "Item with id '%s' not found.", item.Str("id"))
			}
			out = append(out, found)
			continue
		}
		if !respstore.ValidID(item.Str("id")) {
			prefix := itemPrefixes[kind]
			if prefix == "" {
				prefix = "item"
			}
			item.Set("id", respstore.NewID(prefix))
		}
		out = append(out, item)
	}
	return out, nil
}

func conversationIDOf(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case *omap.Map:
		return v.Str("id")
	}
	return ""
}

// notFound maps a store lookup failure onto the 404 the endpoints return.
func notFound(kind, id string) error {
	return errorf(http.StatusNotFound, "%s with id '%s' not found.", kind, id)
}

// responsesCreate is the OpenAI Responses API entry point (POST /v1/responses), so a client that
// only speaks Responses -- VS Code Copilot Chat or Copilot CLI on the Responses wire, Codex, the
// OpenAI SDKs' responses.create -- works unchanged. Converted to the canonical chat payload like the
// Anthropic door (internal/wire/responses.go), after the server-side state has been resolved into a
// complete list of items.
func (a *App) responsesCreate(w http.ResponseWriter, r *http.Request) error {
	key, err := a.apiKey(r)
	if err != nil {
		return err
	}
	start := time.Now()
	user := key.Str("user_login")
	raw, err := readJSONObject(r)
	if err != nil {
		return err
	}
	if raw.Bool("background", false) {
		return errorf(http.StatusBadRequest, "background responses are not supported by this router; "+
			"send the request without background and read the answer from the response")
	}
	previousID := raw.Str("previous_response_id")
	conversationID := conversationIDOf(raw.Value("conversation"))
	if previousID != "" && conversationID != "" {
		return errorf(http.StatusBadRequest, "previous_response_id and conversation cannot be used together")
	}

	inputItems, err := normalizeItems(raw.Value("input"), user)
	if err != nil {
		return err
	}
	store := respstore.Active()
	history := []any{}
	switch {
	case previousID != "":
		var previous *respstore.Response
		if store != nil {
			previous, _ = store.GetResponse(previousID, user)
		}
		if previous == nil {
			// OpenAI's wording, which clients already recognise.
			return errorf(http.StatusBadRequest, "Previous response with id '%s' not found.", previousID)
		}
		history = previous.Context
	case conversationID != "":
		var conversation *respstore.Conversation
		if store != nil {
			conversation, _ = store.GetConversation(conversationID, user)
		}
		if conversation == nil {
			return notFound("Conversation", conversationID)
		}
		history = conversation.Items
	}

	effective := raw.Clone()
	effective.Delete("previous_response_id")
	effective.Delete("conversation")
	effective.Set("input", append(append([]any{}, history...), inputItems...))
	converted, err := wire.ResponsesRequestToOpenAI(effective)
	if err != nil {
		return errorf(http.StatusBadRequest, "%s", err.Error())
	}
	if previousID != "" {
		converted.Echo.Set("previous_response_id", previousID)
	}
	if conversationID != "" {
		converted.Echo.Set("conversation", mapOf("id", conversationID))
	}

	ctx, err := a.prepareCall(r, converted.Payload, key, start, "responses")
	if err != nil {
		return err
	}
	ctx.Responses = converted
	ctx.ResponsesState = &responsesState{user: user, inputItems: inputItems, history: history, conversationID: conversationID}
	if len(converted.Dropped) > 0 {
		items := make([]any, len(converted.Dropped))
		for i, name := range converted.Dropped {
			items[i] = name
		}
		ctx.Trace.Map("request").Set("dropped", items)
	}
	if previousID != "" {
		ctx.Trace.Map("request").Set("previous_response_id", previousID)
	}
	if conversationID != "" {
		ctx.Trace.Map("request").Set("conversation", conversationID)
	}
	if ctx.Gated {
		return a.serveGated(w, r, ctx)
	}
	// A Responses stream ends with the usage in response.completed, and an OpenAI-protocol
	// upstream only streams usage when asked to. Asked here rather than for every caller, so the
	// two older doors keep sending exactly what they always sent.
	if ctx.Payload.Bool("stream", false) && ctx.Resolved.Provider.Protocol() != "anthropic" && ctx.Resolved.API != "responses" {
		ctx.Payload.Set("stream_options", mapOf("include_usage", true))
	}
	return a.serve(w, r, ctx)
}

// rememberResponse stores the response a Responses request produced, and appends the turn to its
// conversation. The conversation is appended even with `store: false`, as OpenAI does: naming a
// conversation is itself the request to keep the turn.
func (a *App) rememberResponse(ctx *callContext, response *omap.Map) {
	store := respstore.Active()
	state := ctx.ResponsesState
	if store == nil || state == nil || response == nil {
		return
	}
	output, _ := response.Value("output").([]any)
	if ctx.Responses.Store {
		context := append(append(append([]any{}, state.history...), state.inputItems...), output...)
		store.PutResponse(&respstore.Response{
			ID: response.Str("id"), User: state.user, CreatedAt: time.Now(),
			Response: response, InputItems: state.inputItems, Context: context,
		})
	}
	if state.conversationID != "" {
		_, err := store.UpdateConversation(state.conversationID, state.user, func(c *respstore.Conversation) {
			c.Items = append(append(c.Items, state.inputItems...), output...)
		})
		if err != nil {
			logWarn("could not append to conversation %s: %v", state.conversationID, err)
		}
	}
}

// -- Reading stored responses --------------------------------------------------

func (a *App) stateStore(r *http.Request) (*respstore.Store, string, error) {
	key, err := a.apiKey(r)
	if err != nil {
		return nil, "", err
	}
	store := respstore.Active()
	if store == nil {
		return nil, "", errorf(http.StatusServiceUnavailable, "server-side state is not available")
	}
	return store, key.Str("user_login"), nil
}

func (a *App) getResponse(w http.ResponseWriter, r *http.Request) error {
	store, user, err := a.stateStore(r)
	if err != nil {
		return err
	}
	id := r.PathValue("id")
	resp, err := store.GetResponse(id, user)
	if err != nil {
		return notFound("Response", id)
	}
	writeJSON(w, http.StatusOK, resp.Response, nil)
	return nil
}

func (a *App) deleteResponse(w http.ResponseWriter, r *http.Request) error {
	store, user, err := a.stateStore(r)
	if err != nil {
		return err
	}
	id := r.PathValue("id")
	if err := store.DeleteResponse(id, user); err != nil {
		return notFound("Response", id)
	}
	writeJSON(w, http.StatusOK, mapOf("id", id, "object", "response", "deleted", true), nil)
	return nil
}

// pageItems applies the list parameters OpenAI's item listings take: order (desc by default),
// after (an item id cursor) and limit (1-100, 20 by default).
func pageItems(r *http.Request, items []any) (*omap.Map, error) {
	query := r.URL.Query()
	order := query.Get("order")
	if order == "" {
		order = "desc"
	}
	if order != "asc" && order != "desc" {
		return nil, errorf(http.StatusBadRequest, "order must be asc or desc")
	}
	limit := queryInt(r, "limit", 20)
	if limit < 1 || limit > 100 {
		return nil, errorf(http.StatusBadRequest, "limit must be between 1 and 100")
	}
	list := append([]any{}, items...)
	if order == "desc" {
		for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
			list[i], list[j] = list[j], list[i]
		}
	}
	if after := query.Get("after"); after != "" {
		for i, entry := range list {
			if item, ok := entry.(*omap.Map); ok && item.Str("id") == after {
				list = list[i+1:]
				break
			}
		}
	}
	hasMore := len(list) > limit
	if hasMore {
		list = list[:limit]
	}
	var first, last any
	if len(list) > 0 {
		first = list[0].(*omap.Map).Value("id")
		last = list[len(list)-1].(*omap.Map).Value("id")
	}
	return mapOf("object", "list", "data", list, "first_id", first, "last_id", last, "has_more", hasMore), nil
}

func (a *App) listResponseInputItems(w http.ResponseWriter, r *http.Request) error {
	store, user, err := a.stateStore(r)
	if err != nil {
		return err
	}
	id := r.PathValue("id")
	resp, err := store.GetResponse(id, user)
	if err != nil {
		return notFound("Response", id)
	}
	page, err := pageItems(r, resp.InputItems)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, page, nil)
	return nil
}

// -- Conversations -------------------------------------------------------------

func conversationObject(c *respstore.Conversation) *omap.Map {
	return mapOf("id", c.ID, "object", "conversation",
		"created_at", json.Number(strconv.FormatInt(c.CreatedAt.Unix(), 10)), "metadata", c.Metadata)
}

func (a *App) createConversation(w http.ResponseWriter, r *http.Request) error {
	store, user, err := a.stateStore(r)
	if err != nil {
		return err
	}
	body, err := readJSONObject(r)
	if err != nil {
		return err
	}
	items, err := normalizeItems(body.Value("items"), user)
	if err != nil {
		return err
	}
	c, err := store.CreateConversation(user, body.Map("metadata"), items)
	if err != nil {
		return errorf(http.StatusInternalServerError, "could not store the conversation: %v", err)
	}
	writeJSON(w, http.StatusOK, conversationObject(c), nil)
	return nil
}

func (a *App) getConversation(w http.ResponseWriter, r *http.Request) error {
	store, user, err := a.stateStore(r)
	if err != nil {
		return err
	}
	id := r.PathValue("id")
	c, err := store.GetConversation(id, user)
	if err != nil {
		return notFound("Conversation", id)
	}
	writeJSON(w, http.StatusOK, conversationObject(c), nil)
	return nil
}

func (a *App) updateConversation(w http.ResponseWriter, r *http.Request) error {
	store, user, err := a.stateStore(r)
	if err != nil {
		return err
	}
	body, err := readJSONObject(r)
	if err != nil {
		return err
	}
	id := r.PathValue("id")
	c, err := store.UpdateConversation(id, user, func(c *respstore.Conversation) {
		if metadata := body.Map("metadata"); metadata != nil {
			c.Metadata = metadata
		}
	})
	if err != nil {
		return notFound("Conversation", id)
	}
	writeJSON(w, http.StatusOK, conversationObject(c), nil)
	return nil
}

func (a *App) deleteConversation(w http.ResponseWriter, r *http.Request) error {
	store, user, err := a.stateStore(r)
	if err != nil {
		return err
	}
	id := r.PathValue("id")
	if err := store.DeleteConversation(id, user); err != nil {
		return notFound("Conversation", id)
	}
	writeJSON(w, http.StatusOK, mapOf("id", id, "object", "conversation.deleted", "deleted", true), nil)
	return nil
}

func (a *App) listConversationItems(w http.ResponseWriter, r *http.Request) error {
	store, user, err := a.stateStore(r)
	if err != nil {
		return err
	}
	id := r.PathValue("id")
	c, err := store.GetConversation(id, user)
	if err != nil {
		return notFound("Conversation", id)
	}
	page, err := pageItems(r, c.Items)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, page, nil)
	return nil
}

func (a *App) createConversationItems(w http.ResponseWriter, r *http.Request) error {
	store, user, err := a.stateStore(r)
	if err != nil {
		return err
	}
	body, err := readJSONObject(r)
	if err != nil {
		return err
	}
	items, err := normalizeItems(body.Value("items"), user)
	if err != nil {
		return err
	}
	id := r.PathValue("id")
	if _, err := store.UpdateConversation(id, user, func(c *respstore.Conversation) {
		c.Items = append(c.Items, items...)
	}); err != nil {
		return notFound("Conversation", id)
	}
	var first, last any
	if len(items) > 0 {
		first = items[0].(*omap.Map).Value("id")
		last = items[len(items)-1].(*omap.Map).Value("id")
	}
	writeJSON(w, http.StatusOK, mapOf("object", "list", "data", items, "first_id", first, "last_id", last, "has_more", false), nil)
	return nil
}

func findItem(items []any, id string) (int, *omap.Map) {
	for i, entry := range items {
		if item, ok := entry.(*omap.Map); ok && item.Str("id") == id {
			return i, item
		}
	}
	return -1, nil
}

func (a *App) getConversationItem(w http.ResponseWriter, r *http.Request) error {
	store, user, err := a.stateStore(r)
	if err != nil {
		return err
	}
	id, itemID := r.PathValue("id"), r.PathValue("item_id")
	c, err := store.GetConversation(id, user)
	if err != nil {
		return notFound("Conversation", id)
	}
	_, item := findItem(c.Items, itemID)
	if item == nil {
		return notFound("Item", itemID)
	}
	writeJSON(w, http.StatusOK, item, nil)
	return nil
}

func (a *App) deleteConversationItem(w http.ResponseWriter, r *http.Request) error {
	store, user, err := a.stateStore(r)
	if err != nil {
		return err
	}
	id, itemID := r.PathValue("id"), r.PathValue("item_id")
	missing := false
	c, err := store.UpdateConversation(id, user, func(c *respstore.Conversation) {
		index, _ := findItem(c.Items, itemID)
		if index < 0 {
			missing = true
			return
		}
		c.Items = append(c.Items[:index:index], c.Items[index+1:]...)
	})
	if errors.Is(err, respstore.ErrNotFound) {
		return notFound("Conversation", id)
	}
	if err != nil {
		return errorf(http.StatusInternalServerError, "could not update the conversation: %v", err)
	}
	if missing {
		return notFound("Item", itemID)
	}
	writeJSON(w, http.StatusOK, conversationObject(c), nil)
	return nil
}
