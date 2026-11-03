import { createContext, useCallback, useContext, useEffect, useState } from 'react'

import { listApprovals, listRequests, type PendingRequest } from '@/lib/api'
import { useEvents, useLiveStatus } from '@/lib/live-status'
import type { ApprovalItem } from '@/lib/types'

export interface Inbox {
  approvals: ApprovalItem[] | null
  requests: PendingRequest[]
  count: number
  error: string | null
  refresh: () => Promise<void>
}

/** Everything waiting on a human, read once per screen and kept current from
 * the event stream. `AppShell` owns it; screens read it with `useInbox`. */
export function useInboxState(): Inbox {
  const [approvals, setApprovals] = useState<ApprovalItem[] | null>(null)
  const [requests, setRequests] = useState<PendingRequest[]>([])
  const [error, setError] = useState<string | null>(null)
  const live = useLiveStatus().status === 'live'

  const refresh = useCallback(async () => {
    try {
      const [a, r] = await Promise.all([listApprovals(), listRequests()])
      setApprovals(a)
      setRequests(r)
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [])

  // Events missed while the stream was down are not replayed, so every open
  // of the stream reloads the queue.
  useEffect(() => {
    void refresh()
  }, [refresh, live])

  useEvents((event) => {
    if (event.kind === 'approval' || event.kind === 'request' || event.kind === 'session') void refresh()
  })

  return { approvals, requests, count: (approvals?.length ?? 0) + requests.length, error, refresh }
}

export const InboxContext = createContext<Inbox | null>(null)

export function useInbox(): Inbox {
  const inbox = useContext(InboxContext)
  if (!inbox) throw new Error('useInbox needs AppShell')
  return inbox
}
