import { useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import {
  createDirectoryGroup,
  deleteDirectoryGroup,
  updateDirectoryGroup,
  type Directory,
  type DirectoryGroup,
} from '../../api'
import { useDialogs } from '../../components/Dialog'
import Modal from '../../components/Modal'
import type { UsersSectionProps } from './types'

/** User groups: named sets of logins, built-in or GitHub, that the three policies bind like a
 *  team. Where each group is used is shown on its card, so deleting one is never a surprise. */
export default function GroupsSection({ data, reload, notify }: UsersSectionProps) {
  const { t } = useTranslation()
  const dialogs = useDialogs()
  const [editing, setEditing] = useState<{ group?: DirectoryGroup } | null>(null)

  const remove = async (g: DirectoryGroup) => {
    const used = g.references.model_group || g.references.key_policy || g.references.key_scope
    if (
      !(await dialogs.confirm({
        title: t('users.groups.deleteTitle', { name: g.name }),
        message: used ? t('users.groups.deleteUsed') : t('users.groups.deleteMessage'),
        confirmLabel: t('common.delete'),
        danger: true,
      }))
    )
      return
    try {
      await deleteDirectoryGroup(g.name)
      notify('ok', t('users.groups.deletedToast', { name: g.name }))
      await reload()
    } catch (e) {
      notify('error', String(e))
    }
  }

  return (
    <>
      <div className="panel">
        <div className="panel-head">
          {t('users.groups.title')}
          <span className="badge">{t('users.groups.count', { count: data.groups.length })}</span>
          <span className="spacer" />
          <button className="btn sm" onClick={() => setEditing({})}>
            {t('users.groups.new')}
          </button>
        </div>
        <div className="panel-body" style={{ paddingBottom: data.groups.length ? 4 : undefined }}>
          <p className="panel-note" style={{ marginTop: 0 }}>
            <Trans i18nKey="users.groups.lead" components={{ strong: <strong /> }} />
          </p>
          {!data.groups.length ? (
            <div className="empty">{t('users.groups.none')}</div>
          ) : (
            data.groups.map((g) => (
              <div className="provider-card" key={g.name} data-group={g.name}>
                <div className="panel-head" style={{ borderBottom: 'none', paddingLeft: 0, paddingRight: 0 }}>
                  <span className="name mono">{g.name}</span>
                  <span className={`badge ${g.members.length ? '' : 'warn'}`}>
                    {t('users.groups.memberCount', { count: g.members.length })}
                  </span>
                  {g.references.model_group && (
                    <span className="badge ok" title={t('users.groups.usedModelHint')}>
                      {t('users.groups.usedModel', { group: g.references.model_group })}
                    </span>
                  )}
                  {g.references.key_policy && (
                    <span className="badge ok">{t('users.groups.usedKeyPolicy')}</span>
                  )}
                  {g.references.key_scope && (
                    <span className="badge ok">{t('users.groups.usedKeyScope')}</span>
                  )}
                  <span className="spacer" />
                  <button className="btn ghost sm" onClick={() => setEditing({ group: g })}>
                    {t('users.users.edit')}
                  </button>
                  <button className="btn danger sm" onClick={() => remove(g)}>
                    {t('common.delete')}
                  </button>
                </div>
                {g.description && <p className="panel-note">{g.description}</p>}
                {g.members.length ? (
                  <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, paddingBottom: 12 }}>
                    {g.members.map((m) => (
                      <span key={m.login} className={`badge ${m.kind === 'builtin' ? '' : 'admin'}`} title={m.name}>
                        <span className="mono">{m.login}</span>
                        <span className="dim"> · {t(`users.kind.${m.kind}`)}</span>
                      </span>
                    ))}
                  </div>
                ) : (
                  <p className="panel-note">{t('users.groups.empty')}</p>
                )}
              </div>
            ))
          )}
        </div>
      </div>

      <div className="panel">
        <div className="panel-head">{t('users.groups.whereTitle')}</div>
        <div className="panel-body">
          <ul className="panel-note" style={{ margin: 0 }}>
            <li>
              <Trans i18nKey="users.groups.whereModels" components={{ strong: <strong />, a: <Link to="/policy" /> }} />
            </li>
            <li>
              <Trans i18nKey="users.groups.whereKeys" components={{ strong: <strong />, a: <Link to="/access/policy" /> }} />
            </li>
            <li>
              <Trans i18nKey="users.groups.whereScope" components={{ strong: <strong />, a: <Link to="/access/keyscope" /> }} />
            </li>
            <li>
              <Trans i18nKey="users.groups.whereSignIn" components={{ strong: <strong />, code: <code /> }} />
            </li>
          </ul>
        </div>
      </div>

      {editing && (
        <GroupForm
          group={editing.group}
          data={data}
          onClose={() => setEditing(null)}
          onDone={async (name) => {
            setEditing(null)
            notify('ok', t('users.groups.savedToast', { name }))
            await reload()
          }}
        />
      )}
    </>
  )
}

