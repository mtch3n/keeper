import { useCallback, useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { Fact, Facts } from '@/components/wrappers/Facts'
import { KeeperMark } from '@/components/wrappers/KeeperMark'
import {
  cancelLocalRequest,
  decideApproval,
  decideLocalRequest,
  getApproval,
  getLocalRequest,
  listConnections,
  submitLocalRequest,
  type LocalRequestView,
} from '@/lib/api'
import type { ApprovalItem, ConnectionSummary } from '@/lib/types'
import { ApprovalCard } from '@/pages/InboxPage'
import { age, relationList, renderSQL } from '@/lib/render'

/**
 * The local page (SPEC R8.7g, UI.md §6).
 *
 * One page, two request kinds, one set of security rules. An **input** request
 * takes a value the agent must never see and returns a token to the session; an
 * **authorization** request grants a path and returns nothing at all. The second
 * difference is the one to render carefully: reusing "delivered to the session"
 * for an authorization would describe something that did not happen.
 *
 * It sits outside the app shell on purpose. Somebody arrives here from a link a
 * blocked agent printed, to make one decision, and the navigation would be an
 * invitation to do something else first.
 */
function RequestPage() {
  const { requestId } = useParams<{ requestId: string }>()
  const [view, setView] = useState<LocalRequestView | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    if (!requestId) return
    try {
      setView(await getLocalRequest(requestId))
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [requestId])

  useEffect(() => {
    void load()
  }, [load])

  if (error && !view) {
    return (
      <Page>
        <h1 className="text-title">This request is not available</h1>
        <p className="text-sm">It was already settled or does not exist. Nothing is waiting here.</p>
        <p className="text-meta text-muted-foreground">
          A request is one-use and expires quickly. Ask the agent to open another.
        </p>
      </Page>
    )
  }

  if (!view) return <Page><Skeleton className="h-64 w-full" /></Page>

  if (done) {
    return (
      <Page>
        <h1 className="text-title">{done}</h1>
        <p className="text-meta text-muted-foreground">You can close this page.</p>
      </Page>
    )
  }

  const expired = view.state === 'expired' || view.state === 'cancelled'

  return (
    <Page>
      <header className="flex items-center gap-3">
        <KeeperMark />
        <h1 className="text-title">
          {view.kind === 'input' ? 'A value keeper will not show the agent' : 'Permission to read one path'}
        </h1>
      </header>

      <Facts>
        <Fact label="asked by">
          {view.session.client.name} · {view.session.client.workspace ?? 'unknown workspace'}
        </Fact>
        <Fact label="task">{view.session.intent || view.purpose || '—'}</Fact>
        <Fact label="connection">{view.connection_id}</Fact>
        {view.mode ? <Fact label="mode">{view.mode}</Fact> : null}
        <Fact label="expires">in {age(view.expires_at)}</Fact>
      </Facts>

      {error ? <p className="text-sm text-blocked">{error}</p> : null}

      <Separator />

      {expired ? (
        <p className="text-sm">
          This request is {view.state}. Nothing was sent. Ask the agent to open another.
        </p>
      ) : view.kind === 'input' ? (
        <InputRequest
          view={view}
          busy={busy}
          setBusy={setBusy}
          onDone={setDone}
          onError={setError}
          requestId={requestId!}
        />
      ) : (
        <AuthorizationRequest
          view={view}
          busy={busy}
          setBusy={setBusy}
          onDone={setDone}
          onError={setError}
          requestId={requestId!}
        />
      )}

      {!expired ? (
        <Button
          variant="outline"
          disabled={busy}
          onClick={async () => {
            setBusy(true)
            try {
              await cancelLocalRequest(requestId!)
              setDone('Cancelled. Nothing was sent.')
            } catch (e) {
              setError(e instanceof Error ? e.message : String(e))
            } finally {
              setBusy(false)
            }
          }}
        >
          Cancel
        </Button>
      ) : null}
    </Page>
  )
}

function InputRequest({
  view,
  requestId,
  busy,
  setBusy,
  onDone,
  onError,
}: {
  view: LocalRequestView
  requestId: string
  busy: boolean
  setBusy: (b: boolean) => void
  onDone: (s: string) => void
  onError: (e: string) => void
}) {
  const [value, setValue] = useState('')

  return (
    <section className="flex flex-col gap-4">
      <p className="text-sm">
        Type the value here rather than in the conversation. It goes straight to keeper on this machine; the
        agent receives a token it can query with and never the value itself.
      </p>
      <label className="flex flex-col gap-1">
        <span className="text-label text-muted-foreground">{view.namespace ?? 'value'}</span>
        <Input
          value={value}
          onChange={(e) => setValue(e.target.value)}
          className="w-96"
          autoComplete="off"
          spellCheck={false}
          /* A synthetic example, never a real-looking one: a placeholder is the
             first thing a person reads and the last thing they should copy. */
          placeholder={view.namespace === 'email' ? 'someone@example.com' : ''}
        />
      </label>
      <Button
        disabled={busy || value.trim() === ''}
        onClick={async () => {
          setBusy(true)
          try {
            await submitLocalRequest(requestId, {
              value,
              namespace: view.namespace,
              connection_id: view.connection_id,
            })
            // Never echo the value back, not on success and not in an error.
            setValue('')
            onDone('Delivered to the session. The agent has a token, not the value.')
          } catch (e) {
            onError(e instanceof Error ? e.message : String(e))
          } finally {
            setBusy(false)
          }
        }}
      >
        Confirm and continue
      </Button>
      <p className="text-meta text-muted-foreground">
        Not kept in the URL, browser storage, or keeper's log.
      </p>
    </section>
  )
}

function AuthorizationRequest({
  view,
  requestId,
  busy,
  setBusy,
  onDone,
  onError,
}: {
  view: LocalRequestView
  requestId: string
  busy: boolean
  setBusy: (b: boolean) => void
  onDone: (s: string) => void
  onError: (e: string) => void
}) {
  const grant = async (lifetime: 'session' | 'standing') => {
    setBusy(true)
    try {
      await decideLocalRequest(requestId, { decision: 'grant', lifetime, row_ceiling: 1000 })
      onDone(
        lifetime === 'session'
          ? 'Granted for this session. It goes when the session does.'
          : 'Granted until revoked. It is listed under Permissions.',
      )
    } catch (e) {
      onError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  if (view.already_granted) {
    return (
      <section className="flex flex-col gap-3">
        <p className="text-sm">
          This path was granted a moment ago, in another window. Nothing more is needed — the statement has
          already resumed.
        </p>
      </section>
    )
  }

  return (
    <section className="flex flex-col gap-4">
      <Facts>
        <Fact label="path">
          {view.path ? `read(${view.path.connection_id}:${view.path.relation.schema}.${view.path.relation.relation})` : '—'}
        </Fact>
        {view.facts ? (
          <>
            <Fact label="impact">
              {view.facts.estimated_rows} rows · {relationList(view.facts.relations)}
            </Fact>
            {view.facts.egress?.length ? <Fact label="egress">{view.facts.egress.join(', ')}</Fact> : null}
          </>
        ) : null}
      </Facts>

      {view.sql ? (
        <pre className="overflow-x-auto text-meta whitespace-pre-wrap">{renderSQL(view.sql)}</pre>
      ) : null}

      <div className="flex gap-3">
        {/* The session grant is first and is the default: "stop asking me for
            the rest of this task" and "stop asking me on this machine" are
            different requests, and collapsing them into the second is how a
            permission list fills with grants nobody remembers making. */}
        <Button disabled={busy} onClick={() => void grant('session')}>
          Allow for this session
        </Button>
        <Button variant="outline" disabled={busy} onClick={() => void grant('standing')}>
          Allow until revoked
        </Button>
      </div>

      <p className="text-meta text-muted-foreground">
        Granting returns nothing to the agent; the waiting statement re-evaluates. Covers this path only.
      </p>
    </section>
  )
}

/**
 * A ticket's own page, which the link in a fallback answer or a desktop
 * notification opens. A waiting ticket shows the card the Inbox shows and
 * decides it the same way; a ticket that is settled or unknown shows no card.
 * Which of the two it is never matters to the person, so one sentence says both.
 */
export function LocalRequestPage() {
  const { requestId } = useParams<{ requestId: string }>()
  const [item, setItem] = useState<ApprovalItem | null | undefined>(undefined)

  useEffect(() => {
    if (!requestId) return
    getApproval(requestId)
      .then((i) => setItem(i?.ticket_id ? i : null))
      .catch(() => setItem(null))
  }, [requestId])

  if (item === undefined) return <Page><Skeleton className="h-64 w-full" /></Page>
  if (item === null) return <RequestPage />
  return <TicketPage item={item} />
}

function TicketPage({ item }: { item: ApprovalItem }) {
  const [connections, setConnections] = useState<ConnectionSummary[]>([])
  const [done, setDone] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    listConnections().then(setConnections).catch(() => {})
  }, [])

  const c = connections.find((x) => x.id === item.connection)
  const decide = async (decision: 'approve' | 'refuse') => {
    setBusy(true)
    try {
      await decideApproval(item.ticket_id, { decision })
      setDone(decision === 'approve' ? 'Approved. The agent has its answer.' : 'Refused. Nothing ran.')
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  if (done) {
    return (
      <Page>
        <h1 className="text-title">{done}</h1>
        <p className="text-meta text-muted-foreground">You can close this page.</p>
      </Page>
    )
  }
  return (
    <Page>
      {error ? <p className="text-sm text-blocked">{error}</p> : null}
      <ApprovalCard
        item={item}
        profile={{ name: c?.name ?? item.connection, username: c?.username, database: c?.database, writes: c?.writes }}
        busy={busy}
        onDecide={(d) => void decide(d)}
      />
    </Page>
  )
}

function Page({ children }: { children: React.ReactNode }) {
  return <main className="mx-auto flex max-w-2xl flex-col gap-6 p-8">{children}</main>
}
