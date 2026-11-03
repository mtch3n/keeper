/**
 * The one hand-written client. It mirrors every route in CONTRACT.md §3, and
 * it is the only place in this app that calls `fetch` — CONTRACT.md §5: "no
 * fetch calls in components." Components import functions from here.
 *
 * The browser talks to keeperd's loopback listener at the same origin it was
 * served from (CONTRACT.md §5), so every path below is relative. Mutating
 * requests (POST, PUT, PATCH, DELETE) carry `X-Keeper-CSRF`; the daemon has no
 * session for a browser request (CONTRACT.md §3), so there is no
 * `X-Keeper-Session` header to send from here.
 *
 * The local page answers a request through `POST /v1/requests/{id}/submit` for
 * an input request and `/decide` for an authorization request, with `/cancel`
 * for either. They are loopback-only and one-use; a second attempt is a 409.
 */

import type {
  Degradation,
  ApprovalItem,
  ActivityDetail,
  AuditRecord,
  ColumnPolicy,
  Connection,
  ConnectionSummary,
  Host,
  HostView,
  SSLMode,
  KeeperError,
  KeeperEvent,
  KeeperEventKind,
  Limits,
  Profile,
  Reads,
  Writes,
  Detection,
  Session,
} from '@/lib/types'

// ── transport ────────────────────────────────────────────────────────────

class KeeperApiError extends Error {
  readonly error: KeeperError
  constructor(error: KeeperError) {
    super(error.summary)
    this.name = 'KeeperApiError'
    this.error = error
  }
}

const MUTATING = new Set(['POST', 'PUT', 'PATCH', 'DELETE'])

/** The daemon's CSRF token for this browser, per CONTRACT.md §3. How
 * keeperd hands it out is not yet specified in CONTRACT.md; this reads a
 * `keeper_csrf` cookie (double-submit), falling back to a `<meta
 * name="keeper-csrf">` tag the served page can set. Adjust the one function
 * below if keeperd does it differently. */
function csrfToken(): string | undefined {
  const cookieMatch = document.cookie.match(/(?:^|;\s*)keeper_csrf=([^;]+)/)
  if (cookieMatch) return decodeURIComponent(cookieMatch[1])
  return document.querySelector<HTMLMetaElement>('meta[name="keeper-csrf"]')?.content
}

function query(params?: Record<string, string | number | boolean | undefined>): string {
  if (!params) return ''
  const usable = Object.entries(params).filter(([, v]) => v !== undefined) as [string, string | number | boolean][]
  if (usable.length === 0) return ''
  return '?' + new URLSearchParams(usable.map(([k, v]) => [k, String(v)])).toString()
}

/** Raised on window when the daemon no longer knows this browser: it restarted,
 * or the page was opened without `keeper ui`. */
export const SESSION_NEEDED = 'keeper:session-needed'

/** True for the error that means "run `keeper ui`". */
export function isSessionNeeded(e: unknown): boolean {
  return e instanceof KeeperApiError && e.error.code === 'ui_session_needed'
}

async function request<T>(method: string, path: string, body?: unknown, quiet = false): Promise<T> {
  const headers: Record<string, string> = {}
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (MUTATING.has(method)) {
    const token = csrfToken()
    if (token) headers['X-Keeper-CSRF'] = token
  }

  const response = await fetch(path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    // The local page's responses are no-store (CONTRACT.md §3); never let the
    // browser cache an approval, a grant list or a query result.
    cache: 'no-store',
  })

  if (response.status === 204) return undefined as T

  const text = await response.text()
  const data = text ? JSON.parse(text) : undefined

  if (!response.ok) {
    if (!quiet && (data as KeeperError | undefined)?.code === 'ui_session_needed') {
      window.dispatchEvent(new Event(SESSION_NEEDED))
    }
    // SPEC R6.4a: the only error shape that reaches the UI. If the daemon
    // ever fails to compose one, refuse to fabricate a friendlier message.
    throw new KeeperApiError(
      (data as KeeperError | undefined) ?? {
        code: 'internal',
        summary: `keeperd returned ${response.status} with no error body`,
      },
    )
  }

  return data as T
}

const get = <T>(path: string) => request<T>('GET', path)
const post = <T>(path: string, body?: unknown) => request<T>('POST', path, body ?? {})
const put = <T>(path: string, body?: unknown) => request<T>('PUT', path, body ?? {})
const patch = <T>(path: string, body?: unknown) => request<T>('PATCH', path, body ?? {})
const del = <T>(path: string) => request<T>('DELETE', path)

