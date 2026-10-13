import { useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { isSystemOne, type LayaSettings, type MicrosoftSettings, type OpenAIDecisionSettings, type SystemOneEngine, type TypeSafeSettings } from '../../api'
import { aiRouterActive } from './strategy'
import DecisionPromptPanel from './DecisionPromptPanel'
import RoutingFlow from './RoutingFlow'
import type { SectionProps } from './types'

/** Routing strategy: rule / ai / rule-then-ai, session stickiness, and the AI decision model's
 *  parameters and prompt. */
export default function StrategySection({ cfg, set, notify, goto }: SectionProps) {
  const { t } = useTranslation()
  const providerNames = Object.keys(cfg.providers ?? {})
  const [revealKey, setRevealKey] = useState(false)
  const engine = cfg.ai_router.decision_engine
  /** Jev, Laya and Microsoft-Decision-1 are all System One engines on the same protocol, so one
   *  switch covers them and a second choice picks which. */
  const systemOne = isSystemOne(engine)
  /** Which of them the switch turns back on: the one last selected in this session. */
  const [lastSystemOne, setLastSystemOne] = useState<SystemOneEngine>(
    isSystemOne(engine) ? engine : 'typesafe',
  )
  const typesafe = cfg.ai_router.typesafe ?? {}
  const laya = cfg.ai_router.laya ?? {}
  const microsoft = cfg.ai_router.microsoft ?? {}
  const openai = cfg.ai_router.openai ?? {}
  /** Connections Microsoft-Decision-1 can borrow an address and key from: any but Anthropic's.
   *  A Foundry / Azure OpenAI connection on the same resource is the one that works. */
  const foundryProviders = providerNames.filter((name) => cfg.providers?.[name]?.api_type !== 'anthropic')
  const borrowed = microsoft.provider ? cfg.providers?.[microsoft.provider] : undefined

  const setEngine = (next: SystemOneEngine | undefined) => {
    if (next) setLastSystemOne(next)
    set({ ai_router: { ...cfg.ai_router, decision_engine: next } })
  }

  /** Empty strings are stored as undefined, so config.yaml keeps no empty field and the
   *  backend's defaults (jev-latest, api.typesafe.ai, TYPESAFE_API_KEY; Laya's automatic
   *  checkpoint; the Microsoft-Decision-1 deployment name and the borrowed connection) apply. */
  function patched<T extends object>(current: T, patch: Partial<T>): T | undefined {
    const next = { ...current } as Record<string, unknown>
    for (const [k, v] of Object.entries(patch)) {
      if (v) next[k] = v
      else delete next[k]
    }
    return Object.keys(next).length ? (next as T) : undefined
  }
  const setTypeSafe = (patch: Partial<TypeSafeSettings>) =>
    set({ ai_router: { ...cfg.ai_router, typesafe: patched(typesafe, patch) } })
  const setLaya = (patch: Partial<LayaSettings>) =>
    set({ ai_router: { ...cfg.ai_router, laya: patched(laya, patch) } })
  const setOpenAI = (patch: Partial<OpenAIDecisionSettings>) =>
    set({ ai_router: { ...cfg.ai_router, openai: patched(openai, patch) } })
  const setMicrosoft = (patch: Partial<MicrosoftSettings>) =>
    set({ ai_router: { ...cfg.ai_router, microsoft: patched(microsoft, patch) } })

  /** The rules link inside a choice card's description. The card is a `<label>`, so a click on
   *  the button would also select its radio -- hence the stopPropagation. */
  const rulesLink = (
    <button
      className="btn subtle sm"
      onClick={(e) => {
        e.preventDefault()
        e.stopPropagation()
        goto('rules')
      }}
    />
  )

  return (
    <>
      {/* The picture comes before the picker: what changes when the radio moves is the chain
          below, and seeing it is what makes the three options comparable. */}
      <RoutingFlow cfg={cfg} goto={goto} />

      <div className="panel">
        <div className="panel-head">{t('config.strategy.title')}</div>
        <div className="panel-body">
          <div className="choice-list">
            {/* rule-then-ai is listed first: it is the strategy that needs no trade-off
                explained, since the rules cost nothing and the decision call is only paid for
                a request they did not answer. */}
            <label className={`choice ${cfg.strategy === 'rule-then-ai' ? 'selected' : ''}`}>
              <input
                type="radio"
                checked={cfg.strategy === 'rule-then-ai'}
                onChange={() => set({ strategy: 'rule-then-ai' })}
              />
              <span>
                <span className="choice-title">
                  {t('config.strategy.ruleThenAi.title')}
                  <span className="badge ok">{t('config.strategy.recommended')}</span>
                </span>
                <span className="choice-desc">
                  <Trans
                    i18nKey="config.strategy.ruleThenAi.desc"
                    components={{ rulesLink }}
                  />
                </span>
              </span>
            </label>
            <label className={`choice ${cfg.strategy === 'ai' ? 'selected' : ''}`}>
              <input
                type="radio"
                checked={cfg.strategy === 'ai'}
                onChange={() => set({ strategy: 'ai' })}
              />
              <span>
                <span className="choice-title">{t('config.strategy.ai.title')}</span>
                <span className="choice-desc">{t('config.strategy.ai.desc')}</span>
              </span>
            </label>
            <label className={`choice ${cfg.strategy === 'rule' ? 'selected' : ''}`}>
              <input
                type="radio"
                checked={cfg.strategy === 'rule'}
                onChange={() => set({ strategy: 'rule' })}
              />
              <span>
                <span className="choice-title">{t('config.strategy.rule.title')}</span>
                <span className="choice-desc">
                  <Trans i18nKey="config.strategy.rule.desc" components={{ rulesLink }} />
                </span>
              </span>
            </label>
          </div>
        </div>
      </div>

      <div className="panel">
        <div className="panel-head">{t('config.session.title')}</div>
        <div className="panel-body">
          <label className="check" style={{ marginBottom: 6 }}>
            <input
              type="checkbox"
              checked={cfg.session.sticky}
              onChange={(e) => set({ session: { ...cfg.session, sticky: e.target.checked } })}
            />
            {t('config.session.sticky')}
          </label>
          <p className="panel-note">
            <Trans i18nKey="config.session.stickyNote" components={{ code: <code /> }} />
          </p>
          <div className="row">
            <label className="field" style={{ marginBottom: 0 }}>
              <span className="field-name">
                {t('config.session.ttl')}
                <span className="field-hint">{t('config.session.ttlHint')}</span>
              </span>
              <input
                type="number"
                value={cfg.session.ttl_seconds}
                onChange={(e) => set({ session: { ...cfg.session, ttl_seconds: +e.target.value } })}
              />
            </label>
            <label className="field" style={{ marginBottom: 0 }}>
              <span className="field-name">
                {t('config.session.maxSessions')}
                <span className="field-hint">{t('config.session.maxSessionsHint')}</span>
              </span>
              <input
                type="number"
                value={cfg.session.max_sessions}
                onChange={(e) => set({ session: { ...cfg.session, max_sessions: +e.target.value } })}
              />
            </label>
          </div>
        </div>
      </div>

      <div className="panel">
        <div className="panel-head">
          {t('config.aiRouter.title')}
          {!aiRouterActive(cfg.strategy) && (
            <span className="badge">{t('config.aiRouter.inactive')}</span>
          )}
        </div>
        <div className="panel-body">
          {/* The engine switch. Unticking it returns to the LLM fields below, which were never
              cleared -- and the TypeSafe, Laya and Microsoft settings are kept too, so flipping back and
              forth to compare them costs nothing. */}
          <label className={`choice ${systemOne ? 'selected' : ''}`} style={{ marginBottom: 12 }}>
            <input
              type="checkbox"
              checked={systemOne}
              onChange={(e) => setEngine(e.target.checked ? lastSystemOne : undefined)}
            />
            <span>
              <span className="choice-title">
                {t('config.aiRouter.typesafe.toggle')}
                <span className="badge ok">{t('config.aiRouter.typesafe.badge')}</span>
              </span>
              <span className="choice-desc">
                <Trans
                  i18nKey="config.aiRouter.typesafe.desc"
                  components={{
                    // Not `link`: that is a void HTML element, which Trans renders empty and
                    // leaves the text outside the anchor.
                    jev: <a href="https://typesafe.ai/blog/introducing-system-one-models-and-jev" target="_blank" rel="noreferrer" />,
                    laya: <a href="https://github.com/NandhaKishorM/laya" target="_blank" rel="noreferrer" />,
                    ms: <a href="https://ai.azure.com/catalog/models/Microsoft-Decision-1" target="_blank" rel="noreferrer" />,
                    oa: <a href="https://developers.openai.com/api/docs/guides/decisions" target="_blank" rel="noreferrer" />,
                  }}
                />
              </span>
            </span>
          </label>

          {systemOne ? (
            <>
              <div className="choice-list" style={{ marginBottom: 12 }}>
                <label className={`choice ${engine === 'typesafe' ? 'selected' : ''}`}>
                  <input
                    type="radio"
                    name="system-one-engine"
                    checked={engine === 'typesafe'}
                    onChange={() => setEngine('typesafe')}
                  />
                  <span>
                    <span className="choice-title">{t('config.aiRouter.systemOne.typesafe')}</span>
                    <span className="choice-desc">{t('config.aiRouter.systemOne.typesafeDesc')}</span>
                  </span>
                </label>
                <label className={`choice ${engine === 'laya' ? 'selected' : ''}`}>
                  <input
                    type="radio"
                    name="system-one-engine"
                    checked={engine === 'laya'}
                    onChange={() => setEngine('laya')}
                  />
                  <span>
                    <span className="choice-title">{t('config.aiRouter.systemOne.laya')}</span>
                    <span className="choice-desc">{t('config.aiRouter.systemOne.layaDesc')}</span>
                  </span>
                </label>
                <label className={`choice ${engine === 'microsoft' ? 'selected' : ''}`}>
                  <input
                    type="radio"
                    name="system-one-engine"
                    checked={engine === 'microsoft'}
                    onChange={() => setEngine('microsoft')}
                  />
                  <span>
                    <span className="choice-title">{t('config.aiRouter.systemOne.microsoft')}</span>
                    <span className="choice-desc">{t('config.aiRouter.systemOne.microsoftDesc')}</span>
                  </span>
                </label>
                <label className={`choice ${engine === 'openai' ? 'selected' : ''}`}>
                  <input
                    type="radio"
                    name="system-one-engine"
                    checked={engine === 'openai'}
                    onChange={() => setEngine('openai')}
                  />
                  <span>
                    <span className="choice-title">{t('config.aiRouter.systemOne.openai')}</span>
                    <span className="choice-desc">{t('config.aiRouter.systemOne.openaiDesc')}</span>
                  </span>
                </label>
              </div>

              {engine === 'typesafe' ? (
                <>
                  <label className="field">
                    <span className="field-name">
                      {t('config.aiRouter.typesafe.apiKey')}
                      <span className="field-hint">{t('config.aiRouter.typesafe.apiKeyHint')}</span>
                    </span>
                    <span style={{ display: 'flex', gap: 8 }}>
                      <input
                        type={revealKey ? 'text' : 'password'}
                        className="mono"
                        value={typesafe.api_key ?? ''}
                        placeholder="TYPESAFE_API_KEY"
                        autoComplete="off"
                        onChange={(e) => setTypeSafe({ api_key: e.target.value.trim() })}
                      />
                      <button
                        className="btn ghost sm"
                        style={{ flex: 'none' }}
                        onClick={() => setRevealKey((v) => !v)}
                      >
                        {revealKey ? t('common.hide') : t('common.show')}
                      </button>
                    </span>
                  </label>
                  <div className="row">
                    <label className="field">
                      <span className="field-name">
                        {t('config.aiRouter.typesafe.model')}
                        <span className="field-hint">{t('config.aiRouter.typesafe.modelHint')}</span>
                      </span>
                      <input
                        type="text"
                        className="mono"
                        value={typesafe.model ?? ''}
                        placeholder="jev-latest"
                        onChange={(e) => setTypeSafe({ model: e.target.value.trim() })}
                      />
                    </label>
                    <label className="field">
                      <span className="field-name">
                        {t('config.aiRouter.typesafe.baseUrl')}
                        <span className="field-hint">{t('config.aiRouter.typesafe.baseUrlHint')}</span>
                      </span>
                      <input
                        type="text"
                        className="mono"
                        value={typesafe.base_url ?? ''}
                        placeholder="https://api.typesafe.ai"
                        onChange={(e) => setTypeSafe({ base_url: e.target.value.trim() })}
                      />
                    </label>
                  </div>
                </>
              ) : engine === 'openai' ? (
                <>
                  <label className="field">
                    <span className="field-name">
                      {t('config.aiRouter.openai.apiKey')}
                      <span className="field-hint">{t('config.aiRouter.openai.apiKeyHint')}</span>
                    </span>
                    <span style={{ display: 'flex', gap: 8 }}>
                      <input
                        type={revealKey ? 'text' : 'password'}
                        className="mono"
                        value={openai.api_key ?? ''}
                        placeholder="OPENAI_API_KEY"
                        autoComplete="off"
                        onChange={(e) => setOpenAI({ api_key: e.target.value.trim() })}
                      />
                      <button
                        className="btn ghost sm"
                        style={{ flex: 'none' }}
                        onClick={() => setRevealKey((v) => !v)}
                      >
                        {revealKey ? t('common.hide') : t('common.show')}
                      </button>
                    </span>
                  </label>
                  <div className="row">
                    <label className="field">
                      <span className="field-name">
                        {t('config.aiRouter.openai.model')}
                        <span className="field-hint">{t('config.aiRouter.openai.modelHint')}</span>
                      </span>
                      <input
                        type="text"
                        className="mono"
                        value={openai.model ?? ''}
                        placeholder="gpt-6-luna"
                        onChange={(e) => setOpenAI({ model: e.target.value.trim() })}
                      />
                    </label>
                    <label className="field">
                      <span className="field-name">
                        {t('config.aiRouter.openai.baseUrl')}
                        <span className="field-hint">{t('config.aiRouter.openai.baseUrlHint')}</span>
                      </span>
                      <input
                        type="text"
                        className="mono"
                        value={openai.base_url ?? ''}
                        placeholder="https://api.openai.com/v1"
                        onChange={(e) => setOpenAI({ base_url: e.target.value.trim() })}
                      />
                    </label>
                  </div>
                </>
              ) : engine === 'microsoft' ? (
                <>
                  <label className="field">
                    <span className="field-name">
                      {t('config.aiRouter.microsoft.provider')}
                      <span className="field-hint">{t('config.aiRouter.microsoft.providerHint')}</span>
                    </span>
                    <select
                      value={microsoft.provider ?? ''}
                      onChange={(e) => setMicrosoft({ provider: e.target.value })}
                    >
                      <option value="">{t('config.aiRouter.microsoft.providerNone')}</option>
                      {foundryProviders.map((name) => (
                        <option key={name} value={name}>
                          {name} · {cfg.providers?.[name]?.base_url}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label className="field">
                    <span className="field-name">
                      {t('config.aiRouter.microsoft.baseUrl')}
                      <span className="field-hint">{t('config.aiRouter.microsoft.baseUrlHint')}</span>
                    </span>
                    <input
                      type="text"
                      className="mono"
                      value={microsoft.base_url ?? ''}
                      placeholder={
                        borrowed?.base_url
                          ? t('config.aiRouter.microsoft.fromProvider', { value: borrowed.base_url })
                          : 'https://<resource>.services.ai.azure.com/models'
                      }
                      onChange={(e) => setMicrosoft({ base_url: e.target.value.trim() })}
                    />
                  </label>
                  <div className="row">
                    <label className="field">
                      <span className="field-name">
                        {t('config.aiRouter.microsoft.apiKey')}
                        <span className="field-hint">{t('config.aiRouter.microsoft.apiKeyHint')}</span>
                      </span>
                      <span style={{ display: 'flex', gap: 8 }}>
                        <input
                          type={revealKey ? 'text' : 'password'}
                          className="mono"
                          value={microsoft.api_key ?? ''}
                          placeholder={
                            microsoft.provider
                              ? t('config.aiRouter.microsoft.fromProvider', { value: microsoft.provider })
                              : 'api-key'
                          }
                          autoComplete="off"
                          onChange={(e) => setMicrosoft({ api_key: e.target.value.trim() })}
                        />
                        <button
                          className="btn ghost sm"
                          style={{ flex: 'none' }}
                          onClick={() => setRevealKey((v) => !v)}
                        >
                          {revealKey ? t('common.hide') : t('common.show')}
                        </button>
                      </span>
                    </label>
                    <label className="field">
                      <span className="field-name">
                        {t('config.aiRouter.microsoft.model')}
                        <span className="field-hint">{t('config.aiRouter.microsoft.modelHint')}</span>
                      </span>
                      <input
                        type="text"
                        className="mono"
                        value={microsoft.model ?? ''}
                        placeholder="Microsoft-Decision-1"
                        onChange={(e) => setMicrosoft({ model: e.target.value.trim() })}
                      />
                    </label>
                  </div>
                </>
              ) : (
                <>
                  <label className="field">
                    <span className="field-name">
                      {t('config.aiRouter.laya.baseUrl')}
                      <span className="field-hint">{t('config.aiRouter.laya.baseUrlHint')}</span>
                    </span>
                    <input
                      type="text"
                      className="mono"
                      value={laya.base_url ?? ''}
                      placeholder="http://127.0.0.1:8100"
                      onChange={(e) => setLaya({ base_url: e.target.value.trim() })}
                    />
                  </label>
                  <div className="row">
                    <label className="field">
                      <span className="field-name">
                        {t('config.aiRouter.laya.apiKey')}
                        <span className="field-hint">{t('config.aiRouter.laya.apiKeyHint')}</span>
                      </span>
                      <span style={{ display: 'flex', gap: 8 }}>
                        <input
                          type={revealKey ? 'text' : 'password'}
                          className="mono"
                          value={laya.api_key ?? ''}
                          placeholder="LAYA_API_KEY"
                          autoComplete="off"
                          onChange={(e) => setLaya({ api_key: e.target.value.trim() })}
                        />
                        <button
                          className="btn ghost sm"
                          style={{ flex: 'none' }}
                          onClick={() => setRevealKey((v) => !v)}
                        >
                          {revealKey ? t('common.hide') : t('common.show')}
                        </button>
                      </span>
                    </label>
                    <label className="field">
                      <span className="field-name">
                        {t('config.aiRouter.laya.model')}
                        <span className="field-hint">{t('config.aiRouter.laya.modelHint')}</span>
                      </span>
                      <select value={laya.model ?? ''} onChange={(e) => setLaya({ model: e.target.value })}>
                        <option value="">{t('config.aiRouter.laya.modelAuto')}</option>
                        <option value="english">english</option>
                        <option value="multilingual">multilingual</option>
                        <option value="typed-decisions">typed-decisions</option>
                      </select>
                    </label>
                  </div>
                </>
              )}
            </>
          ) : (
            <>
            <div className="row">
              <label className="field">
                <span className="field-name">
                  {t('config.aiRouter.decisionModel')}
                  <span className="field-hint">{t('config.aiRouter.decisionModelHint')}</span>
                </span>
                <input
                  type="text"
                  className="mono"
                  value={cfg.ai_router.decision_model}
                  onChange={(e) => set({ ai_router: { ...cfg.ai_router, decision_model: e.target.value } })}
                />
              </label>
              <label className="field">
                <span className="field-name">{t('config.aiRouter.decisionProvider')}</span>
                <select
                  value={cfg.ai_router.decision_provider ?? ''}
                  onChange={(e) =>
                    set({
                      ai_router: {
                        ...cfg.ai_router,
                        decision_provider: e.target.value || undefined,
                      },
                    })
                  }
                >
                  <option value="">
                    {t('config.models.followDefault', { name: cfg.default_provider })}
                  </option>
                  {providerNames.map((n) => (
                    <option key={n} value={n}>{n}</option>
                  ))}
                </select>
              </label>
            </div>
            {/* Only a reasoning model takes reasoning_effort; the router sends it only then. */}
            {cfg.models?.[cfg.ai_router.decision_model]?.reasoning && (
              <label className="field">
                <span className="field-name">
                  {t('config.aiRouter.reasoningEffort')}
                  <span className="field-hint">{t('config.aiRouter.reasoningEffortHint')}</span>
                </span>
                <select
                  value={cfg.ai_router.decision_reasoning_effort ?? ''}
                  onChange={(e) =>
                    set({ ai_router: { ...cfg.ai_router, decision_reasoning_effort: e.target.value || undefined } })
                  }
                >
                  <option value="">{t('config.aiRouter.reasoningEffortDefault')}</option>
                  {['none', 'low', 'medium', 'high', 'xhigh'].map((v) => (
                    <option key={v} value={v}>{v}</option>
                  ))}
                </select>
              </label>
            )}
            </>
          )}
          <div className="row">
            <label className="field" style={{ marginBottom: 0 }}>
              <span className="field-name">
                {t('config.aiRouter.timeout')}
                <span className="field-hint">{t('config.aiRouter.timeoutHint')}</span>
              </span>
              <input
                type="number"
                value={cfg.ai_router.timeout_seconds}
                onChange={(e) => set({ ai_router: { ...cfg.ai_router, timeout_seconds: +e.target.value } })}
              />
            </label>
            <label className="field" style={{ marginBottom: 0 }}>
              <span className="field-name">
                {t('config.aiRouter.maxPromptChars')}
                <span className="field-hint">{t('config.aiRouter.maxPromptCharsHint')}</span>
              </span>
              <input
                type="number"
                value={cfg.ai_router.max_prompt_chars}
                onChange={(e) => set({ ai_router: { ...cfg.ai_router, max_prompt_chars: +e.target.value } })}
              />
            </label>
          </div>
        </div>
      </div>

      <DecisionPromptPanel cfg={cfg} set={set} notify={notify} goto={goto} />
    </>
  )
}
