import { useCallback, useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, SheetTrigger } from '@/components/ui/sheet'
import { TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Lamp } from '@/components/wrappers/Lamp'
import { PagedTable } from '@/components/wrappers/PagedTable'
import { getDoctor } from '@/lib/api'
import { useEvents, useLiveStatus } from '@/lib/live-status'
import { usePaging } from '@/lib/paging'
import type { Session } from '@/lib/types'

/**
 * The agents connected right now: a lamp and a count in the top bar, opening to
 * who they are — client and workspace. It replaces the
 * sidebar's agent list, which took a column of the screen to say one number.
 */
export function AgentsIndicator() {
  const [sessions, setSessions] = useState<Session[]>([])
  const paging = usePaging('agents', '', true)

  const refresh = useCallback(() => {
    getDoctor()
      .then((r) => setSessions([...(r.sessions ?? [])].sort((a, b) => a.connected_at.localeCompare(b.connected_at) || a.id.localeCompare(b.id))))
      .catch(() => {
        // The shell's own lamp reports a daemon that is not answering.
      })
  }, [])

  const live = useLiveStatus().status === 'live'
  useEffect(refresh, [refresh, live])
  useEvents((event) => {
    if (event.kind === 'session') refresh()
  })

  const n = sessions.length
  const label = n === 0 ? 'No agents' : n === 1 ? '1 agent' : `${n} agents`

  return (
    <Sheet>
      <SheetTrigger render={<Button variant="ghost" size="sm" aria-label={label} />}>
        <Lamp state={n === 0 ? 'idle' : 'live'} label={label} />
        <span className="text-meta max-sm:hidden">{label}</span>
      </SheetTrigger>
      <SheetContent side="right">
        <SheetHeader>
          <SheetTitle>Agents</SheetTitle>
          <SheetDescription>Connected to keeper right now.</SheetDescription>
        </SheetHeader>
        <div className="flex flex-col gap-4 overflow-auto px-4">
          {n === 0 ? (
            <p className="text-sm text-muted-foreground">No agent is connected.</p>
          ) : (
            <PagedTable
              paging={paging}
              items={sessions}
              unit="agents"
              label="Agents"
              head={
                <TableHeader>
                  <TableRow>
                    <TableHead>Agent</TableHead>
                    <TableHead>Workspace</TableHead>
                  </TableRow>
                </TableHeader>
              }
              row={(s) => (
                <TableRow key={s.id}>
                  <TableCell className="text-sm">{s.client.name}</TableCell>
                  <TableCell className="text-meta whitespace-normal break-all text-muted-foreground">
                    {s.client.workspace ?? 'unknown workspace'}
                  </TableCell>
                </TableRow>
              )}
            />
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}
