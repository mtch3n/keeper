import { createContext, useContext } from 'react'

export interface SessionScope {
  sessionId: string | null
  setSessionId: (id: string | null) => void
}

/** The agent session a screen is scoped to. `Approvals`, `Permissions` and
 * `Activity` all answer "what is this agent doing", and each of them already
 * carries a session on every row (SPEC R9.1, R9.3e, §10) — this is the one
 * place that choice is held so the three agree.
 *
 * Unlike the connection scope it is deliberately NOT persisted. A session is
 * one socket connection and a reconnect is a new session with an empty
 * reverse map (SPEC R3.4e), so a session id restored from a previous page
 * load names something that no longer exists. The workspace outlives the
 * session; the id does not.
 *
 * The provider (`SessionScopeProvider`) lives in `components/wrappers`
 * because it renders; this file exports only the context and the hook. */
export const SessionScopeContext = createContext<SessionScope>({
  sessionId: null,
  setSessionId: () => {},
})

export function useSessionScope() {
  return useContext(SessionScopeContext)
}
