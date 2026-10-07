import { Fragment, useCallback, useEffect, useState } from 'react'
import { ChevronRightIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Cell, type CellValue } from '@/components/wrappers/Cell'
import { Fact, Facts } from '@/components/wrappers/Facts'
import { getActivityRecord, listActivity, listConnections } from '@/lib/api'
import { age, durationMs, relationList, renderSQL } from '@/lib/render'
import { cn } from '@/lib/utils'
import type { ActivityDetail, AuditRecord, ColumnMeta, ConnectionSummary, QueryResult, Transform } from '@/lib/types'

/** The filter value meaning "no filter". */
const ALL = 'all'
const NO_FILTER = { session: ALL, connection: ALL, tier: ALL, decision: ALL }

/** Rows per page. One more is asked for, to know whether an older page exists. */
const PAGE = 50

/** Mirrors redact.RedactedMarker: a cell that kept nothing. */
const REDACTED = '⟨redacted⟩'

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
  const [hasOlder, setHasOlder] = useState(false)
  // The `before` cursor of every page from the newest to this one. Empty is
  // the first page; Older pushes the last id shown, Newer pops.
  const [cursors, setCursors] = useState<string[]>([])
  const [open, setOpen] = useState<string | null>(null)
  const [details, setDetails] = useState<Record<string, ActivityDetail>>({})
  const [error, setError] = useState<string | null>(null)
  const [filters, setFilters] = useState(NO_FILTER)
  const [connections, setConnections] = useState<ConnectionSummary[]>([])
  // Agents seen so far, so the filter keeps offering one after narrowing to
  // another: a list built from the narrowed page would shrink to one entry.
  const [agents, setAgents] = useState<Record<string, string>>({})

  useEffect(() => {
    listConnections().then(setConnections).catch(() => {})
  }, [])

  // A cursor belongs to the filter it was taken under, so changing a filter
  // goes back to the newest page.
  function filter(change: Partial<typeof filters>) {
    setCursors([])
    setFilters((f) => ({ ...f, ...change }))
  }

  const before = cursors.at(-1)
  const refresh = useCallback(async () => {
    try {
      const page = await listActivity({
        session: filters.session === ALL ? undefined : filters.session,
        connection: filters.connection === ALL ? undefined : filters.connection,
        tier: filters.tier === ALL ? undefined : Number(filters.tier),
        before,
        limit: PAGE + 1,
      })
      setHasOlder(page.length > PAGE)
      setRecords(page.slice(0, PAGE))
      setAgents((known) => {
        const next = { ...known }
        for (const r of page) next[r.session_id] ??= `${r.client.name} · ${r.client.workspace ?? 'unknown workspace'}`
        return next
      })
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [filters, before])

  useEffect(() => {
    void refresh()
  }, [refresh])

  const toggle = async (id: string) => {
    if (open === id) {
      setOpen(null)
      return
    }
    setOpen(id)
    if (details[id]) return
    try {
      const d = await getActivityRecord(id)
      setDetails((all) => ({ ...all, [id]: d }))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-title">Activity</h1>
      {/* A human arriving after a long agent run wants one session's trail.
          Scrolling a merged log to find it is the difference between a screen
          that gets used and one that does not. */}
      <div className="flex flex-wrap items-end gap-3">
        <Filter
          label="Agent"
          value={filters.session}
          onChange={(v) => filter({ session: v })}
          options={Object.entries(agents).map(([value, label]) => ({ value, label }))}
        />
        <Filter
          label="Connection"
          value={filters.connection}
          onChange={(v) => filter({ connection: v })}
          options={connections.map((c) => ({ value: c.id, label: c.name }))}
        />
        <Filter
          label="Tier"
          value={filters.tier}
          onChange={(v) => filter({ tier: v })}
          options={['0', '1', '2', '3', '4'].map((t) => ({ value: t, label: `tier ${t}` }))}
        />
        <Filter
          label="Decision"
          value={filters.decision}
          onChange={(v) => filter({ decision: v })}
          options={['approved', 'refused', 'expired', 'cancelled'].map((d) => ({ value: d, label: d }))}
        />
        <Button variant="outline" onClick={() => filter(NO_FILTER)}>
          Clear
        </Button>
        <Button variant="outline" onClick={() => void refresh()}>
          Refresh
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
              <TableHead className="w-8" />
              <TableHead>When</TableHead>
              <TableHead>Agent</TableHead>
              <TableHead>Connection</TableHead>
              <TableHead>Decision</TableHead>
              <TableHead className="text-right">Tier</TableHead>
              <TableHead className="text-right">Rows</TableHead>
              <TableHead>Policies applied</TableHead>
              <TableHead>Statement</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {records.filter((r) => filters.decision === ALL || r.decision === filters.decision).map((r) => (
              <Fragment key={r.id}>
                <TableRow className="cursor-pointer" aria-expanded={open === r.id} onClick={() => void toggle(r.id)}>
                  <TableCell>
                    <ChevronRightIcon
                      aria-hidden="true"
                      className={cn('size-4 text-muted-foreground transition-transform', open === r.id && 'rotate-90')}
                    />
                  </TableCell>
                  <TableCell className="text-meta">{age(r.at)} ago</TableCell>
                  <TableCell className="text-meta">{r.client.name}</TableCell>
                  <TableCell className="text-meta">
                    {connections.find((c) => c.id === r.connection)?.name ??
                      (connections.length > 0 ? 'removed connection' : r.connection)}
                  </TableCell>
                  <TableCell className="text-meta">
                    {r.decision ? `${r.decision}${r.approver ? ` by ${r.approver}` : ''}` : '—'}
                  </TableCell>
                  <TableCell className="text-right text-meta">{r.tier}</TableCell>
                  <TableCell className="text-right text-meta">{r.row_count}</TableCell>
                  <TableCell className="text-meta">{summarize(r.transforms)}</TableCell>
                  <TableCell className="max-w-md truncate text-meta">{renderSQL(r.statement)}</TableCell>
                </TableRow>
                {open === r.id ? (
                  <TableRow className="hover:bg-transparent">
                    <TableCell colSpan={9} className="p-0 whitespace-normal">
                      {details[r.id] ? (
                        <RecordDetail record={details[r.id]} />
                      ) : (
                        <Skeleton className="h-24 w-full" />
                      )}
                    </TableCell>
                  </TableRow>
                ) : null}
              </Fragment>
            ))}
          </TableBody>
        </Table>
      )}

      {cursors.length > 0 || hasOlder ? (
        <div className="flex items-center gap-3">
          <Button variant="outline" disabled={cursors.length === 0} onClick={() => setCursors((c) => c.slice(0, -1))}>
            Newer
          </Button>
          <span className="text-meta text-muted-foreground">page {cursors.length + 1}</span>
          <Button
            variant="outline"
            disabled={!hasOlder || !records?.length}
            onClick={() => setCursors((c) => [...c, records!.at(-1)!.id])}
          >
            Older
          </Button>
        </div>
      ) : null}

      <p className="text-meta text-muted-foreground">
        Policies applied, not bytes verified: the log records the decision, not proof that redaction ran correctly.
        Literals are never stored, so the statement shown is the statement as kept.
      </p>
    </div>
  )
}

