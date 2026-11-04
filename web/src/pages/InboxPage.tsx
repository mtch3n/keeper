import { useEffect, useRef, useState } from 'react'
import { ChevronRightIcon } from 'lucide-react'
import { Link } from 'react-router-dom'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { PagedTable } from '@/components/wrappers/PagedTable'
import { Fact, Facts } from '@/components/wrappers/Facts'
import { Lamp } from '@/components/wrappers/Lamp'
import { decideApproval, listConnections, listHosts, type PendingRequest } from '@/lib/api'
import { useInbox } from '@/lib/inbox'
import { usePaging } from '@/lib/paging'
import { cn } from '@/lib/utils'
import { age, onRelations, plural, reasonText, until, renderSQL, suspectHomoglyph } from '@/lib/render'
import type { ApprovalItem, ConnectionSummary, HostView, Risk, WriteChanges } from '@/lib/types'

/**
 * Everything waiting on a human, oldest first. Each item is a card
 * that says, without a click, who is asking, on which profile, why it waited
 * and what it would do — the SQL comes last, as evidence.
 *
 * The order never changes under the pointer and deciding one item never
 * advances to the next: an auto-advancing queue trains a person to click at a
 * rhythm, which is the failure keeper exists to prevent.
 */
export function InboxPage() {
  const { approvals, requests, error, refresh } = useInbox()
  const [connections, setConnections] = useState<ConnectionSummary[]>([])
  const [hosts, setHosts] = useState<HostView[]>([])
  const [busy, setBusy] = useState<string | null>(null)
  const [failure, setFailure] = useState<string | null>(null)
  const [open, setOpen] = useState<Record<string, boolean>>({})
  const paging = usePaging('inbox')

  useEffect(() => {
    listConnections().then(setConnections).catch(() => {})
    listHosts().then(setHosts).catch(() => {})
  }, [])

  const decide = async (ticket: string, decision: 'approve' | 'refuse') => {
    setBusy(ticket)
    setFailure(null)
    try {
      await decideApproval(ticket, { decision })
      await refresh()
    } catch (e) {
      setFailure(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(null)
    }
  }

  if (approvals === null) {
    return error ? (
      <div className="flex flex-col gap-6">
        <h1 className="text-title">Inbox</h1>
        <p className="text-sm text-blocked">{error}</p>
      </div>
    ) : (
      <Skeleton className="h-40 w-full" />
    )
  }

  if (approvals.length === 0 && requests.length === 0) {
    return (
      <div className="flex flex-col gap-6">
        <h1 className="text-title">Inbox</h1>
        {error ? <p className="text-sm text-blocked">{error}</p> : null}
        {failure ? <p className="text-sm text-blocked">{failure}</p> : null}
        <Empty>
          <EmptyHeader>
            <EmptyTitle>Nothing is waiting</EmptyTitle>
            <EmptyDescription>
              When a statement needs your answer, or an agent asks for a value only you should type, it arrives
              here, oldest first.
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      </div>
    )
  }

  const profile = (id: string) => {
    const c = connections.find((x) => x.id === id)
    const h = hosts.find((x) => x.connections.includes(id))
    return { name: c?.name ?? id, username: c?.username, database: c?.database, host: h }
  }

  const items: Row[] = [
    ...[...approvals]
      .sort((x, y) => x.created_at.localeCompare(y.created_at) || x.ticket_id.localeCompare(y.ticket_id))
      .map((item): Row => ({ kind: 'approval', id: item.ticket_id, item })),
    ...[...requests]
      .sort((x, y) => x.request_id.localeCompare(y.request_id))
      .map((request): Row => ({ kind: 'request', id: request.request_id, request })),
  ]
  // A lone item opens by itself: there is nothing to choose between, and a
  // write is read before it is decided.
  const isOpen = (id: string) => open[id] ?? items.length === 1
  const toggle = (id: string) => setOpen((o) => ({ ...o, [id]: !isOpen(id) }))

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-title">Inbox</h1>
      {error ? <p className="text-sm text-blocked">{error}</p> : null}
      {failure ? <p className="text-sm text-blocked">{failure}</p> : null}
      <PagedTable
        paging={paging}
        items={items}
        unit="waiting"
        label="Inbox"
        head={
          <TableHeader>
            <TableRow>
              <TableHead className="w-8" />
              <TableHead className="hidden lg:table-cell">Risk</TableHead>
              <TableHead>Agent</TableHead>
              <TableHead className="hidden lg:table-cell">Profile</TableHead>
              <TableHead className="hidden lg:table-cell">Why it waits</TableHead>
              <TableHead className="hidden lg:table-cell">Statement</TableHead>
              <TableHead className="hidden lg:table-cell">Waited</TableHead>
              <TableHead>
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
        }
        row={(r) =>
          r.kind === 'approval' ? (
            <ApprovalRow
              key={r.id}
              item={r.item}
              profile={profile(r.item.connection)}
              open={isOpen(r.id)}
              busy={busy === r.id}
              onToggle={() => toggle(r.id)}
              onDecide={(d) => void decide(r.id, d)}
            />
          ) : (
            <RequestRow key={r.id} request={r.request} profile={profile(r.request.connection_id)} />
          )
        }
      />
    </div>
  )
}

