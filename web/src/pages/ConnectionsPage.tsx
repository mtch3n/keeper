import { useCallback, useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Fact, Facts } from '@/components/wrappers/Facts'
import { RegisterDialog } from '@/components/wrappers/RegisterDialog'
import { Lamp } from '@/components/wrappers/Lamp'
import {
  acceptFindings,
  auditConnection,
  removeConnection,
  getConnection,
  listConnections,
  type ConnectionDetail,
} from '@/lib/api'
import { age } from '@/lib/render'
import type { ConnectionSummary, Finding } from '@/lib/types'

/**
 * Register a database and accept its privilege findings (SPEC R4.1, UI.md §2.7).
 *
 * keeper never refuses a credential. A master account works; what it does not do
 * is let you hold one without knowing. So the sequence here is fixed — audit,
 * then every finding in full, then one checkbox per finding, then the button —
 * and there is deliberately no single "I understand the risks" confirmation over
 * a collapsed list. That control is the one people click without reading, and the
 * requirement exists to prevent exactly it.
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
              Register one and keeper audits the credential, then shows you what it found. It does not refuse
              a credential; it makes holding a broad one impossible by accident.
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
              <TableHead>State</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {list.map((c) => (
              <TableRow key={c.id} className="cursor-pointer" onClick={() => void open(c.id)}>
                <TableCell>
                  <Lamp state={c.degraded ? 'waiting' : 'live'} />
                </TableCell>
                <TableCell className="text-meta">{c.name}</TableCell>
                <TableCell className="text-meta">{c.engine}</TableCell>
                <TableCell className="text-meta">{c.database}</TableCell>
                <TableCell className="text-meta">{c.role}</TableCell>
                <TableCell className="text-sm">
                  {c.degraded ? 'running with accepted privilege findings' : 'ok'}
                </TableCell>
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
  const outstanding = audited.unaccepted ?? []

  return (
    <div className="flex flex-col gap-6 border border-border p-6">
      <div className="flex items-start justify-between gap-4">
        <Facts>
          <Fact label="connection">{detail.name}</Fact>
          <Fact label="role">{detail.role}</Fact>
          <Fact label="database">{detail.database}</Fact>
          <Fact label="catalog">{detail.catalog_status.path}</Fact>
          <Fact label="audited">{age(audited.audited_at)} ago</Fact>
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

      {outstanding.length === 0 ? (
        <p className="text-sm">
          Nothing outstanding.
          {audited.acceptances?.length
            ? ` ${audited.acceptances?.length} finding(s) were accepted; this connection runs without keeper's database-level protection for them.`
            : ' This credential holds nothing keeper would report.'}
        </p>
      ) : (
        <FindingsAcceptance
          connectionId={detail.id}
          findings={outstanding}
          onAccepted={onChanged}
          onError={onError}
        />
      )}

      {audited.acceptances?.length ? (
        <>
          <Separator />
          <div>
            <h3 className="text-label text-muted-foreground">accepted</h3>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Finding</TableHead>
                  <TableHead>Actor</TableHead>
                  <TableHead>Via</TableHead>
                  <TableHead className="text-right">When</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(audited.acceptances ?? []).map((a) => (
                  <TableRow key={`${a.finding_id}-${a.at}`}>
                    <TableCell className="text-meta">{a.finding_id}</TableCell>
                    <TableCell className="text-meta">{a.actor}</TableCell>
                    <TableCell className="text-meta">{a.via}</TableCell>
                    <TableCell className="text-right text-meta">{age(a.at)} ago</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </>
      ) : null}
    </div>
  )
}

/**
 * One checkbox per finding. Never one confirmation over a collapsed list.
 *
 * Each finding says what it means in one sentence and shows the narrower grant
 * that would remove it, copyable as-is — so the operator who wanted to fix it is
 * one paste away, and the one who did not is unaffected.
 */
function FindingsAcceptance({
  connectionId,
  findings,
  onAccepted,
  onError,
}: {
  connectionId: string
  findings: Finding[]
  onAccepted: () => Promise<void>
  onError: (e: string) => void
}) {
  const [checked, setChecked] = useState<Set<string>>(new Set())
  const [actor, setActor] = useState('')
  const [busy, setBusy] = useState(false)

  const toggle = (id: string) =>
    setChecked((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

  return (
    <div className="flex flex-col gap-5">
      <div>
        <h3 className="text-heading">Privilege findings</h3>
        <p className="text-sm text-muted-foreground">
          This connection is disabled until each of these is accepted by name. Accepting one does not accept the
          others, and a finding that appears at a later audit needs its own acceptance.
        </p>
      </div>

      {findings.map((f) => (
        <div key={f.id} className="flex gap-4">
          <Checkbox
            checked={checked.has(f.id)}
            onCheckedChange={() => toggle(f.id)}
            aria-label={`accept ${f.id}`}
          />
          <div className="flex min-w-0 flex-col gap-1">
            <span className="text-meta">{f.id}</span>
            <span className="text-sm">{f.detail}</span>
            {f.narrower ? (
              <>
                <span className="text-label text-muted-foreground">narrower</span>
                <pre className="overflow-x-auto text-meta whitespace-pre-wrap">{f.narrower}</pre>
              </>
            ) : (
              <span className="text-meta text-muted-foreground">
                No single statement removes this one — it may be a vendor-owned object you cannot revoke on.
              </span>
            )}
          </div>
        </div>
      ))}

      <div className="flex items-end gap-3">
        <label className="flex flex-col gap-1">
          <span className="text-label text-muted-foreground">your name, recorded with each acceptance</span>
          <Input value={actor} onChange={(e) => setActor(e.target.value)} className="w-64" />
        </label>
        <Button
          disabled={busy || checked.size === 0 || actor.trim() === ''}
          onClick={async () => {
            setBusy(true)
            try {
              await acceptFindings(connectionId, {
                finding_ids: [...checked],
                actor: actor.trim(),
                via: 'ui',
              })
              setChecked(new Set())
              await onAccepted()
            } catch (e) {
              onError(e instanceof Error ? e.message : String(e))
            } finally {
              setBusy(false)
            }
          }}
        >
          Accept {checked.size} finding{checked.size === 1 ? '' : 's'} and enable
        </Button>
      </div>
    </div>
  )
}

/**
 * Removing a connection takes its credential, its acceptances and every allow
 * rule naming it. The name is retyped rather than confirmed with a click,
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
        Its credential, its accepted findings and every allow rule naming it go with it. The catalog file stays.
      </span>
    </div>
  )
}
