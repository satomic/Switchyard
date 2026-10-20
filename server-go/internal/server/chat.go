package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/satomic/model-router/server-go/internal/aicredits"
	"github.com/satomic/model-router/server-go/internal/config"
	"github.com/satomic/model-router/server-go/internal/keyscope"
	"github.com/satomic/model-router/server-go/internal/modelpolicy"
	"github.com/satomic/model-router/server-go/internal/omap"
	"github.com/satomic/model-router/server-go/internal/routing"
	"github.com/satomic/model-router/server-go/internal/upstream"
	"github.com/satomic/model-router/server-go/internal/wire"
)

// passthroughFields are the request fields passed straight through to the backend model.
var passthroughFields = []string{
	"messages", "temperature", "top_p", "max_tokens", "max_completion_tokens",
	"stop", "n", "presence_penalty", "frequency_penalty", "tools", "tool_choice",
	"response_format", "seed", "user", "stream",
}

// Sensitive request headers are never written to a trace.
var redactedHeaders = map[string]bool{
	"authorization": true, "api-key": true, "cookie": true,
	"proxy-authorization": true, "x-api-key": true,
}

// interactionHeaders is the header GitHub Copilot puts on every request belonging to one user
// interaction: the initial question and each follow-up of its tool-call loop all carry the same
// value, while x-request-id differs per HTTP call. Read in order, so a client that sets a
// plainer name still gets grouped.
var interactionHeaders = []string{"x-interaction-id", "x-conversation-id", "x-copilot-interaction-id"}

func mapOf(pairs ...any) *omap.Map {
	out := omap.New()
	for i := 0; i+1 < len(pairs); i += 2 {
		out.Set(pairs[i].(string), pairs[i+1])
	}
	return out
}

func round1(v float64) json.Number { return json.Number(fmt.Sprintf("%.1f", v)) }

func sanitizedHeaders(r *http.Request) *omap.Map {
	out := omap.New()
	for name, values := range r.Header {
		lowered := strings.ToLower(name)
		if redactedHeaders[lowered] {
			out.Set(lowered, "<redacted>")
			continue
		}
		out.Set(lowered, strings.Join(values, ", "))
	}
	return out
}

func interactionID(r *http.Request) any {
	for _, name := range interactionHeaders {
		value := strings.TrimSpace(r.Header.Get(name))
		if value != "" {
			if len(value) > 128 {
				value = value[:128]
			}
			return value
		}
	}
	return nil
}

// derivedInteractionID stands in for the interaction header on clients that mark their turns
// but do not number them.
//
// The Copilot CLI over BYOK is that client: it talks through the stock OpenAI SDK, so none of
// interactionHeaders arrives, but every request carries `x-initiator` -- `user` for the turn a
// person typed, `agent` for each follow-up of its tool-call loop -- and every turn of one loop
// resends the same last user message, down to the `<current_datetime>` stamp the CLI puts in
// it. That message, scoped to the API key, identifies the interaction as well as a header
// would: the loop routes once and is recorded as one trace, while the next question (a new
// message, a new stamp) gets a fresh decision.
//
// Gated on `x-initiator` so a client that never claimed to run a loop keeps a decision per
// request: two identical curl calls are two interactions, not one.
func derivedInteractionID(r *http.Request, key *omap.Map, prompt string) any {
	if strings.TrimSpace(r.Header.Get("x-initiator")) == "" || strings.TrimSpace(prompt) == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(key.Str("id") + "\x00" + prompt))
	return "derived-" + hex.EncodeToString(sum[:8])
}

// scopedInteractionID narrows a client's interaction id to one request of the person's.
//
// Copilot Chat up to 0.68 sent a fresh x-interaction-id with every prompt, so the header alone
// delimited an interaction. VS Code Copilot Chat 0.71 keeps one id for the whole chat window and
// marks every request `x-initiator: agent`, so the header alone folded every prompt of a chat into
// the first one: one routing decision for the whole conversation, one trace for all of it.
//
// The latest user message is what tells them apart. Within a tool-call loop it is resent unchanged
// -- Copilot's <current_datetime> stamp included -- so the loop stays one interaction; the next
// prompt is a new message with a new stamp, and gets its own decision and its own trace. For a
// client that already sends a new id per prompt this changes nothing.
func scopedInteractionID(id, prompt string) any {
	if strings.TrimSpace(prompt) == "" {
		return id
	}
	sum := sha256.Sum256([]byte(prompt))
	return id + ":" + hex.EncodeToString(sum[:4])
}

// backgroundRoute sends a client's background task to the default model, without a decision.
//
// Copilot CLI asks a model, alongside every new message, whether the person sounds frustrated with
// the assistant (`x-interaction-type: conversation-background`, a classifier prompt that pairs the
// previous message with the current one). It is the client's own bookkeeping: running the AI
// decision on it adds the decision's latency and cost, and routed it to a large model for a
// one-line classification. The default model -- within the caller's model policy and key scope --
// is what such a task is for. Not bound for stickiness: it belongs to no conversation turn.
func backgroundRoute(cfg *config.RouterConfig, allowed []string, interactionType string) (string, string, *omap.Map) {
	view := cfg
	if allowed != nil {
		view = cfg.RestrictedTo(allowed)
	}
	model := view.DefaultModel()
	return model, "client-background", mapOf(
		"type", "client-background",
		"interaction_type", interactionType,
		"note", "a background task the client runs for itself; routed to the default model without a decision",
	)
}

