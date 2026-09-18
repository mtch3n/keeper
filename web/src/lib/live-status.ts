import { createContext, useContext } from 'react'
import type { EventStreamStatus } from '@/lib/api'
import type { KeeperEvent } from '@/lib/types'

export interface LiveStatus {
  status: EventStreamStatus
  lastEvent?: KeeperEvent
}

/** Carries the daemon SSE connection state up to `AppShell`'s `Lamp`, so it
 * is reported once rather than repeated as a banner on every screen. The
 * provider (`LiveStatusProvider`) lives in `components/wrappers` because it
 * renders; this file exports only the context and the hook. */
export const LiveStatusContext = createContext<LiveStatus>({ status: 'idle' })

export function useLiveStatus() {
  return useContext(LiveStatusContext)
}
