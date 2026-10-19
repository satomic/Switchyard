import { useEffect, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

/** A form in a modal, drawn with the same panel, scrim and keyboard rules as the confirm/prompt
 *  dialogs (components/Dialog.tsx). Those take one text field; the user and group editors need a
 *  whole form, so this renders arbitrary children and leaves submission to the caller. */
export default function Modal({
  title,
  children,
  onClose,
  onSubmit,
  submitLabel,
  busy,
  canSubmit = true,
  error,
  wide,
}: {
  title: string
  children: ReactNode
  onClose: () => void
  onSubmit: () => void
  submitLabel: string
  busy?: boolean
  canSubmit?: boolean
  /** Shown above the buttons, so a server refusal is read where the form is. */
  error?: string
  wide?: boolean
}) {
  const { t } = useTranslation()

  // Escape cancels, captured so a page-level handler cannot swallow it -- same as Dialog.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      e.stopPropagation()
      onClose()
    }
    document.addEventListener('keydown', onKey, true)
    return () => document.removeEventListener('keydown', onKey, true)
  }, [onClose])

  useEffect(() => {
    const previous = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.body.style.overflow = previous
    }
  }, [])

  return (
    <div
      className="dialog-overlay"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget && !busy) onClose()
      }}
    >
      <form
        className="dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="modal-title"
        style={{ maxWidth: wide ? 640 : 480 }}
        onSubmit={(e) => {
          e.preventDefault()
          if (canSubmit && !busy) onSubmit()
        }}
      >
        <div className="dialog-head">
          <h2 id="modal-title">{title}</h2>
        </div>
        <div className="dialog-body">{children}</div>
        {error && (
          <div className="toast error" style={{ margin: '8px 24px 0' }}>
            {error}
          </div>
        )}
        <div className="dialog-foot">
          <button type="button" className="btn ghost" onClick={onClose} disabled={busy}>
            {t('dialog.cancel')}
          </button>
          <button type="submit" className="btn" disabled={!canSubmit || busy}>
            {busy ? t('common.saving') : submitLabel}
          </button>
        </div>
      </form>
    </div>
  )
}
