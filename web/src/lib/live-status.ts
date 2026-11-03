import { createContext, useContext, useEffect, useRef } from 'react'
import type { EventStreamStatus } from '@/lib/api'
import type { KeeperEvent } from '@/lib/types'

export interface LiveStatus {
  status: EventStreamStatus
  /** Registers a listener for every daemon event; returns its unsubscribe. */
  subscribe: (listener: (event: KeeperEvent) => void) => () => void
}

/** Carries the one daemon event stream: its state, for `AppShell`'s `Lamp`, and
 * its events, for any screen that wants to stay current. The provider
 * (`LiveStatusProvider`) lives in `components/wrappers` because it renders;
 * this file exports only the context and the hooks. */
export const LiveStatusContext = createContext<LiveStatus>({ status: 'idle', subscribe: () => () => {} })

export function useLiveStatus() {
  return useContext(LiveStatusContext)
}

/** Runs `handler` for each daemon event while the component is mounted. */
export function useEvents(handler: (event: KeeperEvent) => void) {
  const { subscribe } = useLiveStatus()
  const latest = useRef(handler)
  useEffect(() => {
    latest.current = handler
  })
  useEffect(() => subscribe((event) => latest.current(event)), [subscribe])
}
