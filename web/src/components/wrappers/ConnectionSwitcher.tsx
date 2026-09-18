import { useEffect, useState } from 'react'
import { ChevronsUpDown } from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { listConnections } from '@/lib/api'
import { useConnectionScope } from '@/lib/connection-scope'
import type { ConnectionSummary } from '@/lib/types'

/**
 * The connection scope control in `AppShell`: which registered database
 * `Catalog` and `Policy` are currently showing (UI.md §2.3). A degraded
 * connection (accepted G0 findings, SPEC R4.1) carries its marker here too,
 * the same way it does on `Approvals` — the marker means the same thing
 * everywhere it appears.
 */
export function ConnectionSwitcher() {
  const [connections, setConnections] = useState<ConnectionSummary[]>([])
  const { connectionId, setConnectionId } = useConnectionScope()

  useEffect(() => {
    let cancelled = false
    listConnections()
      .then((list) => {
        if (cancelled) return
        setConnections(list)
        if (!connectionId && list.length > 0) setConnectionId(list[0].id)
      })
      .catch(() => {
        /* AppShell's own Lamp already reports the daemon connection */
      })
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const current = connections.find((c) => c.id === connectionId)

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={<Button variant="ghost" size="sm" className="gap-2 px-2" aria-label="Switch connection" />}
      >
        <span className="truncate">{current ? current.name : connections.length === 0 ? 'No connections' : 'Select a connection'}</span>
        {current?.degraded && <span className="text-waiting">⚠</span>}
        <ChevronsUpDown data-icon="inline-end" className="text-muted-foreground" />
      </DropdownMenuTrigger>

      <DropdownMenuContent sideOffset={6} className="w-64 p-1.5">
        <DropdownMenuGroup>
          <DropdownMenuLabel>Connections</DropdownMenuLabel>
          <DropdownMenuRadioGroup
            value={connectionId ?? ''}
            onValueChange={(id: string) => {
              if (id !== connectionId) setConnectionId(id)
            }}
          >
            {connections.map((connection) => (
              <DropdownMenuRadioItem key={connection.id} value={connection.id} className="gap-3 py-1.5">
                <span className="truncate">{connection.name}</span>
                {connection.degraded && (
                  <span className="ml-auto text-waiting" title="Runs with accepted findings">
                    ⚠
                  </span>
                )}
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