/** One labelled filter: a list of known values, never a field for an id. */
function Filter({
  label,
  value,
  onChange,
  options,
}: {
  label: string
  value: string
  onChange: (v: string) => void
  options: { value: string; label: string }[]
}) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-label text-muted-foreground">{label.toLowerCase()}</span>
      <Select value={value} onValueChange={(v) => onChange(v ?? ALL)}>
        <SelectTrigger aria-label={label} className="w-48">
          <SelectValue>{(v: string) => (v === ALL ? 'all' : (options.find((o) => o.value === v)?.label ?? v))}</SelectValue>
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={ALL}>all</SelectItem>
          {options.map((o) => (
            <SelectItem key={o.value} value={o.value}>
              {o.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </label>
  )
}

function RecordDetail({ record }: { record: ActivityDetail }) {
  return (
    <div className="flex flex-col gap-4 bg-muted/30 p-6">
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
        {record.token_scope ? <Fact label="tokens">{record.token_scope}</Fact> : null}
        {record.approver ? <Fact label="approver">{record.approver}</Fact> : null}
        {record.error_code ? <Fact label="error">{record.error_code}</Fact> : null}
        {record.collisions ? (
          <Fact label="collisions">
            {record.collisions} cell(s) redacted because a token was already bound to a different value
          </Fact>
        ) : null}
      </Facts>

      <Separator />

      <div className="flex flex-col gap-2">
        <h3 className="text-label text-muted-foreground">returned to the agent</h3>
        {record.result ? (
          <>
            <div className="max-h-96 overflow-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    {record.result.columns.map((c) => (
                      <TableHead key={c.name}>{c.name}</TableHead>
                    ))}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {record.result.rows.map((row, i) => (
                    <TableRow key={i}>
                      {row.map((v, j) => (
                        <TableCell key={j}>
                          <Cell value={cellValue(v, record.result!.columns[j], record.result!.transforms)} />
                        </TableCell>
                      ))}
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
            <p className="text-meta text-muted-foreground">{resultNote(record.result)}</p>
          </>
        ) : (
          <p className="text-meta text-muted-foreground">
            Not held. keeper keeps the most recent results in memory only, never on disk, and none across a
            restart — by which point every token in them has stopped meaning anything.
          </p>
        )}
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

/**
 * Which of `Cell`'s treatments a returned value gets. Decided from the column's
 * policy and basis, as the agent's copy was, never from how the value looks —
 * except the redacted marker, which is also what a token collision emits.
 */
function cellValue(v: unknown, column: ColumnMeta, transforms: Record<string, Transform>): CellValue {
  if (v === null || v === undefined) return { kind: 'null' }
  if (v === REDACTED) return { kind: 'redacted' }
  if (transforms[column.name]?.basis === 'unknown') return { kind: 'unclassified' }
  const text = typeof v === 'string' ? v : JSON.stringify(v)
  switch (column.policy) {
    case 'allow':
      return { kind: 'value', text: renderSQL(text) }
    case 'token':
      return { kind: 'tokenized', token: text }
    case 'partial':
      return { kind: 'partial', text }
    case 'redact':
      return { kind: 'redacted' }
    case 'scan':
      return { kind: 'scanned', text: renderSQL(text) }
    case 'drop':
      return { kind: 'dropped' }
  }
}

function resultNote(r: QueryResult): string {
  const rows = `${r.rows.length} row(s) exactly as the agent received them`
  return r.truncated ? `${rows}; the row ceiling cut the result` : rows
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
