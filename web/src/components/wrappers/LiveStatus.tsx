import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import type { EventStreamStatus } from '@/lib/api'
import { shareEvents } from '@/lib/events'
import { LiveStatusContext } from '@/lib/live-status'
import type { KeeperEvent } from '@/lib/types'

/**
 * Opens the one `GET /v1/events` subscription for the whole app, and for every
 * tab of the browser, and carries it down through context. `AppShell`'s daemon
 * `Lamp` and any screen that wants live updates read this connection rather
 * than opening an `EventSource` of their own: each one would count against the
 * browser's six connections per host.
 *
 * Registry search: `pnpm dlx shadcn@latest search @shadcn -q "sse" -q
 * "realtime"` returned no items. The stream, its reconnect and its sharing
 * between tabs are `lib/api.ts` and `lib/events.ts`; this component owns only
 * handing the result to React as context.
 */
export function LiveStatusProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<EventStreamStatus>('idle')
  const listeners = useRef(new Set<(event: KeeperEvent) => void>())

  const subscribe = useCallback((listener: (event: KeeperEvent) => void) => {
    listeners.current.add(listener)
    return () => void listeners.current.delete(listener)
  }, [])

  useEffect(
    () =>
      shareEvents(
        (event) => listeners.current.forEach((l) => l(event)),
        (next) => setStatus(next),
      ),
    [],
  )

  const value = useMemo(() => ({ status, subscribe }), [status, subscribe])
  return <LiveStatusContext.Provider value={value}>{children}</LiveStatusContext.Provider>
}
