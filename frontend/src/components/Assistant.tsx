import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useLocation } from 'react-router-dom'
import { askAssistant, createKey, getKeys, type ApiKey, type AssistantTurn, type SessionUser } from '../api'
import Markdown from '../assistant/Markdown'
import { systemPrompt } from '../assistant/prompt'
import { assistantRoutes } from '../assistant/routes'

/** How many earlier turns go with each question: enough for "and the other one?" to make sense,
 *  bounded so a long chat does not resend everything. */
const HISTORY = 10

interface Turn extends AssistantTurn {
  model?: string
  traceId?: string
  error?: boolean
}

// Per user and per browser, like the theme: which key the assistant uses, and the conversation.
const keyStore = (login: string) => `mr_assistant_key:${login}`
const chatStore = (login: string) => `mr_assistant_chat:${login}`

function load<T>(storage: Storage, name: string, fallback: T): T {
  try {
    const raw = storage.getItem(name)
    return raw ? (JSON.parse(raw) as T) : fallback
  } catch {
    return fallback
  }
}

function save(storage: Storage, name: string, value: unknown) {
  try {
    storage.setItem(name, JSON.stringify(value))
  } catch {
    /* private mode or full storage: the assistant still works, it just forgets */
  }
}

/** A key the assistant can use: the user's own, enabled, and readable (keys made before plaintext
 *  was stored cannot be read back, so they cannot be sent). */
const usable = (k: ApiKey) => !k.disabled && Boolean(k.key)

/** The floating help assistant: a button in the bottom-right corner of every console page, and a
 *  chat that answers questions about Model Router with links straight to the right page.
 *
 *  It asks the router itself, with one of the user's own API keys, so its questions are routed,
 *  policed and traced like any other request. Nothing is configured up front: the first time it is
 *  opened it asks for a key -- or creates one -- and remembers the choice in this browser. */
