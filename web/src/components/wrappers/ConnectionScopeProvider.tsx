import { useMemo, useState, type ReactNode } from 'react'
import { ConnectionScopeContext } from '@/lib/connection-scope'

const STORAGE_KEY = 'keeper.connection-scope'

/** Remembers the working connection per browser, the way trellis remembers a
 * project scope — a deliberate act, not something a screen has to rediscover
 * on every visit. */
export function ConnectionScopeProvider({ children }: { children: ReactNode }) {
  const [connectionId, setConnectionIdState] = useState<string | null>(() => {
    try {
      return localStorage.getItem(STORAGE_KEY)
    } catch {
      return null
    }
  })

  const setConnectionId = (id: string | null) => {
    setConnectionIdState(id)
    try {
      if (id) localStorage.setItem(STORAGE_KEY, id)
      else localStorage.removeItem(STORAGE_KEY)
    } catch {
      /* private mode */
    }
  }

  const value = useMemo(() => ({ connectionId, setConnectionId }), [connectionId])

  return <ConnectionScopeContext.Provider value={value}>{children}</ConnectionScopeContext.Provider>
}
