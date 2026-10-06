import { useCallback, useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, SheetTrigger } from '@/components/ui/sheet'
import { Lamp } from '@/components/wrappers/Lamp'
import { getDoctor, subscribeToEvents } from '@/lib/api'
import type { Session } from '@/lib/types'

/**
 * The agents connected right now: a lamp and a count in the top bar, opening to
 * who they are — client, workspace and the intent each stated. It replaces the
 * sidebar's agent list, which took a column of the screen to say one number.
 */
export function AgentsIndicator() {
  const [sessions, setSessions] = useState<Session[]>([])

  const refresh = useCallback(() => {
    getDoctor()
      .then((r) => setSessions(r.sessions ?? []))
      .catch(() => {
        // The shell's own lamp reports a daemon that is not answering.
      })
  }, [])

  useEffect(() => {
    refresh()
    return subscribeToEvents((event) => {
      if (event.kind === 'session') refresh()
    })
  }, [refresh])

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
        <div className="flex flex-col gap-4 px-4">
          {n === 0 ? (
            <p className="text-sm text-muted-foreground">No agent is connected.</p>
          ) : (
            sessions.map((s) => (
              <div key={s.id} className="flex flex-col gap-1">
                <span className="text-sm">{s.client.name}</span>
                <span className="text-meta text-muted-foreground">{s.client.workspace ?? 'unknown workspace'}</span>
                <span className="text-sm">{s.intent ?? 'no intent stated'}</span>
              </div>
            ))
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}