export default function Assistant({ user }: { user: SessionUser }) {
  const { t, i18n } = useTranslation()
  const location = useLocation()
  const [open, setOpen] = useState(false)
  const [keys, setKeys] = useState<ApiKey[] | null>(null)
  const [keyId, setKeyId] = useState<string>(() => load(localStorage, keyStore(user.login), ''))
  const [choosing, setChoosing] = useState(false)
  const [turns, setTurns] = useState<Turn[]>(() => load(sessionStorage, chatStore(user.login), []))
  const [conversation, setConversation] = useState(() => crypto.randomUUID())
  const [input, setInput] = useState('')
  const [busy, setBusy] = useState(false)
  const [setupError, setSetupError] = useState('')
  const abort = useRef<AbortController | null>(null)
  const scroller = useRef<HTMLDivElement>(null)

  const routes = useMemo(() => assistantRoutes(t, user.is_admin), [t, user.is_admin])
  const key = keys?.find((k) => k.id === keyId && usable(k))

  useEffect(() => save(sessionStorage, chatStore(user.login), turns), [turns, user.login])
  useEffect(() => {
    scroller.current?.scrollTo({ top: scroller.current.scrollHeight })
  }, [turns, open])

  const loadKeys = () =>
    getKeys()
      .then(setKeys)
      .catch((e) => setSetupError(String(e)))

  useEffect(() => {
    if (open && keys === null) void loadKeys()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  const choose = (id: string) => {
    setKeyId(id)
    save(localStorage, keyStore(user.login), id)
    setChoosing(false)
    setSetupError('')
  }

  const create = async () => {
    setSetupError('')
    try {
      const created = await createKey('console-assistant')
      setKeys((list) => [created, ...(list ?? [])])
      choose(created.id)
    } catch (e) {
      // Most often the key policy: this account may not create keys. Said as the server said it,
      // with the page that explains why.
      setSetupError(String(e))
    }
  }

  const send = async (text: string) => {
    const question = text.trim()
    if (!question || !key?.key || busy) return
    const history: Turn[] = [...turns, { role: 'user', content: question }]
    setTurns([...history, { role: 'assistant', content: '' }])
    setInput('')
    setBusy(true)
    abort.current = new AbortController()
    let answer = ''
    try {
      const meta = await askAssistant({
        apiKey: key.key,
        system: systemPrompt({
          routes,
          login: user.login,
          isAdmin: user.is_admin,
          currentPath: location.pathname,
          language: i18n.language,
        }),
        history: history
          .filter((x) => !x.error && x.content)
          .slice(-HISTORY)
          .map(({ role, content }) => ({ role, content })),
        conversationId: conversation,
        signal: abort.current.signal,
        onDelta: (delta) => {
          answer += delta
          setTurns([...history, { role: 'assistant', content: answer }])
        },
      })
      setTurns([...history, { role: 'assistant', content: answer, model: meta.model, traceId: meta.traceId }])
    } catch (e) {
      if ((e as Error).name === 'AbortError') {
        setTurns([...history, { role: 'assistant', content: answer || t('assistant.stopped') }])
      } else {
        setTurns([...history, { role: 'assistant', content: String(e), error: true }])
      }
    } finally {
      setBusy(false)
      abort.current = null
    }
  }

  const clear = () => {
    abort.current?.abort()
    setTurns([])
    setConversation(crypto.randomUUID())
  }

  const readyKeys = (keys ?? []).filter(usable)
  const needSetup = keys !== null && (!key || choosing)
  const suggestions = [t('assistant.suggest.copilot'), t('assistant.suggest.models'), t('assistant.suggest.why')]

  return (
    <>
      {!open && (
        <button className="assistant-fab" onClick={() => setOpen(true)} title={t('assistant.open')} aria-label={t('assistant.open')}>
          <span aria-hidden>✦</span>
        </button>
      )}
      {open && (
        <section className="assistant-panel" role="dialog" aria-label={t('assistant.title')}>
          <header className="assistant-head">
            <span className="assistant-title">✦ {t('assistant.title')}</span>
            {key && !choosing && (
              <button className="btn subtle sm" onClick={() => setChoosing(true)} title={t('assistant.changeKey')}>
                {key.name}
              </button>
            )}
            <span className="spacer" />
            {turns.length > 0 && (
              <button className="btn ghost sm" onClick={clear}>
                {t('assistant.clear')}
              </button>
            )}
            <button className="icon-btn" onClick={() => setOpen(false)} aria-label={t('common.close')} title={t('common.close')}>
              ✕
            </button>
          </header>

          <div className="assistant-body" ref={scroller}>
            {keys === null && !setupError && <div className="empty">{t('common.loading')}</div>}
            {keys === null && setupError && <div className="toast error">{setupError}</div>}

            {needSetup && (
              <div className="assistant-setup">
                <p className="assistant-heading">{t('assistant.setup.title')}</p>
                <p className="dim">{t('assistant.setup.lead')}</p>
                {readyKeys.length > 0 && (
                  <>
                    <div className="field-name" style={{ marginTop: 10 }}>{t('assistant.setup.pick')}</div>
                    <div className="assistant-keys">
                      {readyKeys.map((k) => (
                        <button key={k.id} className={`assistant-key ${k.id === keyId ? 'active' : ''}`} onClick={() => choose(k.id)}>
                          <span>{k.name}</span>
                          <span className="mono dim">{k.prefix}…</span>
                        </button>
                      ))}
                    </div>
                  </>
                )}
                <button className="btn sm" style={{ marginTop: 12 }} onClick={create}>
                  {readyKeys.length ? t('assistant.setup.createAnother') : t('assistant.setup.create')}
                </button>
                {setupError && (
                  <div className="toast error" style={{ marginTop: 10 }}>
                    {setupError}{' '}
                    <Link to="/keys" className="assistant-link">{t('nav.keys.label')}</Link>
                  </div>
                )}
                <p className="faint" style={{ marginTop: 12 }}>{t('assistant.setup.note')}</p>
                {choosing && key && (
                  <button className="btn-link" onClick={() => setChoosing(false)}>{t('dialog.cancel')}</button>
                )}
              </div>
            )}

            {!needSetup && keys !== null && turns.length === 0 && (
              <div className="assistant-welcome">
                <p>{t('assistant.welcome')}</p>
                {suggestions.map((s) => (
                  <button key={s} className="assistant-suggestion" onClick={() => send(s)}>
                    {s}
                  </button>
                ))}
              </div>
            )}

            {!needSetup &&
              turns.map((turn, i) =>
                turn.role === 'user' ? (
                  <div key={i} className="assistant-msg user">{turn.content}</div>
                ) : (
                  <div key={i} className={`assistant-msg bot ${turn.error ? 'error' : ''}`}>
                    {turn.content ? (
                      turn.error ? turn.content : <Markdown text={turn.content} routes={routes} />
                    ) : (
                      <span className="dim">{t('assistant.thinking')}</span>
                    )}
                    {turn.model && (
                      <div className="assistant-meta">
                        {t('assistant.via', { model: turn.model })}
                        {turn.traceId && (
                          <>
                            {' · '}
                            <Link to={`/traces/${turn.traceId}`} className="assistant-link">{t('assistant.trace')}</Link>
                          </>
                        )}
                      </div>
                    )}
                  </div>
                ),
              )}
          </div>

          {!needSetup && keys !== null && (
            <form
              className="assistant-input"
              onSubmit={(e) => {
                e.preventDefault()
                void send(input)
              }}
            >
              <textarea
                value={input}
                rows={2}
                placeholder={t('assistant.placeholder')}
                onChange={(e) => setInput(e.target.value)}
                onKeyDown={(e) => {
                  // Enter sends, Shift+Enter breaks the line; an IME composing Chinese must not send.
                  if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
                    e.preventDefault()
                    void send(input)
                  }
                }}
              />
              {busy ? (
                <button type="button" className="btn ghost sm" onClick={() => abort.current?.abort()}>
                  {t('assistant.stop')}
                </button>
              ) : (
                <button type="submit" className="btn sm" disabled={!input.trim()}>
                  {t('assistant.send')}
                </button>
              )}
            </form>
          )}
        </section>
      )}
    </>
  )
}
