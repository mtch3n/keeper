import { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'

import { Button } from '@/components/ui/button'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { HostDialog } from '@/components/wrappers/HostDialog'
import { RegisterDialog } from '@/components/wrappers/RegisterDialog'
import { listConnections, listHosts, removeHost } from '@/lib/api'
import type { ConnectionSummary, HostView } from '@/lib/types'

const COLUMNS = 5

/**
 * Every connection keeper holds, in one table .
 *
 * A host is entered once; each database and role on it is its own connection,
 * which is what an agent queries and what owns a catalog and token keys. So each host is a row that heads its connections, and a database is
 * added from that row rather than by retyping an address. One table keeps the
 * columns aligned across hosts, so a filter and a glance both work.
 *
 * Nothing here connects to a database: keeper connects only when a database is
 * needed, and whether an account logs in is asked on its page with Test

 */
export function ConnectionsPage() {
  const [hosts, setHosts] = useState<HostView[] | null>(null)
  const [list, setList] = useState<ConnectionSummary[]>([])
  const [filter, setFilter] = useState('')
  const [error, setError] = useState<string | null>(null)
  const navigate = useNavigate()

  const load = useCallback(async () => {
    try {
      const [hs, cs] = await Promise.all([listHosts(), listConnections()])
      setHosts(hs)
      setList(cs)
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  if (hosts === null) {
    return error ? <p className="text-sm text-blocked">{error}</p> : <Skeleton className="h-40 w-full" />
  }

  const needle = filter.trim().toLowerCase()
  const matches = (c: ConnectionSummary, h: HostView) =>
    needle === '' ||
    [c.name, c.database, c.username, h.name, h.address].some((v) => v?.toLowerCase().includes(needle))
  const groups = hosts
    .map((h) => ({ host: h, rows: list.filter((c) => h.connections.includes(c.id) && matches(c, h)) }))
    .filter((g) => needle === '' || g.rows.length > 0)

  return (
    <div className="flex flex-col gap-6">
      {error ? (
        <p role="alert" className="text-sm text-blocked">
          {error}
        </p>
      ) : null}

      <div className="flex flex-wrap items-center justify-between gap-4">
        <h1 className="text-title">Connections</h1>
        <HostDialog onRegistered={load} onError={setError} />
      </div>

      {hosts.length === 0 ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>No hosts</EmptyTitle>
            <EmptyDescription>
              Add the database server first, then each database and role on it. A connection works straight away.
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <>
          <Input
            type="search"
            aria-label="Filter connections"
            placeholder="Filter by name, database, user or host"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            className="w-full sm:w-80"
          />
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Database</TableHead>
                <TableHead>User</TableHead>
                <TableHead>Writes</TableHead>
                <TableHead>Reads</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {groups.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={COLUMNS} className="text-meta text-muted-foreground">
                    No connection matches “{filter.trim()}”.
                  </TableCell>
                </TableRow>
              ) : (
                groups.map(({ host, rows }) => (
                  <HostRows
                    key={host.id}
                    host={host}
                    rows={rows}
                    onOpen={(id) => navigate(`/connections/${id}`)}
                    onChanged={load}
                    onError={setError}
                  />
                ))
              )}
            </TableBody>
          </Table>
        </>
      )}
    </div>
  )
}

/** One host's heading row and its connections' rows, inside the one table. */
function HostRows({
  host,
  rows,
  onOpen,
  onChanged,
  onError,
}: {
  host: HostView
  rows: ConnectionSummary[]
  onOpen: (id: string) => void
  onChanged: () => Promise<void>
  onError: (e: string) => void
}) {
  return (
    <>
      <TableRow className="bg-muted/40 hover:bg-muted/40">
        <TableCell colSpan={COLUMNS} className="whitespace-normal">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="flex min-w-0 flex-col gap-0.5">
              <span className="text-sm font-medium">{host.name}</span>
              <span className="text-meta break-all text-muted-foreground">
                {host.address}:{host.port} · sslmode {host.sslmode}
              </span>
            </div>
            <div className="flex gap-2">
              <RegisterDialog host={host} onRegistered={async (c) => onOpen(c.id)} onError={onError} />
              {/* A host with connections on it cannot be removed: the daemon
                  refuses, and offering a button that always fails is a lie. */}
              {host.connections.length === 0 ? (
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
        </TableCell>
      </TableRow>
      {host.connections.length === 0 ? (
        <TableRow>
          <TableCell colSpan={COLUMNS} className="text-meta text-muted-foreground">
            No databases on this host yet.
          </TableCell>
        </TableRow>
      ) : null}
      {rows.map((c) => {
        return (
          <TableRow key={c.id} className="cursor-pointer" onClick={() => onOpen(c.id)}>
            <TableCell className="text-meta">
              <Link to={`/connections/${c.id}`} onClick={(e) => e.stopPropagation()} className="hover:underline">
                {c.name}
              </Link>
            </TableCell>
            <TableCell className="text-meta">{c.database}</TableCell>
            <TableCell className="text-meta">{c.username}</TableCell>
            <TableCell className="text-meta">{c.writes}</TableCell>
            <TableCell className="text-meta">{c.reads}</TableCell>
          </TableRow>
        )
      })}
    </>
  )
}