func headerOrNil(r *http.Request, name string) any {
	if value := r.Header.Get(name); value != "" {
		return value
	}
	return nil
}

func clientIP(r *http.Request) any {
	host := r.RemoteAddr
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		host = host[:idx]
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return nil
	}
	return host
}

// callContext is everything serve needs after routing has happened.
type callContext struct {
	Gated          bool
	Message        string
	Model          string
	Resolved       *config.ResolvedModel
	Payload        *omap.Map
	Trace          *omap.Map
	Headers        map[string]string
	Start          time.Time
	ClientProtocol string
	Cfg            *config.RouterConfig
	// Responses is set when the caller spoke the Responses API: the response side needs to know
	// which tools were declared as custom, and which request fields to echo back.
	Responses *wire.ResponsesRequest
	// ResponsesState is what has to be stored once a Responses answer is in (responses_state.go).
	ResponsesState *responsesState
	// CustomTools names the chat-completions custom tools that were declared upstream as
	// functions, so calls to them go back to the caller as custom calls.
	CustomTools map[string]bool
}

// -- The three protocol entry points ------------------------------------------
// A caller may speak either protocol and an upstream connection may speak either protocol, and
// the two choices are independent: an Anthropic client can be answered by an Azure deployment
// and an OpenAI client by a Claude endpoint. Rather than four pipelines, both entry points
// convert to one canonical form -- an OpenAI chat-completions payload -- which is what routing,
// the model policy and the trace record all already read. See internal/wire.

func (a *App) chatCompletions(w http.ResponseWriter, r *http.Request) error {
	key, err := a.apiKey(r)
	if err != nil {
		return err
	}
	start := time.Now()
	body, err := readJSONObject(r)
	if err != nil {
		return err
	}
	if len(body.Slice("messages")) == 0 {
		return errorf(http.StatusBadRequest, "messages is required")
	}
	custom := wire.BridgeChatCustomTools(body)
	ctx, err := a.prepareCall(r, body, key, start, "openai")
	if err != nil {
		return err
	}
	ctx.CustomTools = custom
	if ctx.Gated {
		return a.serveGated(w, r, ctx)
	}
	return a.serve(w, r, ctx)
}

// anthropicMessages is the Anthropic Messages entry point, so a client configured with
// ANTHROPIC_BASE_URL pointing at this router works unchanged.
//
// Authenticated by the same mr_ key as /v1/chat/completions, accepted from either `x-api-key`
// or `Authorization: Bearer` because the two ecosystems send different headers.
func (a *App) anthropicMessages(w http.ResponseWriter, r *http.Request) error {
	key, err := a.apiKey(r)
	if err != nil {
		return err
	}
	start := time.Now()
	raw, err := readJSONObject(r)
	if err != nil {
		return err
	}
	if len(raw.Slice("messages")) == 0 {
		return errorf(http.StatusBadRequest, "messages is required")
	}
	body := wire.AnthropicRequestToOpenAI(raw)
	ctx, err := a.prepareCall(r, body, key, start, "anthropic")
	if err != nil {
		return err
	}
	if ctx.Gated {
		return a.serveGated(w, r, ctx)
	}
	return a.serve(w, r, ctx)
}

