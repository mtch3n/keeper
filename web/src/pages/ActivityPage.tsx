import { useCallback, useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Fact, Facts } from '@/components/wrappers/Facts'
import { getActivityRecord, listActivity } from '@/lib/api'
import { age, durationMs, relationList, renderSQL } from '@/lib/render'
import type { AuditRecord, Transform } from '@/lib/types'

/**
 * The query log (SPEC §10, UI.md §2.6). This is where "what left this machine,
 * and when" gets answered, and it is what makes reviewing worthwhile afterwards.
 *
 * Two of its rules are requirements rather than wording preferences. The log
 * records what keeper *intended* to emit — policies applied, transform counts,
 * spans redacted — and does not verify the emitted bytes (R10d), so nothing here
 * may say "values masked". And there is no "show full statement" affordance,
 * because literals are never stored (R10a): the normalized statement *is* the
 * statement as kept, and an affordance implying otherwise would be offering
 * something that does not exist.
 */
export function ActivityPage() {
  const [records, setRecords] = useState<AuditRecord[] | null>(null)
  const [open, setOpen] = useState<AuditRecord | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [filters, setFilters] = useState<{ session: string; connection: string; tier: string }>({
    session: '',
    connection: '',
    tier: '',
  })

  const refresh = useCallback(async () => {
    try {
      const tier = filters.tier === '' ? undefined : Number(filters.tier)
      setRecords(
        await listActivity({
          session: filters.session || undefined,
          connection: filters.connection || undefined,
          tier: Number.isFinite(tier) ? tier : undefined,
          limit: 200,
        }),
      )
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [filters])

  useEffect(() => {
    void refresh()
  }, [refresh])

  return (
    <div className="flex flex-col gap-6">
      {/* A human arriving after a long agent run wants one session's trail.
          Scrolling a merged log to find it is the difference between a screen
          that gets used and one that does not. */}
      <div className="flex flex-wrap items-end gap-3">
        <Filter
          label="session"
          value={filters.session}
          onChange={(v) => setFilters((f) => ({ ...f, session: v }))}
        />
        <Filter
          label="connection"
          value={filters.connection}
          onChange={(v) => setFilters((f) => ({ ...f, connection: v }))}
        />
        <Filter label="tier" value={filters.tier} onChange={(v) => setFilters((f) => ({ ...f, tier: v }))} />
        <Button variant="outline" onClick={() => setFilters({ session: '', connection: '', tier: '' })}>
          Clear
        </Button>
      </div>

      {error ? <p className="text-sm text-blocked">{error}</p> : null}

      {records === null ? (
        <Skeleton className="h-40 w-full" />
      ) : records.length === 0 ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>No activity</EmptyTitle>
            <EmptyDescription>Every statement keeper runs is recorded here, with what it applied.</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>When</TableHead>
              <TableHead>Session</TableHead>
              <TableHead>Connection</TableHead>
              <TableHead className="text-right">Tier</TableHead>
              <TableHead className="text-right">Rows</TableHead>
              <TableHead>Policies applied</TableHead>
              <TableHead>Statement</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {records.map((r) => (
              <TableRow
                key={r.id}
                className="cursor-pointer"
                onClick={() => void openRecord(r, setOpen, setError)}
              >
                <TableCell className="text-meta">{age(r.at)} ago</TableCell>
                <TableCell className="text-meta">{r.client.name}</TableCell>
                <TableCell className="text-meta">{r.connection}</TableCell>
                <TableCell className="text-right text-meta">{r.tier}</TableCell>
                <TableCell className="text-right text-meta">{r.row_count}</TableCell>
                <TableCell className="text-meta">{summarize(r.transforms)}</TableCell>
                <TableCell className="max-w-md truncate text-meta">{renderSQL(r.statement)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      {open ? <RecordDetail record={open} onClose={() => setOpen(null)} /> : null}

      <p className="text-meta text-muted-foreground">
        Policies applied, not bytes verified: the log records the decision, not proof that redaction ran correctly.
        Literals are never stored, so the statement shown is the statement as kept.
      </p>
    </div>
  )
}

async function openRecord(
  r: AuditRecord,
  setOpen: (r: AuditRecord) => void,
  setError: (e: string) => void,
) {
  try {
    setOpen(await getActivityRecord(r.id))
  } catch (e) {
    setError(e instanceof Error ? e.message : String(e))
  }
}

function Filter({ label, value, onChange }: { label: string; value: string; onChange: (v: string) => void }) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-label text-muted-foreground">{label}</span>
      <Input value={value} onChange={(e) => onChange(e.target.value)} className="w-48" />
    </label>
  )
}

function RecordDetail({ record, onClose }: { record: AuditRecord; onClose: () => void }) {
  return (
    <div className="flex flex-col gap-4 border border-border p-6">
      <div className="flex items-start justify-between gap-4">
        <Facts>
          <Fact label="audit id">
            <span className="text-meta">{record.id}</span>
          </Fact>
          <Fact label="intent">{record.intent || '—'}</Fact>
          <Fact label="agent">
            {record.client.name} · {record.client.workspace ?? 'unknown workspace'}
          </Fact>
          <Fact label="relations">{relationList(record.relations)}</Fact>
          <Fact label="duration">{durationMs(record.duration)}</Fact>
          <Fact label="authorization">{record.authorization || 'tier 0'}</Fact>
          {record.approver ? <Fact label="approver">{record.approver}</Fact> : null}
          {record.connection_degraded ? (
            <Fact label="connection">
              <span className="text-waiting">ran with accepted privilege findings</span>
            </Fact>
          ) : null}
          {record.collisions ? (
            <Fact label="collisions">
              {record.collisions} cell(s) redacted because a token was already bound to a different value
            </Fact>
          ) : null}
        </Facts>
        <Button variant="outline" onClick={onClose}>
          Close
        </Button>
      </div>

      <Separator />

      <div>
        <h3 className="text-label text-muted-foreground">policies applied</h3>
        {record.transforms && Object.keys(record.transforms).length > 0 ? (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Column</TableHead>
                <TableHead>Policy</TableHead>
                <TableHead>Basis</TableHead>
                <TableHead className="text-right">Spans redacted</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {Object.entries(record.transforms).map(([name, t]) => (
                <TableRow key={name}>
                  <TableCell className="text-meta">{name}</TableCell>
                  <TableCell className="text-meta">{t.policy}</TableCell>
                  <TableCell className="text-meta">
                    {t.basis}
                    {t.sample_size ? ` (${t.sample_size} rows sampled)` : ''}
                  </TableCell>
                  <TableCell className="text-right text-meta">{t.spans_redacted ?? 0}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : (
          <p className="text-meta text-muted-foreground">nothing was transformed</p>
        )}
      </div>

      {record.degradations?.length ? (
        <div>
          <h3 className="text-label text-muted-foreground">degradations</h3>
          <ul className="text-sm">
            {record.degradations.map((d, i) => (
              <li key={i}>
                {d.layer}: {d.reason}
              </li>
            ))}
          </ul>
          <p className="text-meta text-muted-foreground">
            A layer that was configured and did not run narrows coverage. It is recorded so a narrower result is
            never mistaken for a clean one.
          </p>
        </div>
      ) : null}

      <div>
        <h3 className="text-label text-muted-foreground">statement, as kept</h3>
        <pre className="overflow-x-auto text-meta whitespace-pre-wrap">{renderSQL(record.statement)}</pre>
      </div>
    </div>
  )
}


function summarize(transforms: Record<string, Transform> | undefined): string {
  if (!transforms) return '—'
  const counts = new Map<string, number>()
  for (const t of Object.values(transforms)) {
    if (t.policy === 'allow') continue
    counts.set(t.policy, (counts.get(t.policy) ?? 0) + 1)
  }
  if (counts.size === 0) return '—'
  return [...counts.entries()].map(([policy, n]) => `${n} ${policy}`).join(' ')
}
