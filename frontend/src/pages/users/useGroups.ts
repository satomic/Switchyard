import { useCallback, useEffect, useState } from 'react'
import { getDirectory, type DirectoryGroup } from '../../api'

/** The built-in user groups, for the policy pages that bind them. Read-only here: groups are
 *  created and edited on the Users page, and every policy page only picks from what exists. */
export function useDirectoryGroups(): {
  groups: DirectoryGroup[] | null
  error: string
  reload: () => void
} {
  const [groups, setGroups] = useState<DirectoryGroup[] | null>(null)
  const [error, setError] = useState('')

  const reload = useCallback(() => {
    getDirectory()
      .then((d) => {
        setGroups(d.groups)
        setError('')
      })
      .catch((e) => setError(String(e)))
  }, [])

  useEffect(() => {
    reload()
  }, [reload])

  return { groups, error, reload }
}