// prepareCall is everything both entry points do before an upstream is touched: resolve what
// the caller may use, route, build the trace, build the response headers.
//
// Split out rather than inlined twice because every line of it -- the policy, the key scope,
// stickiness, the trace shape -- must behave identically whichever protocol the caller spoke,
// and two copies would drift.
func (a *App) prepareCall(r *http.Request, body, key *omap.Map, start time.Time, clientProtocol string) (*callContext, error) {
	cfg := a.Config()
	// user_id comes from the API key's owner: Copilot BYOK never sends x-user-id, so it used to
	// be permanently null.
	userID := key.Str("user_login")
	messages := body.Slice("messages")
	prompt := routing.ExtractUserPrompt(messages)
	interaction := interactionID(r)
	if interaction != nil {
		interaction = scopedInteractionID(omap.AsString(interaction), prompt)
	} else {
		interaction = derivedInteractionID(r, key, prompt)
	}
	// What the person asked, without the client's timestamp: the decision model, the rules and the
	// trace list read this. The identity above keeps the timestamp, which is what tells two
	// identical questions apart.
	asked := routing.DecisionPrompt(prompt)
	sessionID := strings.TrimSpace(r.Header.Get("x-session-id"))

	// The AI-credit gate comes before everything else on purpose: while the caller's Copilot
	// pool still has credits the request is answered with a note and is neither routed nor shown
	// to the decision model, so it costs nothing on any upstream. Administrators are gated too --
	// the point is the customer's budget, not a privilege boundary.
	if gated := aicredits.Gate(cfg, userID); gated != nil {
		return a.gatedContext(r, body, key, sessionID, start, clientProtocol, gated, asked, interaction), nil
	}

	// The caller's effective model set, resolved before anything routes. An empty list is a
	// configured outcome rather than an error -- an operator can bind a scope to an empty group,
	// which is how "this user gets nothing yet" is expressed -- so it is refused here with the
	// reason, instead of being handed to a router that has no model to pick.
	allowed := modelpolicy.AllowedModels(r.Context(), cfg, userID, a.isAdminLogin(userID))
	if allowed != nil && len(allowed) == 0 {
		return nil, errorf(http.StatusForbidden,
			"no models are available to you under the current model policy; "+
				"ask an administrator to assign a model group")
	}
	// Then narrowed again by this key's own scope. Reported separately from the policy refusal
	// above, because the two have different owners: the first needs an administrator, the second
	// the user can fix themselves on the API keys page.
	scope := key.Map("scope")
	allowed = keyscope.Narrow(cfg, allowed, scope)
	if allowed != nil && len(allowed) == 0 {
		return nil, errorf(http.StatusForbidden,
			"this API key is scoped to models you cannot currently use; "+
				"widen its scope on the API keys page (scope: %s)", keyscope.Describe(scope))
	}

	interactionType := strings.TrimSpace(r.Header.Get("x-interaction-type"))
	background := strings.Contains(strings.ToLower(interactionType), "background")
	var model, reason string
	var decisionMS float64
	var analysis *omap.Map
	if background {
		model, reason, analysis = backgroundRoute(cfg, allowed, interactionType)
	} else {
		model, reason, decisionMS, analysis = a.decideModel(r.Context(), cfg, asked, sessionID, interaction, allowed)
	}
	resolved := cfg.ResolveModel(model)

	payload := omap.New()
	for _, field := range passthroughFields {
		if body.Has(field) {
			payload.Set(field, body.Value(field))
		}
	}
	if resolved.Reasoning {
		// Reasoning models: max_tokens -> max_completion_tokens, and no sampling params.
		if payload.Has("max_tokens") {
			payload.Set("max_completion_tokens", payload.Value("max_tokens"))
			payload.Delete("max_tokens")
		}
		for _, param := range []string{"temperature", "top_p", "presence_penalty", "frequency_penalty"} {
			payload.Delete(param)
		}
	}

	params := omap.New()
	for _, field := range body.Keys() {
		if field != "messages" {
			params.Set(field, body.Value(field))
		}
	}
	sentParams := omap.New()
	for _, field := range payload.Keys() {
		if field != "messages" {
			sentParams.Set(field, payload.Value(field))
		}
	}

	trace := mapOf(
		"id", wire.NewID("")[:8],
		"ts", time.Now().UTC().Format("2006-01-02T15:04:05.000000-07:00"),
		"user_id", userID,
		"api_key_id", key.Value("id"),
		"api_key_name", key.Value("name"),
		"api_key_scope", keyscope.Describe(scope),
		"session_id", nilIfBlank(sessionID),
		// What makes this request part of a user interaction rather than an isolated call.
		// nil for a client that sends no such header, in which case every request is its own
		// interaction.
		"interaction_id", interaction,
		"request_id", headerOrNil(r, "x-request-id"),
		// Copilot marks a request the user triggered as "user" and the follow-ups of its tool
		// loop as "agent"; recorded per turn so the chain shows which is which.
		"initiator", headerOrNil(r, "x-initiator"),
		"client_ip", clientIP(r),
		// Which protocol the caller spoke. Worth recording even though the stored request is
		// always canonical: "the answer came back in a shape my client could not read" is
		// otherwise impossible to diagnose from a trace.
		"client_protocol", clientProtocol,
		// The client's own label for the request (Copilot: conversation-user, conversation-agent,
		// conversation-background, ...). A background one is a task the client runs for itself,
		// not something the person asked; the console marks it so.
		"interaction_type", nilIfBlank(interactionType),
		"background", background,
		"strategy", cfg.Strategy,
		"sticky", cfg.Sticky,
		"prompt_preview", headRunes(asked, 120),
		"request", mapOf(
			"headers", sanitizedHeaders(r),
			"messages", messages,
			"params", params,
			"stream", body.Bool("stream", false),
		),
		"routing", mapOf(
			"model", model,
			"reason", reason,
			"decision_ms", round1(decisionMS),
			"analysis", analysis,
		),
		"backend", mapOf(
			"deployment", resolved.UpstreamModel,
			"api", resolved.API,
			"protocol", resolved.Provider.Protocol(),
			"provider", resolved.Provider.Name,
			"base_url", resolved.Provider.BaseURL,
			"api_type", resolved.Provider.APIType,
			"sent_params", sentParams,
		),
		"response", nil,
		"status", "pending",
		"total_ms", nil,
	)
	// Resolved now rather than at write time, so x-trace-id names the interaction record this
	// turn joins -- a per-request id would 404 on GET /v1/traces/<id>.
	a.Traces.ResolveInteraction(trace)
	logInfo("route id=%s user=%s session=%s interaction=%v model=%s provider=%s protocol=%s->%s reason=%s decision_ms=%.1f",
		trace.Str("id"), userID, sessionID, interaction, model, resolved.Provider.Name,
		clientProtocol, resolved.Provider.Protocol(), reason, decisionMS)

	headers := map[string]string{
		"x-trace-id":           trace.Str("id"),
		"x-routed-model":       model,
		"x-router-reason":      reason,
		"x-router-decision-ms": fmt.Sprintf("%.1f", decisionMS),
	}
	if interaction != nil {
		headers["x-router-interaction-id"] = omap.AsString(interaction)
	}

	return &callContext{
		Model:          model,
		Resolved:       resolved,
		Payload:        payload,
		Trace:          trace,
		Headers:        headers,
		Start:          start,
		ClientProtocol: clientProtocol,
		Cfg:            cfg,
	}, nil
}

