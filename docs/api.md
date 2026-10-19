# API

| Endpoint | Notes |
|---|---|
| `POST /v1/chat/completions` | OpenAI-compatible, streaming supported. **Requires an API key**; optional `x-interaction-id` (one decision and one trace per user interaction) and `x-session-id` (session stickiness); response headers include `x-trace-id` (the *interaction's* trace) / `x-routed-model` / `x-router-reason` / `x-router-decision-ms`, plus `x-router-interaction-id` when the request carried one |
| `POST /v1/messages` | Anthropic Messages-compatible, streaming supported, so a client configured with `ANTHROPIC_BASE_URL` pointing here works unchanged. **Requires an API key**, accepted from either `x-api-key` or `Authorization: Bearer` because the two ecosystems send different headers. Same routing, same stickiness headers and same response headers as `/v1/chat/completions`; the body's `model` is ignored, since the router picks the model |
| `POST /v1/responses` | OpenAI Responses-compatible, streaming supported, so a client that speaks only the Responses API (Copilot CLI with `COPILOT_PROVIDER_WIRE_API=responses`, Codex, `responses.create` in the OpenAI SDKs) works unchanged. **Requires an API key**. Same routing, stickiness and response headers as `/v1/chat/completions`. Stateless: `previous_response_id`, `conversation` and `item_reference` are refused with a `400`, so send the whole conversation in `input` (`store: false`). See [The Responses API](#the-responses-api) |
| `GET /v1/models` | the available backend models (requires an API key), **narrowed to what the key's owner may use** under the [model policy](model-policy.md) and then by that key's own [scope](#api-key-scopes) |
| `GET /v1/models/available` | the signed-in user's effective model list and why it looks that way: the resolved names, the catalog descriptions, the default model, a `reason`, and the grants that applied. Resolved through the same code the API path uses, so it cannot disagree with a real request. Available to **every** signed-in user, and returns no trace of the teams or organizations they are not in |
| `GET /v1/auth/status` | what the frontend uses to choose between the setup page, the sign-in page and the console |
| `POST /v1/auth/setup` | the first-run wizard (only while unconfigured + from the local machine) |
| `GET /v1/auth/github/login` · `GET /v1/auth/github/callback` · `POST /v1/auth/logout` | the GitHub OAuth flow |
| `GET/POST /v1/keys`, `PATCH/DELETE /v1/keys/{id}` | list / create (returns the one-time plaintext, subject to the key policy) / disable / delete API keys. `POST` takes an optional `scope`; `PATCH` accepts `disabled` and `scope` independently, so saving one does not clear the other |
| `GET /v1/access/me` | whether the signed-in user may create a key: the verdict, the reason, and the per-check evidence; plus `key_scope`, the separate verdict on whether they may [narrow a key](#api-key-scopes). Both verdicts also carry `reason_code` and `reason_params`, the same answer machine-readably, so a console can say it in the reader's language; `reason` stays the English record used by the logs and the `403` bodies (see [access control](access-control.md#both-verdicts-in-the-readers-language)) |
| `GET /v1/access/token` | the status of the Enterprise administrator token (administrators; returns only a mask and the owner, **never echoes the plaintext**) |
| `POST /v1/access/verify-token` | validate a token and report its scopes (administrators) |
| `GET /v1/access/discover?refresh=` | automatically fetch the enterprise, Enterprise Team and organization lists (administrators; served from `data/github/structure.json`, `?refresh=1` goes to GitHub) |
| `GET /v1/access/cache` | the state of the on-disk GitHub cache: fetch ages, per-scope member counts, truncation and errors (administrators; never returns member logins) |
| `POST /v1/access/cache/refresh` | refresh that cache now instead of waiting for the background loop (administrators) |
| `GET /v1/access/users` | every login that has signed in at least once, with first / last sign-in, a count and the model group currently bound to it (administrators). Read from `data/known_users.json` rather than the session table, because expired sessions are purged, so sessions can only answer "who is signed in right now" |
| `GET /v1/access/topology/members?kind=&name=&page=` | administrators only; lazy topology member page, 50 rows maximum. `kind=organization` uses an organization login; `kind=team` uses `enterprise/numeric-team-id`; `kind=known` pages registered/configured accounts without GitHub membership claims. Returns `{users, page, has_more, source}`. Fresh complete token-matching member caches are reused; otherwise only the requested GitHub page is fetched. No user policy evaluations run. Invalid scope/page returns 422; unavailable GitHub data returns 503. A full live page can require one final empty page to establish the end |
| `GET /v1/access/topology/user?login=` | administrators only; evaluates **one** selected user's key-creation permission, scope-editing permission and model policy through the existing evaluators. Returns `{login, is_admin, access, key_scope, model_policy, keys}`. Each evaluation has a 20-second deadline; failed evaluations are `null` (unknown), not false. Keys belong only to that login and never contain plaintext. Model policy is the owner's model set; each key still intersects it with its own scope |
| `POST /v1/auth/local/login` | sign in as the local super administrator or a built-in user (`{username, password}`; the response reports `must_change_password`) |
| `POST /v1/auth/local/password` | change that password, and optionally the username (`{current_password, new_password, new_username?}`) |
| `POST /v1/auth/local/enabled` | enable / disable the local account (administrators) |
| `POST /v1/auth/password` | a built-in user changes their own password (`{current_password, new_password}`) |
| `GET /v1/directory` | built-in users, user groups and the GitHub accounts that have signed in (administrators) |
| `POST /v1/directory/users`, `PATCH/DELETE /v1/directory/users/{username}` | create, edit, disable, reset the password of, or delete a built-in user (administrators); see [users and groups](users-and-groups.md#api) |
| `POST /v1/directory/groups`, `PATCH/DELETE /v1/directory/groups/{name}` | create, edit or delete a user group (administrators) |
| `GET /v1/usage?days=&user_id=` | usage statistics: request count, tokens, error rate, latency, and the distribution by model / date / user |
| `GET /v1/credits` | the Copilot AI-credit pool snapshot per enterprise (size, used, remaining, state, seats, per-user budget rows when per-user mode is on) plus the polling / gate settings, freshness and the next scheduled poll (administrators). Never returns the token; per-user rows carry budget figures, not consumption details. See [Copilot AI credits](ai-credits.md) |
| `POST /v1/credits/refresh` | poll GitHub for the pool state now regardless of the schedule and return the fresh snapshot (administrators; `409` when another worker holds the refresh lease, `502` when GitHub could not be read) |
| `POST /v1/credits/schedule/preview` | validate a cron expression, `{schedule}`, and return its next five firing times as epoch seconds: `{valid, error, next}` (administrators). Rendered by the same parser the scheduler uses |
| `GET /v1/config` / `PUT /v1/config` | read / update the configuration (administrators; hot reload + write-back to config.yaml) |
| `GET /v1/traces?offset=&limit=&date=&user_id=&session_id=&trace_id=` | a page of trace summaries, `{total, items, offset, limit, truncated}`, read straight off disk, filterable by date / user / session / trace-id fragment. Each summary carries `interaction_id` and `turn_count`, so a row that took several upstream calls says so. `limit` is clamped to 500; `truncated` says the date-directory cap was reached, so a shortened `total` is not mistaken for "that is all there is" |
| `GET /v1/traces/{id}` | one full interaction: the complete message chain, the single routing decision, the backend call, the model response, and `turns[]`, one entry per upstream call with its tool calls, latency and tokens |
| `DELETE /v1/traces/{id}` | delete one trace, i.e. the whole interaction (administrators); 404 if it is already gone |
| `DELETE /v1/traces?date=&user_id=` | delete every trace matching the criteria, returning `{deleted: N}` (administrators). **At least one criterion is required**: an unfiltered call is refused with 422 rather than treated as a wipe-all |
| `GET /v1/router/decisions` | a legacy endpoint, kept for compatibility |
| `GET /healthz` | health check: the loaded provider list, plus the running `version` and the project links the console's header shows |
| `GET /v1/release` | the last answer from the background release check: `{current_version, latest_version, update_available, release_url, published_at, checked_at, error}` (public). Reads a cached result, so it never calls GitHub |
| `POST /v1/release/check` | ask GitHub for the latest release now instead of waiting for the daily check (administrators, since it makes an outbound request) |

Which identity each endpoint requires is tabulated in
[the permission matrix](authentication.md#permission-matrix).

## The AI-credit gate

When the [BYOK gate](ai-credits.md) is on and the caller's enterprise pool still has credits, both
`POST /v1/chat/completions`, `POST /v1/responses` and `POST /v1/messages` answer **without routing**: a `200` in the
caller's protocol and streaming mode whose single assistant message is the configured note, zero
tokens in `usage`, and `x-routed-model: ai-credits-gate` / `x-router-reason: ai-credits-gate` in the
response headers. A trace is still written under that name.

## Every protocol reaches every model

`/v1/chat/completions`, `/v1/responses` and `/v1/messages` are three front doors onto the same
router. Which one a client uses has no bearing on which models it can reach: the router converts
between the OpenAI chat-completions shape, the OpenAI Responses shape and the Anthropic Messages
shape at the edges, so an OpenAI-style client can
be answered by a Claude endpoint and an Anthropic-style client by an Azure deployment, streaming
included. Each trace turn records `client_protocol` (the door the request came in) and `protocol`
(what the backend spoke), so a conversion is visible after the fact. Details and the exact field
mapping: [Backend connections](providers.md).

## The Responses API

`POST /v1/responses` is converted to a chat-completions request on the way in and the answer is
rendered back as a `response` object, or, with `stream: true`, as Responses events
(`response.created`, `response.output_text.delta`, `response.function_call_arguments.delta`,
`response.completed`, ...). Conformance is tested with the official OpenAI Python SDK, which parses
every object and event strictly, and with GitHub Copilot CLI.

| Responses request | Becomes |
|---|---|
| `instructions` | a system message |
| `input` as a string | one user message |
| `input` message items (`user`, `assistant`, `system`, `developer`) | messages; `developer` becomes `system` |
| `function_call` / `function_call_output` items | an assistant `tool_calls` turn / a `tool` message |
| `tools` of `type: "function"` | chat function tools |
| `tools` of `type: "custom"` (free-form, e.g. `apply_patch`) | a function with one string argument `input`; the call comes back as a `custom_tool_call` |
| `max_output_tokens`, `temperature`, `top_p`, `tool_choice`, `text.format` | their chat equivalents |

Not forwarded, and listed under `request.dropped` in the trace:

* **Reasoning items.** Their encrypted content is only meaningful to the model that produced it, and
  the next turn may be routed to another model.
* **Hosted tools** (`web_search`, `file_search`, `code_interpreter`, ...). They run inside OpenAI's
  own service, which a routed backend is not.

Refused with a `400`, because the router keeps no response store and the next turn may go
elsewhere: `previous_response_id`, `conversation`, `item_reference`. Clients that run with
`store: false` (Copilot CLI and Codex do) already send the whole conversation every turn.

The same custom-tool bridge applies on `/v1/chat/completions`: a `{"type": "custom"}` tool, which
Copilot CLI sends in GPT-5 mode, is declared to the backend as a function and the call is returned as
a `custom` tool call. Without it, Azure refuses the request when the router picks a model that
doesn't know the type.

### When the client's output limit is too large for the routed model

A client sizes its output limit for the model it thinks it's using. Copilot CLI configured for
`gpt-5.4` asks for 128000 output tokens; if the router picks `gpt-4.1-mini`, Azure refuses the
request ("supports at most 32768 completion tokens"). The router retries once with the limit the
backend named, remembers that limit for the model, and clamps later requests before they're sent.
The trace notes it under `backend.adjusted`. This applies to every door.

## API key scopes

A key carries a `scope` describing what it may reach, and it is composed with the owner's model
policy by **intersection**: `effective = policy(owner) ∩ scope(key)`. A scope can only narrow, so it
is never a way around the policy, and a key cannot outlive its owner's permissions: revoke a model
group from the user and every one of their keys loses it on the next request.

```json
{"kind": "all"}
{"kind": "api_types", "api_types": ["anthropic", "azure"]}
{"kind": "models", "models": ["gpt-4o", "claude-sonnet-5"]}
```

`api_types` is stored as a rule rather than as the models it matched at creation time, so a model
added to an `anthropic` connection later is picked up without editing the key. `models` is validated
against the owner's own available models when it is written, so a scope cannot name something its
owner could not reach in the first place.

Setting any scope other than `{"kind": "all"}` is itself a **permission**, granted per user, team and
organization under `auth.key_scope_policy` and off by default, because a key pinned to one expensive
model defeats the routing that keeps costs down. `POST /v1/keys` and `PATCH /v1/keys/{id}` answer
`403` with the failing level named when the caller does not have it. Widening a key back to
`{"kind": "all"}` is always accepted, and administrators are exempt. The verdict a console can read
ahead of time is `key_scope` on `GET /v1/access/me`; the semantics are in
[Access control](access-control.md#who-may-narrow-a-keys-scope).
