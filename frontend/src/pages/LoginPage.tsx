import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { githubLoginUrl, localLogin, RETURN_KEY, type AuthStatus } from '../api'
import LocalePicker from '../components/LocalePicker'

/** Signed-out state: an Azure-style sign-in card carrying whichever doors are actually open --
 *  GitHub OAuth when it is configured, the username/password form when the local super
 *  administrator is enabled or built-in users exist. Both may be present; with only one, the
 *  other's markup is not rendered at all. */
export default function LoginPage({
  error,
  status,
  onSignedIn,
}: {
  error?: string | null
  status: AuthStatus
  onSignedIn: () => void
}) {
  const { t } = useTranslation()
  // Older servers report only the local administrator; built-in users arrived with 2.5.0.
  const localOn = status.local_login_enabled ?? status.local_admin_enabled
  const builtin = Boolean(status.builtin_users)
  // Prefilled with the administrator's name only when that is the one account the form serves:
  // with built-in users present, a prefilled "admin" would be wrong for nearly everyone.
  const [username, setUsername] = useState(builtin ? '' : status.local_admin_username || 'admin')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [localError, setLocalError] = useState('')

  // The local form starts collapsed behind a button, so GitHub is the one obvious way in. When
  // OAuth is *not* configured the local account is the only door there is, so hiding it behind an
  // extra click would be a dead end -- it opens expanded in that case.
  const [localOpen, setLocalOpen] = useState(!status.configured)
  const passwordRef = useRef<HTMLInputElement>(null)

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setLocalError('')
    try {
      await localLogin(username, password)
      setPassword('')
      // App re-reads /v1/auth/status, which is what decides between the console and the
      // forced change-password form.
      onSignedIn()
    } catch (err) {
      // One message for a wrong username and a wrong password alike -- the server does not
      // distinguish them and neither should the UI. A 403 is different: the server only sends
      // it once the password checked out, for a built-in account that has been disabled.
      setLocalError(
        String(err).includes('HTTP 403') ? t('login.local.disabled') : t('login.local.failed'),
      )
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="auth-wrap">
      <div className="auth-card">
        <div className="auth-lang"><LocalePicker compact /></div>
        <div className="logo">◆</div>
        <h1>{t('login.title')}</h1>
        <p className="lead">{status.configured ? t('login.lead') : t('login.leadLocal')}</p>
        {error && <div className="toast error">{t('login.failed', { error })}</div>}
        {status.configured && (
          <a
            className="btn-github"
            href={githubLoginUrl}
            onClick={() => {
              // The OAuth callback can only redirect to a fixed path, so remember where the
              // user actually wanted to go and let App restore it once the session lands.
              const here = window.location.pathname + window.location.search
              if (here !== '/') sessionStorage.setItem(RETURN_KEY, here)
            }}
          >
            <svg width="18" height="18" viewBox="0 0 16 16" fill="currentColor" aria-hidden>
              <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.07-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82a7.42 7.42 0 0 1 2-.27c.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A7.995 7.995 0 0 0 16 8c0-4.42-3.58-8-8-8Z" />
            </svg>
            {t('login.signInWithGitHub')}
          </a>
        )}
        {status.configured && localOn && (
          <div className="auth-or"><span>{t('login.local.or')}</span></div>
        )}
        {localOn && !localOpen && (
          <button
            className="btn-alt"
            type="button"
            aria-expanded={false}
            onClick={() => {
              setLocalOpen(true)
              // With the username prefilled the password is the field that wants the caret; with
              // built-in users the username is empty and autoFocus on it takes over. The rAF
              // waits for the input to exist before focusing it.
              if (!builtin) requestAnimationFrame(() => passwordRef.current?.focus())
            }}
          >
            {t('login.local.reveal')}
          </button>
        )}
        {localOn && localOpen && (
          <form className="auth-form" onSubmit={submit}>
            <div className="field-name">{t('login.local.username')}</div>
            <input
              type="text"
              name="username"
              autoComplete="username"
              autoFocus={builtin}
              value={username}
              onChange={(e) => setUsername(e.target.value)}
            />
            <div className="field-name" style={{ marginTop: 8 }}>{t('login.local.password')}</div>
            <input
              ref={passwordRef}
              name="password"
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
            {localError && <div className="toast error" style={{ marginTop: 10 }}>{localError}</div>}
            <button className="btn primary" type="submit" disabled={busy || !username || !password}>
              {busy ? t('common.loading') : t('login.local.submit')}
            </button>
            {/* Collapsing again is only offered when there is something to go back to. */}
            {status.configured && (
              <button
                className="btn-link"
                type="button"
                style={{ marginTop: 10 }}
                onClick={() => { setLocalOpen(false); setPassword(''); setLocalError('') }}
              >
                {t('login.local.hide')}
              </button>
            )}
            <p className="faint" style={{ marginTop: 10 }}>
              {builtin ? t('login.local.hintBuiltin') : t('login.local.hint')}
            </p>
          </form>
        )}
      </div>
    </div>
  )
}
