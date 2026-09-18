import { createContext, useContext } from 'react'

export interface ConnectionScope {
  connectionId: string | null
  setConnectionId: (id: string | null) => void
}

/** The connection a screen is scoped to — `Catalog` and `Policy` are
 * per-connection (UI.md §2.3); `Approvals` and `Activity` are the shared
 * queue and log across every connection and read this only to pre-filter.
 * The provider (`ConnectionScopeProvider`) lives in `components/wrappers`
 * because it renders; this file exports only the context and the hook. */
export const ConnectionScopeContext = createContext<ConnectionScope>({
  connectionId: null,
  setConnectionId: () => {},
})

export function useConnectionScope() {
  return useContext(ConnectionScopeContext)
}
