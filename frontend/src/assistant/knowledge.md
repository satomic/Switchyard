# Model Router console knowledge base

Reference for the in-console help assistant. Page paths in backticks are the only valid link
targets. "Admin only" pages are hidden from standard users and their endpoints return 403 to them.

---

## What Model Router is

- A gateway that accepts requests on three protocols and sends each to a suitable backend model:
  `POST /v1/chat/completions` (OpenAI Chat Completions), `POST /v1/responses` (OpenAI Responses API,
  incl. `previous_response_id`, `conversation`, `item_reference`, `store: false`; `background: true`
  is refused with 400) and `POST /v1/messages` (Anthropic Messages). Streaming and tool calls work on all.
- Backends ("connections") are `azure` (Azure OpenAI / AI Foundry), `openai` (any OpenAI-compatible
  endpoint) or `anthropic` (Messages API). Any client protocol reaches any backend; the router converts
  request, response and stream events. OpenAI-only params are dropped for Anthropic backends and
  listed in the trace.
- The model is chosen by rules, by an AI decision (an LLM or a dedicated decision engine), or rules
  first then AI. The `model` a client sends is a hint; the router decides. `auto` is accepted.
- One user interaction (e.g. a Copilot tool-call loop) = one routing decision and one trace (stickiness).
- Every request needs an API key (`mr_…`). The key's owner is the caller's identity, which is how
  GitHub Copilot BYOK traffic (which carries no user id) is attributed.
- Policies: key policy (who may create keys), model policy (which models each caller gets), key scope
  (who may narrow a key). AI credits page keeps BYOK from wasting the Copilot AI-credit pool.
- Sign-in: GitHub OAuth, the local administrator (`admin` / `admin1234` by default, forced change),
  and built-in users. All state lives in `data/` (`data/config.yaml`, keys, sessions, traces); no database.
- Two roles: administrators (GitHub logins in the admin list, the local administrator, built-in users
  with the Administrator role) and standard users. Standard users see Overview (Usage, Available
  models, API keys) and Monitoring (Traces, Playground); the Management group is admin only.
- The help assistant (this chat): the button in the bottom-right corner of every page. It asks this
  router with one of the user's own API keys (chosen or created on first use, changeable from its
  header), so its questions are routed, count towards usage, and appear in `/traces` labelled AI assistant.
- Top bar: status chip (`Running · <strategy> · sticky`), language, theme toggle, account menu (role,
  time zone, sign out; built-in users also get "Change password").
- Saving on configuration pages (Routing configuration, Model policy, Access control, AI credits)
  goes through a save bar: it turns blue with a dot on changed tabs when there are unsaved edits;
  **Save and apply** writes `data/config.yaml` (comments kept) and applies immediately, no restart.
  **Reload** discards the draft. The Users page and the Local administrator tab apply immediately.
  Editing `config.yaml` by hand needs a restart.

---

## Usage — `/usage`

- Range buttons: Today, Last 7, 30, 90 days. Tiles: Requests, Total tokens (prompt/completion split),
  Error rate (failure count), Average latency (with P95). Charts: Requests by model, Requests by day;
  a By user table with **Drill down**; a Detail (table view).
- Administrators get a **View scope** selector (All users or one user). Standard users see only their own.
- Figures come from a background rollup (default hourly); **Refresh** recomputes now. Config:
  `usage.rollup_interval_seconds` (min 60, default 3600), `usage.rollup_days` (default 90).

---

## Available models — `/models`

- Every signed-in user. Shows the caller's effective model list, a count, the reason, each model's
  description, `default` and `reasoning` badges, and "Where your list comes from" (only grants that
  actually applied; teams/orgs you are not in are never shown).
- Reasons: `policy-disabled` / full catalog (no model policy), `administrator` (admins are exempt),
  `no-binding` (policy on but nothing bound to you: unrestricted), `union` (union of your granted
  groups), `empty-group` (every grant is empty: calls refused with 403).
- Grant scopes shown: Default group, Account, User group, Team, Organization.
- To change it, an admin edits `/policy`.

---

## API keys — `/keys`

