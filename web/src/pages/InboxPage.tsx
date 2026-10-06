import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'

import { Button } from '@/components/ui/button'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { Fact, Facts } from '@/components/wrappers/Facts'
import { Lamp } from '@/components/wrappers/Lamp'
import { decideApproval, listConnections, listHosts, type PendingRequest } from '@/lib/api'
import { useInbox } from '@/lib/inbox'
import { age, relationList, renderSQL, suspectHomoglyph } from '@/lib/render'
import type { ApprovalItem, ConnectionSummary, HostView, Writes } from '@/lib/types'

/**
 * Everything waiting on a human (SPEC R9.1), oldest first. Each item is a card
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

  useEffect(() => {
    listConnections().then(setConnections).catch(() => {})
    listHosts().then(setHosts).catch(() => {})
  }, [])

  const decide = async (ticket: string, decision: 'approve' | 'refuse') => {
    setBusy(ticket)
    try {
      await decideApproval(ticket, { decision })
      await refresh()
    } catch (e) {
      setFailure(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(null)
    }
  }

  if (approvals === null) return <Skeleton className="h-40 w-full" />

  if (approvals.length === 0 && requests.length === 0) {
    return (
      <Empty>
        <EmptyHeader>
          <EmptyTitle>Nothing is waiting</EmptyTitle>
          <EmptyDescription>
            When an agent's statement needs a decision, or it asks for a value only you should type, it arrives
            here, oldest first.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  const profile = (id: string) => {
    const c = connections.find((x) => x.id === id)
    const h = hosts.find((x) => x.connections.includes(id))
    return { name: c?.name ?? id, username: c?.username, database: c?.database, writes: c?.writes, host: h }
  }

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-title">Inbox</h1>
      {error || failure ? <p className="text-sm text-blocked">{error ?? failure}</p> : null}
      {approvals.map((item) => (
        <ApprovalCard
          key={item.ticket_id}
          item={item}
          profile={profile(item.connection)}
          busy={busy === item.ticket_id}
          onDecide={(d) => void decide(item.ticket_id, d)}
        />
      ))}
      {requests.map((r) => (
        <RequestCard key={r.request_id} request={r} profile={profile(r.connection_id)} />
      ))}
    </div>
  )
}

type Profile = { name: string; username?: string; database?: string; writes?: Writes; host?: HostView }

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

/**
 * One statement waiting for approval: facts first, SQL last (SPEC §9.2). A
 * human cannot tell from the text of a query that it returns 50,000 SSNs, so the
 * impact is what is decided on and the statement is the evidence.
 */
function ApprovalCard({
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
  const homoglyph = (f.relations ?? []).some((r) => suspectHomoglyph(r.relation) || suspectHomoglyph(r.schema))
  const who = item.session.client.name

  return (
    <article aria-label={`${who} — ${f.intent || 'no intent'}`} className="flex flex-col gap-4 border border-border p-6">
      <div className="flex flex-wrap items-center gap-3">
        <Lamp state="waiting" label="waiting for you" />
        <span className="text-sm">{who}</span>
        <span className="text-meta text-muted-foreground">{item.session.client.workspace ?? 'unknown workspace'}</span>
        <span className="ml-auto text-meta text-muted-foreground">
          tier {item.tier} · waiting {age(item.created_at)}
        </span>
      </div>
      <Facts>
        <Fact label="intent">{f.intent || '—'}</Fact>
        <ProfileFact profile={profile} />
        {f.reasons?.length ? <Fact label="why it waits">{f.reasons.join(', ')}</Fact> : null}
        {item.write ? (
          <Fact label="impact">
            {item.write.operation} · {item.write.row_count} rows as of the preview · {relationList(f.relations)}
          </Fact>
        ) : (
          <Fact label="impact">
            {f.statement_type} · about {f.estimated_rows} rows · {relationList(f.relations)}
          </Fact>
        )}
        {f.egress?.length ? <Fact label="masked">{f.egress.join(', ')}</Fact> : null}
        {homoglyph ? (
          <Fact label="warning">
            <span className="text-waiting">A relation name mixes scripts. Check it is the table you think it is.</span>
          </Fact>
        ) : null}
      </Facts>
      <Separator />
      <pre className="overflow-x-auto text-meta whitespace-pre-wrap">{renderSQL(item.sql)}</pre>
      {item.write ? (
        <p className="text-sm">
          Approving changes stored data. Revoking afterwards stops future writes and restores nothing.
        </p>
      ) : null}
      <div className="flex gap-3">
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

/** A request for a value or an authorization, answered on its own page. */
function RequestCard({ request, profile }: { request: PendingRequest; profile: Profile }) {
  const path = new URL(request.url, window.location.origin).pathname
  return (
    <article aria-label={`${request.kind} request`} className="flex flex-col gap-4 border border-border p-6">
      <div className="flex items-center gap-3">
        <Lamp state="waiting" label="waiting for you" />
        <span className="text-sm">{request.kind === 'input' ? 'Asks you to type a value' : 'Asks for access'}</span>
      </div>
      <Facts>
        <Fact label="purpose">{request.purpose || '—'}</Fact>
        <ProfileFact profile={profile} />
        {request.path ? (
          <Fact label="path">
            {request.path.relation.schema}.{request.path.relation.relation}
          </Fact>
        ) : null}
      </Facts>
      <div>
        <Link to={path} className="text-sm underline underline-offset-4">
          Open
        </Link>
      </div>
    </article>
  )
}
