import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'

import { Button } from '@/components/ui/button'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Fact, Facts } from '@/components/wrappers/Facts'
import { RegisterDialog } from '@/components/wrappers/RegisterDialog'
import { Lamp } from '@/components/wrappers/Lamp'
import {
  auditConnection,
  removeConnection,
  getConnection,
  listConnections,
  type ConnectionDetail,
} from '@/lib/api'
import { auditedAge } from '@/lib/render'
import type { ConnectionSummary } from '@/lib/types'

/**
 * Register a database (SPEC R4.1, UI.md §2.7).
 *
 * keeper never refuses a credential, and it no longer holds one shut either. A
 * registered connection works; what its role can do beyond reading is a report
 * on `Audit`, where it can be read as a piece of database work rather than as a
 * gate standing between the operator and a connection they are trying to set
 * up. Nothing on this page asks anyone to agree to anything.
 */
export function ConnectionsPage() {
  const [list, setList] = useState<ConnectionSummary[] | null>(null)
  const [selected, setSelected] = useState<ConnectionDetail | null>(null)
  const [error, setError] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    try {
      const cs = await listConnections()
      setList(cs)
      setError(null)
      if (selected) setSelected(await getConnection(selected.id))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [selected])

  useEffect(() => {
    void (async () => {
      try {
        setList(await listConnections())
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e))
      }
    })()
  }, [])

  const open = async (id: string) => {
    try {
      setSelected(await getConnection(id))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  if (list === null) return <Skeleton className="h-40 w-full" />

  return (
    <div className="flex flex-col gap-8">
      {error ? <p className="text-sm text-blocked">{error}</p> : null}

      <div className="flex items-center justify-between gap-4">
        <h1 className="text-title">Connections</h1>
        <RegisterDialog
          onRegistered={async (c) => {
            await refresh()
            await open(c.id)
          }}
          onError={setError}
        />
      </div>

      {list.length === 0 ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>No connections</EmptyTitle>
            <EmptyDescription>
              Register one and it works straight away. keeper audits the credential in the background and
              reports what the role can do on Audit — it does not refuse a credential, and it does not hold
              one shut.
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
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
            {list.map((c) => (
              <TableRow key={c.id} className="cursor-pointer" onClick={() => void open(c.id)}>
                <TableCell>
                  <Lamp state="live" />
                </TableCell>
                <TableCell className="text-meta">{c.name}</TableCell>
                <TableCell className="text-meta">{c.engine}</TableCell>
                <TableCell className="text-meta">{c.database}</TableCell>
                <TableCell className="text-meta">{c.role}</TableCell>
                <TableCell className="text-meta">{c.mode}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      {selected ? (
        <ConnectionDetailPanel
          detail={selected}
          onChanged={refresh}
          onClose={() => setSelected(null)}
          onError={setError}
        />
      ) : null}

    </div>
  )
}

function ConnectionDetailPanel({
  detail,
  onChanged,
  onClose,
  onError,
}: {
  detail: ConnectionDetail
  onChanged: () => Promise<void>
  onClose: () => void
  onError: (e: string) => void
}) {
  const audited = detail.audited_privileges
  const findings = audited.findings ?? []

  return (
    <div className="flex flex-col gap-6 border border-border p-6">
      <div className="flex items-start justify-between gap-4">
        <Facts>
          <Fact label="connection">{detail.name}</Fact>
          <Fact label="role">{detail.role}</Fact>
          <Fact label="database">{detail.database}</Fact>
          <Fact label="catalog">{detail.catalog_status.path}</Fact>
          <Fact label="audited">{auditedAge(audited.audited_at)}</Fact>
          <Fact label="write credential">
            {detail.has_write_credential ? 'present' : 'none — write mode does not exist for this connection'}
          </Fact>
        </Facts>
        <div className="flex gap-2">
          <Button
            variant="outline"
            onClick={async () => {
              try {
                await auditConnection(detail.id)
                await onChanged()
              } catch (e) {
                onError(e instanceof Error ? e.message : String(e))
              }
            }}
          >
            Re-audit
          </Button>
          <RemoveConnection id={detail.id} name={detail.name} onRemoved={onChanged} onError={onError} />
          <Button variant="outline" onClick={onClose}>
            Close
          </Button>
        </div>
      </div>

      <Separator />

      {findings.length === 0 ? (
        <p className="text-sm">
          The privilege audit found nothing: this credential holds nothing keeper would report.
        </p>
      ) : (
        <p className="text-sm">
          The privilege audit found {findings.length} thing{findings.length === 1 ? '' : 's'} this role can do
          beyond reading.{' '}
          <Link to="/audit" className="underline underline-offset-4">
            Read them on Audit
          </Link>
          , with the statement that would narrow each one. The connection works either way.
        </p>
      )}
    </div>
  )
}

/**
 * Removing a connection takes its credential and every allow rule naming it. The name is retyped rather than confirmed with a click,
 * because a grant naming a connection that no longer exists is a rule nobody
 * can read and nobody can revoke.
 *
 * The catalog file stays. It lives in the project repo and is reviewed like
 * code; deleting it is not this button's business.
 */
function RemoveConnection({
  id,
  name,
  onRemoved,
  onError,
}: {
  id: string
  name: string
  onRemoved: () => Promise<void>
  onError: (e: string) => void
}) {
  const [confirming, setConfirming] = useState(false)
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)

  if (!confirming) {
    return (
      <Button variant="outline" onClick={() => setConfirming(true)}>
        Remove
      </Button>
    )
  }

  return (
    <div className="flex flex-col gap-2">
      <span className="text-meta text-muted-foreground">retype {name} to remove it</span>
      <div className="flex gap-2">
        <Input value={typed} onChange={(e) => setTyped(e.target.value)} className="w-40" />
        <Button
          disabled={busy || typed !== name}
          onClick={async () => {
            setBusy(true)
            try {
              await removeConnection(id)
              await onRemoved()
            } catch (e) {
              onError(e instanceof Error ? e.message : String(e))
            } finally {
              setBusy(false)
            }
          }}
        >
          Remove
        </Button>
        <Button variant="outline" onClick={() => setConfirming(false)}>
          Cancel
        </Button>
      </div>
      <span className="text-meta text-muted-foreground">
        Its credential and every allow rule naming it go with it. The catalog file stays.
      </span>
    </div>
  )
}