- **Key creation permission** panel: allowed/denied, the reason, **Granted via**, **Show evidence**
  (per-scope checks, answered by `cache`, `probe`, `live` or `local`), **Check again**. If denied,
  creation is disabled and you cannot use BYOK; quote the reason to an admin.
- **New key**: Name (empty = `default`), **Scope**, **Create key**. Scopes: **All your models**
  (default), **By connection type** (every model on connections of the ticked `api_type`s, including
  models added later), **Specific models**. A scope only narrows: effective = owner's model policy ∩ key scope.
  If the scope picker shows **Not allowed**, the admin has not granted key-scope permission
  (`/access/keyscope`); the key covers everything you may use.
- **My keys** table: Name, Key (Show/Copy; readable only by its owner), Scope, Created, Last used,
  Calls, Enable/Disable, Delete, **Usage example**. Admins can tick "Show keys of all users" and can
  disable/delete anyone's key but never read it ("owner only"). Old keys created before readable
  storage show "unavailable". Disabling a key makes it fail with 401 at once.
- **Usage example** (also shown right after creation) has three tabs with your key filled in and a
  **Copy command** button:
  - **OpenAI Chat Completions**: `OPENAI_BASE_URL = <origin>/v1`, `OPENAI_API_KEY = mr_…`, curl to `/v1/chat/completions`.
  - **OpenAI Responses**: the same plus the Copilot CLI block:
    `COPILOT_PROVIDER_BASE_URL=<origin>/v1`, `COPILOT_PROVIDER_API_KEY=mr_…`,
    `COPILOT_PROVIDER_WIRE_API=responses`, `COPILOT_PROVIDER_WIRE_MODEL=auto`. The operations guide
    also suggests `COPILOT_PROVIDER_MODEL_ID=gpt-5.4` (what Copilot CLI tunes its agent for).
  - **Anthropic-compatible**: `ANTHROPIC_BASE_URL = <origin>` (no `/v1`), `ANTHROPIC_AUTH_TOKEN = mr_…`,
    curl to `/v1/messages` with `x-api-key` and `anthropic-version: 2023-06-01`.
- Key headers accepted: `Authorization: Bearer mr_…`, `api-key`, `x-api-key`.

---

## Traces — `/traces`, `/traces/<id>`

- One row = one user interaction. Columns: Time, User, Model, Decision, Latency, Calls (`×N` = an
  agent tool loop). Filters: Date, Trace ID fragment, User (admins only). Paging with Rows per page;
  **Auto refresh**. Background requests are labelled `background`.
- Decision values: a rule's name, `default` (rule routing, nothing matched), `ai-decision`,
  `ai-fallback-default` (AI decision failed/timed out/invalid), `interaction-sticky` / `session-sticky`
  (reused binding), `client-background`, `ai-credits-gate` (answered by the AI-credit gate, not routed).
- Detail panes: Overview (time, user, API key, interaction and session ids, latency = decision +
  backend), Routing decision, Request parameters, Backend call, Model response, and a Call chain for
  multi-call interactions (initiator, model, latency, tokens, tool calls per call). JSON trees have
  Expand all / Collapse all / Copy JSON. The divider is draggable.
- Under `rule-then-ai` the decision shows `1. Rules` (each rule, matched or not) then `2. AI decision`
  (system prompt sent, input, raw output, rationale, latency, tokens); no AI stage = a rule fired.
  Dedicated engines also record probabilities and confidence.
- Each turn records `client_protocol` and backend `protocol`; differing = protocol conversion.
- Standard users see only their own traces. Admins can delete one trace or all traces matching the
  filters.
- Every response carries `x-trace-id`, `x-routed-model`, `x-router-reason`, `x-router-decision-ms`
  (and `x-router-interaction-id` if the request had one).

---

## Playground — `/playground`

- Paste an API key (optional "Remember in this browser"), type a prompt, optional Session ID (to test
  stickiness), Streaming (SSE) toggle, **Send**. **Routing result** shows model, reason and decision
  ms; **View the full trace** opens `/traces/<id>`. It calls `/v1/chat/completions` with that key, so it
  behaves exactly like a real client.

---

## Policy topology — `/topology` (admin only)