// ── description, read on either surface ─────────────────────────────────

export function listConnections() {
  return get<ConnectionSummary[]>('/v1/connections')
}

/** Mirrors daemon.ConnectionDetail. Deliberately not `Connection`:
 * describe_connection returns what it lists and no more, so host,
 * password and connection string have nowhere to appear. */
export interface ConnectionDetail extends Profile {
  id: string
  name: string
  engine: string
  version?: string
  schemas?: string[]
  catalog_status: CatalogStatus
  policy_summary?: Record<string, number>
  limits: Limits
  degradations?: Degradation[]
}

interface CatalogStatus {
  path: string
  fresh: boolean
  freshness_known: boolean
  unclassified: number
  unclassified_top?: string[]
}

export function getConnection(id: string) {
  return get<ConnectionDetail>(`/v1/connections/${id}`)
}

/** Inferred shape: CONTRACT.md §3 says "tables with columns and policies"
 * without a linked Go type. */
export interface LocalRequestView {
  request_id: string
  kind: 'input'
  connection_id: string
  namespace?: string
  purpose?: string
  session: Session
  state: string
  expires_at: string
}

export function getLocalRequest(id: string, waitMs?: number) {
  return get<LocalRequestView>(`/v1/requests/${id}${query({ wait_ms: waitMs })}`)
}

/**
 * Answer an input request with the value the agent must never see. The value
 * goes straight into the requesting session's reverse map; a token returns to
 * the *agent*, and this response carries no token and no echo of the value.
 */
export function submitLocalRequest(id: string, body: { value: string; namespace?: string; connection_id?: string }) {
  return post<{ kind: string; state: string; delivered: boolean }>(`/v1/requests/${id}/submit`, body)
}

/** Cancel the request, from the page's Cancel. */
export function cancelLocalRequest(id: string) {
  return post<{ kind: string; state: string }>(`/v1/requests/${id}/cancel`, {})
}

// ── human surface ────────────────────────────────────────────────────────

export function listHosts() {
  return get<HostView[]>('/v1/hosts')
}

/** A zero port and an absent sslmode take the daemon's defaults, 5432 and prefer. */
export function registerHost(body: { name: string; address: string; port?: number; sslmode?: SSLMode }) {
  return post<Host>('/v1/hosts', body)
}

/** Refused while any connection is registered on the host. */
export function removeHost(id: string) {
  return del<{ state: string }>(`/v1/hosts/${id}`)
}

export interface Credential {
  user: string
  password?: string
}

export function registerConnection(body: {
  name: string
  host_id: string
  database: string
  credential: Credential
  writes?: Writes
  catalog_path?: string
}) {
  return post<Connection>('/v1/connections', body)
}

/** Deletes a connection and its credentials. The catalog file on disk stays: it lives in the project repo and is
 * reviewed like code. */
export function removeConnection(id: string) {
  return del<{ state: string }>(`/v1/connections/${id}`)
}

export function updateConnection(
  id: string,
  body: { reads?: Reads; writes?: Writes; detection?: Detection; limits?: Limits },
) {
  return patch<Connection>(`/v1/connections/${id}`, body)
}

/** Every local request still waiting on a human, for the Inbox. */
export function listRequests() {
  return get<PendingRequest[]>('/v1/requests')
}

/** One input request waiting on a human. */
export interface PendingRequest {
  request_id: string
  kind: 'input'
  connection_id: string
  namespace?: string
  purpose?: string
  url: string
  state: string
  expires_at: string
}

/** Mirrors types.Settings: daemon-wide choices, set here and never from MCP. */
export interface Settings {
  log_retention_days: number
  /** How long a database's connections stay open with nobody using them. */
  connection_idle_minutes: number
  /** Absent means the dashboard is on. */
  ui_off?: boolean
}

export function getSettings() {
  return get<Settings>('/v1/settings')
}

/** Changes only the settings named; the rest keep their stored value. */
export function updateSettings(body: Partial<Settings>) {
  return put<Settings>('/v1/settings', body)
}

/** Who a connection's credential logs in as. round_trip is nanoseconds. */
export interface PingResult {
  user: string
  server_version: string
  round_trip: number
}

/** Logs in once with the connection's credential. keeper never does this on
 * its own; this is how an operator asks whether an account connects. */
