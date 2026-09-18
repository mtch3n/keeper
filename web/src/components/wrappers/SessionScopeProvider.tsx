import { useMemo, useState, type ReactNode } from 'react'
import { SessionScopeContext } from '@/lib/session-scope'

/** Holds the selected agent session for the screens scoped by one. In memory
 * only, and see `@/lib/session-scope` for why: a persisted session id would
 * be restored after the agent it named had already reconnected under a new
 * one. */
export function SessionScopeProvider({ children }: { children: ReactNode }) {
  const [sessionId, setSessionId] = useState<string | null>(null)
  const value = useMemo(() => ({ sessionId, setSessionId }), [sessionId])
  return <SessionScopeContext.Provider value={value}>{children}</SessionScopeContext.Provider>
}