- Read-only graph of saved configuration: policies, identities (enterprises, organizations, Enterprise
  Teams, users) and models. Searchable Policies / Identities / Models indexes. Click a node to explore;
  **Back**, **Policy overview**, **Fit graph**, **Open configuration** (jumps to the editor), **Reload**.
- Click an organization/team to load members; click a user to see their **Effective model access**.
  Failed evaluations show Unknown, never Denied.

---

## Routing configuration (admin only)

Four steps, each linking to the next; `/config` opens `/config/providers`.

### Backend connections — `/config/providers`

- Table: Connection, Type, Address, Key (`set`/`missing`), Bound models, Actions (**Set as default**,
  **Rename**, **Delete**). Search and Interface type filter. Select a row to edit; **+ Add connection**.
- Fields: **Address (base_url)** — `azure`: resource root `https://<resource>.openai.azure.com/`;
  `openai`: up to `/v1`; `anthropic`: host serving `/v1/messages` (e.g. `https://api.anthropic.com`).
  **Key (api_key)** (sent as `x-api-key` for anthropic). **Interface type (api_type)**: azure / openai /
  anthropic. Version: azure `?api-version=` (default `2024-12-01-preview`); anthropic
  `anthropic-version` header (default `2023-06-01`); openai none. Changing type clears it.
- One connection is `default`; models without their own connection use it. You cannot delete the
  default connection or one still bound to models.
- Config: `providers.<name>.{base_url, api_key, api_type, api_version}`, `default_provider`.

### Model catalog — `/config/models`

- Table: Model, Connection, Upstream name, Capabilities (`default`, `reasoning`, `responses api`),
  Description. Filters: Search, Connection, Capability. **+ Add model**.
- Fields: **Description** (read by the AI decision model; write what tasks it suits), **Connection
  (provider)** (Follow the default or a specific one), **Upstream name override (model_name)**,
  **Default model** (exactly one; used when no rule matches or a decision fails), **Reasoning model**
  (sends `max_completion_tokens`, strips `temperature`/`top_p`; tick for gpt-5.x/o3 and for Claude
  endpoints that reject sampling params, e.g. Databricks), **Responses API** (call upstream via
  `/openai/v1/responses`).
- Deleting a model removes it from rules and model groups and reports how many.
- Config: `models.<name>.{provider, model_name, description, default, reasoning, api: responses}`.

### Routing strategy — `/config/strategy`

- **Routing chain in effect** diagram (Request → Keyword rules → AI decision → Default model); unused
  stages are dimmed `not used`.
- Strategy (`strategy`): **Rules first, then AI** (`rule-then-ai`, recommended: only unmatched
  requests pay a decision call), **AI routing** (`ai`: decision on every request, ~1 s), **Rule routing**
  (`rule`: no LLM call, <1 ms, default model when nothing matches).
- **Session stickiness** (`session.sticky`, `session.ttl_seconds` default 1800, `session.max_sessions`
  default 10000, LRU eviction): reuse the chosen model for the rest of an interaction/session.
- **AI decision model** (LLM path): Decision model (`ai_router.decision_model`, e.g. `gpt-4.1`),
  Connection used by the decision model (`ai_router.decision_provider`, empty = default),
  Decision timeout (`ai_router.timeout_seconds`, default 5; timeout → default model),
  Prompt truncation length (`ai_router.max_prompt_chars`, default 4000; keeps half from each end),
  Reasoning effort (`ai_router.decision_reasoning_effort`: none/low/medium/high/xhigh; only when the
  decision model is marked reasoning).
- **Decision engine**: tick "Use a dedicated decision model" and choose (`ai_router.decision_engine`;
  omitted or `llm` = the LLM path):
  - `typesafe` — TypeSafe Jev (hosted, ~300 ms, most accurate): `ai_router.typesafe.{api_key (empty = TYPESAFE_API_KEY env), model (default jev-latest), base_url}`.
  - `laya` — self-hosted Laya: `ai_router.laya.{base_url (required), api_key, model}`; less accurate.
  - `microsoft` — Microsoft-Decision-1 in your Foundry resource: `ai_router.microsoft.{provider (reuse a connection's address/key), base_url, api_key, model = deployment name, default Microsoft-Decision-1}`. Wrong deployment name → 404 → fallback.
  - `openai` — OpenAI Decisions API (gpt-6-luna): `ai_router.openai.{api_key (empty = OPENAI_API_KEY), model, base_url}`.
  Dedicated engines use no system prompt; they get one choice question whose options are the catalog
  models, described by their descriptions. Catalog limits: 2–255 models (Laya 100), otherwise fallback.