type Row =
  | { kind: 'approval'; id: string; item: ApprovalItem }
  | { kind: 'request'; id: string; request: PendingRequest }

/** The reasons an item waits, as sentences; the row keeps the first and counts the rest. */
function reasonSummary(reasons: string[] | undefined): string {
  if (!reasons?.length) return '—'
  const first = reasonText(reasons[0])
  return reasons.length > 1 ? `${first} +${reasons.length - 1} more` : first
}

/**
 * One waiting statement as a row: what is decided on, in the order it is
 * decided on. A read can be approved or refused from the row; a write cannot,
 * because its changed rows are in the expansion and they are what a person
 * decides on. The expansion is the full card.
 */
function ApprovalRow({
  item,
  profile,
  open,
  busy,
  onToggle,
  onDecide,
}: {
  item: ApprovalItem
  profile: Profile
  open: boolean
  busy: boolean
  onToggle: () => void
  onDecide: (decision: 'approve' | 'refuse') => void
}) {
  const risk: Risk = item.facts.risk ?? 'destructive'
  const readOnly = risk === 'read' && !item.write
  return (
    <>
      <TableRow className="cursor-pointer" aria-expanded={open} onClick={onToggle}>
        <TableCell>
          <ChevronRightIcon
            aria-hidden="true"
            className={cn('size-4 text-muted-foreground transition-transform', open && 'rotate-90')}
          />
        </TableCell>
        <TableCell className="hidden lg:table-cell">
          <Badge variant={RISK_BADGE[risk]}>{risk}</Badge>
        </TableCell>
        <TableCell className="text-sm">
          <div className="flex items-center gap-2">
            <Badge variant={RISK_BADGE[risk]} className="lg:hidden">
              {risk}
            </Badge>
            {item.session.client.name}
          </div>
          <div className="max-w-44 truncate text-meta text-muted-foreground lg:hidden">{renderSQL(item.sql)}</div>
        </TableCell>
        <TableCell className="hidden text-meta lg:table-cell">{profile.name}</TableCell>
        <TableCell className="hidden max-w-xs truncate text-sm lg:table-cell">{reasonSummary(item.facts.reasons)}</TableCell>
        <TableCell className="hidden max-w-xs truncate text-meta lg:table-cell">{renderSQL(item.sql)}</TableCell>
        <TableCell className="hidden text-meta lg:table-cell">{age(item.created_at)}</TableCell>
        <TableCell onClick={(e) => e.stopPropagation()}>
          {open ? null : readOnly ? (
            <div className="flex flex-col gap-2 lg:flex-row">
              <Button size="sm" disabled={busy} onClick={() => onDecide('approve')}>
                Approve
              </Button>
              <Button size="sm" variant="outline" disabled={busy} onClick={() => onDecide('refuse')}>
                Refuse
              </Button>
            </div>
          ) : (
            <Button size="sm" variant="outline" onClick={onToggle}>
              Review
            </Button>
          )}
        </TableCell>
      </TableRow>
      {open ? (
        <TableRow className="hover:bg-transparent">
          <TableCell colSpan={8} className="max-w-0 p-2 whitespace-normal sm:p-4">
            <ApprovalCard item={item} profile={profile} busy={busy} onDecide={onDecide} />
          </TableCell>
        </TableRow>
      ) : null}
    </>
  )
}