func nilIfBlank(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func headRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// decideModel returns (model, reason, decision ms, analysis), including session-sticky logic.
//
// Two binding keys, checked in that order. `interaction` is the tighter one: it holds the model
// constant across the tool-call loop of a single user question, which is what stops one question
// being routed N times. `sessionID` is the broader, opt-in one: it holds a model across a whole
// conversation.
//
// `allowed` is the caller's effective model set from the model policy, or nil when they are
// unrestricted. It narrows the catalog every strategy sees (see RouterConfig.RestrictedTo), so a
// model the caller may not use is unreachable through rules, through the decision model, and
// through the default-model substitution alike -- rather than being caught by a check that each
// of those three paths would have to remember to make.
func (a *App) decideModel(ctx context.Context, cfg *config.RouterConfig, prompt, sessionID string,
	interaction any, allowed []string) (string, string, float64, *omap.Map) {
	start := time.Now()
	elapsed := func() float64 { return float64(time.Since(start).Microseconds()) / 1000 }

	// The narrowed view is what every branch below reads. `cfg` itself is untouched: it is
	// shared by every concurrent request, so a per-caller restriction may never be written into it.
	view := cfg
	if allowed != nil {
		view = cfg.RestrictedTo(allowed)
	}

	if cfg.Sticky {
		bindings := []struct{ key, kind string }{
			{omap.AsString(interaction), "interaction"},
			{sessionID, "session"},
		}
		for _, binding := range bindings {
			if binding.key == "" {
				continue
			}
			cached := a.Sessions().Get(bindKey(binding.kind, binding.key))
			if cached == "" {
				continue
			}
			if !view.Models.Has(cached) {
				// The binding predates a policy change, or two callers with different effective
				// sets share a session id. Either way a stale binding must not resurrect a model
				// this caller may no longer use, so the decision is made again.
				logInfo("sticky %s binding to %s dropped: not in the caller's effective model set",
					binding.kind, cached)
				continue
			}
			analysis := mapOf(
				// The console renders both kinds the same way -- a note saying the decision was
				// skipped -- so they share the analysis type.
				"type", "session",
				"note", fmt.Sprintf("%s %s is already bound to model %s, skipping the routing decision",
					binding.kind, binding.key, cached),
				"bound_by", binding.kind,
			)
			reason := "session-sticky"
			if binding.kind == "interaction" {
				reason = "interaction-sticky"
			}
			return cached, reason, elapsed(), analysis
		}
	}

	var model, reason string
	var analysis *omap.Map
	switch cfg.Strategy {
	case "ai":
		model, reason, analysis = routing.RouteByAI(ctx, prompt, view, a.Pool)
	case "rule-then-ai":
		model, reason, analysis = routing.RouteCombined(ctx, prompt, view, a.Pool)
	default:
		model, reason, analysis = routing.RouteByRules(prompt, view)
	}

	if allowed != nil {
		policyModels := make([]any, 0, view.Models.Len())
		for _, name := range view.Models.Keys() {
			policyModels = append(policyModels, name)
		}
		analysis.Set("policy_models", policyModels)
	}

	if cfg.Sticky {
		if id := omap.AsString(interaction); id != "" {
			a.Sessions().Set(bindKey("interaction", id), model)
			analysis.Set("interaction_bound", id)
		}
		if sessionID != "" {
			a.Sessions().Set(bindKey("session", sessionID), model)
			analysis.Set("session_bound", sessionID)
		}
	}
	return model, reason, elapsed(), analysis
}

// bindKey namespaces the two kinds of sticky key so they share one store without an id of one
// kind ever being able to answer a lookup of the other.
func bindKey(kind, value string) string { return kind + ":" + value }

// finalizeTrace closes this request out as one turn and hands it to the store.
//
// The store decides whether that turn opens a new record or appends to the interaction already
// underway. Everything here describes this HTTP request only; the interaction-level totals are
// computed at merge time.
func (a *App) finalizeTrace(trace *omap.Map, start time.Time, status string,
	content any, usage *omap.Map, finishReason any, errText string, toolCalls []any) {
	trace.Set("status", status)
	totalMS := float64(time.Since(start).Microseconds()) / 1000
	trace.Set("total_ms", round1(totalMS))

	var response *omap.Map
	if errText != "" {
		trace.Set("error", errText)
	} else {
		response = mapOf(
			"content", content,
			"finish_reason", finishReason,
			"usage", nilIfEmptyMap(usage),
			// The tool calls the model asked for. This is the half of an agentic chain that used
			// to be lost: the assistant message carrying them was only ever visible in the *next*
			// request's replayed messages, so the final turn -- which asks for no tools -- made it
			// look as though none had been requested at all.
			"tool_calls", nilIfEmptySlice(toolCalls),
		)
		trace.Set("response", response)
	}

	routing := trace.Map("routing")
	decisionMS := 0.0
	if routing != nil {
		decisionMS = routing.Float("decision_ms", 0)
	}
	if backend := trace.Map("backend"); backend != nil {
		backend.Set("latency_ms", round1(totalMS-decisionMS))
	}

	request := trace.Map("request")
	if request == nil {
		request = omap.New()
	}
	messages := request.Slice("messages")
	turn := mapOf(
		"ts", trace.Value("ts"),
		"request_id", trace.Value("request_id"),
		"initiator", trace.Value("initiator"),
		"message_count", json.Number(fmt.Sprintf("%d", len(messages))),
		// Kept so the merge can tell an appended chain from a rewritten one, then dropped for the
		// appended case -- the full chain is stored once at the top level.
		"messages", messages,
		"params", request.Value("params"),
		"stream", request.Value("stream"),
		"model", valueOf(routing, "model"),
		"deployment", valueOf(trace.Map("backend"), "deployment"),
		// Both protocols are recorded per turn rather than once per interaction: one chain can be
		// answered by an Azure deployment on its first turn and a Claude endpoint on its second,
		// and a turn that does not say which is which cannot explain itself.
		"client_protocol", trace.Value("client_protocol"),
		"protocol", valueOf(trace.Map("backend"), "protocol"),
		"status", status,
		"total_ms", trace.Value("total_ms"),
		"response", nilIfEmptyMap(response),
		"error", nilIfBlank(errText),
	)
	trace.Set("turns", []any{turn})
	a.Traces.Add(trace)
}

func valueOf(m *omap.Map, key string) any {
	if m == nil {
		return nil
	}
	return m.Value(key)
}

func nilIfEmptyMap(m *omap.Map) any {
	if m == nil {
		return nil
	}
	return m
}

func nilIfEmptySlice(items []any) any {
	if len(items) == 0 {
		return nil
	}
	return items
}

// -- The gated answer ---------------------------------------------------------

// gatedContext builds the context for a request the AI-credit gate answers itself.
//
// A trace is still written -- an operator asking "why did nobody route through the router this
// morning" needs to find the answer in the trace list -- with the gate's name in the model and
// reason columns so it neither counts as a backend call nor as an error.
func (a *App) gatedContext(r *http.Request, body, key *omap.Map, sessionID string, start time.Time,
	clientProtocol string, gated *omap.Map, prompt string, interaction any) *callContext {
	cfg := a.Config()
	messages := body.Slice("messages")
	params := omap.New()
	for _, field := range body.Keys() {
		if field != "messages" {
			params.Set(field, body.Value(field))
		}
	}

	note := fmt.Sprintf("answered by the AI-credit gate: %s -- ", omap.AsString(gated.Value("enterprise")))
	if gated.Str("reason") == aicredits.ReasonBudget {
		note += fmt.Sprintf("the caller's user-level budget still has $%s left", commas(gated.Float("headroom_usd", 0)))
	} else {
		note += "the Copilot pool still has credits"
	}
	note += ", so the request was neither routed nor sent to the decision model"

	trace := mapOf(
		"id", wire.NewID("")[:8],
		"ts", time.Now().UTC().Format("2006-01-02T15:04:05.000000-07:00"),
		"user_id", key.Value("user_login"),
		"api_key_id", key.Value("id"),
		"api_key_name", key.Value("name"),
		"api_key_scope", keyscope.Describe(key.Map("scope")),
		"session_id", nilIfBlank(sessionID),
		"interaction_id", interaction,
		"request_id", headerOrNil(r, "x-request-id"),
		"initiator", headerOrNil(r, "x-initiator"),
		"client_ip", clientIP(r),
		"client_protocol", clientProtocol,
		"strategy", cfg.Strategy,
		"sticky", cfg.Sticky,
		"prompt_preview", headRunes(prompt, 120),
		"request", mapOf(
			"headers", sanitizedHeaders(r),
			"messages", messages,
			"params", params,
			"stream", body.Bool("stream", false),
		),
		"routing", mapOf(
			"model", aicredits.GateReason,
			"reason", aicredits.GateReason,
			"decision_ms", json.Number("0.0"),
			"analysis", mapOf(
				"type", "gate",
				"note", note,
				"reason", gated.Value("reason"),
				"enterprise", gated.Value("enterprise"),
				"remaining_credits", gated.Value("remaining"),
				"pool_total", gated.Value("pool_total"),
				"headroom_usd", gated.Value("headroom_usd"),
				"budget_usd", gated.Value("budget_usd"),
			),
		),
		"backend", mapOf(
			"deployment", nil, "api", nil, "protocol", nil,
			"provider", nil, "base_url", nil, "api_type", nil, "sent_params", omap.New(),
		),
		"response", nil,
		"status", "pending",
		"total_ms", nil,
	)
	a.Traces.ResolveInteraction(trace)
	logInfo("gated id=%s user=%s enterprise=%v reason=%s remaining=%v headroom=%v",
		trace.Str("id"), key.Str("user_login"), gated.Value("enterprise"), gated.Str("reason"),
		gated.Value("remaining"), gated.Value("headroom_usd"))

	headers := map[string]string{
		"x-trace-id":           trace.Str("id"),
		"x-routed-model":       aicredits.GateReason,
		"x-router-reason":      aicredits.GateReason,
		"x-router-decision-ms": "0.0",
	}
	if interaction != nil {
		headers["x-router-interaction-id"] = omap.AsString(interaction)
	}

	model := omap.AsString(body.Value("model"))
	if model == "" {
		model = aicredits.GateReason
	}
	return &callContext{
		Gated:          true,
		Message:        gated.Str("message"),
		Model:          model,
		Payload:        mapOf("stream", body.Bool("stream", false)),
		Trace:          trace,
		Headers:        headers,
		Start:          start,
		ClientProtocol: clientProtocol,
		Cfg:            cfg,
	}
}

func commas(v float64) string {
	formatted := fmt.Sprintf("%.2f", v)
	whole, fraction, _ := strings.Cut(formatted, ".")
	var grouped strings.Builder
	for i, digit := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			grouped.WriteByte(',')
		}
		grouped.WriteRune(digit)
	}
	return grouped.String() + "." + fraction
}