- **Instructions** (`ai_router.decision_instructions`): the question every dedicated engine is asked;
  empty = neutral default "Which backend model should handle this request? Pick the option whose
  description best matches what the request needs." Keep it neutral; put trade-offs in descriptions.
- **AI decision prompt** (`ai_router.decision_prompt`, LLM path): system prompt template; `{catalog}`
  is replaced by model names + descriptions (no brace escaping; if missing, the catalog is appended).
  Empty = built-in default. Buttons: Load the default prompt, Clear (back to default), Insert
  `{catalog}`. **Actual prompt preview** is rendered by the backend from the unsaved draft; a sample
  request shows truncation. Models without description are listed ("Go and fill them in").
  **Run a real decision** sends the sample to the selected engine with unsaved settings.

### Rule routing — `/config/rules`

- Table: Order, Rule, Conditions, Target model. Search (name or keyword), Target model filter,
  **+ Add rule**, Move up/down (disabled while filtering).
- Evaluated top to bottom; first match wins. **Keywords**: comma separated, case-insensitive, any one
  match routes (matched literally as text, so list every language users write in). **Minimum prompt
  length**: routes prompts at least that long. A rule whose model is not in the caller's catalog is skipped.
- With `rule` strategy unmatched → default model; with `rule-then-ai` unmatched → decision model; with
  `ai` rules are ignored (badge says so). A fired rule is never second-guessed by AI.
- Config: `rules: [{name, keywords: [...], min_prompt_chars, model}]`.

---

## Model policy — `/policy` (admin only)

- **Restrict which models each caller may use** (`model_policy.enabled`). Off = everyone gets the whole catalog.
- **Default group for signed-in users** (`model_policy.default_group`): "No default group" leaves
  unbound accounts unrestricted; an empty group gives new accounts nothing.
- **Model groups** (`model_groups: {name: [models]}`): **+ Add group**, tick models, All/None,
  Rename, Delete. Empty groups are legal and grant nothing.
- **Users who have signed in**: per-user group selector (`model_policy.users`). **User group bindings** (`model_policy.user_groups`), **Teams**
  (`model_policy.teams`, key `'<enterprise slug>/<team id>'`), **Organizations**
  (`model_policy.organizations`); pick from discovered structure or **+ Enter one manually**.
- Only scopes the key policy allows are listed (`filtered` count); bound scopes no longer allowed stay
  marked `no keys`. `partial list` = GitHub returned an incomplete list.
- Semantics: effective list = union of default group and every matching binding; admins exempt; no
  binding = unrestricted; only grants that all resolve empty deny (403). Membership lookups fail open.
  Sticky bindings to a no-longer-allowed model are re-decided.

---

## Access control (admin only)

`/access` opens `/access/policy`.

### Administrators and sign-in — `/access/admins`

- **Administrator GitHub logins** (`auth.admin_logins`, comma separated). Admins see management pages,
  all usage and traces, and are exempt from key policy and model policy. Emptying the list removes
  admin rights from all GitHub accounts (fix by editing `data/config.yaml`).
- **Allow any GitHub account to sign in** (`auth.allow_any_github_user`). Off = only admins (and
  GitHub accounts in any user group) can sign in. Signing in never grants API access by itself.
- Session lifetime: `auth.session_ttl_seconds` (default 604800 = 7 days).

### GitHub OAuth — `/access/oauth`

- Client ID, Client Secret (shown as configured, never echoed), **Callback URL** (leave empty: derived
  from the request origin, honours `X-Forwarded-Proto`/`Host`). The panel shows the URLs to register on
  GitHub (callback path `/v1/auth/github/callback`). Config `auth.github.{client_id, client_secret, callback_url}`.
- A wrong setting locks out GitHub sign-in; keep the local administrator enabled while changing it.
- First-run setup wizard (Client ID/Secret + admin logins) is offered only while OAuth is unconfigured
  and the request comes from 127.0.0.1.