/** A request for a value, answered on its own page. */
function RequestRow({ request, profile }: { request: PendingRequest; profile: Profile }) {
  const path = new URL(request.url, window.location.origin).pathname
  return (
    <TableRow>
      <TableCell />
      <TableCell className="hidden lg:table-cell">
        <Badge variant="outline">input</Badge>
      </TableCell>
      <TableCell className="text-sm">
        <div className="flex items-center gap-2">
          <Badge variant="outline" className="lg:hidden">
            input
          </Badge>
          —
        </div>
        <div className="max-w-40 truncate text-meta text-muted-foreground lg:hidden">
          {request.purpose || 'Asks you to type a value'}
        </div>
      </TableCell>
      <TableCell className="hidden text-meta lg:table-cell">{profile.name}</TableCell>
      <TableCell className="hidden max-w-xs truncate text-sm lg:table-cell">{request.purpose || 'Asks you to type a value'}</TableCell>
      <TableCell className="hidden text-meta lg:table-cell">—</TableCell>
      <TableCell className="hidden text-meta lg:table-cell">expires in {until(request.expires_at)}</TableCell>
      <TableCell>
        <Link to={path} className="text-sm underline underline-offset-4">
          Open
        </Link>
      </TableCell>
    </TableRow>
  )
}

export type Profile = { name: string; username?: string; database?: string; host?: HostView }

function ProfileFact({ profile }: { profile: Profile }) {
  return (
    <Fact label="profile">
      {profile.name}
      {profile.host ? ` · ${profile.host.name} (${profile.host.address}:${profile.host.port})` : ''}
      {profile.username ? ` · user ${profile.username}` : ''}
      {profile.database ? ` · database ${profile.database}` : ''}
    </Fact>
  )
}

const RISK_BADGE = { read: 'outline', write: 'secondary', destructive: 'destructive' } as const

/**
 * One statement waiting for approval: facts first, SQL last. A
 * human cannot tell from the text of a query that it returns 50,000 SSNs, so the
 * impact is what is decided on and the statement is the evidence.
 */
