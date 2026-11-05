import { Fragment, useCallback, useEffect, useState } from 'react'
import { ChevronRightIcon } from 'lucide-react'
import { Link } from 'react-router-dom'

import { Button } from '@/components/ui/button'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Cell, type CellValue } from '@/components/wrappers/Cell'
import { Fact, Facts } from '@/components/wrappers/Facts'
import { PagedTable } from '@/components/wrappers/PagedTable'
import { getActivityRecord, listActivity, listConnections } from '@/lib/api'
import { usePaging } from '@/lib/paging'
import {
  basisText,
  clockTime,
  dayKey,
  dayLabel,
  durationMs,
  plural,
  outcomeText,
  recordTitle,
  relationList,
  renderSQL,
  rowsText,
} from '@/lib/render'
import { cn } from '@/lib/utils'
import type { ActivityDetail, AuditRecord, ColumnMeta, ConnectionSummary, QueryResult, Transform } from '@/lib/types'

/** The filter value meaning "no filter". */
const ALL = 'all'
const NO_FILTER = { session: ALL, connection: ALL, decision: ALL }

/** Mirrors redact.RedactedMarker: a cell that kept nothing. */
const REDACTED = '⟨redacted⟩'

/**
 * The query log. This is where "what left this machine,
 * and when" gets answered, and it is what makes reviewing worthwhile afterwards.
 *
 * Two of its rules are requirements rather than wording preferences. The log
 * records what keeper *intended* to emit — policies applied, transform counts,
 * spans redacted — and does not verify the emitted bytes , so nothing here
 * may say "values masked". And there is no "show full statement" affordance,
 * because literals are never stored : the normalized statement *is* the
 * statement as kept, and an affordance implying otherwise would be offering
 * something that does not exist.
 */