export function testConnection(id: string) {
  return post<PingResult>(`/v1/connections/${id}/test`)
}

export function listApprovals() {
  return get<ApprovalItem[]>('/v1/approvals')
}

/** Quiet: the ticket page probes this before it knows whether the id is a
 * ticket, and handles a missing session itself. */
export function getApproval(ticket: string) {
  return request<ApprovalItem>('GET', `/v1/approvals/${ticket}`, undefined, true)
}

export function decideApproval(ticket: string, body: { decision: 'approve' | 'refuse' }) {
  return post<{ ok: boolean }>(`/v1/approvals/${ticket}/decide`, body)
}

export interface CatalogResponse {
  connection_id: string
  entries: Record<string, ColumnPolicy>
  unclassified?: string[]
  unclassified_count: number
  policy_summary?: Record<string, number>
}

export function getCatalog(connection: string) {
  return get<CatalogResponse>(`/v1/catalog/${connection}`)
}

export function updateCatalogColumns(connection: string, entries: Record<string, ColumnPolicy>) {
  return put<void>(`/v1/catalog/${connection}/columns`, { entries })
}

/** Mirrors ports.InitProposal. The grouping is the product: what may be
 * accepted without reading, and what is the review task. */
export interface InitProposal {
  safe_to_bulk_accept: Record<string, ColumnPolicy>
  needs_review: Record<string, ColumnPolicy>
  sample_rates?: Record<string, number>
}

export function initCatalog(connection: string, sample?: number) {
  return post<InitProposal>(`/v1/catalog/${connection}/init`, { sample })
}

export function listActivity(params?: {
  session?: string
  connection?: string
  since?: string
  before?: string
  limit?: number
}) {
  return get<AuditRecord[]>(`/v1/activity${query(params)}`)
}

export function getActivityRecord(auditId: string) {
  return get<ActivityDetail>(`/v1/activity/${auditId}`)
}

/** Mirrors daemon.DoctorReport. */
export interface DoctorReport {
  version: string
  ui_base?: string
  key_source: string
  sessions?: Session[]
  pending_approvals: number
  open_tickets: number
  open_requests: number
  /** How many days the activity log keeps a record. */
  log_retention_days: number
  /** How long a database's connections stay open with nobody using them. */
  connection_idle_minutes: number
  /** A plaintext log an earlier keeper left, never read; absent when there is none. */
  legacy_audit_log?: string
  /** What was examining the data, and whether it could reach the network. */
  detector?: { name: string; version?: string; network_posture: string }
}

export function getDoctor() {
  return get<DoctorReport>('/v1/doctor')
}

// ── GET /v1/events (SSE) ─────────────────────────────────────────────────

export type EventStreamStatus = 'idle' | 'waiting' | 'live' | 'blocked'

/**
 * Subscribes to the daemon's SSE stream and reconnects with backoff when it
 * drops. Returns an unsubscribe function. `onStatus` is how `AppShell` drives
 * its daemon `Lamp` (CONTRACT.md §3: `GET /v1/events` — approval, request,
 * connection, catalog, session).
 */
export function subscribeToEvents(
  onEvent: (event: KeeperEvent) => void,
  onStatus?: (status: EventStreamStatus) => void,
): () => void {
  let source: EventSource | undefined
  let retryDelay = 1000
  let retryTimer: ReturnType<typeof setTimeout> | undefined
  let stopped = false

  const kinds: KeeperEventKind[] = ['approval', 'request', 'connection', 'catalog', 'session']

  const connect = () => {
    if (stopped) return
    onStatus?.('waiting')
    source = new EventSource('/v1/events')

    source.onopen = () => {
      retryDelay = 1000
      onStatus?.('live')
    }

    source.onerror = () => {
      source?.close()
      onStatus?.('blocked')
      if (stopped) return
      retryTimer = setTimeout(connect, retryDelay)
      retryDelay = Math.min(retryDelay * 2, 30_000)
    }

    for (const kind of kinds) {
      source.addEventListener(kind, (message: MessageEvent<string>) => {
        try {
          onEvent({ kind, data: JSON.parse(message.data) })
        } catch {
          // A malformed event is dropped rather than crashing the stream.
        }
      })
    }
  }

  connect()

  return () => {
    stopped = true
    if (retryTimer) clearTimeout(retryTimer)
    source?.close()
    onStatus?.('idle')
  }
}
