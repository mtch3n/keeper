import { useCallback, useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Input } from '@/components/ui/input'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Fact, Facts } from '@/components/wrappers/Facts'
import { getConnection, listConnections, setDenylist, updateConnection, type ConnectionDetail } from '@/lib/api'
import type { ConnectionSummary, Mode, RelationRef } from '@/lib/types'

/**
 * Per-connection limits (SPEC §4.5) and the mode selector (§9.4).
 *
 * `Policy` answers *what may this connection do*; `Settings` answers *what is
 * this daemon doing*. Keeping them apart is why neither screen is a bag of
 * preferences.
 */
export function PolicyPage() {
  const [list, setList] = useState<ConnectionSummary[] | null>(null)
  const [detail, setDetail] = useState<ConnectionDetail | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    void (async () => {
      try {
        const cs = await listConnections()
        setList(cs)
        if (cs.length > 0) setDetail(await getConnection(cs[0].id))
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e))
      }
    })()
  }, [])

  const reload = useCallback(async (id: string) => {
    try {
      setDetail(await getConnection(id))
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [])

  if (list === null) return <Skeleton className="h-40 w-full" />
  if (list.length === 0) {
    return (
      <Empty>
        <EmptyHeader>
          <EmptyTitle>No connections</EmptyTitle>
          <EmptyDescription>Register one first; these settings belong to a connection.</EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  return (
    <div className="flex flex-col gap-8">
      {error ? <p className="text-sm text-blocked">{error}</p> : null}

      <div className="flex flex-wrap gap-2">
        {list.map((c) => (
          <Button
            key={c.id}
            variant={detail?.id === c.id ? 'default' : 'outline'}
            onClick={() => void reload(c.id)}
          >
            {c.name}
          </Button>
        ))}
      </div>

      {detail ? (
        <>
          <ModeSelector detail={detail} onChanged={() => void reload(detail.id)} onError={setError} />
          <Separator />
          <LimitsForm detail={detail} onChanged={() => void reload(detail.id)} onError={setError} />
          <Separator />
          <DenylistEditor detail={detail} onChanged={() => void reload(detail.id)} onError={setError} />
          <Separator />
          <WriteScope detail={detail} />
        </>
      ) : null}
    </div>
  )
}

const MODES: { value: Mode; title: string; consequence: string }[] = [
  {
    value: 'strict',
    title: 'Strict',
    consequence:
      'Known-safe reads run. Anything keeper cannot resolve waits for you, and a local model advises without releasing anything.',
  },
  {
    value: 'assisted',
    title: 'Assisted automatic',
    consequence:
      'Ordinary reads run and a local model helps with intent and scope. Values keeper cannot resolve are masked rather than released.',
  },
  {
    value: 'permissive',
    title: 'Permissive automatic',
    consequence:
      'Inside a scope you delegate, a local model may release uncertain output. You are accepting that its mistakes disclose data. It still cannot lower a policy you pinned, and it grants no writes.',
  },
]

/**
 * Mode is text with a sentence of consequence under each option, never a
 * coloured pill or a slider. The three differ in what they let a local model
 * release, and a control that rendered that as a position on a scale would be
 * lying about what it does.
 */
function ModeSelector({
  detail,
  onChanged,
  onError,
}: {
  detail: ConnectionDetail
  onChanged: () => void
  onError: (e: string) => void
}) {
  const [busy, setBusy] = useState(false)

  return (
    <section className="flex flex-col gap-4">
      <h2 className="text-heading">Mode</h2>
      <RadioGroup
        value={detail.mode}
        onValueChange={async (value) => {
          setBusy(true)
          try {
            await updateConnection(detail.id, { mode: value as Mode })
            onChanged()
          } catch (e) {
            onError(e instanceof Error ? e.message : String(e))
          } finally {
            setBusy(false)
          }
        }}
        className="flex flex-col gap-4"
      >
        {MODES.map((m) => (
          <label key={m.value} className="flex gap-4">
            <RadioGroupItem value={m.value} disabled={busy} className="mt-1" />
            <span className="flex flex-col">
              <span className="text-sm">{m.title}</span>
              <span className="text-meta text-muted-foreground">{m.consequence}</span>
            </span>
          </label>
        ))}
      </RadioGroup>
      <p className="text-meta text-muted-foreground">
        A mode never provisions a write credential and never authorizes a disclosure. Executing a query,
        disclosing cleartext and modifying the database are three separate grants.
      </p>
    </section>
  )
}

function LimitsForm({
  detail,
  onChanged,
  onError,
}: {
  detail: ConnectionDetail
  onChanged: () => void
  onError: (e: string) => void
}) {
  const [maxRows, setMaxRows] = useState(String(detail.limits.max_rows_ceiling))
  const [timeout, setTimeoutMs] = useState(String(Math.round(detail.limits.statement_timeout / 1e6)))
  const [scanSample, setScanSample] = useState(String(detail.limits.scan_sample))
  const [busy, setBusy] = useState(false)

  return (
    <section className="flex flex-col gap-4">
      <h2 className="text-heading">Limits</h2>
      <div className="flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1">
          <span className="text-label text-muted-foreground">row ceiling</span>
          <Input value={maxRows} onChange={(e) => setMaxRows(e.target.value)} className="w-32" />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-label text-muted-foreground">statement timeout (ms)</span>
          <Input value={timeout} onChange={(e) => setTimeoutMs(e.target.value)} className="w-40" />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-label text-muted-foreground">scan sample (rows)</span>
          <Input value={scanSample} onChange={(e) => setScanSample(e.target.value)} className="w-32" />
        </label>
        <Button
          disabled={busy}
          onClick={async () => {
            setBusy(true)
            try {
              await updateConnection(detail.id, {
                limits: {
                  max_rows_ceiling: Number(maxRows),
                  statement_timeout: Number(timeout) * 1e6,
                  scan_sample: Number(scanSample),
                },
              })
              onChanged()
            } catch (e) {
              onError(e instanceof Error ? e.message : String(e))
            } finally {
              setBusy(false)
            }
          }}
        >
          Save
        </Button>
      </div>
      <p className="text-meta text-muted-foreground">
        The row ceiling is a privacy control, not a performance one: every row returned is a row sent to a third
        party. An agent can ask for fewer and never for more.
      </p>
    </section>
  )
}

function DenylistEditor({
  detail,
  onChanged,
  onError,
}: {
  detail: ConnectionDetail
  onChanged: () => void
  onError: (e: string) => void
}) {
  const [entry, setEntry] = useState('')
  const [busy, setBusy] = useState(false)
  const current = detail.denylist ?? []

  const save = async (next: RelationRef[]) => {
    setBusy(true)
    try {
      await setDenylist(detail.id, next)
      onChanged()
    } catch (e) {
      onError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="flex flex-col gap-4">
      <h2 className="text-heading">Denylist</h2>
      {current.length === 0 ? (
        <p className="text-meta text-muted-foreground">nothing denied</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Relation</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {current.map((r) => (
              <TableRow key={`${r.schema}.${r.relation}`}>
                <TableCell className="text-meta">
                  {r.schema}.{r.relation}
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    variant="outline"
                    disabled={busy}
                    onClick={() =>
                      void save(current.filter((x) => !(x.schema === r.schema && x.relation === r.relation)))
                    }
                  >
                    Remove
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      <div className="flex items-end gap-3">
        <label className="flex flex-col gap-1">
          <span className="text-label text-muted-foreground">schema.relation</span>
          <Input value={entry} onChange={(e) => setEntry(e.target.value)} className="w-64" />
        </label>
        <Button
          disabled={busy || !entry.includes('.')}
          onClick={() => {
            const [schema, ...rest] = entry.split('.')
            void save([...current, { schema, relation: rest.join('.') }])
            setEntry('')
          }}
        >
          Deny
        </Button>
      </div>
      <p className="text-meta text-muted-foreground">
        Evaluated against the plan, so a denied base table is caught even when the statement only names a view
        over it. No approval overrides it — remove the entry instead, which is a visible edit rather than a click
        inside a prompt. It is a convenience, not a boundary: the boundary is a grant the role never had.
      </p>
    </section>
  )
}

function WriteScope({ detail }: { detail: ConnectionDetail }) {
  return (
    <section className="flex flex-col gap-4">
      <h2 className="text-heading">Write scope</h2>
      {!detail.has_write_credential ? (
        <p className="text-sm">
          No write credential. Write mode does not exist for this connection — there is no setting here that
          creates it.
        </p>
      ) : detail.write_scope?.length ? (
        <>
          <Facts>
            <Fact label="recorded">at registration, and re-audited on schedule</Fact>
          </Facts>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Relation</TableHead>
                <TableHead>Operations</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {detail.write_scope.map((w) => (
                <TableRow key={`${w.relation.schema}.${w.relation.relation}`}>
                  <TableCell className="text-meta">
                    {w.relation.schema}.{w.relation.relation}
                  </TableCell>
                  <TableCell className="text-meta">{w.operations.join(', ')}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          <p className="text-meta text-muted-foreground">
            Read-only here: the scope is what the credential actually holds, discovered by the audit. A statement
            writing outside it is refused before the database sees it, so the agent gets a message naming the
            relation instead of a sanitised permission error.
          </p>
        </>
      ) : (
        <p className="text-meta text-muted-foreground">the write credential holds no recorded relations</p>
      )}
    </section>
  )
}
