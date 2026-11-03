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
  getApproval,
  getLocalRequest,
  isSessionNeeded,
  listConnections,
  listHosts,
  submitLocalRequest,
  type LocalRequestView,
} from '@/lib/api'
import { useEvents, useLiveStatus } from '@/lib/live-status'
import type { ApprovalItem, ConnectionSummary, HostView } from '@/lib/types'
import { ApprovalCard } from '@/pages/InboxPage'
import { until } from '@/lib/render'

/**
 * The local page.
 *
 * An input request takes a value the agent must never see and returns a token
 * to the session.
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
  const [connections, setConnections] = useState<ConnectionSummary[]>([])

  useEffect(() => {
    listConnections().then(setConnections).catch(() => {})
  }, [])

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

  const closed = view.state !== 'pending_input'

  return (
    <Page>
      <header className="flex items-center gap-3">
        <KeeperMark />
        <h1 className="text-title">A value keeper will not show the agent</h1>
      </header>

      <Facts>
        <Fact label="asked by">
          {view.session.client.name} · {view.session.client.workspace ?? 'unknown workspace'}
        </Fact>
        <Fact label="task">{view.purpose || '—'}</Fact>
        <Fact label="connection">{connections.find((c) => c.id === view.connection_id)?.name ?? view.connection_id}</Fact>
        <Fact label="expires">in {until(view.expires_at)}</Fact>
      </Facts>

      {error ? <p className="text-sm text-blocked">{error}</p> : null}

      <Separator />

      {closed ? (
        <p className="text-sm">
          {view.state === 'ready'
            ? 'This request was already answered.'
            : `This request is ${view.state}. Nothing was sent. Ask the agent to open another.`}
        </p>
      ) : (
        <InputRequest
          view={view}
          busy={busy}
          setBusy={setBusy}
          onDone={setDone}
          onError={setError}
          requestId={requestId!}
        />
      )}

      {!closed ? (
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
          className="w-full max-w-96"
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

/**
 * A ticket's own page, which the link in a fallback answer or a desktop
 * notification opens. A waiting ticket shows the card the Inbox shows and
 * decides it the same way; a ticket that is settled or unknown shows no card.
 * Which of the two it is never matters to the person, so one sentence says both.
 */
export function LocalRequestPage() {
  const { requestId } = useParams<{ requestId: string }>()
  const [item, setItem] = useState<ApprovalItem | null | 'no-session' | undefined>(undefined)

  useEffect(() => {
    if (!requestId) return
    getApproval(requestId)
      .then((i) => setItem(i?.ticket_id ? i : null))
      .catch(async (e) => {
        if (!isSessionNeeded(e)) return setItem(null)
        // Without the dashboard session only an input page, which its id
        // alone opens, can be shown; a ticket needs the person.
        setItem(await getLocalRequest(requestId).then(() => null, () => 'no-session'))
      })
  }, [requestId])

  if (item === undefined) return <Page><Skeleton className="h-64 w-full" /></Page>
  if (item === 'no-session') return <SessionNeeded command={`keeper ui /r/${requestId}`} />
  if (item === null) return <RequestPage />
  return <TicketPage first={item} />
}

function TicketPage({ first }: { first: ApprovalItem }) {
  const ticket = first.ticket_id
  const [item, setItem] = useState<ApprovalItem | null>(first)
  const [connections, setConnections] = useState<ConnectionSummary[]>([])
  const [hosts, setHosts] = useState<HostView[]>([])
  const [done, setDone] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const { status } = useLiveStatus()

  useEffect(() => {
    listConnections().then(setConnections).catch(() => {})
    listHosts().then(setHosts).catch(() => {})
  }, [])

  // A ticket another surface decided, or the clock expired, leaves the queue.
  const follow = useCallback(() => {
    getApproval(ticket)
      .then((i) => setItem(i?.ticket_id ? i : null))
      .catch(() => setItem(null))
  }, [ticket])
  useEvents((event) => {
    if (event.kind === 'approval') follow()
  })
  useEffect(() => {
    if (status === 'live') follow()
  }, [status, follow])

  const decide = async (decision: 'approve' | 'refuse') => {
    setBusy(true)
    setError(null)
    try {
      await decideApproval(ticket, { decision })
      setDone(decision === 'approve' ? 'Approved. The statement is running.' : 'Refused. Nothing ran.')
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
  if (item === null) {
    return (
      <Page>
        <h1 className="text-title">This ticket is no longer waiting</h1>
        <p className="text-sm">It was decided on another screen, or it expired. Nothing is waiting here.</p>
        <p className="text-meta text-muted-foreground">You can close this page.</p>
      </Page>
    )
  }
  const c = connections.find((x) => x.id === item.connection)
  const host = hosts.find((h) => h.connections.includes(item.connection))
  return (
    <Page>
      {error ? <p className="text-sm text-blocked">{error}</p> : null}
      <ApprovalCard
        item={item}
        profile={{ name: c?.name ?? item.connection, username: c?.username, database: c?.database, host }}
        busy={busy}
        onDecide={(d) => void decide(d)}
      />
    </Page>
  )
}

/** Shown wherever the daemon does not know this browser. The command is the
 * whole instruction: the person's terminal is the only place a session starts. */
export function SessionNeeded({ command = 'keeper ui' }: { command?: string }) {
  return (
    <Page>
      <h1 className="text-title">Open keeper from your terminal</h1>
      <p className="text-sm">Run keeper ui in your terminal, then open the link it prints.</p>
      <p>
        <code className="text-meta">{command}</code>
      </p>
      <p className="text-meta text-muted-foreground">
        The link works for as long as the daemon runs. A restart of the daemon needs a new one.
      </p>
    </Page>
  )
}

function Page({ children }: { children: React.ReactNode }) {
  return <main className="mx-auto flex max-w-2xl flex-col gap-6 p-8">{children}</main>
}
