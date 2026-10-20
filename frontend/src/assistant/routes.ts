import type { TFunction } from 'i18next'

/** One console page the assistant may link to. */
export interface AssistantRoute {
  path: string
  /** What the reader sees in the menu, in their language: "Access control › Key policy". */
  label: string
  /** For the model: what the page is for, in English (the knowledge base's language). */
  about: string
  admin: boolean
}

/** Every page and sub-page of the console, with the translation keys of its menu labels.
 *
 *  Kept next to the assistant rather than derived from App's route table because a route there is
 *  `/config/:section` -- the sub-pages only exist as section lists inside each page -- and the
 *  assistant needs every concrete address. The list is checked against the real routes by
 *  isAssistantPath, which is what the link renderer trusts. */
const ROUTES: { path: string; labels: string[]; about: string; admin?: boolean }[] = [
  { path: '/usage', labels: ['nav.usage.label'], about: 'request counts, tokens, latency; administrators see every user' },
  { path: '/models', labels: ['nav.models.label'], about: "the models the signed-in user may call, and which grant made each available" },
  { path: '/keys', labels: ['nav.keys.label'], about: 'create, scope, disable and delete API keys; usage examples (Chat Completions, Responses incl. Copilot CLI, Anthropic)' },
  { path: '/traces', labels: ['nav.traces.label'], about: 'full-chain records of every request: routing decision, backend call, response' },
  { path: '/playground', labels: ['nav.playground.label'], about: 'send a test request with an API key and see the routing result' },
  { path: '/topology', labels: ['nav.topology.label'], about: 'graph of identities, access policies and model bindings', admin: true },
  { path: '/config/providers', labels: ['nav.config.label', 'config.section.providers.label'], about: 'backend connections (Azure OpenAI / Foundry, OpenAI-compatible, Anthropic)', admin: true },
  { path: '/config/models', labels: ['nav.config.label', 'config.section.models.label'], about: 'the model catalog: models, descriptions, default model, reasoning flag', admin: true },
  { path: '/config/strategy', labels: ['nav.config.label', 'config.section.strategy.label'], about: 'routing strategy, AI decision model/engine, decision prompt, stickiness', admin: true },
  { path: '/config/rules', labels: ['nav.config.label', 'config.section.rules.label'], about: 'keyword and length rules', admin: true },
  { path: '/policy', labels: ['nav.policy.label'], about: 'model groups and which users, user groups, teams and organizations get them', admin: true },
  { path: '/access/admins', labels: ['nav.access.label', 'access.section.admins.label'], about: 'administrator GitHub logins, who may sign in', admin: true },
  { path: '/access/oauth', labels: ['nav.access.label', 'access.section.oauth.label'], about: 'the GitHub OAuth app', admin: true },
  { path: '/access/local', labels: ['nav.access.label', 'access.section.local.label'], about: 'the local administrator account and its password', admin: true },
  { path: '/access/policy', labels: ['nav.access.label', 'access.section.policy.label'], about: 'key policy: who may create API keys (user groups, enterprises, teams, organizations); GitHub token and cache', admin: true },
  { path: '/access/keyscope', labels: ['nav.access.label', 'access.section.keyscope.label'], about: 'who may narrow an API key to specific models or connection types', admin: true },
  { path: '/users/users', labels: ['nav.users.label', 'users.section.users.label'], about: 'built-in user accounts (username/password, no GitHub)', admin: true },
  { path: '/users/groups', labels: ['nav.users.label', 'users.section.groups.label'], about: 'user groups of built-in users and GitHub accounts', admin: true },
  { path: '/credits', labels: ['nav.credits.label'], about: "Copilot's AI-credit pool, and the gate that holds BYOK back while credits remain", admin: true },
]

/** The pages this user can open, labelled in their language. */
export function assistantRoutes(t: TFunction, isAdmin: boolean): AssistantRoute[] {
  return ROUTES.filter((r) => isAdmin || !r.admin).map((r) => ({
    path: r.path,
    label: r.labels.map((key) => t(key)).join(' › '),
    about: r.about,
    admin: Boolean(r.admin),
  }))
}

/** Whether a link the model wrote points at a real page: one of the routes above, or a trace by id.
 *  Anything else is shown as text rather than as a link that lands on "not found". */
export function isAssistantPath(path: string, routes: AssistantRoute[]): boolean {
  const bare = path.split(/[?#]/)[0].replace(/\/+$/, '') || '/'
  return routes.some((r) => r.path === bare) || /^\/traces\/[A-Za-z0-9_-]+$/.test(bare)
}