export function ApprovalCard({
  item,
  profile,
  busy,
  onDecide,
}: {
  item: ApprovalItem
  profile: Profile
  busy: boolean
  onDecide: (decision: 'approve' | 'refuse') => void
}) {
  const f = item.facts
  // A card that cannot say how dangerous its statement is says the worst.
  const risk: Risk = f.risk ?? 'destructive'
  const targets = f.targets?.length ? f.targets : f.relations
  const homoglyph = (f.relations ?? []).some((r) => suspectHomoglyph(r.relation) || suspectHomoglyph(r.schema))
  const who = item.session.client.name

  return (
    <article aria-label={`${who} — ${f.statement_type || 'unknown statement'}`} className="flex min-w-0 flex-col gap-4 border border-border p-4 sm:p-6">
      <div className="flex flex-wrap items-center gap-3">
        <Lamp state="waiting" label="waiting for you" />
        <Badge variant={RISK_BADGE[risk]}>{risk}</Badge>
        <span className="text-sm">{who}</span>
        <span className="text-meta text-muted-foreground">{item.session.client.workspace ?? 'unknown workspace'}</span>
        <span className="ml-auto text-meta text-muted-foreground">
          waiting {age(item.created_at)}
        </span>
      </div>
      <Facts>
        <ProfileFact profile={profile} />
        {f.reasons?.length ? <Fact label="why it waits">{f.reasons.map(reasonText).join(' ')}</Fact> : null}
        {f.unreadable || !f.risk ? (
          <Fact label="warning">
            <span className="text-blocked">keeper could not read this statement, so it is treated as destructive.</span>
          </Fact>
        ) : null}
        {item.write ? (
          <Fact label="impact">
            {onRelations(item.write.operation, targets)} · {plural(item.write.row_count, 'row')} affected, as of the preview
          </Fact>
        ) : (
          <Fact label="impact">
            {onRelations(f.statement_type || 'unknown', targets)} · about {plural(f.estimated_rows, 'row')}
          </Fact>
        )}
        {f.columns?.length ? <Fact label="columns">{f.columns.join(', ')}</Fact> : null}
        {f.filters?.length ? (
          <Fact label="filters">
            <span className="flex flex-col">
              {f.filters.map((c) => (
                <span key={`${c.relation.schema}.${c.relation.relation}.${c.column}`} className="text-waiting">
                  filters on {c.relation.schema}.{c.relation.relation}.{c.column} (
                  {c.flag === 'sensitive' ? `sensitive: ${c.policy}` : 'unclassified'})
                </span>
              ))}
            </span>
          </Fact>
        ) : null}
        {f.egress?.length ? <Fact label="masked">{f.egress.join(', ')}</Fact> : null}
        {homoglyph ? (
          <Fact label="warning">
            <span className="text-waiting">A relation name mixes scripts. Check it is the table you think it is.</span>
          </Fact>
        ) : null}
      </Facts>
      {item.write?.changes ? <ChangedRows changes={item.write.changes} /> : null}
      <Separator />
      <pre className="overflow-x-auto text-meta whitespace-pre-wrap [overflow-wrap:anywhere]">{renderSQL(item.sql)}</pre>
      {item.write ? (
        <p className="text-sm">
          Approving changes stored data. It cannot be undone from here.
        </p>
      ) : null}
      <div className="flex flex-wrap gap-3">
        <Button disabled={busy} onClick={() => onDecide('approve')}>
          Approve
        </Button>
        <Button variant="outline" disabled={busy} onClick={() => onDecide('refuse')}>
          Refuse
        </Button>
      </div>
    </article>
  )
}

/**
 * The rows a write changes, in cleartext, because an approver deciding on a
 * count alone is deciding blind. A changed value shows what it was, struck
 * through, beside what it becomes; the agent never receives either.
 */
function ChangedRows({ changes }: { changes: WriteChanges }) {
  const region = useRef<HTMLDivElement>(null)
  const [scrolls, setScrolls] = useState(false)
  useEffect(() => {
    const el = region.current?.firstElementChild
    if (!el) return
    const measure = () => setScrolls(el.scrollWidth > el.clientWidth)
    measure()
    window.addEventListener('resize', measure)
    return () => window.removeEventListener('resize', measure)
  }, [changes])
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <div ref={region} className="min-w-0 border border-border">
        <Table aria-label="Changed rows">
          <TableHeader>
            <TableRow>
              {changes.columns.map((c, i) => (
                <TableHead key={i}>{c}</TableHead>
              ))}
            </TableRow>
          </TableHeader>
          <TableBody>
            {changes.rows.map((row, r) => (
              <TableRow key={r}>
                {changes.columns.map((_, i) => {
                  const before = row.old?.[i]
                  const after = row.new?.[i]
                  return (
                    <TableCell key={i} className="text-meta">
                      {before !== undefined && before !== after ? (
                        <span className="text-muted-foreground line-through">{before}</span>
                      ) : null}
                      {before !== undefined && after !== undefined && before !== after ? ' ' : null}
                      {after !== undefined ? <span>{after}</span> : null}
                    </TableCell>
                  )
                })}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      {scrolls ? <p className="text-meta text-waiting">More columns lie to the right. Scroll this table sideways.</p> : null}
      <p className="text-meta text-muted-foreground">
        {changes.omitted ? `${changes.omitted} more rows are not shown. ` : ''}
        Shown to you only. Not logged or sent to the agent.
      </p>
    </div>
  )
}