### Local administrator — `/access/local`

- **Allow signing in with the local administrator account** (`auth.local_admin.enabled`), User name,
  Current password, New password, **Change the credential**. Applies immediately. New password ≥ 8
  chars, not `admin1234`. Changing it signs out other local-admin sessions.
- Recovery: clear `auth.local_admin.password_hash` and `password_salt` in `data/config.yaml` → default
  `admin`/`admin1234` returns with forced change. Disabling it while OAuth is unconfigured and no
  built-in admin exists leaves no way to sign in (re-enable with `auth.local_admin.enabled: true` + restart).

### Key policy — `/access/policy`

- **Only allow users in the listed user groups / enterprises / organizations to create API keys**
  (`auth.key_policy.enabled`). Off = anyone who can sign in may create a key.
- **Allowed user groups** (`auth.key_policy.user_groups`): checked first, no GitHub token needed.
- **GitHub Enterprise administrator token** (`auth.key_policy.github_token`, PAT with
  `admin:enterprise`), **Verify** (owner, scopes, can list enterprises). Enabled without token and
  without matching user groups denies all non-admins (fail-closed).
- **Local GitHub cache** card (`data/github/`, `auth.key_policy.cache_refresh_seconds`, default 3600,
  min 60): fetch times, cached scopes, per-scope state (complete/truncated/errored), **Refresh now**.
  Truncated/errored lists fall back to live probes.
- **Enterprises, enterprise teams and organizations** (**Fetch again**): per enterprise a master switch
  (`enterprises.<slug>.enabled`), allow any organization (`allow_all_orgs`), **Allowed organizations**
  (`organizations`), **Allowed enterprise teams** (`teams`, numeric ids). Any one match allows (OR).
  Admins are exempt. This token is also used by `/credits`.

### Key scope — `/access/keyscope`

- Who may narrow their keys to models/connection types. Cost control, off by default
  (`auth.key_scope_policy.enabled`): every key covers all models.
- **Let the users, user groups, teams and organizations listed below restrict their API keys**; lists:
  Users (`users`), User groups (`user_groups`), Enterprise teams (`teams`, `'<slug>/<id>'`),
  Organizations (`organizations`). Only accounts that may create keys are offered (`filtered`).
- AND across the filled-in levels, OR within a level; empty levels are "Not consulted". On with all
  empty = **Grants nobody**. Admins exempt. Removing the permission does not change existing keys; a
  key can always be widened back to all. The per-user verdict uses the saved key policy.

---

## Users — `/users/users`, `/users/groups` (admin only)

Changes apply immediately (no save bar). Stored in `data/directory.json`, not `config.yaml`.

- **Built-in users** (`/users/users`): **New user** — User name (1–64 letters/digits/`.`/`_`/`-`, fixed
  later), Display name, Email, Password (≥ 8, not `admin1234`), Administrator, User groups, "Require a
  new password at the next sign-in" (default on). Row actions: **Edit**, **Reset password** (signs out
  everywhere), **Disable** (signs out; its API keys get 403), **Delete** (deletes keys; traces stay).
  You cannot disable, demote or delete yourself. Names already used by the local admin, an admin login
  or a signed-in GitHub account are refused (409).
- **User groups** (`/users/groups`): **New group** — Group name, Description, members (built-in users,
  signed-in GitHub accounts, or typed GitHub logins). Cards show where a group is used. Deleting a group
  removes it from Model policy, Key policy and Key scope.
- Built-in users sign in on the user name/password form; they change their password from the account menu.

---

## AI credits — `/credits` (admin only)

- Purpose: Copilot Business/Enterprise seats share a monthly AI-credit pool forfeited at month end;
  stop BYOK from spending the customer's Azure/OpenAI bill while credits remain. Uses the key policy's
  enterprise token.
- **Shared AI-credit pool** cards per enterprise: state (credits remaining / pool exhausted / unknown /
  no Copilot seats / error), Pool size (exact or estimated from seats: 3,900 per Enterprise, 1,900 per
  Business seat), Used, Remaining, Metered overage, Seats, membership source. **Refresh now**.
