import { useMemo, useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import {
  createDirectoryUser,
  deleteDirectoryUser,
  updateDirectoryUser,
  type DirectoryUser,
} from '../../api'
import { useDialogs } from '../../components/Dialog'
import Modal from '../../components/Modal'
import { formatDateTime } from '../../i18n/format'
import type { UsersSectionProps } from './types'

/** The password rule the server enforces, checked here only to spare a round trip. */
const MIN_PASSWORD = 8

type Editing = { mode: 'create' } | { mode: 'edit'; user: DirectoryUser } | { mode: 'reset'; user: DirectoryUser }

/** Built-in accounts: create, edit, disable, reset a password, delete. */
export default function UsersSection({ data, me, reload, notify }: UsersSectionProps) {
  const { t } = useTranslation()
  const dialogs = useDialogs()
  const [editing, setEditing] = useState<Editing | null>(null)
  const [query, setQuery] = useState('')

  const needle = query.trim().toLowerCase()
  const rows = useMemo(
    () =>
      data.users.filter(
        (u) =>
          !needle ||
          u.username.toLowerCase().includes(needle) ||
          u.display_name.toLowerCase().includes(needle) ||
          u.email.toLowerCase().includes(needle) ||
          u.groups.some((g) => g.toLowerCase().includes(needle)),
      ),
    [data.users, needle],
  )

  // The signed-in administrator's own row: the server refuses self-demotion, self-disable and
  // self-delete, and the buttons say so up front rather than after a 409.
  const isMe = (u: DirectoryUser) => Boolean(me.local_user) && me.login.toLowerCase() === u.username.toLowerCase()

  const run = async (action: () => Promise<unknown>, ok: string) => {
    try {
      await action()
      notify('ok', ok)
      await reload()
    } catch (e) {
      notify('error', String(e))
    }
  }

  const toggleDisabled = async (u: DirectoryUser) => {
    if (
      !u.disabled &&
      !(await dialogs.confirm({
        title: t('users.users.disableTitle', { name: u.username }),
        message: t('users.users.disableMessage', { count: u.key_count }),
        confirmLabel: t('users.users.disable'),
        danger: true,
      }))
    )
      return
    await run(
      () => updateDirectoryUser(u.username, { disabled: !u.disabled }),
      t(u.disabled ? 'users.users.enabledToast' : 'users.users.disabledToast', { name: u.username }),
    )
  }

  const remove = async (u: DirectoryUser) => {
    if (
      !(await dialogs.confirm({
        title: t('users.users.deleteTitle', { name: u.username }),
        message: (
          <Trans
            i18nKey="users.users.deleteMessage"
            values={{ name: u.username, count: u.key_count }}
            components={{ strong: <strong /> }}
          />
        ),
        confirmLabel: t('common.delete'),
        danger: true,
      }))
    )
      return
    await run(() => deleteDirectoryUser(u.username), t('users.users.deletedToast', { name: u.username }))
  }

  return (
    <>
      <div className="panel">
        <div className="panel-head">
          {t('users.users.title')}
          <span className="badge">{t('users.users.count', { count: data.users.length })}</span>
          <span className="spacer" />
          {data.users.length > 8 && (
            <input
              type="text"
              className="head-search"
              value={query}
              placeholder={t('users.users.search')}
              onChange={(e) => setQuery(e.target.value)}
            />
          )}
          <button className="btn sm" onClick={() => setEditing({ mode: 'create' })}>
            {t('users.users.new')}
          </button>
        </div>
        <div className="panel-body">
          <p className="panel-note" style={{ marginTop: 0 }}>
            <Trans i18nKey="users.users.lead" components={{ strong: <strong />, code: <code /> }} />
          </p>
          {!data.users.length ? (
            <div className="empty">{t('users.users.none')}</div>
          ) : !rows.length ? (
            <div className="empty">{t('users.noMatch', { query: query.trim() })}</div>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>{t('users.users.table.user')}</th>
                  <th>{t('users.users.table.role')}</th>
                  <th>{t('users.users.table.status')}</th>
                  <th>{t('users.users.table.groups')}</th>
                  <th>{t('users.users.table.keys')}</th>
                  <th>{t('users.users.table.lastSeen')}</th>
                  <th style={{ textAlign: 'right' }}>{t('common.actions')}</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((u) => (
                  <tr key={u.username} data-username={u.username}>
                    <td>
                      <div className="mono">{u.username}</div>
                      {(u.display_name || u.email) && (
                        <div className="dim" style={{ fontSize: 12 }}>
                          {[u.display_name, u.email].filter(Boolean).join(' · ')}
                        </div>
                      )}
                    </td>
                    <td>
                      {u.is_admin ? (
                        <span className="badge admin">{t('shell.roleAdmin')}</span>
                      ) : (
                        <span className="badge">{t('shell.roleUser')}</span>
                      )}
                    </td>
                    <td>
                      {u.disabled ? (
                        <span className="badge error">{t('users.users.statusDisabled')}</span>
                      ) : (
                        <span className="badge ok">{t('users.users.statusActive')}</span>
                      )}
                      {u.must_change_password && !u.disabled && (
                        <span className="badge warn" style={{ marginLeft: 4 }} title={t('users.users.mustChangeHint')}>
                          {t('users.users.mustChange')}
                        </span>
                      )}
                    </td>
                    <td>
                      {u.groups.length ? (
                        u.groups.map((g) => (
                          <span key={g} className="badge" style={{ marginRight: 4 }}>
                            {g}
                          </span>
                        ))
                      ) : (
                        <span className="dim">—</span>
                      )}
                    </td>
                    <td className="mono">{u.key_count}</td>
                    <td className="dim">
                      {u.last_seen ? formatDateTime(u.last_seen * 1000) : t('users.users.neverSignedIn')}
                    </td>
                    <td>
                      {/* Wraps rather than clips: four actions do not fit beside six columns on a
                          laptop-width screen. */}
                      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4, justifyContent: 'flex-end' }}>
                        <button className="btn ghost sm" onClick={() => setEditing({ mode: 'edit', user: u })}>
                          {t('users.users.edit')}
                        </button>
                        <button className="btn ghost sm" onClick={() => setEditing({ mode: 'reset', user: u })}>
                          {t('users.users.resetPassword')}
                        </button>
                        <button
                          className="btn ghost sm"
                          disabled={isMe(u)}
                          title={isMe(u) ? t('users.users.notYourself') : undefined}
                          onClick={() => toggleDisabled(u)}
                        >
                          {u.disabled ? t('users.users.enable') : t('users.users.disable')}
                        </button>
                        <button
                          className="btn danger sm"
                          disabled={isMe(u)}
                          title={isMe(u) ? t('users.users.notYourself') : undefined}
                          onClick={() => remove(u)}
                        >
                          {t('common.delete')}
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>

      <div className="panel">
        <div className="panel-head">{t('users.users.howTitle')}</div>
        <div className="panel-body">
          <ul className="panel-note" style={{ margin: 0 }}>
            <li><Trans i18nKey="users.users.howSignIn" components={{ strong: <strong /> }} /></li>
            <li><Trans i18nKey="users.users.howPolicies" components={{ strong: <strong /> }} /></li>
            <li><Trans i18nKey="users.users.howNames" components={{ strong: <strong /> }} /></li>
            <li><Trans i18nKey="users.users.howDisable" components={{ strong: <strong /> }} /></li>
          </ul>
        </div>
      </div>

      {editing?.mode === 'create' && (
        <UserForm
          groups={data.groups.map((g) => g.name)}
          onClose={() => setEditing(null)}
          onDone={async (name) => {
            setEditing(null)
            notify('ok', t('users.users.createdToast', { name }))
            await reload()
          }}
        />
      )}
      {editing?.mode === 'edit' && (
        <UserForm
          user={editing.user}
          self={isMe(editing.user)}
          groups={data.groups.map((g) => g.name)}
          onClose={() => setEditing(null)}
          onDone={async (name) => {
            setEditing(null)
            notify('ok', t('users.users.savedToast', { name }))
            await reload()
          }}
        />
      )}
      {editing?.mode === 'reset' && (
        <ResetPassword
          user={editing.user}
          onClose={() => setEditing(null)}
          onDone={async (name) => {
            setEditing(null)
            notify('ok', t('users.users.resetToast', { name }))
            await reload()
          }}
        />
      )}
    </>
  )
}

/** Create a user, or edit one. The password is asked for on create only; an existing account's
 *  password goes through ResetPassword, which says what a reset does to its sessions. */
function UserForm({
  user,
  self,
  groups,
  onClose,
  onDone,
}: {
  user?: DirectoryUser
  self?: boolean
  groups: string[]
  onClose: () => void
  onDone: (name: string) => void
}) {
  const { t } = useTranslation()
  const creating = !user
  const [username, setUsername] = useState(user?.username ?? '')
  const [displayName, setDisplayName] = useState(user?.display_name ?? '')
  const [email, setEmail] = useState(user?.email ?? '')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [isAdmin, setIsAdmin] = useState(user?.is_admin ?? false)
  const [mustChange, setMustChange] = useState(user?.must_change_password ?? true)
  const [chosen, setChosen] = useState<string[]>(user?.groups ?? [])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const usernameOk = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(username)
  const tooShort = creating && password.length > 0 && password.length < MIN_PASSWORD
  const mismatch = creating && confirm.length > 0 && password !== confirm
  const ready = creating ? usernameOk && password.length >= MIN_PASSWORD && password === confirm : true

  const submit = async () => {
    setBusy(true)
    setError('')
    try {
      if (creating) {
        await createDirectoryUser({
          username,
          display_name: displayName,
          email,
          password,
          is_admin: isAdmin,
          must_change_password: mustChange,
          groups: chosen,
        })
      } else {
        await updateDirectoryUser(user.username, {
          display_name: displayName,
          email,
          is_admin: isAdmin,
          must_change_password: mustChange,
          groups: chosen,
        })
      }
      onDone(username)
    } catch (e) {
      setError(String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      title={creating ? t('users.form.createTitle') : t('users.form.editTitle', { name: user.username })}
      submitLabel={creating ? t('users.form.create') : t('users.form.save')}
      onClose={onClose}
      onSubmit={submit}
      busy={busy}
      canSubmit={ready}
      error={error}
    >
      <label className="field">
        <span className="field-name">
          {t('users.form.username')}
          <span className="field-hint">{creating ? t('users.form.usernameHint') : t('users.form.usernameFixed')}</span>
        </span>
        <input
          type="text"
          className="mono"
          name="username"
          autoComplete="off"
          value={username}
          disabled={!creating}
          autoFocus={creating}
          onChange={(e) => setUsername(e.target.value.trim())}
        />
        {creating && username && !usernameOk && (
          <span className="dialog-error">{t('users.form.usernameInvalid')}</span>
        )}
      </label>
      <label className="field">
        <span className="field-name">{t('users.form.displayName')}</span>
        <input type="text" name="display_name" value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
      </label>
      <label className="field">
        <span className="field-name">
          {t('users.form.email')}
          <span className="field-hint">{t('users.form.emailHint')}</span>
        </span>
        <input type="text" inputMode="email" name="email" value={email} onChange={(e) => setEmail(e.target.value)} />
      </label>
      {creating && (
        <>
          <label className="field">
            <span className="field-name">
              {t('users.form.password')}
              <span className="field-hint">{t('users.form.passwordHint', { count: MIN_PASSWORD })}</span>
            </span>
            <input
              type="password"
              name="password"
              autoComplete="new-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
            {tooShort && <span className="dialog-error">{t('password.tooShort')}</span>}
          </label>
          <label className="field">
            <span className="field-name">{t('users.form.confirm')}</span>
            <input
              type="password"
              name="confirm"
              autoComplete="new-password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
            />
            {mismatch && <span className="dialog-error">{t('password.mismatch')}</span>}
          </label>
        </>
      )}
      <label className="check" style={{ marginBottom: 6 }}>
        <input
          type="checkbox"
          name="is_admin"
          checked={isAdmin}
          disabled={self && isAdmin}
          onChange={(e) => setIsAdmin(e.target.checked)}
        />
        {t('users.form.isAdmin')}
      </label>
      <p className="panel-note">{self ? t('users.form.isAdminSelf') : t('users.form.isAdminHint')}</p>
      <label className="check" style={{ marginBottom: 6 }}>
        <input
          type="checkbox"
          name="must_change_password"
          checked={mustChange}
          onChange={(e) => setMustChange(e.target.checked)}
        />
        {t('users.form.mustChange')}
      </label>
      <p className="panel-note">{t('users.form.mustChangeHint')}</p>

      <div className="field-name" style={{ marginTop: 4 }}>{t('users.form.groups')}</div>
      {groups.length ? (
        <div className="tiles" style={{ marginTop: 6 }}>
          {groups.map((g) => (
            <label key={g} className="tile check">
              <input
                type="checkbox"
                checked={chosen.includes(g)}
                onChange={(e) =>
                  setChosen(e.target.checked ? [...chosen, g] : chosen.filter((x) => x !== g))
                }
              />
              <span className="mono truncate">{g}</span>
            </label>
          ))}
        </div>
      ) : (
        <p className="panel-note">{t('users.form.noGroups')}</p>
      )}
    </Modal>
  )
}

/** An administrator sets a new password. Every session of the account ends; the user is made to
 *  pick their own at the next sign-in unless the box is cleared. */
function ResetPassword({
  user,
  onClose,
  onDone,
}: {
  user: DirectoryUser
  onClose: () => void
  onDone: (name: string) => void
}) {
  const { t } = useTranslation()
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [mustChange, setMustChange] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const ready = password.length >= MIN_PASSWORD && password === confirm

  const submit = async () => {
    setBusy(true)
    setError('')
    try {
      await updateDirectoryUser(user.username, { password, must_change_password: mustChange })
      onDone(user.username)
    } catch (e) {
      setError(String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      title={t('users.reset.title', { name: user.username })}
      submitLabel={t('users.reset.submit')}
      onClose={onClose}
      onSubmit={submit}
      busy={busy}
      canSubmit={ready}
      error={error}
    >
      <p className="dialog-message" style={{ marginTop: 0 }}>{t('users.reset.lead')}</p>
      <label className="field">
        <span className="field-name">
          {t('users.form.password')}
          <span className="field-hint">{t('users.form.passwordHint', { count: MIN_PASSWORD })}</span>
        </span>
        <input
          type="password"
          name="new_password"
          autoComplete="new-password"
          autoFocus
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        {password.length > 0 && password.length < MIN_PASSWORD && (
          <span className="dialog-error">{t('password.tooShort')}</span>
        )}
      </label>
      <label className="field">
        <span className="field-name">{t('users.form.confirm')}</span>
        <input
          type="password"
          name="confirm_password"
          autoComplete="new-password"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
        />
        {confirm.length > 0 && password !== confirm && (
          <span className="dialog-error">{t('password.mismatch')}</span>
        )}
      </label>
      <label className="check">
        <input type="checkbox" checked={mustChange} onChange={(e) => setMustChange(e.target.checked)} />
        {t('users.form.mustChange')}
      </label>
    </Modal>
  )
}
