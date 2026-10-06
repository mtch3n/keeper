import { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'

import { Button } from '@/components/ui/button'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { HostDialog } from '@/components/wrappers/HostDialog'
import { RegisterDialog } from '@/components/wrappers/RegisterDialog'
import { Lamp } from '@/components/wrappers/Lamp'
import {
  listConnections,
  listHosts,
  removeHost,
} from '@/lib/api'
import type { ConnectionSummary, HostView } from '@/lib/types'

/**
 * Hosts and the databases registered on them (SPEC R4.1, UI.md §2.7).
 *
 * A host is entered once; each database and role on it is its own connection,
 * which is what an agent queries and what owns a catalog, token keys and an
 * audit. So the page is grouped by host, and a database is added from its
 * host's section rather than by retyping an address.
 *
 * keeper never refuses a credential, and it no longer holds one shut either. A
 * registered connection works; what its role can do beyond reading is a report
 * on `Audit`. Nothing on this page asks anyone to agree to anything.
 */
export function ConnectionsPage() {
  const [hosts, setHosts] = useState<HostView[] | null>(null)
  const [list, setList] = useState<ConnectionSummary[]>([])
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    const [hs, cs] = await Promise.all([listHosts(), listConnections()])
    setHosts(hs)
    setList(cs)
  }, [])

  const refresh = useCallback(async () => {
    try {
      await load()
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [load])

  useEffect(() => {
    void (async () => {
      try {
        await load()
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e))
      }
    })()
  }, [load])

  const navigate = useNavigate()

  if (hosts === null) return <Skeleton className="h-40 w-full" />

  return (
    <div className="flex flex-col gap-8">
      {error ? <p className="text-sm text-blocked">{error}</p> : null}

      <div className="flex items-center justify-between gap-4">
        <h1 className="text-title">Connections</h1>
        <HostDialog onRegistered={refresh} onError={setError} />
      </div>

      {hosts.length === 0 ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>No hosts</EmptyTitle>
            <EmptyDescription>
              Add the database server first, then each database and role on it. A connection works straight away;
              keeper audits the credential and reports what the role can do on its page — it does not refuse a
              credential, and it does not hold one shut.
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        hosts.map((h) => (
          <HostSection
            key={h.id}
            host={h}
            connections={list.filter((c) => h.connections.includes(c.id))}
            onOpen={(id) => navigate(`/connections/${id}`)}
            onRegistered={async (c) => navigate(`/connections/${c.id}`)}
            onChanged={refresh}
            onError={setError}
          />
        ))
      )}
    </div>
  )
}

/** One host: where it is, the connections on it, and the way to add another. */
function HostSection({
  host,
  connections,
  onOpen,
  onRegistered,
  onChanged,
  onError,
}: {
  host: HostView
  connections: ConnectionSummary[]
  onOpen: (id: string) => void
  onRegistered: (c: { id: string }) => Promise<void>
  onChanged: () => Promise<void>
  onError: (e: string) => void
}) {
  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-center justify-between gap-4">
        <div className="flex flex-col gap-1">
          <h2 className="text-label">{host.name}</h2>
          <span className="text-meta text-muted-foreground">
            {host.address}:{host.port} · sslmode {host.sslmode}
          </span>
        </div>
        <div className="flex gap-2">
          <RegisterDialog host={host} onRegistered={onRegistered} onError={onError} />
          {/* A host with connections on it cannot be removed: the daemon
              refuses, and offering a button that always fails is a lie. */}
          {connections.length === 0 ? (
            <Button
              variant="outline"
              size="sm"
              onClick={async () => {
                try {
                  await removeHost(host.id)
                  await onChanged()
                } catch (e) {
                  onError(e instanceof Error ? e.message : String(e))
                }
              }}
            >
              Remove host
            </Button>
          ) : null}
        </div>
      </div>

      {connections.length === 0 ? (
        <p className="text-meta text-muted-foreground">No databases on this host yet.</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-8" />
              <TableHead>Name</TableHead>
              <TableHead>Engine</TableHead>
              <TableHead>Database</TableHead>
              <TableHead>Role</TableHead>
              <TableHead>Mode</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {connections.map((c) => (
              <TableRow key={c.id} className="cursor-pointer" onClick={() => onOpen(c.id)}>
                <TableCell>
                  <Lamp state="live" />
                </TableCell>
                <TableCell className="text-meta">
                  <Link to={`/connections/${c.id}`} onClick={(e) => e.stopPropagation()} className="hover:underline">
                    {c.name}
                  </Link>
                </TableCell>
                <TableCell className="text-meta">{c.engine}</TableCell>
                <TableCell className="text-meta">{c.database}</TableCell>
                <TableCell className="text-meta">{c.role}</TableCell>
                <TableCell className="text-meta">{c.mode}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </section>
  )
}
