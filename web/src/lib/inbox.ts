import { useCallback, useEffect, useState } from 'react'

import { listApprovals, listRequests, subscribeToEvents, type PendingRequest } from '@/lib/api'
import type { ApprovalItem } from '@/lib/types'

/** Everything waiting on a human, kept current from the event stream. */
export function useInbox() {
  const [approvals, setApprovals] = useState<ApprovalItem[] | null>(null)
  const [requests, setRequests] = useState<PendingRequest[]>([])
  const [error, setError] = useState<string | null>(null)

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

  useEffect(() => {
    void refresh()
    return subscribeToEvents((event) => {
      if (event.kind === 'approval' || event.kind === 'request' || event.kind === 'session') void refresh()
    })
  }, [refresh])

  const count = (approvals?.length ?? 0) + requests.length
  return { approvals, requests, count, error, refresh }
}
