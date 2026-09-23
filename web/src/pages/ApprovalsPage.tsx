import { useCallback, useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Fact, Facts } from '@/components/wrappers/Facts'
import { Lamp } from '@/components/wrappers/Lamp'
import { decideApproval, listApprovals, subscribeToEvents } from '@/lib/api'
import { useSessionScope } from '@/lib/session-scope'
import { age, relationList, renderSQL, suspectHomoglyph } from '@/lib/render'
import type { ApprovalItem } from '@/lib/types'

/**
 * The shared queue (SPEC R9.1, UI.md §2.5).
 *
 * One daemon serves every MCP session on the machine, so a human working here is
 * working across all of them. Three rules shape this screen and none of them is
 * presentation:
 *
 *   - every row names its origin, because two agents on one database with
 *     similar SQL are otherwise indistinguishable, and that is the normal case
 *     with three windows open rather than an edge case;
 *   - oldest first, always, never reordered while the list is on screen, because
 *     a row that moves under a pointer is how the wrong thing gets approved;
 *   - deciding one item never advances to the next, because an auto-advancing
 *     queue trains a person to click at a rhythm, which is the failure the whole
 *     product exists to avoid.
 */
export function ApprovalsPage() {
  const { sessionId, setSessionId } = useSessionScope()
  const [items, setItems] = useState<ApprovalItem[] | null>(null)
  const [open, setOpen] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    try {
      setItems(await listApprovals())
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [])

  useEffect(() => {
    void refresh()
    return subscribeToEvents((event) => {
      if (event.kind === 'approval' || event.kind === 'session') void refresh()
    })
  }, [refresh])

  const decide = async (ticket: string, decision: 'approve' | 'refuse') => {
    setBusy(true)
    try {
      await decideApproval(ticket, { decision })
      // Close the item that was decided and stop. The next one is a deliberate
      // click away.
      setOpen(null)
      await refresh()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  if (items === null) return <Skeleton className="h-40 w-full" />

  // Scoping to one agent narrows the queue but never reorders it: oldest
  // first is the whole point, and a filtered queue that reads as empty would
  // be the one way this screen could lie.
  const scoped = sessionId ? items.filter((item) => item.session.id === sessionId) : items

  if (scoped.length === 0) {
    return (
      <Empty>
        <EmptyHeader>
          <EmptyTitle>Nothing is waiting</EmptyTitle>
          <EmptyDescription>
            {sessionId
              ? `Nothing from this agent. ${items.length} item(s) are waiting from others.`
              : 'Escalations from every agent session arrive here, oldest first. A day with none is a day nothing needed you.'}
          </EmptyDescription>
        </EmptyHeader>
        {sessionId ? (
          <EmptyContent>
            <Button variant="outline" size="sm" onClick={() => setSessionId(null)}>
              Show every agent
            </Button>
          </EmptyContent>
        ) : null}
      </Empty>
    )
  }

  return (
    <div className="flex flex-col gap-6">
      {error ? <p className="text-sm text-blocked">{error}</p> : null}

      {/* A narrowed queue that looks like the whole queue is the one way this
          screen could mislead: it would read as "nothing else needs you". */}
      {sessionId && scoped.length !== items.length ? (
        <div className="flex items-center gap-3">
          <p className="text-sm text-muted-foreground">
            Showing one agent. {items.length - scoped.length} item(s) from others are hidden.
          </p>
          <Button variant="outline" size="sm" onClick={() => setSessionId(null)}>
            Show every agent
          </Button>
        </div>
      ) : null}

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-8" />
            <TableHead>Agent</TableHead>
            <TableHead>Workspace</TableHead>
            <TableHead>Intent</TableHead>
            <TableHead>Connection</TableHead>
            <TableHead className="text-right">Tier</TableHead>
            <TableHead className="text-right">Waiting</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {scoped.map((item) => (
            <TableRow
              key={item.ticket_id}
              onClick={() => setOpen(open === item.ticket_id ? null : item.ticket_id)}
              className="cursor-pointer"
            >
              <TableCell>
                <Lamp state="waiting" label="waiting for you" />
              </TableCell>
              <TableCell className="text-meta">{item.session.client.name}</TableCell>
              <TableCell className="text-meta">{item.session.client.workspace ?? '—'}</TableCell>
              <TableCell className="text-sm">{item.session.intent ?? '—'}</TableCell>
              <TableCell className="text-meta">{item.connection}</TableCell>
              <TableCell className="text-right text-meta">{item.tier}</TableCell>
              <TableCell className="text-right text-meta">{age(item.created_at)}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>

      {open ? (
        <ApprovalDetail
          item={items.find((i) => i.ticket_id === open)!}
          busy={busy}
          onDecide={(d) => void decide(open, d)}
        />
      ) : (
        <p className="text-meta text-muted-foreground">Select a row to see what it would do.</p>
      )}
    </div>
  )
}

/**
 * §9.2's layout: facts first, SQL last. A human cannot tell from
 * `SELECT * FROM users WHERE created_at > '2024-01-01'` that it returns 50,000
 * SSNs, so the statement is evidence and the impact summary is the thing being
 * decided on.
 */
function ApprovalDetail({
  item,
  busy,
  onDecide,
}: {
  item: ApprovalItem
  busy: boolean
  onDecide: (decision: 'approve' | 'refuse') => void
}) {
  const f = item.facts
  const homoglyph = (f.relations ?? []).some((r) => suspectHomoglyph(r.relation) || suspectHomoglyph(r.schema))

  return (
    <div className="flex flex-col gap-4 border border-border p-6">
      <Facts>
        <Fact label="intent">{f.intent || '—'}</Fact>
        <Fact label="agent">
          {item.session.client.name} · {item.session.client.workspace ?? 'unknown workspace'}
        </Fact>
        {item.write ? (
          <Fact label="impact">
            {item.write.operation} · {item.write.row_count} rows as of the preview, {age(item.write.previewed_at)} ago
            · {relationList(f.relations)}
            <div className="text-meta text-muted-foreground">
              the count is not a lock; the executed count is reported back
            </div>
          </Fact>
        ) : (
          <Fact label="impact">
            {f.estimated_rows} rows · {relationList(f.relations)}
          </Fact>
        )}
        {f.egress?.length ? <Fact label="egress">{f.egress.join(', ')}</Fact> : null}
        <Fact label="cost">est. {Math.round(f.estimated_cost)}</Fact>
        {f.reasons?.length ? <Fact label="why">{f.reasons.join(', ')}</Fact> : null}
        {homoglyph ? (
          <Fact label="warning">
            <span className="text-waiting">
              A relation name mixes scripts. Check it is the table you think it is.
            </span>
          </Fact>
        ) : null}
      </Facts>

      <Separator />

      <pre className="overflow-x-auto text-meta whitespace-pre-wrap">{renderSQL(item.sql)}</pre>

      {item.write ? (
        <p className="text-sm">
          Approving executes a change to stored data. Revoking this approval afterwards stops future writes and
          restores nothing.
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
    </div>
  )
}