// serveGated answers a gated request with the note as a normal assistant message, in the
// caller's protocol and streaming mode.
//
// A 200 rather than a 4xx because the note is meant to be read in the chat window, and Copilot
// shows an error status as a bare failure.
func (a *App) serveGated(w http.ResponseWriter, r *http.Request, ctx *callContext) error {
	completion := mapOf(
		"id", "chatcmpl-"+ctx.Trace.Str("id"),
		"object", "chat.completion",
		"created", json.Number(fmt.Sprintf("%d", time.Now().Unix())),
		"model", ctx.Model,
		"choices", []any{mapOf(
			"index", json.Number("0"),
			"message", mapOf("role", "assistant", "content", ctx.Message),
			"finish_reason", "stop",
		)},
		"usage", mapOf(
			"prompt_tokens", json.Number("0"),
			"completion_tokens", json.Number("0"),
			"total_tokens", json.Number("0"),
		),
	)
	if ctx.Payload.Bool("stream", false) {
		chunks := chunksFromCompletion(completion)
		return a.streamResponse(w, r, ctx, chunks)
	}
	return a.finishAndRender(w, completion, ctx)
}

// finishAndRender records the turn, then answers in the caller's protocol.
func (a *App) finishAndRender(w http.ResponseWriter, completion *omap.Map, ctx *callContext) error {
	choice := firstChoice(completion)
	message := choice.Map("message")
	if message == nil {
		message = omap.New()
	}
	a.finalizeTrace(ctx.Trace, ctx.Start, "ok",
		message.Value("content"), completion.Map("usage"), choice.Value("finish_reason"),
		"", message.Slice("tool_calls"))
	switch ctx.ClientProtocol {
	case "anthropic":
		writeJSON(w, http.StatusOK, wire.OpenAIResponseToAnthropic(completion, ctx.Model), ctx.Headers)
		return nil
	case "responses":
		response := wire.OpenAIResponseToResponses(completion, ctx.Model, ctx.Responses)
		a.rememberResponse(ctx, response)
		writeJSON(w, http.StatusOK, response, ctx.Headers)
		return nil
	}
	wire.RestoreChatCustomCalls(completion, ctx.CustomTools)
	writeJSON(w, http.StatusOK, completion, ctx.Headers)
	return nil
}

