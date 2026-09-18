import { useEffect, useState, type ReactNode } from 'react'
import { subscribeToEvents, type EventStreamStatus } from '@/lib/api'
import { LiveStatusContext, type LiveStatus } from '@/lib/live-status'
import type { KeeperEvent } from '@/lib/types'

/**
 * Opens the one `GET /v1/events` subscription for the whole app and carries
 * its state down through context, so `AppShell`'s daemon `Lamp` and any
 * screen that wants live updates read the same connection rather than each
 * opening its own `EventSource`.
 *
 * Registry search: `pnpm dlx shadcn@latest search @shadcn -q "sse" -q
 * "realtime"` returned no items. This is a browser API binding with
 * reconnect and backoff, which is `lib/api.ts`'s job; this component owns
 * only handing the result to React as context.
 */
export function LiveStatusProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<LiveStatus>({ status: 'idle' })

  useEffect(() => {
    const unsubscribe = subscribeToEvents(
      (event: KeeperEvent) => setState((prev) => ({ ...prev, lastEvent: event })),
      (status: EventStreamStatus) => setState((prev) => ({ ...prev, status })),
    )
    return unsubscribe
  }, [])

  return <LiveStatusContext.Provider value={state}>{children}</LiveStatusContext.Provider>
}