- **Polling schedule**: **Poll GitHub for the pool state on a schedule** (`ai_credits.enabled`), Cron
  schedule in UTC (`ai_credits.schedule`, default `*/30 * * * *`; presets 15 min to daily; preview of
  next runs), Enterprises to poll (`ai_credits.enterprises`, none = all).
- **BYOK gate** (`ai_credits.gate.enabled`): while the user's enterprise pool has credits, requests are
  not routed and get a note as a normal assistant reply (200). Needs polling on. **Decide per user**
  (`gate.per_user`): the user's own GitHub user-level budget wins over the pool. **Lift the gate when
  fewer than N credits remain** (`ai_credits.min_remaining_credits`). Notes: `gate.message`
  (placeholders `{enterprise}`, `{slug}`, `{remaining}`, `{total}`, `{remaining_note}`) and
  `gate.message_budget` (adds `{budget_remaining_usd}`, `{budget_total_usd}`, `{budget_remaining_credits}`);
  empty = built-in English. Admins are gated too.
- Fail-open: any uncertainty or an exhausted pool lets the request through. Gated requests are traced
  as `ai-credits-gate`.

---

## How do I…

**Connect VS Code GitHub Copilot (BYOK).** Create a key at `/keys`. In Copilot add an
OpenAI-compatible provider: Base URL `http://<host>:8000/v1`, API key `mr_…`, any model name from
`/models`. Copilot sends `x-interaction-id`, so each prompt's tool loop is routed once and traced once.

**Connect Copilot CLI.** Use the OpenAI Responses tab of **Usage example** at `/keys`:
`COPILOT_PROVIDER_BASE_URL=http://<host>:8000/v1`, `COPILOT_PROVIDER_API_KEY=mr_…`,
`COPILOT_PROVIDER_WIRE_API=responses` (GPT-5-series ids need it; unset = chat completions),
`COPILOT_PROVIDER_WIRE_MODEL=auto` (or `COPILOT_MODEL=auto`, which the provider settings default to),
optionally `COPILOT_PROVIDER_MODEL_ID=gpt-5.4`. The CLI sends no
interaction header; the router derives one from `x-initiator` and the last message.

**Connect an Anthropic-style client (e.g. Claude Code).** `ANTHROPIC_BASE_URL=http://<host>:8000`,
`ANTHROPIC_AUTH_TOKEN=mr_…`. The model field is ignored/`auto`.

**Add a backend connection.** `/config/providers` → **+ Add connection** → name, base_url, key,
api_type (+ version) → **Set as default** if needed → **Save and apply**.

**Add a model.** `/config/models` → **+ Add model** → name, description, connection, upstream name if
different, flags (Default / Reasoning / Responses API) → **Save and apply**. Then add it to model groups
at `/policy` if the policy is on.

**Choose a routing strategy and decision engine.** `/config/strategy`. Recommended: Rules first, then
AI, stickiness on, a light decision model (or a dedicated engine such as TypeSafe Jev for speed).
Use **Run a real decision** to test before saving. Add certain-intent rules at `/config/rules`.

**Restrict which models a user / team / org / user group gets.** `/policy` → enable the switch →
create model groups → bind them in Users who have signed in, User group bindings, Teams or
Organizations → **Save and apply**. To give new users nothing by default, set the default group to an
empty group. Users verify at `/models`.

**Let only certain people create keys.** `/access/policy` → enable the policy → either Allowed user
groups (no token) or token + enterprise switch + allowed organizations/teams → **Save and apply**.

**Let someone restrict a key to certain models.** `/access/keyscope` → enable → add them to a list → save.
They then use **Scope** on `/keys`.

**Set up users without GitHub.** `/users/users` → **New user**; optionally group them at
`/users/groups`; allow the group at `/access/policy` (Allowed user groups) if the key policy is on and
bind a model group at `/policy`.

**Configure GitHub OAuth.** Create a GitHub OAuth App (homepage `http://<host>:8000/`, callback
`http://<host>:8000/v1/auth/github/callback`), enter Client ID/Secret at `/access/oauth`, list admin
logins at `/access/admins`, save.