func firstChoice(data *omap.Map) *omap.Map {
	choices := data.Slice("choices")
	if len(choices) == 0 {
		return omap.New()
	}
	if choice, ok := choices[0].(*omap.Map); ok {
		return choice
	}
	return omap.New()
}

// serve calls the upstream in its own protocol and answers in the caller's.
//
// Four combinations reduce to two decisions taken independently: which upstream branch produces
// the canonical result, and which renderer turns it into the caller's shape.
func (a *App) serve(w http.ResponseWriter, r *http.Request, ctx *callContext) error {
	resolved := ctx.Resolved
	payload := ctx.Payload
	streaming := payload.Bool("stream", false)

	fail := func(err error) error {
		if ctx.Trace.Str("status") == "pending" {
			a.finalizeTrace(ctx.Trace, ctx.Start, "error", nil, nil, nil, err.Error(), nil)
		}
		log.Printf("ERROR mr: backend call failed model=%s provider=%s: %v",
			ctx.Model, resolved.Provider.Name, err)
		return errorf(http.StatusBadGateway, "backend model call failed: %v", err)
	}

	var chunks chunkSource
	var completion *omap.Map

	// adjusted records, in the trace, a change the router made to the request before it went out.
	// Appended rather than overwritten: one request can need both the effort and the limit fixed.
	adjusted := func(s string) {
		backend := ctx.Trace.Map("backend")
		if previous := backend.Str("adjusted"); previous != "" {
			s = previous + "; " + s
		}
		backend.Set("adjusted", s)
	}

	switch {
	case resolved.Provider.Protocol() == "anthropic":
		client, err := a.Pool.Get(resolved.Provider, "chat")
		if err != nil {
			return fail(err)
		}
		body := wire.OpenAIRequestToAnthropic(payload, resolved.UpstreamModel)
		// What actually went on the wire, which for a converted request is not what the caller
		// sent: a trace that showed only the caller's parameters could not explain a max_tokens
		// the caller never set.
		sent := omap.New()
		for _, field := range body.Keys() {
			if field != "messages" && field != "system" {
				sent.Set(field, body.Value(field))
			}
		}
		backend := ctx.Trace.Map("backend")
		backend.Set("sent_params", sent)
		if dropped := wire.AnthropicUnsupportedParams(payload); len(dropped) > 0 {
			items := make([]any, len(dropped))
			for i, name := range dropped {
				items[i] = name
			}
			backend.Set("dropped_params", items)
		}
		if streaming {
			source, err := withOutputCapRetry(resolved.Provider.Name, resolved.UpstreamModel, body, adjusted,
				func(b *omap.Map) (chunkSource, error) {
					return a.openAnthropicStream(r.Context(), client, b, ctx.Model)
				})
			if err != nil {
				return fail(err)
			}
			chunks = source
		} else {
			data, err := withOutputCapRetry(resolved.Provider.Name, resolved.UpstreamModel, body, adjusted,
				func(b *omap.Map) (*omap.Map, error) { return client.Create(r.Context(), resolved.UpstreamModel, b) })
			if err != nil {
				return fail(err)
			}
			completion = wire.AnthropicResponseToOpenAI(data, ctx.Model)
		}
	case resolved.API == "responses":
		client, err := a.Pool.Get(resolved.Provider, "responses")
		if err != nil {
			return fail(err)
		}
		completion, err = withOutputCapRetry(resolved.Provider.Name, resolved.UpstreamModel, payload.Clone(), adjusted,
			func(b *omap.Map) (*omap.Map, error) {
				return completeViaResponsesAPI(r.Context(), client, resolved.UpstreamModel, b)
			})
		if err != nil {
			return fail(err)
		}
		if streaming {
			chunks = chunksFromCompletion(completion)
		}
	default:
		client, err := a.Pool.Get(resolved.Provider, "chat")
		if err != nil {
			return fail(err)
		}
		body := payload.Clone()
		body.Set("model", resolved.UpstreamModel)
		note := func(s string) {
			adjusted(s)
			if sent := ctx.Trace.Map("backend").Map("sent_params"); sent != nil {
				sent.Set("reasoning_effort", body.Value("reasoning_effort"))
				for _, field := range outputCapFields {
					if body.Has(field) {
						sent.Set(field, body.Value(field))
					}
				}
			}
		}
		// Two independent refusals a routed request can meet, each fixed by one retry: an output
		// limit sized for another model, and tools on a reasoning model at its default effort.
		if streaming {
			source, err := withOutputCapRetry(resolved.Provider.Name, resolved.UpstreamModel, body, note,
				func(b *omap.Map) (chunkSource, error) {
					return withEffortRetry(resolved.Reasoning, resolved.Provider.Name, resolved.UpstreamModel, b, note,
						func(b *omap.Map) (chunkSource, error) {
							return openOpenAIStream(r.Context(), client, resolved.UpstreamModel, b)
						})
				})
			if err != nil {
				return fail(err)
			}
			chunks = source
		} else {
			data, err := withOutputCapRetry(resolved.Provider.Name, resolved.UpstreamModel, body, note,
				func(b *omap.Map) (*omap.Map, error) {
					return withEffortRetry(resolved.Reasoning, resolved.Provider.Name, resolved.UpstreamModel, b, note,
						func(b *omap.Map) (*omap.Map, error) { return client.Create(r.Context(), resolved.UpstreamModel, b) })
				})
			if err != nil {
				return fail(err)
			}
			completion = data
		}
	}

	if streaming {
		return a.streamResponse(w, r, ctx, chunks)
	}
	return a.finishAndRender(w, completion, ctx)
}

