import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { NavLink, useParams } from 'react-router-dom'
import { getDirectory, type Directory, type SessionUser } from '../api'
import GroupsSection from './users/GroupsSection'
import UsersSection from './users/UsersSection'

type SectionKey = 'users' | 'groups'
const SECTIONS: SectionKey[] = ['users', 'groups']

/** Built-in users and user groups.
 *
 *  The identity half that GitHub supplies for GitHub accounts: who may sign in with a username and
 *  password, and which named groups they -- and GitHub accounts too -- belong to. The groups are
 *  then bound like any other scope on Model policy, Key policy and Key scope.
 *
 *  Unlike the configuration pages there is no draft and no save bar. Every action writes the
 *  directory immediately: creating an account or resetting a password is an event, not an edit to
 *  be batched, and a password must never sit in a draft that a later save could resubmit.
 */
export default function UsersPage({ user }: { user: SessionUser }) {
  const { t } = useTranslation()
  const { section } = useParams()
  const current: SectionKey = SECTIONS.includes(section as SectionKey) ? (section as SectionKey) : 'users'
  const [data, setData] = useState<Directory | null>(null)
  const [error, setError] = useState('')
  const [toast, setToast] = useState<{ kind: 'ok' | 'error'; msg: string } | null>(null)

  const reload = useCallback(
    () =>
      getDirectory()
        .then((d) => {
          setData(d)
          setError('')
        })
        .catch((e) => setError(String(e))),
    [],
  )

  useEffect(() => {
    void reload()
  }, [reload])

  const props = {
    data: data!,
    me: user,
    reload,
    notify: (kind: 'ok' | 'error', msg: string) => setToast({ kind, msg }),
  }

  return (
    <div>
      <div className="config-sticky">
        <nav className="subnav">
          {SECTIONS.map((key) => (
            <NavLink
              key={key}
              to={`/users/${key}`}
              className={`subnav-item ${current === key ? 'active' : ''}`}
              title={t(`users.section.${key}.hint`)}
              onClick={() => setToast(null)}
            >
              {t(`users.section.${key}.label`)}
              {data && (
                <span className="badge" style={{ marginLeft: 6 }}>
                  {key === 'users' ? data.users.length : data.groups.length}
                </span>
              )}
            </NavLink>
          ))}
        </nav>
      </div>

      {toast && <div className={`toast ${toast.kind}`}>{toast.msg}</div>}

      <div className="section-intro">
        <h2>{t(`users.section.${current}.label`)}</h2>
        <span className="dim">{t(`users.section.${current}.hint`)}</span>
      </div>

      {error ? (
        <div className="toast error">{error}</div>
      ) : !data ? (
        <div className="empty">{t('common.loading')}</div>
      ) : current === 'users' ? (
        <UsersSection {...props} />
      ) : (
        <GroupsSection {...props} />
      )}
    </div>
  )
}
