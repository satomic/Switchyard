import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { changeOwnPassword } from '../api'
import Modal from './Modal'

/** A built-in user changes their own password from the user menu. Other sessions of the account
 *  end; this one carries on. */
export default function PasswordModal({ onClose }: { onClose: (changed: boolean) => void }) {
  const { t } = useTranslation()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const tooShort = next.length > 0 && next.length < 8
  const mismatch = confirm.length > 0 && next !== confirm
  const ready = Boolean(current) && next.length >= 8 && next === confirm

  const submit = async () => {
    setBusy(true)
    setError('')
    try {
      await changeOwnPassword({ current_password: current, new_password: next })
      onClose(true)
    } catch (e) {
      setError(String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      title={t('password.changeTitle')}
      submitLabel={t('password.changeSubmit')}
      onClose={() => onClose(false)}
      onSubmit={submit}
      busy={busy}
      canSubmit={ready}
      error={error}
    >
      <p className="dialog-message" style={{ marginTop: 0 }}>{t('password.changeLead')}</p>
      <label className="field">
        <span className="field-name">{t('password.current')}</span>
        <input
          type="password"
          name="current_password"
          autoComplete="current-password"
          autoFocus
          value={current}
          onChange={(e) => setCurrent(e.target.value)}
        />
      </label>
      <label className="field">
        <span className="field-name">{t('password.next')}</span>
        <input
          type="password"
          name="new_password"
          autoComplete="new-password"
          value={next}
          onChange={(e) => setNext(e.target.value)}
        />
        {tooShort && <span className="dialog-error">{t('password.tooShort')}</span>}
      </label>
      <label className="field" style={{ marginBottom: 0 }}>
        <span className="field-name">{t('password.confirm')}</span>
        <input
          type="password"
          name="confirm_password"
          autoComplete="new-password"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
        />
        {mismatch && <span className="dialog-error">{t('password.mismatch')}</span>}
      </label>
    </Modal>
  )
}