export function ActivityPage() {
  const [records, setRecords] = useState<AuditRecord[] | null>(null)
  const [total, setTotal] = useState(0)
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

  const paging = usePaging('activity', JSON.stringify(filters))
  const { page: pageNo, size } = paging
  const filter = (change: Partial<typeof filters>) => setFilters((f) => ({ ...f, ...change }))

  const refresh = useCallback(async () => {
    try {
      const { records: page, total } = await listActivity({
        session: filters.session === ALL ? undefined : filters.session,
        connection: filters.connection === ALL ? undefined : filters.connection,
        decision: filters.decision === ALL ? undefined : filters.decision,
        limit: size,
        offset: (pageNo - 1) * size,
      })
      setTotal(total)
      setRecords(page)
      // A record settled since it was opened is a new record under the same
      // id: the detail held for the question has no result and no transforms.
      setDetails((known) =>
        Object.fromEntries(
          Object.entries(known).filter(([id, d]) => page.find((r) => r.id === id)?.at === d.at),
        ),
      )
      setAgents((known) => {
        const next = { ...known }
        for (const r of page) next[r.session_id] ??= `${r.client.name} · ${r.client.workspace ?? 'unknown workspace'}`
        return next
      })
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [filters, pageNo, size])

  useEffect(() => {
    void refresh()
  }, [refresh])

  const toggle = (id: string) => setOpen(open === id ? null : id)

  useEffect(() => {
    if (open === null || details[open]) return
    getActivityRecord(open)
      .then((d) => setDetails((all) => ({ ...all, [open]: d })))
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [open, details])

  const { ordered, heads } = group(records ?? [])

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-title">Activity</h1>
      {/* A human arriving after a long agent run wants one session's trail.
          Scrolling a merged log to find it is the difference between a screen
          that gets used and one that does not. */}
      <div className="grid grid-cols-2 items-end gap-3 sm:flex sm:flex-wrap">
        <Filter
          className="col-span-2 sm:col-auto"
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
          label="Decision"
          value={filters.decision}
          onChange={(v) => filter({ decision: v })}
          options={['approved', 'refused', 'expired', 'cancelled'].map((d) => ({ value: d, label: d }))}
        />
        <div className="col-span-2 flex gap-3 sm:col-auto">
          <Button variant="outline" onClick={() => filter(NO_FILTER)}>
            Clear
          </Button>
          <Button variant="outline" onClick={() => void refresh()}>
            Refresh
          </Button>
        </div>
      </div>

      {error ? <p className="text-sm text-blocked">{error}</p> : null}

      {records === null ? (
        <Skeleton className="h-40 w-full" />
      ) : records.length === 0 && total === 0 ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>No activity</EmptyTitle>
            <EmptyDescription>Statements keeper runs appear here.</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <PagedTable
          paging={paging}
          items={ordered}
          total={total}
          unit="records"
          label="Activity"
          head={
            <TableHeader>
              <TableRow>
                <TableHead className="w-16">When</TableHead>
                <TableHead>What</TableHead>
                <TableHead className="hidden sm:table-cell">Connection</TableHead>
                <TableHead>Outcome</TableHead>
                <TableHead className="text-right">Rows</TableHead>
                <TableHead className="hidden lg:table-cell">Policies applied</TableHead>
              </TableRow>
            </TableHeader>
          }
          row={(r) => (
            <Fragment key={r.id}>
              {heads.get(r.id)?.day ? (
                <TableRow className="hover:bg-transparent">
                  <TableCell colSpan={COLUMNS} className="pt-4 text-heading">
                    {heads.get(r.id)!.day}
                  </TableCell>
                </TableRow>
              ) : null}
              {heads.get(r.id)?.session ? (
                <TableRow className="hover:bg-transparent">
                  <TableCell colSpan={COLUMNS} className="text-label text-muted-foreground">
                    {heads.get(r.id)!.session}
                  </TableCell>
                </TableRow>
              ) : null}
              <TableRow>
                <TableCell className="text-meta">{clockTime(r.at)}</TableCell>
                <TableCell className="max-w-48 sm:max-w-xs lg:max-w-md">
                  <Button
                    variant="ghost"
                    size="xs"
                    aria-expanded={open === r.id}
                    onClick={() => toggle(r.id)}
                    className="w-full justify-start px-0 font-normal"
                  >
                    <ChevronRightIcon
                      aria-hidden="true"
                      className={cn('text-muted-foreground transition-transform', open === r.id && 'rotate-90')}
                    />
                    <span className="truncate">{recordTitle(r)}</span>
                  </Button>
                </TableCell>
                <TableCell className="hidden text-meta sm:table-cell">{connectionName(r.connection, connections)}</TableCell>
                <TableCell className="text-meta whitespace-normal">{outcomeText(r)}</TableCell>
                <TableCell className="text-right text-meta">{rowsText(r)}</TableCell>
                <TableCell className="hidden text-meta lg:table-cell">{summarize(r.transforms)}</TableCell>
              </TableRow>
              {open === r.id ? (
                <TableRow
                  key={details[r.id] ? 'loaded' : 'loading'}
                  ref={(el) => el?.scrollIntoView?.({ block: 'nearest' })}
                  className="hover:bg-transparent"
                >
                  <TableCell colSpan={COLUMNS} className="max-w-0 p-0 whitespace-normal">
                    {details[r.id] ? (
                      <RecordDetail record={details[r.id]} connection={connectionName(r.connection, connections)} />
                    ) : (
                      <Skeleton className="h-24 w-full" />
                    )}
                  </TableCell>
                </TableRow>
              ) : null}
            </Fragment>
          )}
        />
      )}

      <p className="text-meta text-muted-foreground">Records policies applied, not verified output. Literals are not stored.</p>
    </div>
  )
}

/** One labelled filter: a list of known values, never a field for an id. */
function Filter({
  label,
  value,
  onChange,
  options,
  className,
}: {
  className?: string
  label: string
  value: string
  onChange: (v: string) => void
  options: { value: string; label: string }[]
}) {
  return (
    <label className={cn('flex min-w-0 flex-col gap-1', className)}>
      <span className="text-label text-muted-foreground">{label.toLowerCase()}</span>
      <Select value={value} onValueChange={(v) => onChange(v ?? ALL)}>
        <SelectTrigger aria-label={label} className="w-full sm:w-48">
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

function RecordDetail({ record, connection }: { record: ActivityDetail; connection: string }) {
  const transforms = Object.entries(record.transforms ?? {})
  const spans = transforms.some(([, t]) => t.spans_redacted)
  return (
    <div className="flex min-w-0 flex-col gap-4 bg-muted/30 p-4 sm:p-6">
      <Facts>
        {record.approver && record.decision && outcomeText(record) !== outcomeText({ decision: record.decision, approver: record.approver }) ? (
          <Fact label="decision">{outcomeText({ decision: record.decision, approver: record.approver })}</Fact>
        ) : null}
        <RedactionFix record={record} />
        <Fact label="connection">{connection}</Fact>
        <Fact label="relations">{relationList(record.relations)}</Fact>
        <Fact label="duration">{durationMs(record.duration)}</Fact>
        {record.collisions ? (
          <Fact label="collisions">
            {record.collisions} cell(s) redacted: token already bound
          </Fact>
        ) : null}
        <Fact label="audit id">
          <span className="text-meta">{record.id}</span>
        </Fact>
      </Facts>

      <Separator />

      <div>
        <h3 className="text-label text-muted-foreground">policies applied</h3>
        {transforms.length > 0 ? (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Column</TableHead>
                <TableHead>Policy</TableHead>
                <TableHead>Why</TableHead>
                {spans ? <TableHead className="text-right">Spans redacted</TableHead> : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {transforms.map(([name, t]) => (
                <TableRow key={name}>
                  <TableCell className="text-meta">{name}</TableCell>
                  <TableCell className="text-meta">{t.policy}</TableCell>
                  <TableCell className="text-meta">{basisText(t.basis)}</TableCell>
                  {spans ? <TableCell className="text-right text-meta">{t.spans_redacted ?? 0}</TableCell> : null}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : (
          <p className="text-meta text-muted-foreground">none</p>
        )}
      </div>

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
                      <TableHead key={c.name}>
                        {c.policy === 'redact' ? (
                          <Link
                            to={`/connections/${record.connection}/catalog`}
                            className="underline underline-offset-4"
                            title="Change this column's policy in the catalog"
                          >
                            {c.name}
                          </Link>
                        ) : (
                          c.name
                        )}{' '}
                        <span className="text-meta text-muted-foreground">{c.policy}</span>
                      </TableHead>
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
          <p className="text-meta text-muted-foreground">Not kept. Recent results live in memory only.</p>
        )}
      </div>

      {record.degradations?.length ? (
        <div>
          <h3 className="text-label text-muted-foreground">checks that did not run</h3>
          <ul className="text-sm">
            {record.degradations.map((d, i) => (
              <li key={i}>
                {d.layer}: {d.reason}
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      <div>
        <h3 className="text-label text-muted-foreground">statement, as kept</h3>
        <pre className="overflow-x-auto text-meta whitespace-pre-wrap">{renderSQL(record.statement)}</pre>
      </div>
    </div>
  )
}

/** Schemas keeper's catalog never holds, so a column read from one stays unclassified. */
const SYSTEM = new Set(['pg_catalog', 'information_schema'])

/** True when a statement read system catalogs and nothing else. */
function systemOnly(record: AuditRecord): boolean {
  const rels = record.relations ?? []
  return rels.length > 0 && rels.every((r) => SYSTEM.has(r.schema))
}

/** A connection's name, or what became of it. */
function connectionName(id: string, connections: ConnectionSummary[]): string {
  return connections.find((c) => c.id === id)?.name ?? (connections.length > 0 ? 'removed connection' : id)
}

/**
 * The one action that clears a redaction keeper made because it did not know a
 * column, or, for a system catalog, that no action does. The link opens Catalog
 * unfiltered, where unclassified columns come first: a result names its columns
 * by their output name, which an alias or a duplicate-name suffix changes, so a
 * filter built from it can hide the column or show another table's.
 */
function RedactionFix({ record }: { record: ActivityDetail }) {
  const unknown = Object.entries(record.transforms ?? {})
    .filter(([, t]) => t.basis === 'unknown')
    .map(([name]) => name)
  if (unknown.length === 0) return null
  if (systemOnly(record)) {
    return (
      <Fact label="redacted">
        {plural(unknown.length, 'column')} from a system catalog. keeper does not classify system catalogs, so
        these stay redacted.
      </Fact>
    )
  }
  return (
    <Fact label="redacted">
      {plural(unknown.length, 'column')} not in the catalog.{' '}
      <Link to={`/connections/${record.connection}/catalog`} className="underline underline-offset-4">
        Classify in Catalog
      </Link>
    </Fact>
  )
}

/**
 * Which of `Cell`'s treatments a returned value gets. Decided from the column's
 * policy and basis, as the agent's copy was, never from how the value looks —
 * except the redacted marker, which is also what a token collision emits. An
 * unclassified column is checked first: its cells are the marker too, and
 * showing them as plain redactions hides the one cell that needs a person.
 */
function cellValue(v: unknown, column: ColumnMeta, transforms: Record<string, Transform>): CellValue {
  if (v === null || v === undefined) return { kind: 'null' }
  if (transforms[column.name]?.basis === 'unknown') return { kind: 'unclassified' }
  if (v === REDACTED) return { kind: 'redacted' }
  const text = typeof v === 'string' ? v : JSON.stringify(v)
  switch (column.policy) {
    case 'allow':
      return { kind: 'value', text: renderSQL(text) }
    case 'token':
      return { kind: 'tokenized', token: text }
    case 'redact':
      return { kind: 'redacted' }
    case 'scan':
      return { kind: 'scanned', text: renderSQL(text) }
  }
}

const CUT = { rows: 'row ceiling', size: 'size cap' } as const

function resultNote(r: QueryResult): string {
  if (r.executed_rows) return `${r.executed_rows} row(s) changed`
  const rows = `${r.rows.length} row(s)`
  if (!r.truncated) return rows
  return r.truncated_by ? `${rows} · truncated at the ${CUT[r.truncated_by]}` : `${rows} · truncated`
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

const COLUMNS = 6

/** Who ran a record: the agent and its workspace. */
function who(r: AuditRecord): string {
  return `${r.client.name} · ${r.client.workspace ?? 'unknown workspace'}`
}

/**
 * The page's records by day, then by agent and workspace within a day, so two
 * sessions of one agent in one workspace read as one trail, each in the order
 * the server sent them, newest first. A record that starts a day or a session
 * carries the heading to draw above it.
 */
function group(records: AuditRecord[]) {
  const days = new Map<string, Map<string, AuditRecord[]>>()
  for (const r of records) {
    const day = days.get(dayKey(r.at)) ?? new Map<string, AuditRecord[]>()
    days.set(dayKey(r.at), day)
    day.set(who(r), [...(day.get(who(r)) ?? []), r])
  }
  const ordered: AuditRecord[] = []
  const heads = new Map<string, { day?: string; session?: string }>()
  for (const sessions of days.values()) {
    let first = true
    for (const rs of sessions.values()) {
      const r = rs[0]
      heads.set(r.id, {
        day: first ? dayLabel(r.at) : undefined,
        session: who(r),
      })
      first = false
      ordered.push(...rs)
    }
  }
  return { ordered, heads }
}