// completeViaResponsesAPI adapts a chat request to the Responses API and converts the result
// back to the chat.completion shape.
//
// Tools travel both ways: chat tools become the Responses API's flat function tools, and the
// function_call items that come back become chat tool_calls. Without that, an agent routed to a
// Responses-only model got a plain-text answer to a request that needed a tool, and stopped.
func completeViaResponsesAPI(ctx context.Context, client *upstream.Client, model string, payload *omap.Map) (*omap.Map, error) {
	body := mapOf("model", model, "input", responsesInput(payload.Slice("messages")))
	maxOut := payload.Value("max_completion_tokens")
	if maxOut == nil {
		maxOut = payload.Value("max_tokens")
	}
	if maxOut != nil {
		body.Set("max_output_tokens", maxOut)
	}
	if tools := responsesTools(payload.Slice("tools")); len(tools) > 0 {
		body.Set("tools", tools)
		switch choice := payload.Value("tool_choice").(type) {
		case string:
			body.Set("tool_choice", choice)
		case *omap.Map:
			if fn := choice.Map("function"); fn != nil && fn.Str("name") != "" {
				body.Set("tool_choice", mapOf("type", "function", "name", fn.Str("name")))
			}
		}
	}
	if format := payload.Map("response_format"); format != nil {
		switch format.Str("type") {
		case "json_schema":
			if schema := format.Map("json_schema"); schema != nil {
				out := mapOf("type", "json_schema")
				for _, field := range schema.Keys() {
					out.Set(field, schema.Value(field))
				}
				body.Set("text", mapOf("format", out))
			}
		case "json_object":
			body.Set("text", mapOf("format", mapOf("type", "json_object")))
		}
	}
	resp, err := client.Create(ctx, model, body)
	if err != nil {
		return nil, err
	}
	// Any OpenAI-compatible upstream can be configured, so a missing created_at must not fail
	// the whole request.
	created := resp.Value("created_at")
	if created == nil {
		created = json.Number(fmt.Sprintf("%d", time.Now().Unix()))
	}
	message := mapOf("role", "assistant", "content", outputText(resp))
	finish := "stop"
	calls := []any{}
	for _, item := range resp.Slice("output") {
		entry, ok := item.(*omap.Map)
		if !ok || entry.Str("type") != "function_call" {
			continue
		}
		calls = append(calls, mapOf(
			"id", entry.Value("call_id"),
			"type", "function",
			"function", mapOf("name", entry.Value("name"), "arguments", entry.Str("arguments")),
		))
	}
	if len(calls) > 0 {
		message.Set("tool_calls", calls)
		if message.Str("content") == "" {
			message.Set("content", nil)
		}
		finish = "tool_calls"
	} else if resp.Str("status") == "incomplete" {
		finish = "length"
	}
	return mapOf(
		"id", resp.Value("id"),
		"object", "chat.completion",
		"created", created,
		"model", model,
		"choices", []any{mapOf(
			"index", json.Number("0"),
			"message", message,
			"finish_reason", finish,
		)},
		"usage", chatUsage(resp.Map("usage")),
	), nil
}