**Change the local admin password.** `/access/local` → current + new password → **Change the
credential**. Forgotten: blank `password_hash`/`password_salt` under `auth.local_admin`.

**Find out why a request went to a model.** Open the trace (`/traces`, or `x-trace-id` header) →
Routing decision pane: rule name, AI decision with prompt/rationale/probabilities, sticky or fallback.

**Avoid wasting Copilot AI credits.** `/credits` → turn polling on → enable the BYOK gate (optionally
per user) → save. Needs the enterprise token at `/access/policy`.

**Move data or config.** Server env vars: `MR_DATA_DIR` (root of all state; `/data` in the Docker
image), `MR_CONFIG_FILE` (default `<data>/config.yaml`), `MR_LOG_DIR` (default `<data>/logs`).

---

## Glossary

- **Connection (provider)**: backend address + key + `api_type`.
- **Interaction**: one user question, incl. every request of its tool-call loop; keyed by
  `x-interaction-id` (or `x-conversation-id` / `x-copilot-interaction-id`) plus a hash of the latest
  user message, or derived from `x-initiator` for Copilot CLI.
- **Session**: opt-in `x-session-id` grouping across a conversation.
- **Stickiness**: reusing the first decision for the rest of an interaction/session (`interaction-sticky`/`session-sticky`).
- **Decision engine**: what makes the AI decision: `llm` (default), `typesafe`, `laya`, `microsoft`, `openai`.
- **Decision instructions / prompt**: question for dedicated engines / system prompt for the LLM path.
- **client-background**: a client background task (`x-interaction-type` containing `background`, e.g.
  Copilot CLI's side checks) sent to the default model without a decision.
- **Key policy**: who may create API keys (OR across groups/orgs/teams; fail-closed).
- **Key scope**: what one key may reach inside the owner's models; narrowing needs permission (AND across levels).
- **Model group**: named list of models, granted by the model policy (union; empty group = nothing).
- **User group**: named set of logins (built-in users and GitHub accounts) usable in every policy.
- **BYOK**: Bring Your Own Key — GitHub Copilot calling this router as a custom model provider.
- **AI-credit gate**: answers BYOK requests with a note while the Copilot pool still has credits.
- **Trace / turn**: record of one interaction / one upstream call inside it.

---

## Troubleshooting

- **401 "missing API key"** — no key sent; use `Authorization: Bearer mr_…` (or `x-api-key`).
- **401 "API key is invalid or has been revoked"** — key mistyped, disabled or deleted; check `/keys`.
- **403 "the account that owns this API key is disabled"** — built-in user disabled at `/users/users`.
- **403 `password_change_required`** — signed in with a default or admin-set password; change it first
  (local admin: forced screen; built-in user: forced form). Everything else is blocked until then.
- **403 "no models are available to you under the current model policy"** — every grant is an empty
  group; admin binds a model group at `/policy`. `/models` shows `empty-group`.
- **403 "this API key is scoped to models you cannot currently use"** — widen the key's Scope at `/keys`.
- **403 on key creation** — key policy denies you; read the panel at `/keys`, ask an admin
  (`/access/policy`). "Enabled but no token" denies everyone not in an allowed user group.
- **403 when setting a key scope** — no permission at `/access/keyscope`.
- **Cannot sign in with GitHub** — sign-in limited to admins, OAuth misconfigured, or the login is a
  built-in user name. Admins can use the local administrator.
- **Decision `ai-fallback-default`** — decision model timed out, errored, returned an unknown model, or
  the catalog is out of engine limits; the default model served it. Check the trace for the reason,
  the timeout, keys and deployment names at `/config/strategy`.
- **Every call reports `interaction-sticky`** — expected with stickiness: one decision per interaction.
- **Response says to use Copilot / model `ai-credits-gate`** — the BYOK gate at `/credits` is holding
  you back while your enterprise pool has credits.
- **Upstream 400 about `temperature`** — tick Reasoning model for that model at `/config/models`.
- **Rules ignored** — strategy is AI routing; switch to Rules first, then AI at `/config/strategy`.
- **Model missing from `/models`** — the model policy does not grant it; the page names the grants.
- **Admin page shows "for administrators only"** — your account is not an admin; ask to be added at `/access/admins`.
