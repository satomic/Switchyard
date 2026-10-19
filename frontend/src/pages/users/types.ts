import type { Directory, SessionUser } from '../../api'

/** What both sub-pages of Users take. The directory is loaded once by UsersPage and reloaded after
 *  every action, so the two tabs never disagree about which groups exist. */
export interface UsersSectionProps {
  data: Directory
  /** The signed-in administrator, so their own row can refuse self-lockout up front. */
  me: SessionUser
  reload: () => Promise<void>
  notify: (kind: 'ok' | 'error', msg: string) => void
}