// responsesTools converts chat tools ({type, function: {...}}) to the Responses API's flat form.
func responsesTools(tools []any) []any {
	out := []any{}
	for _, item := range tools {
		tool, ok := item.(*omap.Map)
		if !ok || tool.Str("type") != "function" {
			continue
		}
		fn := tool.Map("function")
		if fn == nil {
			continue
		}
		flat := mapOf("type", "function", "name", fn.Value("name"))
		for _, field := range []string{"description", "parameters", "strict"} {
			if fn.Has(field) {
				flat.Set(field, fn.Value(field))
			}
		}
		out = append(out, flat)
	}
	return out
}

// chatUsage converts Responses usage ({input_tokens, output_tokens}) to the chat shape the trace
// and the usage rollup read. Already-chat usage passes through.
func chatUsage(usage *omap.Map) any {
	if usage == nil || usage.Len() == 0 {
		return nil
	}
	if usage.Has("prompt_tokens") {
		return usage
	}
	input := usage.Value("input_tokens")
	output := usage.Value("output_tokens")
	out := mapOf("prompt_tokens", input, "completion_tokens", output, "total_tokens", usage.Value("total_tokens"))
	if details := usage.Map("output_tokens_details"); details != nil {
		out.Set("completion_tokens_details", details)
	}
	if details := usage.Map("input_tokens_details"); details != nil {
		out.Set("prompt_tokens_details", details)
	}
	return out
}

// outputText flattens the Responses API's output array into the plain text a chat completion
// carries. The SDK exposed this as `output_text`; over the wire it has to be assembled.
func outputText(resp *omap.Map) string {
	if text := resp.Str("output_text"); text != "" {
		return text
	}
	var parts strings.Builder
	for _, item := range resp.Slice("output") {
		entry, ok := item.(*omap.Map)
		if !ok || entry.Str("type") != "message" {
			continue
		}
		for _, contentItem := range entry.Slice("content") {
			block, ok := contentItem.(*omap.Map)
			if !ok {
				continue
			}
			if kind := block.Str("type"); kind == "output_text" || kind == "text" {
				parts.WriteString(block.Str("text"))
			}
		}
	}
	return parts.String()
}

// responsesInput converts chat-completions messages into Responses API input items.
//
// The two protocols disagree about tool calls, and the Responses API rejects the chat shape
// outright: an assistant tool call is `content: null` plus `tool_calls` there and a
// `function_call` item here, and a result is a `role: tool` message there and a
// `function_call_output` item here. Without this, every agentic loop routed to a Responses-only
// model died on its second call.
func responsesInput(messages []any) []any {
	items := []any{}
	for _, entry := range messages {
		msg, ok := entry.(*omap.Map)
		if !ok {
			continue
		}
		if msg.Str("role") == "tool" {
			items = append(items, mapOf(
				"type", "function_call_output",
				"call_id", msg.Value("tool_call_id"),
				"output", orEmptyString(msg.Value("content")),
			))
			continue
		}
		calls := msg.Slice("tool_calls")
		content := msg.Value("content")
		// A tool-call turn carries no text, and an empty message item is not worth sending.
		if !isBlank(content) || len(calls) == 0 {
			items = append(items, mapOf("role", msg.Value("role"), "content", responsesContent(msg.Str("role"), content)))
		}
		for _, callItem := range calls {
			call, ok := callItem.(*omap.Map)
			if !ok {
				continue
			}
			fn := call.Map("function")
			if fn == nil {
				fn = omap.New()
			}
			arguments := fn.Str("arguments")
			if arguments == "" {
				arguments = "{}"
			}
			items = append(items, mapOf(
				"type", "function_call",
				"call_id", call.Value("id"),
				"name", fn.Value("name"),
				"arguments", arguments,
			))
		}
	}
	return items
}

// responsesContent converts chat content parts to Responses parts. A string stays a string; the
// Responses API rejects chat's "text" / "image_url" part types outright.
func responsesContent(role string, content any) any {
	parts, ok := content.([]any)
	if !ok {
		return orEmptyString(content)
	}
	textType := "input_text"
	if role == "assistant" {
		textType = "output_text"
	}
	out := []any{}
	for _, item := range parts {
		part, ok := item.(*omap.Map)
		if !ok {
			continue
		}
		switch part.Str("type") {
		case "text":
			out = append(out, mapOf("type", textType, "text", part.Str("text")))
		case "image_url":
			image := part.Map("image_url")
			if image == nil {
				continue
			}
			converted := mapOf("type", "input_image", "image_url", image.Value("url"))
			if detail := image.Str("detail"); detail != "" {
				converted.Set("detail", detail)
			}
			out = append(out, converted)
		default:
			out = append(out, part)
		}
	}
	return out
}

func isBlank(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case []any:
		return len(t) == 0
	}
	return false
}

func orEmptyString(v any) any {
	if isBlank(v) {
		return ""
	}
	return v
}