/** Create or edit a group. Members are ticked from the built-in users and the GitHub accounts that
 *  have signed in; a GitHub login that has not signed in yet can be typed. */
function GroupForm({
  group,
  data,
  onClose,
  onDone,
}: {
  group?: DirectoryGroup
  data: Directory
  onClose: () => void
  onDone: (name: string) => void
}) {
  const { t } = useTranslation()
  const creating = !group
  const [name, setName] = useState(group?.name ?? '')
  const [description, setDescription] = useState(group?.description ?? '')
  const [members, setMembers] = useState<string[]>(group?.members.map((m) => m.login) ?? [])
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const nameOk = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(name)
  const has = (login: string) => members.some((m) => m.toLowerCase() === login.toLowerCase())
  const toggle = (login: string, on: boolean) =>
    setMembers(on ? (has(login) ? members : [...members, login]) : members.filter((m) => m.toLowerCase() !== login.toLowerCase()))

  const builtin = data.users.map((u) => u.username)
  const github = data.github_users.map((u) => u.login)
  const listed = new Set([...builtin, ...github].map((l) => l.toLowerCase()))
  // Members that are neither a built-in user nor a GitHub account seen so far: typed by hand.
  const manual = members.filter((m) => !listed.has(m.toLowerCase()))

  const addTyped = () => {
    const login = typed.trim()
    if (!login) return
    if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$/.test(login)) {
      setError(t('users.groups.badLogin', { name: login }))
      return
    }
    setError('')
    toggle(login, true)
    setTyped('')
  }

  const submit = async () => {
    setBusy(true)
    setError('')
    try {
      if (creating) await createDirectoryGroup({ name, description, members })
      else await updateDirectoryGroup(group.name, { description, members })
      onDone(name)
    } catch (e) {
      setError(String(e))
    } finally {
      setBusy(false)
    }
  }

  const tiles = (logins: string[]) => (
    <div className="tiles" style={{ marginTop: 6, marginBottom: 12 }}>
      {logins.map((login) => (
        <label key={login} className="tile check">
          <input type="checkbox" checked={has(login)} onChange={(e) => toggle(login, e.target.checked)} />
          <span className="mono truncate">{login}</span>
        </label>
      ))}
    </div>
  )

  return (
    <Modal
      wide
      title={creating ? t('users.groups.createTitle') : t('users.groups.editTitle', { name: group.name })}
      submitLabel={creating ? t('users.form.create') : t('users.form.save')}
      onClose={onClose}
      onSubmit={submit}
      busy={busy}
      canSubmit={nameOk}
      error={error}
    >
      <label className="field">
        <span className="field-name">
          {t('users.groups.name')}
          <span className="field-hint">{creating ? t('users.groups.nameHint') : t('users.form.usernameFixed')}</span>
        </span>
        <input
          type="text"
          className="mono"
          name="group_name"
          value={name}
          disabled={!creating}
          autoFocus={creating}
          onChange={(e) => setName(e.target.value.trim())}
        />
        {creating && name && !nameOk && <span className="dialog-error">{t('users.groups.nameInvalid')}</span>}
      </label>
      <label className="field">
        <span className="field-name">{t('users.groups.description')}</span>
        <input type="text" name="description" value={description} onChange={(e) => setDescription(e.target.value)} />
      </label>

      <div className="field-name">
        {t('users.groups.builtinMembers')}
        <span className="field-hint">{t('users.groups.selected', { count: members.length })}</span>
      </div>
      {builtin.length ? tiles(builtin) : <p className="panel-note">{t('users.groups.noBuiltin')}</p>}

      <div className="field-name">
        {t('users.groups.githubMembers')}
        <span className="field-hint">{t('users.groups.githubMembersHint')}</span>
      </div>
      {github.length ? tiles(github) : <p className="panel-note">{t('users.groups.noGithub')}</p>}

      <div className="secret-reveal" style={{ marginBottom: 8 }}>
        <input
          type="text"
          className="mono"
          name="github_login"
          placeholder={t('users.groups.typePlaceholder')}
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              addTyped()
            }
          }}
        />
        <button type="button" className="btn ghost sm" onClick={addTyped}>
          {t('users.groups.add')}
        </button>
      </div>
      {manual.length > 0 && (
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
          {manual.map((login) => (
            <span key={login} className="badge admin">
              <span className="mono">{login}</span>{' '}
              <button
                type="button"
                className="btn-link"
                aria-label={t('common.delete')}
                onClick={() => toggle(login, false)}
              >
                ×
              </button>
            </span>
          ))}
        </div>
      )}
    </Modal>
  )
}
